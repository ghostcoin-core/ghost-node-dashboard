package status

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ghostcoin-core/ghost-node-dashboard/internal/ipc"
	"github.com/ghostcoin-core/ghost-node-dashboard/internal/ipc/ipctest"
)

// startHandlers runs a fake node with handlers and returns a client for it.
func startHandlers(t *testing.T, h map[string]ipctest.Handler) *ipc.Client {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "geth.ipc")
	fake, err := ipctest.Start(sock, h)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fake.Close)
	node := ipc.New(sock)
	t.Cleanup(func() { _ = node.Close() })
	return node
}

func wallNumbers(p NetworkPanel) []uint64 {
	var out []uint64
	for _, b := range p.Wall {
		out = append(out, b.Number)
	}
	return out
}

// countDown is the numbers from high down to low.
func countDown(high, low uint64) []uint64 {
	var out []uint64
	for n := high; n+1 > low; n-- {
		out = append(out, n)
	}
	return out
}

func TestBlockWall(t *testing.T) {
	ctx := context.Background()
	// The panel reads the latest blocks and one more for the average block
	// time on every request.
	const perRequest = RecentBlocks + 1

	t.Run("cap and cache", func(t *testing.T) {
		chain := &fakeChain{}
		chain.grow(300, "a", everyFifth)
		node := startChain(t, chain)
		n := NewNetwork(NetworkEnv{})

		p := n.Fetch(ctx, node)
		if !p.Running || len(p.Blocks) != RecentBlocks {
			t.Fatalf("running %v, %d blocks", p.Running, len(p.Blocks))
		}
		if got, want := wallNumbers(p), countDown(300, 61); !slices.Equal(got, want) {
			t.Fatalf("wall %v, want 300 down to 61 (%d blocks)", got, WallBlocks)
		}
		for _, b := range p.Wall {
			wantShort := ShortAddress(otherAddr)
			if everyFifth(b.Number) {
				wantShort = ShortAddress(ownAddr)
			}
			if b.Own != everyFifth(b.Number) || b.MinerShort != wantShort || b.Time != chainBase+12*b.Number || b.LostRace {
				t.Errorf("wall block %+v", b)
			}
		}
		if reads := chain.blockReads(); reads != WallBlocks+1 {
			t.Errorf("first request read %d blocks, want %d", reads, WallBlocks+1)
		}

		// A second request reads only the latest blocks again.
		before := chain.blockReads()
		p = n.Fetch(ctx, node)
		if reads := chain.blockReads() - before; reads != perRequest {
			t.Errorf("second request read %d blocks, want %d", reads, perRequest)
		}
		if len(p.Wall) != WallBlocks {
			t.Errorf("second request: %d wall blocks", len(p.Wall))
		}

		// New blocks move the wall on without reading the older ones.
		chain.grow(305, "a", everyFifth)
		before = chain.blockReads()
		p = n.Fetch(ctx, node)
		if reads := chain.blockReads() - before; reads != perRequest {
			t.Errorf("after 5 new blocks read %d blocks, want %d", reads, perRequest)
		}
		if got, want := wallNumbers(p), countDown(305, 66); !slices.Equal(got, want) {
			t.Errorf("wall after new blocks %v", got)
		}
	})

	t.Run("budget", func(t *testing.T) {
		chain := &fakeChain{}
		chain.grow(300, "a", everyFifth)
		node := startChain(t, chain)
		n := NewNetwork(NetworkEnv{Budget: time.Nanosecond})

		// With no time to read, a request adds one older block to those
		// the panel read.
		p := n.Fetch(ctx, node)
		if got, want := wallNumbers(p), countDown(300, 280); !slices.Equal(got, want) {
			t.Fatalf("first wall %v, want 300 down to 280", got)
		}
		before := chain.blockReads()
		p = n.Fetch(ctx, node)
		if got, want := wallNumbers(p), countDown(300, 279); !slices.Equal(got, want) {
			t.Errorf("second wall %v, want 300 down to 279", got)
		}
		if reads := chain.blockReads() - before; reads != perRequest+1 {
			t.Errorf("second request read %d blocks, want %d", reads, perRequest+1)
		}
	})

	t.Run("reorganisation", func(t *testing.T) {
		chain := &fakeChain{}
		chain.grow(300, "a", everyFifth)
		h := chain.handlers()
		blocks := h["eth_getBlockByNumber"]
		var mu sync.Mutex
		hidden := ""
		h["eth_getBlockByNumber"] = func(params json.RawMessage) (any, *ipctest.Error) {
			mu.Lock()
			skip := hidden != "" && strings.Contains(string(params), `"`+hidden+`"`)
			mu.Unlock()
			if skip {
				return nil, nil
			}
			return blocks(params)
		}
		node := startHandlers(t, h)
		n := NewNetwork(NetworkEnv{})
		n.Fetch(ctx, node)

		// From block 295 on another chain wins, and the node mined all of
		// its blocks. Block 299 is not served on the next poll: the cached
		// one, from the old chain, must not be shown in its place.
		chain.reorg(295)
		chain.grow(301, "b", func(n uint64) bool { return n >= 295 || everyFifth(n) })
		mu.Lock()
		hidden = blockTag(299)
		mu.Unlock()
		p := n.Fetch(ctx, node)
		want := append(countDown(301, 300), countDown(298, 62)...)
		if got := wallNumbers(p); !slices.Equal(got, want) {
			t.Fatalf("wall %v, want %v", got, want)
		}
		for _, b := range p.Wall[:6] {
			if !b.Own {
				t.Errorf("block %d of the new chain not shown as own", b.Number)
			}
		}
		if p.Wall[7].Number != 293 || p.Wall[7].Own {
			t.Errorf("block 293 = %+v, want not own", p.Wall[7])
		}
		if n.cache[296].hash != "0xbb-296" {
			t.Errorf("cached block 296 hash %q, want the new chain's", n.cache[296].hash)
		}

		// Once the node serves it again, block 299 comes from the new chain.
		mu.Lock()
		hidden = ""
		mu.Unlock()
		p = n.Fetch(ctx, node)
		if b := p.Wall[2]; b.Number != 299 || !b.Own || len(p.Wall) != WallBlocks {
			t.Errorf("block 299 = %+v, wall of %d", b, len(p.Wall))
		}
	})

	t.Run("own follows the payout address", func(t *testing.T) {
		chain := &fakeChain{}
		chain.grow(260, "a", everyFifth)
		h := chain.handlers()
		var mu sync.Mutex
		payout := ownAddr
		h["eth_coinbase"] = func(json.RawMessage) (any, *ipctest.Error) {
			mu.Lock()
			defer mu.Unlock()
			return payout, nil
		}
		node := startHandlers(t, h)
		n := NewNetwork(NetworkEnv{})
		p := n.Fetch(ctx, node)
		if !p.Wall[0].Own || p.Wall[1].Own || len(p.Wall) != WallBlocks {
			t.Fatalf("wall starts %+v, %+v", p.Wall[0], p.Wall[1])
		}

		mu.Lock()
		payout = otherAddr
		mu.Unlock()
		p = n.Fetch(ctx, node)
		for _, b := range p.Wall {
			if b.Own == everyFifth(b.Number) {
				t.Errorf("block %d own = %v after the payout address changed", b.Number, b.Own)
			}
		}
	})

	t.Run("node down", func(t *testing.T) {
		p := NewNetwork(NetworkEnv{}).Fetch(ctx, downNode(t))
		if p.Running || p.Wall != nil {
			t.Errorf("running %v, wall %v", p.Running, p.Wall)
		}
	})
}

