package status

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ghostcoin-core/ghost-node-dashboard/internal/ipc"
)

// TargetBlockTime is the block time the network aims for. The estimates
// (network hash rate, share and time to the next block) are worked out from
// it.
const TargetBlockTime = 15 * time.Second

// GhostTestChainID is the chain ID of the Ghost test network.
const GhostTestChainID = 6777972

// NetworkName is the plain name of a chain.
func NetworkName(chainID uint64) string {
	if chainID == GhostTestChainID {
		return "Ghost test network"
	}
	return "Chain ID " + strconv.FormatUint(chainID, 10)
}

// probe asks the node for its client version. The error is only set when
// the node could not be reached; a node that does not offer the method still
// counts as running, with a nil version.
func probe(ctx context.Context, node Caller) (*string, error) {
	var version string
	if err := node.Call(ctx, &version, "web3_clientVersion"); err != nil {
		var rpcErr *ipc.RPCError
		if errors.As(err, &rpcErr) {
			return nil, nil
		}
		return nil, err
	}
	return &version, nil
}

func quantity(ctx context.Context, node Caller, method string, params ...any) (*uint64, error) {
	var hex string
	if err := node.Call(ctx, &hex, method, params...); err != nil {
		return nil, err
	}
	n, err := parseQuantity(hex)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	return &n, nil
}

// coinbase is the node's payout address, or "" when it has none.
func coinbase(ctx context.Context, node Caller) (string, error) {
	var addr string
	if err := node.Call(ctx, &addr, "eth_coinbase"); err != nil {
		return "", err
	}
	return addr, nil
}

type block struct {
	Number     uint64
	Time       uint64
	Miner      string
	Difficulty *uint64
}

// getBlock reads one block header by number. It returns nil without an error
// when the node does not have that block.
func getBlock(ctx context.Context, node Caller, number uint64) (*block, error) {
	var raw *struct {
		Number     string `json:"number"`
		Timestamp  string `json:"timestamp"`
		Miner      string `json:"miner"`
		Difficulty string `json:"difficulty"`
	}
	if err := node.Call(ctx, &raw, "eth_getBlockByNumber", "0x"+strconv.FormatUint(number, 16), false); err != nil {
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
	b := &block{Number: n, Time: ts, Miner: raw.Miner}
	if d, err := parseQuantity(raw.Difficulty); err == nil {
		b.Difficulty = &d
	}
	return b, nil
}

// networkHashRate estimates the network's hashes per second from the
// difficulty of the latest block.
func networkHashRate(difficulty uint64) float64 {
	return float64(difficulty) / TargetBlockTime.Seconds()
}

// ShortAddress is the 0x1234…abcd form of an address.
func ShortAddress(addr string) string {
	if len(addr) <= 12 {
		return addr
	}
	return addr[:6] + "…" + addr[len(addr)-4:]
}

func sameAddress(a, b string) bool {
	return a != "" && strings.EqualFold(a, b)
}

// Uptime remembers when the node was first seen answering after it was down.
// The zero value is ready to use.
type Uptime struct {
	mu    sync.Mutex
	up    bool
	since time.Time
}

// Seen records whether the node answered at now and returns since when it
// has been answering (the zero time when it is down).
func (u *Uptime) Seen(running bool, now time.Time) time.Time {
	if u == nil {
		return time.Time{}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if !running {
		u.up = false
		u.since = time.Time{}
		return u.since
	}
	if !u.up {
		u.up = true
		u.since = now
	}
	return u.since
}

// parseRate reads a hash rate given as a hex quantity or a plain number.
func parseRate(raw json.RawMessage) (float64, bool) {
	var hex string
	if err := json.Unmarshal(raw, &hex); err == nil {
		n, err := parseQuantity(hex)
		if err != nil {
			return 0, false
		}
		return float64(n), true
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil && f >= 0 {
		return f, true
	}
	return 0, false
}
