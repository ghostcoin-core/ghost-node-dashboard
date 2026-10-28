package status

import (
	"context"
	"strings"
	"sync"
	"time"
)

// WallBlocks is how many of the latest blocks the block wall shows.
const WallBlocks = 240

// wallRecheck is how many of the latest blocks are read again on every
// request, to catch a reorganisation; one deeper than that is not seen.
const wallRecheck = 12

// WallBlock is one block of the block wall. Own is set for blocks the
// current payout address mined, LostRace for heights where the node found a
// block but another node's block was accepted.
type WallBlock struct {
	Number     uint64 `json:"number"`
	Time       uint64 `json:"time"`
	MinerShort string `json:"minerShort"`
	Own        bool   `json:"own"`
	LostRace   bool   `json:"lostRace"`
}

// NetworkEnv is what the network tracker reads besides the node itself.
type NetworkEnv struct {
	// Rewards gives the heights of lost races; nil means none.
	Rewards *Rewards
	// Budget bounds the time spent reading older blocks per request; zero
	// means ScanBudget.
	Budget time.Duration
}

// Network reads the network panel with its block wall. It keeps the block
// headers it read in memory, so each request only reads the latest blocks
// and those still missing. It is safe for concurrent use.
type Network struct {
	rewards *Rewards
	budget  time.Duration

	mu    sync.Mutex
	cache map[uint64]wallHeader
}

type wallHeader struct {
	hash  string
	time  uint64
	miner string
}

// NewNetwork returns a network tracker with an empty block cache.
func NewNetwork(env NetworkEnv) *Network {
	n := &Network{rewards: env.Rewards, budget: env.Budget, cache: map[uint64]wallHeader{}}
	if n.budget <= 0 {
		n.budget = ScanBudget
	}
	return n
}

// Fetch reads the network panel and the block wall. It never fails: a
// stopped node is reported in the result. A wall that could not be read
// whole within the budget is returned as it is and fills in over the next
// requests.
func (n *Network) Fetch(ctx context.Context, node Caller) NetworkPanel {
	n.mu.Lock()
	defer n.mu.Unlock()

	p, read := fetchNetwork(ctx, node)
	if !p.Running {
		return p
	}
	p.Wall = []WallBlock{}
	if read == nil || p.Height == nil {
		return p
	}
	height := *p.Height
	low := uint64(0)
	if height >= WallBlocks {
		low = height - WallBlocks + 1
	}
	for num := range n.cache {
		if num < low || num > height {
			delete(n.cache, num)
		}
	}

	// The panel has just read the latest blocks. A block among the latest
	// wallRecheck whose hash changed means the chain was reorganised there:
	// what is cached from that height up is dropped.
	for _, b := range read.blocks {
		if b.Number+wallRecheck <= height {
			continue
		}
		if c, ok := n.cache[b.Number]; ok && !strings.EqualFold(c.hash, b.Hash) {
			for num := range n.cache {
				if num >= b.Number {
					delete(n.cache, num)
				}
			}
		}
	}
	for _, b := range read.blocks {
		if b.Number >= low && b.Number <= height {
			n.cache[b.Number] = wallHeader{hash: b.Hash, time: b.Time, miner: b.Miner}
		}
	}

	// Read the blocks still missing, newest first, within the budget. Each
	// request reads at least one, so the wall always fills in.
	deadline := time.Now().Add(n.budget)
	first := true
	for num := height; num+1 > low; num-- {
		if _, ok := n.cache[num]; ok {
			continue
		}
		if ctx.Err() != nil || (!first && !time.Now().Before(deadline)) {
			break
		}
		first = false
		b, err := getBlock(ctx, node, num)
		if err != nil {
			break
		}
		if b != nil {
			n.cache[num] = wallHeader{hash: b.Hash, time: b.Time, miner: b.Miner}
		}
	}

	lostAt := n.rewards.LostHeights()
	for num := height; num+1 > low; num-- {
		c, ok := n.cache[num]
		if !ok {
			continue
		}
		p.Wall = append(p.Wall, WallBlock{
			Number:     num,
			Time:       c.time,
			MinerShort: ShortAddress(c.miner),
			Own:        sameAddress(read.own, c.miner),
			LostRace:   lostAt[num],
		})
	}
	return p
}