func TestBlockTransactions(t *testing.T) {
	// Even blocks list n transactions; odd blocks come without the list.
	node := startHandlers(t, map[string]ipctest.Handler{
		"web3_clientVersion": ipctest.Value("Ghost/test"),
		"eth_blockNumber":    ipctest.Value("0x8"),
		"eth_coinbase":       ipctest.Value(ownAddr),
		"eth_getBlockByNumber": func(params json.RawMessage) (any, *ipctest.Error) {
			var args []any
			_ = json.Unmarshal(params, &args)
			tag, _ := args[0].(string)
			n, _ := strconv.ParseUint(strings.TrimPrefix(tag, "0x"), 16, 64)
			if full, _ := args[1].(bool); full {
				return nil, &ipctest.Error{Code: -32602, Message: "asked for full transactions"}
			}
			b := map[string]any{"number": tag, "hash": "0xh" + tag, "timestamp": blockTag(chainBase + n), "miner": otherAddr}
			if n%2 == 0 {
				txs := []string{}
				for i := uint64(0); i < n; i++ {
					txs = append(txs, fmt.Sprintf("0xt%d", i))
				}
				b["transactions"] = txs
			}
			return b, nil
		},
	})
	p := FetchNetwork(context.Background(), node)
	if len(p.Blocks) != 9 {
		t.Fatalf("%d blocks, want 9", len(p.Blocks))
	}
	for _, row := range p.Blocks {
		if row.Number%2 == 1 {
			if row.Transactions != nil {
				t.Errorf("block %d transactions %d, want null", row.Number, *row.Transactions)
			}
			continue
		}
		if row.Transactions == nil || *row.Transactions != int(row.Number) {
			t.Errorf("block %d transactions %v, want %d", row.Number, row.Transactions, row.Number)
		}
	}
	b, _ := json.Marshal(p.Blocks[1])
	if !strings.Contains(string(b), `"transactions":null`) {
		t.Errorf("block without the list: %s", b)
	}
	b, _ = json.Marshal(p.Blocks[0])
	if !strings.Contains(string(b), `"transactions":8`) {
		t.Errorf("block 8: %s", b)
	}
}

