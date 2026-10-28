package status

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// IndexFile is the file in the node directory where the dashboard keeps the
// blocks the payout address won and the last block it read, so a restart
// only reads new blocks.
const IndexFile = "dashboard-index.json"

// indexVersion is bumped when the index layout changes; an index of another
// version is read again from the start.
const indexVersion = 1

// recentHashes is how many of the last blocks read keep their hash in the
// index, to find where the chain forked after a reorganisation.
const recentHashes = 128

// ScanBudget bounds how long one request spends reading new blocks. A long
// chain is indexed over several refreshes.
const ScanBudget = 2 * time.Second

// ListedWonBlocks is how many of the latest blocks won the panel lists.
const ListedWonBlocks = 100

// LostRaceNote explains a lost race in plain words.
const LostRaceNote = "Your node found a block, but another node's block at the same height was accepted first. " +
	"This is normal and earns nothing."

var weiPerCoin = new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)

// RewardsPanel is what /api/rewards returns. The blocks won and the totals
// come from the index, so they are shown while the node is down too.
type RewardsPanel struct {
	Running       bool    `json:"running"`
	Error         string  `json:"error,omitempty"`
	IndexError    string  `json:"indexError,omitempty"`
	PayoutAddress *string `json:"payoutAddress"`
	// Balance is the payout address's balance at the latest block.
	Balance     *Amount `json:"balance"`
	WonToday    int     `json:"wonToday"`
	RewardToday Amount  `json:"rewardToday"`
	WonTotal    int     `json:"wonTotal"`
	RewardTotal Amount  `json:"rewardTotal"`
	// UnknownRewards counts blocks won whose reward the node could not
	// tell (a node that no longer keeps the balances of old blocks); they
	// are left out of the sums.
	UnknownRewards int `json:"unknownRewards"`
	// Blocks are the latest blocks won, newest first.
	Blocks []WonBlockRow `json:"blocks"`
	// Indexing is set while the index is behind the node's latest block.
	Indexing *IndexProgress `json:"indexing"`
	// NextBefore is the Before to ask for the next page of Blocks; null on
	// the last page.
	NextBefore *uint64 `json:"nextBefore"`
	// LostRaces counts the "block lost" lines in the node log; null when
	// there is no log to read.
	LostRaces     *int   `json:"lostRaces"`
	LostRacesNote string `json:"lostRacesNote"`
	// LostRaceBlocks are the latest lost races that name their block,
	// newest first, at most ListedLostRaces; null when there is no log to
	// read.
	LostRaceBlocks []LostRace `json:"lostRaceBlocks"`
	// Daily is the blocks won and their reward per local day.
	Daily DailyRewards `json:"daily"`
	// Last7 is the blocks won and their reward over the days of
	// Daily.Days7.
	Last7 PeriodTotal `json:"last7"`
}

// MaxWonBlocksPage is the largest page of blocks won one request lists.
const MaxWonBlocksPage = 500

// Page picks which blocks won the panel lists: those numbered below Before
// (every one when nil), newest first, at most Limit (ListedWonBlocks when
// zero).
type Page struct {
	Before *uint64
	Limit  int
}

// Amount is a sum in wei and the same sum in whole coins.
type Amount struct {
	Wei   string `json:"wei"`
	Coins string `json:"coins"`
}

// WonBlockRow is one block won by the payout address.
type WonBlockRow struct {
	Number uint64  `json:"number"`
	Hash   string  `json:"hash"`
	Time   uint64  `json:"time"`
	Reward *Amount `json:"reward"`
}

// IndexProgress is how far the index has read.
type IndexProgress struct {
	ReadTo *uint64 `json:"readTo"`
	Height uint64  `json:"height"`
}

type blockRef struct {
	Number uint64 `json:"number"`
	Hash   string `json:"hash"`
}

type wonBlock struct {
	Number uint64 `json:"number"`
	Hash   string `json:"hash"`
	Time   uint64 `json:"time"`
	// Reward is the miner's balance change across the block in wei, or ""
	// when the node could not tell.
	Reward string `json:"reward,omitempty"`
}

type index struct {
	Version int        `json:"version"`
	Address string     `json:"address"`
	Last    *blockRef  `json:"lastBlock"`
	Recent  []blockRef `json:"recent"`
	Blocks  []wonBlock `json:"blocks"`
}

// Rewards keeps the index of blocks won and reads the rewards panel. It is
// safe for concurrent use.
type Rewards struct {
	nodeDir  string
	now      func() time.Time
	location *time.Location
	budget   time.Duration

	mu    sync.Mutex
	idx   *index
	dirty bool
	lost  lostRaces
}

