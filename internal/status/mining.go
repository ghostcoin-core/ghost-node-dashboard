package status

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ghostcoin-core/ghost-node-dashboard/internal/ipc"
)

// MinerThreadsFile is the file in the node directory that holds how many
// cores the node mines on.
const MinerThreadsFile = "miner-threads"

// MiningPanel is what /api/mining returns. A field is null when the node is
// not running or does not offer it.
type MiningPanel struct {
	Running bool   `json:"running"`
	Error   string `json:"error,omitempty"`
	Mining  *bool  `json:"mining"`
	// CoresUsed is from the miner-threads file, else every logical core.
	CoresUsed        int  `json:"coresUsed"`
	CoresFromSetting bool `json:"coresFromSetting"`
	LogicalCores     int  `json:"logicalCores"`
	// Paused is true when <node-dir>/mining-paused exists.
	Paused bool `json:"paused"`
	// HashRate is in hashes per second; null when zero or not offered.
	HashRate      *float64 `json:"hashRate"`
	PayoutAddress *string  `json:"payoutAddress"`
	// NetworkShare (0 to 1) and ExpectedBlockSeconds are estimates from the
	// hash rate, the latest difficulty and the target block time.
	NetworkShare         *float64 `json:"networkShare"`
	ExpectedBlockSeconds *float64 `json:"expectedBlockSeconds"`
	TargetBlockSeconds   float64  `json:"targetBlockSeconds"`
	// HashRateTrend is the latest hash rates, oldest first. Only a
	// HashTrend fills it; it is empty, never null, otherwise.
	HashRateTrend []HashSample `json:"hashRateTrend"`
}

// MiningEnv is what the mining panel reads besides the node itself.
type MiningEnv struct {
	NodeDir      string
	LogicalCores int
}

// FetchMining reads the mining panel. It never fails: a stopped node is
// reported in the result.
func FetchMining(ctx context.Context, node Caller, env MiningEnv) MiningPanel {
	p := MiningPanel{LogicalCores: env.LogicalCores, TargetBlockSeconds: TargetBlockTime.Seconds(), HashRateTrend: []HashSample{}}
	p.CoresUsed = p.LogicalCores
	if n, ok := minerThreads(env.NodeDir); ok {
		p.CoresUsed, p.CoresFromSetting = n, true
	}
	p.Paused = MiningPaused(env.NodeDir)
	down := func(err error) MiningPanel {
		return MiningPanel{
			Error:              downMessage(err),
			CoresUsed:          p.CoresUsed,
			CoresFromSetting:   p.CoresFromSetting,
			LogicalCores:       p.LogicalCores,
			Paused:             p.Paused,
			TargetBlockSeconds: p.TargetBlockSeconds,
			HashRateTrend:      []HashSample{},
		}
	}

	if _, err := probe(ctx, node); err != nil {
		return down(err)
	}
	p.Running = true

	var mining bool
	if err := node.Call(ctx, &mining, "eth_mining"); lost(err) {
		return down(err)
	} else if err == nil {
		p.Mining = &mining
	}

	rate, err := hashRate(ctx, node)
	if lost(err) {
		return down(err)
	}
	p.HashRate = rate

	if addr, err := coinbase(ctx, node); lost(err) {
		return down(err)
	} else if addr != "" {
		p.PayoutAddress = &addr
	}

	if p.HashRate == nil {
		return p
	}
	head, err := quantity(ctx, node, "eth_blockNumber")
	if lost(err) {
		return down(err)
	}
	if head == nil {
		return p
	}
	latest, err := getBlock(ctx, node, *head)
	if lost(err) {
		return down(err)
	}
	if latest == nil || latest.Difficulty == nil || *latest.Difficulty == 0 {
		return p
	}
	share := *p.HashRate / networkHashRate(*latest.Difficulty)
	if share > 1 {
		share = 1
	}
	// One block in every 1/share is ours, so the wait is the target block
	// time divided by the share.
	wait := TargetBlockTime.Seconds() / share
	p.NetworkShare, p.ExpectedBlockSeconds = &share, &wait
	return p
}

// hashRate is the node's own hash rate: the RandomX API's when the node
// offers it, else eth_hashrate. It is nil when zero or not offered.
func hashRate(ctx context.Context, node Caller) (*float64, error) {
	for _, method := range []string{"randomx_hashrate", "eth_hashrate"} {
		var raw json.RawMessage
		err := node.Call(ctx, &raw, method)
		var rpcErr *ipc.RPCError
		if errors.As(err, &rpcErr) {
			continue
		}
		if err != nil {
			return nil, err
		}
		rate, ok := parseRate(raw)
		if !ok || rate == 0 {
			return nil, nil
		}
		return &rate, nil
	}
	return nil, nil
}

// minerThreads reads <node-dir>/miner-threads.
func minerThreads(nodeDir string) (int, bool) {
	if nodeDir == "" {
		return 0, false
	}
	b, err := os.ReadFile(filepath.Join(nodeDir, MinerThreadsFile))
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}