func TestLostRacesList(t *testing.T) {
	loc := time.FixedZone("test", 2*3600)
	now := func() time.Time { return time.Date(2026, 10, 28, 12, 0, 0, 0, loc) }
	ctx := context.Background()

	t.Run("number and time", func(t *testing.T) {
		dir := t.TempDir()
		writeLog(t, dir, lineLost+"\n"+
			"WARN [10-28|10:01:00.000] block lost hash=0x123\n"+
			"INFO [12-31|23:59:59.500] \x1b[31mblock lost\x1b[0m number=1,234,567 hash=0xdef\n"+
			lineMined+"\n"+
			"INFO block lost number=50\n")
		r := NewRewards(RewardsEnv{NodeDir: dir, Now: now, Location: loc})
		p := r.Fetch(ctx, downNode(t))
		if p.LostRaces == nil || *p.LostRaces != 4 {
			t.Fatalf("lost races %v, want 4 (the line without a number counts)", p.LostRaces)
		}
		at := func(y int, m time.Month, d, h, mi, s int) *int64 {
			u := time.Date(y, m, d, h, mi, s, 0, loc).Unix()
			return &u
		}
		want := []LostRace{
			{Number: 50},
			// 31 December this year would be months ahead: it is last year's.
			{Number: 1234567, Time: at(2025, 12, 31, 23, 59, 59)},
			{Number: 41, Time: at(2026, 10, 28, 10, 0, 2)},
		}
		if len(p.LostRaceBlocks) != len(want) {
			t.Fatalf("list %+v, want %d entries", p.LostRaceBlocks, len(want))
		}
		for i, w := range want {
			got := p.LostRaceBlocks[i]
			if got.Number != w.Number || (got.Time == nil) != (w.Time == nil) || (w.Time != nil && *got.Time != *w.Time) {
				t.Errorf("entry %d = %d at %v, want %d at %v", i, got.Number, deref(got.Time), w.Number, deref(w.Time))
			}
		}
		b, _ := json.Marshal(p.LostRaceBlocks[0])
		if string(b) != `{"number":50,"time":null}` {
			t.Errorf("entry without a stamp: %s", b)
		}
		if h := r.LostHeights(); !h[41] || !h[1234567] || !h[50] || len(h) != 3 {
			t.Errorf("lost heights %v", h)
		}

		// A new line comes first.
		appendLog(t, dir, "INFO [10-28|11:00:00.000] block lost number=60\n")
		p = r.Fetch(ctx, downNode(t))
		if *p.LostRaces != 5 || p.LostRaceBlocks[0].Number != 60 || len(p.LostRaceBlocks) != 4 {
			t.Errorf("after a new line: %d lost, list %+v", *p.LostRaces, p.LostRaceBlocks)
		}
	})

	t.Run("cap", func(t *testing.T) {
		dir := t.TempDir()
		var b strings.Builder
		for i := 1; i <= 150; i++ {
			fmt.Fprintf(&b, "INFO [10-28|09:%02d:%02d.000] block lost number=%d\n", i/60, i%60, i)
		}
		writeLog(t, dir, b.String())
		p := NewRewards(RewardsEnv{NodeDir: dir, Now: now, Location: loc}).Fetch(ctx, downNode(t))
		if *p.LostRaces != 150 || len(p.LostRaceBlocks) != ListedLostRaces {
			t.Fatalf("%d lost, %d listed; want 150 and %d", *p.LostRaces, len(p.LostRaceBlocks), ListedLostRaces)
		}
		if first, last := p.LostRaceBlocks[0], p.LostRaceBlocks[ListedLostRaces-1]; first.Number != 150 || last.Number != 51 {
			t.Errorf("list runs from %d to %d, want 150 to 51", first.Number, last.Number)
		}
		if t0 := p.LostRaceBlocks[0].Time; t0 == nil || *t0 != time.Date(2026, 10, 28, 9, 2, 30, 0, loc).Unix() {
			t.Errorf("newest time %v", deref(t0))
		}
	})

	t.Run("no log", func(t *testing.T) {
		p := NewRewards(RewardsEnv{NodeDir: t.TempDir()}).Fetch(ctx, downNode(t))
		if p.LostRaces != nil || p.LostRaceBlocks != nil {
			t.Errorf("without a log: %v, %v", p.LostRaces, p.LostRaceBlocks)
		}
	})
}