// RewardsEnv is what the rewards panel reads besides the node itself.
type RewardsEnv struct {
	NodeDir string
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Location is where "today" is; nil means the computer's local time.
	Location *time.Location
	// Budget bounds the time spent reading new blocks per request; zero
	// means ScanBudget.
	Budget time.Duration
}

// NewRewards returns a tracker that keeps its index in env.NodeDir.
func NewRewards(env RewardsEnv) *Rewards {
	r := &Rewards{nodeDir: env.NodeDir, now: env.Now, location: env.Location, budget: env.Budget}
	if r.now == nil {
		r.now = time.Now
	}
	if r.location == nil {
		r.location = time.Local
	}
	if r.budget <= 0 {
		r.budget = ScanBudget
	}
	r.lost.now, r.lost.location = r.now, r.location
	return r
}

// LostHeights is the set of block numbers the node log has a "block lost"
// line for. It is empty when there is no log to read.
func (r *Rewards) LostHeights() map[uint64]bool {
	if r == nil {
		return nil
	}
	return r.lost.heights(r.nodeDir)
}

// IndexPath is the index file for a node directory.
func IndexPath(nodeDir string) string {
	return filepath.Join(nodeDir, IndexFile)
}

// Fetch brings the index up to the node's latest block and reads the
// rewards panel. It never fails: a stopped node is reported in the result.
func (r *Rewards) Fetch(ctx context.Context, node Caller) RewardsPanel {
	return r.FetchPage(ctx, node, Page{})
}

// FetchPage is Fetch with the blocks won listed from page. The sums and
// counts cover every block won, not only the page.
func (r *Rewards) FetchPage(ctx context.Context, node Caller, page Page) RewardsPanel {
	r.mu.Lock()
	defer r.mu.Unlock()

	p := RewardsPanel{LostRacesNote: LostRaceNote}
	if n, races, err := r.lost.read(r.nodeDir); err == nil {
		p.LostRaces = &n
		p.LostRaceBlocks = []LostRace{}
		for i := len(races) - 1; i >= 0 && len(p.LostRaceBlocks) < ListedLostRaces; i-- {
			p.LostRaceBlocks = append(p.LostRaceBlocks, races[i])
		}
	}
	if err := r.load(); err != nil {
		p.IndexError = err.Error()
	}
	down := func(err error) RewardsPanel {
		p.Running, p.Error, p.Balance, p.Indexing = false, downMessage(err), nil, nil
		r.fill(&p, page)
		return p
	}

	if _, err := probe(ctx, node); err != nil {
		return down(err)
	}
	p.Running = true

	addr, err := coinbase(ctx, node)
	if lost(err) {
		return down(err)
	}
	if addr == "" {
		// Without a payout address there are no blocks to look for.
		p.Blocks = []WonBlockRow{}
		p.RewardToday, p.RewardTotal = amount(new(big.Int)), amount(new(big.Int))
		p.Daily, p.Last7 = r.daily(nil)
		return p
	}
	head, err := quantity(ctx, node, "eth_blockNumber")
	if lost(err) {
		return down(err)
	}
	if head != nil {
		err := r.sync(ctx, node, addr, *head)
		if serr := r.save(); serr != nil && p.IndexError == "" {
			p.IndexError = serr.Error()
		}
		if lost(err) {
			return down(err)
		}
		if err != nil && p.IndexError == "" {
			p.IndexError = "reading blocks: " + err.Error()
		}
		if r.idx.Last == nil || r.idx.Last.Number < *head {
			p.Indexing = &IndexProgress{Height: *head}
			if r.idx.Last != nil {
				n := r.idx.Last.Number
				p.Indexing.ReadTo = &n
			}
		}
	} else if !sameAddress(r.idx.Address, addr) {
		r.idx = &index{Version: indexVersion, Address: addr}
		r.dirty = true
	}

	if bal, err := balanceAt(ctx, node, addr, "latest"); lost(err) {
		return down(err)
	} else if err == nil {
		a := amount(bal)
		p.Balance = &a
	}
	r.fill(&p, page)
	return p
}

