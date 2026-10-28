package status

import (
	"context"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ghostcoin-core/ghost-node-dashboard/internal/ipc"
	"github.com/ghostcoin-core/ghost-node-dashboard/internal/ipc/ipctest"
)

const (
	ownAddr   = "0x00000000000000000000000000000000000000aa"
	otherAddr = "0x1111111111111111111111111111111111112222"
	chainBase = 1_700_000_000
)

// fakeBlock is one block of a fake chain. Blocks of a fork have another
// fork name and so another hash.
type fakeBlock struct {
	miner string
	time  uint64
	fork  string
}

// fakeChain is a node whose chain the test can extend and reorganise. Each
// block won pays 2 coins plus the block number in thousandths of a coin, so
// a reward cannot be a fixed number.
type fakeChain struct {
	mu     sync.Mutex
	blocks []fakeBlock
	reads  int
}

func reward(n uint64) *big.Int {
	r := new(big.Int).Mul(big.NewInt(2000+int64(n)), big.NewInt(1_000_000_000_000_000))
	return r
}

func (c *fakeChain) hash(n uint64) string {
	if n >= uint64(len(c.blocks)) {
		return ""
	}
	return "0xb" + c.blocks[n].fork + "-" + strconv.FormatUint(n, 10)
}

// grow adds blocks up to height; own says which the node mines.
func (c *fakeChain) grow(height uint64, fork string, own func(uint64) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for n := uint64(len(c.blocks)); n <= height; n++ {
		miner := otherAddr
		if own(n) {
			miner = ownAddr
		}
		c.blocks = append(c.blocks, fakeBlock{miner: miner, time: chainBase + 12*n, fork: fork})
	}
}

// reorg drops the blocks from height on.
func (c *fakeChain) reorg(from uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blocks = c.blocks[:from]
}

func (c *fakeChain) setTime(n, t uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blocks[n].time = t
}

func (c *fakeChain) blockReads() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads
}

func (c *fakeChain) balance(addr string, at uint64) *big.Int {
	sum := new(big.Int)
	for n := uint64(0); n <= at && n < uint64(len(c.blocks)); n++ {
		if n > 0 && strings.EqualFold(c.blocks[n].miner, addr) {
			sum.Add(sum, reward(n))
		}
	}
	return sum
}

func (c *fakeChain) handlers() map[string]ipctest.Handler {
	return map[string]ipctest.Handler{
		"web3_clientVersion": ipctest.Value("Ghost/test"),
		"eth_coinbase":       ipctest.Value(strings.ToUpper(ownAddr[:2]) + ownAddr[2:]),
		"eth_blockNumber": func(json.RawMessage) (any, *ipctest.Error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			return blockTag(uint64(len(c.blocks) - 1)), nil
		},
		"eth_getBlockByNumber": func(params json.RawMessage) (any, *ipctest.Error) {
			var args []any
			if err := json.Unmarshal(params, &args); err != nil || len(args) != 2 {
				return nil, &ipctest.Error{Code: -32602, Message: "bad params"}
			}
			tag, _ := args[0].(string)
			n, err := strconv.ParseUint(strings.TrimPrefix(tag, "0x"), 16, 64)
			c.mu.Lock()
			defer c.mu.Unlock()
			c.reads++
			if err != nil || n >= uint64(len(c.blocks)) {
				return nil, nil
			}
			parent := "0x0"
			if n > 0 {
				parent = c.hash(n - 1)
			}
			return map[string]string{
				"number":     tag,
				"hash":       c.hash(n),
				"parentHash": parent,
				"timestamp":  blockTag(c.blocks[n].time),
				"miner":      c.blocks[n].miner,
			}, nil
		},
		"eth_getBalance": func(params json.RawMessage) (any, *ipctest.Error) {
			var args []string
			if err := json.Unmarshal(params, &args); err != nil || len(args) != 2 {
				return nil, &ipctest.Error{Code: -32602, Message: "bad params"}
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			at := uint64(len(c.blocks) - 1)
			if args[1] != "latest" {
				n, err := strconv.ParseUint(strings.TrimPrefix(args[1], "0x"), 16, 64)
				if err != nil {
					return nil, &ipctest.Error{Code: -32602, Message: "bad block"}
				}
				at = n
			}
			return "0x" + c.balance(args[0], at).Text(16), nil
		},
	}
}

func startChain(t *testing.T, c *fakeChain) *ipc.Client {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "geth.ipc")
	fake, err := ipctest.Start(sock, c.handlers())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fake.Close)
	node := ipc.New(sock)
	t.Cleanup(func() { _ = node.Close() })
	return node
}