func deref(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestRewardsPerDay(t *testing.T) {
	loc := time.FixedZone("test", 2*3600)
	now := func() time.Time { return time.Date(2026, 3, 10, 15, 0, 0, 0, loc) }
	at := func(m time.Month, d, h int) uint64 { return uint64(time.Date(2026, m, d, h, 0, 0, 0, loc).Unix()) }

	chain := &fakeChain{}
	chain.grow(41, "a", everyFifth)
	chain.setTime(5, at(3, 10, 1))
	chain.setTime(10, at(3, 10, 2)) // reward unknown
	chain.setTime(40, at(3, 10, 0))
	chain.setTime(15, at(3, 8, 9))
	chain.setTime(20, at(3, 4, 0))  // first of the 7 days
	chain.setTime(25, at(3, 3, 23)) // a day too early for the 7 days
	chain.setTime(30, at(2, 1, 1))  // before the 30 days
	chain.setTime(35, at(1, 31, 1)) // first win
	h := chain.handlers()
	balances := h["eth_getBalance"]
	h["eth_getBalance"] = func(params json.RawMessage) (any, *ipctest.Error) {
		if strings.Contains(string(params), `"0xa"`) {
			return nil, &ipctest.Error{Code: -32000, Message: "missing trie node"}
		}
		return balances(params)
	}
	node := startHandlers(t, h)

	p := NewRewards(RewardsEnv{NodeDir: t.TempDir(), Now: now, Location: loc}).Fetch(context.Background(), node)
	if p.WonTotal != 8 || p.UnknownRewards != 1 {
		t.Fatalf("won %d, unknown %d; want 8 and 1", p.WonTotal, p.UnknownRewards)
	}
	sum := func(ns ...uint64) string {
		s := new(big.Int)
		for _, n := range ns {
			s.Add(s, reward(n))
		}
		return s.String()
	}
	check := func(name string, days []DayRewards, n int, first string, want map[string][2]any) {
		t.Helper()
		if len(days) != n {
			t.Fatalf("%s: %d days, want %d", name, len(days), n)
		}
		if days[0].Day != first || days[n-1].Day != "2026-03-10" {
			t.Errorf("%s runs %s to %s, want %s to 2026-03-10", name, days[0].Day, days[n-1].Day, first)
		}
		for i, d := range days {
			if i > 0 && d.Day <= days[i-1].Day {
				t.Errorf("%s: %s after %s", name, d.Day, days[i-1].Day)
			}
			w, ok := want[d.Day]
			if !ok {
				w = [2]any{0, "0"}
			}
			if d.Won != w[0] || d.Reward.Wei != w[1] {
				t.Errorf("%s %s: won %d, %s wei; want %v, %v", name, d.Day, d.Won, d.Reward.Wei, w[0], w[1])
			}
			if d.Reward.Coins != Coins(mustBig(t, d.Reward.Wei)) {
				t.Errorf("%s %s: coins %q for %s wei", name, d.Day, d.Reward.Coins, d.Reward.Wei)
			}
		}
	}
	recent := map[string][2]any{
		"2026-03-10": {3, sum(5, 40)},
		"2026-03-08": {1, sum(15)},
		"2026-03-04": {1, sum(20)},
	}
	check("days7", p.Daily.Days7, 7, "2026-03-04", recent)
	with30 := map[string][2]any{"2026-03-03": {1, sum(25)}}
	for k, v := range recent {
		with30[k] = v
	}
	check("days30", p.Daily.Days30, 30, "2026-02-09", with30)
	all := map[string][2]any{"2026-02-01": {1, sum(30)}, "2026-01-31": {1, sum(35)}}
	for k, v := range with30 {
		all[k] = v
	}
	check("all", p.Daily.All, 39, "2026-01-31", all)

	if p.Last7.Won != 5 || p.Last7.Reward.Wei != sum(5, 40, 15, 20) || p.Last7.Reward.Coins != Coins(mustBig(t, sum(5, 40, 15, 20))) {
		t.Errorf("last7 = %+v, want 5 blocks, %s wei", p.Last7, sum(5, 40, 15, 20))
	}

	// An empty index lists zero days and no all-time days.
	p = NewRewards(RewardsEnv{NodeDir: t.TempDir(), Now: now, Location: loc}).Fetch(context.Background(), downNode(t))
	check("empty days7", p.Daily.Days7, 7, "2026-03-04", nil)
	check("empty days30", p.Daily.Days30, 30, "2026-02-09", nil)
	if p.Last7.Won != 0 || p.Last7.Reward.Wei != "0" {
		t.Errorf("empty last7 = %+v", p.Last7)
	}
	if b, _ := json.Marshal(p.Daily.All); string(b) != "[]" {
		t.Errorf("empty all = %s, want []", b)
	}
}

func mustBig(t *testing.T, s string) *big.Int {
	t.Helper()
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		t.Fatalf("not a number: %q", s)
	}
	return n
}