// fill sets the blocks won on page and the sums from the index.
func (r *Rewards) fill(p *RewardsPanel, page Page) {
	limit := page.Limit
	if limit <= 0 {
		limit = ListedWonBlocks
	}
	today, total := new(big.Int), new(big.Int)
	p.Blocks = []WonBlockRow{}
	p.WonToday, p.WonTotal, p.UnknownRewards, p.NextBefore = 0, 0, 0, nil
	var won []wonBlock
	if r.idx != nil && r.idx.Address != "" {
		won = r.idx.Blocks
	}
	p.Daily, p.Last7 = r.daily(won)
	if r.idx != nil && r.idx.Address != "" {
		addr := r.idx.Address
		p.PayoutAddress = &addr
		y, m, d := r.now().In(r.location).Date()
		for i := len(r.idx.Blocks) - 1; i >= 0; i-- {
			b := r.idx.Blocks[i]
			by, bm, bd := time.Unix(int64(b.Time), 0).In(r.location).Date()
			isToday := by == y && bm == m && bd == d
			p.WonTotal++
			if isToday {
				p.WonToday++
			}
			row := WonBlockRow{Number: b.Number, Hash: b.Hash, Time: b.Time}
			if reward, ok := new(big.Int).SetString(b.Reward, 10); ok {
				a := amount(reward)
				row.Reward = &a
				total.Add(total, reward)
				if isToday {
					today.Add(today, reward)
				}
			} else {
				p.UnknownRewards++
			}
			if page.Before != nil && b.Number >= *page.Before {
				continue
			}
			if len(p.Blocks) < limit {
				p.Blocks = append(p.Blocks, row)
			} else if p.NextBefore == nil {
				// More blocks below the page: the next one starts under
				// the last block listed.
				next := p.Blocks[len(p.Blocks)-1].Number
				p.NextBefore = &next
			}
		}
	}
	p.RewardToday, p.RewardTotal = amount(today), amount(total)
}

// load reads the index file the first time it is needed. A missing,
// unreadable or older index starts again from the first block.
func (r *Rewards) load() error {
	if r.idx != nil {
		return nil
	}
	r.idx = &index{Version: indexVersion}
	if r.nodeDir == "" {
		return nil
	}
	b, err := os.ReadFile(IndexPath(r.nodeDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", IndexFile, err)
	}
	var idx index
	if json.Unmarshal(b, &idx) != nil || idx.Version != indexVersion {
		// Read the chain again rather than trust a damaged index.
		r.dirty = true
		return nil
	}
	r.idx = &idx
	return nil
}

// save writes the index with owner-only permissions when it changed. It
// writes a new file and renames it over the old one, so a crash leaves
// either the old index or the new one.
func (r *Rewards) save() error {
	if !r.dirty || r.nodeDir == "" {
		return nil
	}
	b, err := json.Marshal(r.idx)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(r.nodeDir, ".dashboard-index-*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", IndexFile, err)
	}
	tmp := f.Name()
	err = f.Chmod(0o600)
	if err == nil {
		_, err = f.Write(b)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, IndexPath(r.nodeDir))
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", IndexFile, err)
	}
	r.dirty = false
	return nil
}

// sync reads the blocks after the last one indexed, up to head, recording
// those mined by addr. It first checks that the last block indexed is still
// on the node's chain and drops what a reorganisation replaced.
func (r *Rewards) sync(ctx context.Context, node Caller, addr string, head uint64) error {
	if !sameAddress(r.idx.Address, addr) {
		// A new payout address: its blocks have to be found from the start.
		r.idx = &index{Version: indexVersion, Address: addr}
		r.dirty = true
	}
	if last := r.idx.Last; last != nil {
		still := false
		if last.Number <= head {
			b, err := getChainBlock(ctx, node, last.Number)
			if err != nil {
				return err
			}
			still = b != nil && strings.EqualFold(b.Hash, last.Hash)
		}
		if !still {
			if err := r.rollback(ctx, node, head); err != nil {
				return err
			}
		}
	}

	// Every request reads at least one block, so the index always moves on.
	deadline := time.Now().Add(r.budget)
	for first := true; ctx.Err() == nil && (first || time.Now().Before(deadline)); first = false {
		next := uint64(0)
		if r.idx.Last != nil {
			next = r.idx.Last.Number + 1
		}
		if next > head {
			return nil
		}
		b, err := getChainBlock(ctx, node, next)
		if err != nil {
			return err
		}
		if b == nil {
			return nil
		}
		if r.idx.Last != nil && !strings.EqualFold(b.ParentHash, r.idx.Last.Hash) {
			// The chain was reorganised while we were reading it.
			if err := r.rollback(ctx, node, head); err != nil {
				return err
			}
			continue
		}
		if sameAddress(b.Miner, addr) {
			reward, err := minerReward(ctx, node, b)
			if err != nil {
				return err
			}
			r.idx.Blocks = append(r.idx.Blocks, wonBlock{Number: b.Number, Hash: b.Hash, Time: b.Time, Reward: reward})
		}
		r.idx.Last = &blockRef{Number: b.Number, Hash: b.Hash}
		r.idx.Recent = append(r.idx.Recent, *r.idx.Last)
		if extra := len(r.idx.Recent) - recentHashes; extra > 0 {
			r.idx.Recent = append([]blockRef(nil), r.idx.Recent[extra:]...)
		}
		r.dirty = true
	}
	return nil
}

