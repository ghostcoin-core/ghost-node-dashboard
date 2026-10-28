package status

import "context"

// RecentBlocks is how many of the latest blocks the network panel lists.
const RecentBlocks = 20

// AverageOver is how many blocks the average block time is taken over.
const AverageOver = 100

// NetworkPanel is what /api/network returns. A field is null when the node
// is not running or does not offer it.
type NetworkPanel struct {
	Running bool    `json:"running"`
	Error   string  `json:"error,omitempty"`
	Height  *uint64 `json:"height"`
	// AverageBlockSeconds is taken over AverageOverBlocks blocks, which is
	// fewer than 100 while the chain is younger than that.
	AverageBlockSeconds *float64 `json:"averageBlockSeconds"`
	AverageOverBlocks   *uint64  `json:"averageOverBlocks"`
	Difficulty          *uint64  `json:"difficulty"`
	// NetworkHashRate is an estimate: difficulty ÷ target block time.
	NetworkHashRate    *float64   `json:"networkHashRate"`
	TargetBlockSeconds float64    `json:"targetBlockSeconds"`
	Blocks             []BlockRow `json:"blocks"`
}

// BlockRow is one of the latest blocks. Own is set for blocks the node mined.
type BlockRow struct {
	Number     uint64 `json:"number"`
	Time       uint64 `json:"time"`
	Miner      string `json:"miner"`
	MinerShort string `json:"minerShort"`
	Own        bool   `json:"own"`
}

// FetchNetwork reads the network panel. It never fails: a stopped node is
// reported in the result.
func FetchNetwork(ctx context.Context, node Caller) NetworkPanel {
	p := NetworkPanel{TargetBlockSeconds: TargetBlockTime.Seconds()}
	down := func(err error) NetworkPanel {
		return NetworkPanel{Error: downMessage(err), TargetBlockSeconds: p.TargetBlockSeconds}
	}

	if _, err := probe(ctx, node); err != nil {
		return down(err)
	}
	p.Running = true

	var err error
	if p.Height, err = quantity(ctx, node, "eth_blockNumber"); lost(err) {
		return down(err)
	}
	if p.Height == nil {
		return p
	}
	height := *p.Height

	own, err := coinbase(ctx, node)
	if lost(err) {
		return down(err)
	}

	p.Blocks = []BlockRow{}
	var latest *block
	for i := uint64(0); i < RecentBlocks && i <= height; i++ {
		b, err := getBlock(ctx, node, height-i)
		if lost(err) {
			return down(err)
		}
		if b == nil {
			continue
		}
		if latest == nil {
			latest = b
		}
		p.Blocks = append(p.Blocks, BlockRow{
			Number:     b.Number,
			Time:       b.Time,
			Miner:      b.Miner,
			MinerShort: ShortAddress(b.Miner),
			Own:        sameAddress(own, b.Miner),
		})
	}
	if latest == nil {
		return p
	}

	if latest.Difficulty != nil {
		p.Difficulty = latest.Difficulty
		rate := networkHashRate(*latest.Difficulty)
		p.NetworkHashRate = &rate
	}

	span := min(uint64(AverageOver), latest.Number)
	if span == 0 {
		return p
	}
	older, err := getBlock(ctx, node, latest.Number-span)
	if lost(err) {
		return down(err)
	}
	if older != nil && latest.Time >= older.Time {
		avg := float64(latest.Time-older.Time) / float64(span)
		p.AverageBlockSeconds, p.AverageOverBlocks = &avg, &span
	}
	return p
}