func TestWonBlocksPaging(t *testing.T) {
	ctx := context.Background()
	u := func(n uint64) *uint64 { return &n }
	page := func(t *testing.T, p RewardsPanel, want []uint64, next *uint64) {
		t.Helper()
		if got := wonNumbers(p); !slices.Equal(got, want) {
			t.Errorf("page %v, want %v", got, want)
		}
		if (p.NextBefore == nil) != (next == nil) || (next != nil && *p.NextBefore != *next) {
			t.Errorf("nextBefore %v, want %v", deref64(p.NextBefore), deref64(next))
		}
	}

	t.Run("empty index", func(t *testing.T) {
		p := NewRewards(RewardsEnv{NodeDir: t.TempDir()}).FetchPage(ctx, downNode(t), Page{Limit: 5})
		page(t, p, nil, nil)
		if p.Blocks == nil {
			t.Error("blocks null, want []")
		}
	})

	chain := &fakeChain{}
	chain.grow(50, "a", everyFifth)
	node := startChain(t, chain)
	r := NewRewards(RewardsEnv{NodeDir: t.TempDir()})

	t.Run("pages", func(t *testing.T) {
		p := r.FetchPage(ctx, node, Page{Limit: 4})
		page(t, p, []uint64{50, 45, 40, 35}, u(35))
		// The sums and counts cover every block, not the page.
		if p.WonTotal != 10 || p.RewardTotal.Wei != chain.balance(ownAddr, 50).String() {
			t.Errorf("won %d, total %s", p.WonTotal, p.RewardTotal.Wei)
		}
		page(t, r.FetchPage(ctx, node, Page{Before: u(35), Limit: 4}), []uint64{30, 25, 20, 15}, u(15))
		page(t, r.FetchPage(ctx, node, Page{Before: u(15), Limit: 4}), []uint64{10, 5}, nil)
		// A last page that is exactly full has no next page either.
		page(t, r.FetchPage(ctx, node, Page{Before: u(25), Limit: 4}), []uint64{20, 15, 10, 5}, nil)
		// before need not be a block won.
		page(t, r.FetchPage(ctx, node, Page{Before: u(33), Limit: 2}), []uint64{30, 25}, u(25))
	})

	t.Run("edges", func(t *testing.T) {
		p := r.FetchPage(ctx, node, Page{Before: u(0)})
		page(t, p, nil, nil)
		if p.Blocks == nil || p.WonTotal != 10 {
			t.Errorf("before=0: blocks %v, won %d", p.Blocks, p.WonTotal)
		}
		page(t, r.FetchPage(ctx, node, Page{Before: u(5)}), nil, nil)
		page(t, r.FetchPage(ctx, node, Page{Limit: 1}), []uint64{50}, u(50))
		page(t, r.FetchPage(ctx, node, Page{Limit: MaxWonBlocksPage}), countDownBy5(50, 5), nil)
		page(t, r.Fetch(ctx, node), countDownBy5(50, 5), nil)
	})

	t.Run("default limit", func(t *testing.T) {
		long := &fakeChain{}
		long.grow(600, "a", everyFifth)
		node := startChain(t, long)
		r := NewRewards(RewardsEnv{NodeDir: t.TempDir()})
		p := r.Fetch(ctx, node)
		if p.Indexing != nil {
			t.Fatalf("still indexing: %+v", p.Indexing)
		}
		page(t, p, countDownBy5(600, 105), u(105))
		page(t, r.FetchPage(ctx, node, Page{Before: u(105)}), countDownBy5(100, 5), nil)
		page(t, r.FetchPage(ctx, node, Page{Limit: MaxWonBlocksPage}), countDownBy5(600, 5), nil)
	})
}