// downNode is a client for a node that is not running.
func downNode(t *testing.T) *ipc.Client {
	t.Helper()
	node := ipc.New(filepath.Join(t.TempDir(), "geth.ipc"))
	t.Cleanup(func() { _ = node.Close() })
	return node
}

func everyFifth(n uint64) bool { return n > 0 && n%5 == 0 }

func wonNumbers(p RewardsPanel) []uint64 {
	var out []uint64
	for _, b := range p.Blocks {
		out = append(out, b.Number)
	}
	return out
}

func TestBlocksWonIndex(t *testing.T) {
	chain := &fakeChain{}
	chain.grow(30, "a", everyFifth)
	node := startChain(t, chain)
	dir := t.TempDir()
	now := func() time.Time { return time.Unix(chainBase+12*40, 0) }

	p := NewRewards(RewardsEnv{NodeDir: dir, Now: now, Location: time.UTC}).Fetch(context.Background(), node)
	if !p.Running || p.Error != "" || p.IndexError != "" {
		t.Fatalf("running %v, error %q, index error %q", p.Running, p.Error, p.IndexError)
	}
	if got, want := wonNumbers(p), []uint64{30, 25, 20, 15, 10, 5}; !slices.Equal(got, want) {
		t.Fatalf("blocks won %v, want %v", got, want)
	}
	if p.Indexing != nil {
		t.Errorf("indexing = %+v after reading the whole chain", p.Indexing)
	}
	first := p.Blocks[0]
	if first.Hash != "0xba-30" || first.Time != chainBase+12*30 || first.Reward == nil || first.Reward.Coins != "2.03" {
		t.Errorf("block 30 = %+v, reward %+v; want hash 0xba-30, reward 2.03", first, first.Reward)
	}
	if p.WonTotal != 6 || p.PayoutAddress == nil || !strings.EqualFold(*p.PayoutAddress, ownAddr) {
		t.Errorf("won total %d, payout %v", p.WonTotal, p.PayoutAddress)
	}
	if p.Balance == nil || p.Balance.Wei != chain.balance(ownAddr, 30).String() {
		t.Errorf("balance %+v, want %s wei", p.Balance, chain.balance(ownAddr, 30))
	}

	path := IndexPath(dir)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("index not written: %v", err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("index permissions %v, want 0600", fi.Mode().Perm())
	}
	var saved index
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatalf("index is not JSON: %v", err)
	}
	if saved.Last == nil || saved.Last.Number != 30 || saved.Last.Hash != "0xba-30" || len(saved.Blocks) != 6 {
		t.Fatalf("saved index: last %+v, %d blocks", saved.Last, len(saved.Blocks))
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".dashboard-index-*")); len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}

	// A restart with ten new blocks reads only those, plus the last block
	// indexed to check it is still on the chain.
	chain.grow(40, "a", everyFifth)
	before := chain.blockReads()
	p = NewRewards(RewardsEnv{NodeDir: dir, Now: now, Location: time.UTC}).Fetch(context.Background(), node)
	if reads := chain.blockReads() - before; reads != 11 {
		t.Errorf("restart read %d blocks, want 11 (block 30 again, then 31 to 40)", reads)
	}
	if got, want := wonNumbers(p), []uint64{40, 35, 30, 25, 20, 15, 10, 5}; !slices.Equal(got, want) {
		t.Errorf("after restart blocks won %v, want %v", got, want)
	}

	// The list follows new blocks as they arrive.
	r := NewRewards(RewardsEnv{NodeDir: dir, Now: now, Location: time.UTC})
	r.Fetch(context.Background(), node)
	chain.grow(45, "a", everyFifth)
	if p = r.Fetch(context.Background(), node); p.WonTotal != 9 || p.Blocks[0].Number != 45 {
		t.Errorf("after block 45: won %d, newest %v", p.WonTotal, wonNumbers(p))
	}
}