// rollback finds the newest block the index remembers that is still on the
// node's chain and drops every index entry above it. When none is left the
// index starts again from the first block.
func (r *Rewards) rollback(ctx context.Context, node Caller, head uint64) error {
	r.dirty = true
	for i := len(r.idx.Recent) - 1; i >= 0; i-- {
		ref := r.idx.Recent[i]
		if ref.Number > head {
			continue
		}
		b, err := getChainBlock(ctx, node, ref.Number)
		if err != nil {
			return err
		}
		if b == nil || !strings.EqualFold(b.Hash, ref.Hash) {
			continue
		}
		keep := ref
		r.idx.Last = &keep
		r.idx.Recent = r.idx.Recent[:i+1]
		kept := r.idx.Blocks[:0]
		for _, w := range r.idx.Blocks {
			if w.Number <= keep.Number {
				kept = append(kept, w)
			}
		}
		r.idx.Blocks = kept
		return nil
	}
	r.idx.Last, r.idx.Recent, r.idx.Blocks = nil, nil, nil
	return nil
}

type chainBlock struct {
	Number     uint64
	Hash       string
	ParentHash string
	Time       uint64
	Miner      string
}

// getChainBlock reads the header fields the index needs. It returns nil
// without an error when the node does not have that block.
func getChainBlock(ctx context.Context, node Caller, number uint64) (*chainBlock, error) {
	var raw *struct {
		Number     string `json:"number"`
		Hash       string `json:"hash"`
		ParentHash string `json:"parentHash"`
		Timestamp  string `json:"timestamp"`
		Miner      string `json:"miner"`
	}
	if err := node.Call(ctx, &raw, "eth_getBlockByNumber", blockTag(number), false); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}
	n, err := parseQuantity(raw.Number)
	if err != nil {
		return nil, fmt.Errorf("block number: %w", err)
	}
	ts, err := parseQuantity(raw.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("block timestamp: %w", err)
	}
	if n != number || raw.Hash == "" {
		return nil, fmt.Errorf("%w: asked for block %d, got block %d with hash %q", errBadQuantity, number, n, raw.Hash)
	}
	return &chainBlock{Number: n, Hash: raw.Hash, ParentHash: raw.ParentHash, Time: ts, Miner: raw.Miner}, nil
}

// minerReward is the miner's balance change across block b, in wei. It is
// "" when the node cannot tell, for example because it no longer keeps the
// balances of old blocks.
func minerReward(ctx context.Context, node Caller, b *chainBlock) (string, error) {
	if b.Number == 0 {
		return "", nil
	}
	after, err := balanceAt(ctx, node, b.Miner, blockTag(b.Number))
	if lost(err) {
		return "", err
	}
	if err != nil {
		return "", nil
	}
	before, err := balanceAt(ctx, node, b.Miner, blockTag(b.Number-1))
	if lost(err) {
		return "", err
	}
	if err != nil {
		return "", nil
	}
	return new(big.Int).Sub(after, before).String(), nil
}

func balanceAt(ctx context.Context, node Caller, addr, tag string) (*big.Int, error) {
	var hex string
	if err := node.Call(ctx, &hex, "eth_getBalance", addr, tag); err != nil {
		return nil, err
	}
	n, ok := parseBig(hex)
	if !ok {
		return nil, fmt.Errorf("eth_getBalance: %w: %q", errBadQuantity, hex)
	}
	return n, nil
}

func parseBig(s string) (*big.Int, bool) {
	if !strings.HasPrefix(s, "0x") || len(s) < 3 {
		return nil, false
	}
	n, ok := new(big.Int).SetString(s[2:], 16)
	if !ok || n.Sign() < 0 {
		return nil, false
	}
	return n, true
}

func blockTag(n uint64) string {
	return "0x" + strconv.FormatUint(n, 16)
}

func amount(wei *big.Int) Amount {
	return Amount{Wei: wei.String(), Coins: Coins(wei)}
}

// Coins writes an amount in wei as whole coins (18 decimals) without
// trailing zeros: 2500000000000000000 is "2.5".
func Coins(wei *big.Int) string {
	abs := new(big.Int).Abs(wei)
	whole, frac := new(big.Int).QuoRem(abs, weiPerCoin, new(big.Int))
	s := whole.String()
	if frac.Sign() != 0 {
		digits := frac.String()
		digits = strings.Repeat("0", 18-len(digits)) + digits
		s += "." + strings.TrimRight(digits, "0")
	}
	if wei.Sign() < 0 {
		s = "-" + s
	}
	return s
}