// countDownBy5 is the multiples of 5 from high down to low.
func countDownBy5(high, low uint64) []uint64 {
	var out []uint64
	for n := high; n >= low; n -= 5 {
		out = append(out, n)
	}
	return out
}

func deref64(p *uint64) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestHashRateTrend(t *testing.T) {
	var h HashTrend
	t0 := time.Unix(chainBase, 0)

	p := MiningPanel{Running: true}
	h.Record(&p, t0)
	if p.HashRateTrend == nil || len(p.HashRateTrend) != 0 {
		t.Fatalf("empty trend %v, want []", p.HashRateTrend)
	}
	if b, _ := json.Marshal(p.HashRateTrend); string(b) != "[]" {
		t.Errorf("empty trend JSON %s", b)
	}

	for i := range 70 {
		rate := float64(1000 + i)
		p := MiningPanel{Running: true, HashRate: &rate}
		h.Record(&p, t0.Add(time.Duration(i)*5*time.Second))
		if want := min(i+1, TrendSamples); len(p.HashRateTrend) != want {
			t.Fatalf("poll %d: %d samples, want %d", i, len(p.HashRateTrend), want)
		}
	}
	got := h.Samples()
	if len(got) != TrendSamples {
		t.Fatalf("%d samples, want %d", len(got), TrendSamples)
	}
	for i, s := range got {
		if want := float64(1010 + i); s.HashRate != want || s.Time != chainBase+int64(10+i)*5 {
			t.Errorf("sample %d = %+v, want rate %v", i, s, want)
		}
	}

	// Polls without a rate, or with the node down, add nothing.
	rate := 5.0
	for _, p := range []MiningPanel{{Running: true}, {Running: false, HashRate: &rate}} {
		h.Record(&p, t0.Add(time.Hour))
		if len(p.HashRateTrend) != TrendSamples || p.HashRateTrend[TrendSamples-1].HashRate != 1069 {
			t.Errorf("poll %+v changed the trend", p)
		}
	}

	// The panel alone has an empty trend, never null.
	if p := FetchMining(context.Background(), downNode(t), MiningEnv{}); p.HashRateTrend == nil {
		t.Error("node down: trend null")
	}
}