func TestBlocksWonIndexInParts(t *testing.T) {
	// With no time to read, each request reads one block and the panel
	// says how far it got; the next request carries on from there.
	chain := &fakeChain{}
	chain.grow(12, "a", everyFifth)
	node := startChain(t, chain)
	r := NewRewards(RewardsEnv{NodeDir: t.TempDir(), Budget: time.Nanosecond})
	p := r.Fetch(context.Background(), node)
	if p.Indexing == nil || p.Indexing.Height != 12 {
		t.Fatalf("indexing = %+v, want progress towards 12", p.Indexing)
	}
	for i := 0; i < 20 && p.Indexing != nil; i++ {
		p = r.Fetch(context.Background(), node)
	}
	if p.Indexing != nil {
		t.Fatalf("still indexing: %+v", p.Indexing)
	}
	if got, want := wonNumbers(p), []uint64{10, 5}; !slices.Equal(got, want) {
		t.Errorf("blocks won %v, want %v", got, want)
	}
}

func TestReorgDropsIndex(t *testing.T) {
	chain := &fakeChain{}
	chain.grow(30, "a", everyFifth)
	node := startChain(t, chain)
	dir := t.TempDir()
	r := NewRewards(RewardsEnv{NodeDir: dir})
	if p := r.Fetch(context.Background(), node); p.WonTotal != 6 {
		t.Fatalf("won %d before the reorganisation, want 6", p.WonTotal)
	}

	// From block 22 on another chain wins: blocks 25 and 30 are no longer
	// ours, and on the new chain we won 23 and 31.
	chain.reorg(22)
	chain.grow(31, "b", func(n uint64) bool { return n == 23 || n == 31 || (n < 22 && everyFifth(n)) })

	p := r.Fetch(context.Background(), node)
	if got, want := wonNumbers(p), []uint64{31, 23, 20, 15, 10, 5}; !slices.Equal(got, want) {
		t.Fatalf("blocks won %v, want %v", got, want)
	}
	if p.Blocks[1].Hash != "0xbb-23" {
		t.Errorf("block 23 hash %q, want the new chain's", p.Blocks[1].Hash)
	}
	want := new(big.Int)
	for _, n := range []uint64{5, 10, 15, 20, 23, 31} {
		want.Add(want, reward(n))
	}
	if p.RewardTotal.Wei != want.String() {
		t.Errorf("total %s, want %s", p.RewardTotal.Wei, want)
	}

	// The saved index matches, so a restart keeps the new chain's blocks.
	p = NewRewards(RewardsEnv{NodeDir: dir}).Fetch(context.Background(), node)
	if got, want := wonNumbers(p), []uint64{31, 23, 20, 15, 10, 5}; !slices.Equal(got, want) {
		t.Errorf("after restart blocks won %v, want %v", got, want)
	}

	// A chain that is now shorter than the index (the node was reset to a
	// shorter chain) is handled the same way.
	chain.reorg(12)
	chain.grow(14, "c", everyFifth)
	p = r.Fetch(context.Background(), node)
	if got, want := wonNumbers(p), []uint64{10, 5}; !slices.Equal(got, want) {
		t.Errorf("shorter chain: blocks won %v, want %v", got, want)
	}

	// When no block the index remembers is left, it reads everything again.
	chain.reorg(0)
	chain.grow(6, "d", func(n uint64) bool { return n == 3 })
	p = r.Fetch(context.Background(), node)
	if got, want := wonNumbers(p), []uint64{3}; !slices.Equal(got, want) {
		t.Errorf("new chain: blocks won %v, want %v", got, want)
	}
}

func TestRewardsTodayAndTotal(t *testing.T) {
	loc := time.FixedZone("test", 2*3600)
	now := time.Date(2026, 3, 10, 15, 0, 0, 0, loc)
	midnight := uint64(time.Date(2026, 3, 10, 0, 0, 0, 0, loc).Unix())

	chain := &fakeChain{}
	chain.grow(20, "a", everyFifth)
	// Block 5 is one second before local midnight, so yesterday; blocks 10
	// and 15 are today (block 10 would be yesterday in UTC); block 20 is
	// tomorrow.
	chain.setTime(5, midnight-1)
	chain.setTime(10, midnight)
	chain.setTime(15, midnight+3600)
	chain.setTime(20, midnight+86400)
	node := startChain(t, chain)

	p := NewRewards(RewardsEnv{NodeDir: t.TempDir(), Now: func() time.Time { return now }, Location: loc}).
		Fetch(context.Background(), node)
	today := new(big.Int).Add(reward(10), reward(15))
	total := new(big.Int).Add(today, new(big.Int).Add(reward(5), reward(20)))
	if p.WonToday != 2 || p.RewardToday.Wei != today.String() {
		t.Errorf("today: %d blocks, %s wei; want 2, %s", p.WonToday, p.RewardToday.Wei, today)
	}
	if p.WonTotal != 4 || p.RewardTotal.Wei != total.String() {
		t.Errorf("total: %d blocks, %s wei; want 4, %s", p.WonTotal, p.RewardTotal.Wei, total)
	}
	if want := Coins(total); p.RewardTotal.Coins != want || want != "8.05" {
		t.Errorf("total coins %q, want 8.05", p.RewardTotal.Coins)
	}
}

func TestRewardsUnknownWhenNodeHasNoOldBalances(t *testing.T) {
	chain := &fakeChain{}
	chain.grow(10, "a", everyFifth)
	h := chain.handlers()
	balances := h["eth_getBalance"]
	h["eth_getBalance"] = func(params json.RawMessage) (any, *ipctest.Error) {
		if !strings.Contains(string(params), "latest") {
			return nil, &ipctest.Error{Code: -32000, Message: "missing trie node"}
		}
		return balances(params)
	}
	sock := filepath.Join(t.TempDir(), "geth.ipc")
	fake, err := ipctest.Start(sock, h)
	if err != nil {
		t.Fatal(err)
	}
	defer fake.Close()
	node := ipc.New(sock)
	defer node.Close()

	p := NewRewards(RewardsEnv{NodeDir: t.TempDir()}).Fetch(context.Background(), node)
	if p.WonTotal != 2 || p.UnknownRewards != 2 || p.RewardTotal.Wei != "0" || p.Blocks[0].Reward != nil {
		t.Errorf("won %d, unknown %d, total %s, newest reward %v", p.WonTotal, p.UnknownRewards, p.RewardTotal.Wei, p.Blocks[0].Reward)
	}
	if p.Balance == nil {
		t.Error("balance missing")
	}
}

func TestRewardsNodeDownKeepsIndex(t *testing.T) {
	chain := &fakeChain{}
	chain.grow(10, "a", everyFifth)
	sock := filepath.Join(t.TempDir(), "geth.ipc")
	fake, err := ipctest.Start(sock, chain.handlers())
	if err != nil {
		t.Fatal(err)
	}
	node := ipc.New(sock)
	defer node.Close()
	dir := t.TempDir()
	NewRewards(RewardsEnv{NodeDir: dir}).Fetch(context.Background(), node)
	fake.Close()

	p := NewRewards(RewardsEnv{NodeDir: dir}).Fetch(context.Background(), node)
	if p.Running || p.Error != "node not running" {
		t.Errorf("running %v, error %q", p.Running, p.Error)
	}
	if got, want := wonNumbers(p), []uint64{10, 5}; !slices.Equal(got, want) || p.Balance != nil {
		t.Errorf("blocks won %v (want %v), balance %v", got, want, p.Balance)
	}
}

func TestCoins(t *testing.T) {
	for wei, want := range map[string]string{
		"0":                    "0",
		"1":                    "0.000000000000000001",
		"2500000000000000000":  "2.5",
		"12000000000000000000": "12",
		"-1500000000000000000": "-1.5",
	} {
		n, _ := new(big.Int).SetString(wei, 10)
		if got := Coins(n); got != want {
			t.Errorf("Coins(%s) = %q, want %q", wei, got, want)
		}
	}
}
