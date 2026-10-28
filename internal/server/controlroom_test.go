package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/ghostcoin-core/ghost-node-dashboard/internal/ipc/ipctest"
	"github.com/ghostcoin-core/ghost-node-dashboard/internal/status"
)

// wonChain is chain with block hashes and balances, so the rewards index can
// read it: the node won every fifth block after the first, and block n
// holds n mod 4 transactions, except every seventh block, which comes
// without the list.
func wonChain(height uint64) map[string]ipctest.Handler {
	h := chain(height)
	blocks := h["eth_getBlockByNumber"]
	hash := func(n uint64) string { return "0xh" + strconv.FormatUint(n, 10) }
	h["eth_getBlockByNumber"] = func(params json.RawMessage) (any, *ipctest.Error) {
		v, rpcErr := blocks(params)
		b, ok := v.(map[string]string)
		if rpcErr != nil || !ok {
			return v, rpcErr
		}
		n, _ := strconv.ParseUint(strings.TrimPrefix(b["number"], "0x"), 16, 64)
		out := map[string]any{}
		for k, v := range b {
			out[k] = v
		}
		if n == 0 {
			out["miner"] = otherAddress
		}
		out["hash"] = hash(n)
		if n > 0 {
			out["parentHash"] = hash(n - 1)
		}
		if n%7 != 0 {
			out["transactions"] = make([]string, n%4)
		}
		return out, nil
	}
	h["eth_getBalance"] = ipctest.Value("0x0")
	return h
}

func TestRewardsPagingEndpoint(t *testing.T) {
	// Blocks won: 5, 10, ... 30.
	_, node := startFake(t, wonChain(30))
	h := panelHandler(t, node, panelOpts{})

	numbers := func(got map[string]any) []float64 {
		var out []float64
		for _, b := range got["blocks"].([]any) {
			out = append(out, b.(map[string]any)["number"].(float64))
		}
		return out
	}
	for _, c := range []struct {
		target string
		want   []float64
		next   any
	}{
		{"/api/rewards", []float64{30, 25, 20, 15, 10, 5}, nil},
		{"/api/rewards?limit=2", []float64{30, 25}, 25.0},
		{"/api/rewards?before=25&limit=2", []float64{20, 15}, 15.0},
		{"/api/rewards?before=15&limit=2", []float64{10, 5}, nil},
		{"/api/rewards?before=12", []float64{10, 5}, nil},
		{"/api/rewards?before=0", nil, nil},
		{"/api/rewards?limit=1", []float64{30}, 30.0},
		{"/api/rewards?limit=500", []float64{30, 25, 20, 15, 10, 5}, nil},
	} {
		got := getPanel(t, h, c.target)
		if n := numbers(got); jsonOf(n) != jsonOf(c.want) {
			t.Errorf("%s: blocks %v, want %v", c.target, n, c.want)
		}
		next, ok := got["nextBefore"]
		if !ok || next != c.next {
			t.Errorf("%s: nextBefore %v, want %v", c.target, next, c.next)
		}
		// The sums cover every block won, whatever the page.
		if got["wonTotal"] != 6.0 {
			t.Errorf("%s: wonTotal %v, want 6", c.target, got["wonTotal"])
		}
	}

	cookie := signIn(t, h)
	for _, target := range []string{
		"/api/rewards?before=abc",
		"/api/rewards?before=-1",
		"/api/rewards?before=1.5",
		"/api/rewards?before=",
		"/api/rewards?before=%2B5",
		"/api/rewards?limit=0",
		"/api/rewards?limit=501",
		"/api/rewards?limit=-3",
		"/api/rewards?limit=2.0",
		"/api/rewards?limit=lots",
		"/api/rewards?before=10&limit=",
	} {
		rec := do(h, http.MethodGet, target, goodHost, func(r *http.Request) { r.AddCookie(cookie) })
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", target, rec.Code)
			continue
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: Content-Type %q", target, ct)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body) != 1 {
			t.Errorf("%s: body %s", target, rec.Body.String())
			continue
		}
		if msg, _ := body["error"].(string); !strings.Contains(msg, "whole") {
			t.Errorf("%s: error %q, want it in plain words", target, msg)
		}
	}
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestNetworkWallEndpoint(t *testing.T) {
	fake, node := startFake(t, wonChain(300))
	dir := t.TempDir()
	log := "INFO [10-28|10:00:02.000] block lost number=298 hash=0x1\n" +
		"INFO [10-28|10:00:03.000] block lost number=290 hash=0x2\n" +
		"INFO [10-28|10:00:04.000] block lost hash=0x3\n"
	if err := os.WriteFile(filepath.Join(dir, "node.log"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	h := panelHandler(t, node, panelOpts{nodeDir: dir})

	got := getPanel(t, h, "/api/network")
	blocks, _ := got["blocks"].([]any)
	if len(blocks) != status.RecentBlocks {
		t.Fatalf("%d blocks, want %d", len(blocks), status.RecentBlocks)
	}
	for _, raw := range blocks {
		b := raw.(map[string]any)
		n := uint64(b["number"].(float64))
		var want any = float64(n % 4)
		if n%7 == 0 {
			want = nil
		}
		if tx, ok := b["transactions"]; !ok || tx != want {
			t.Errorf("block %d transactions %v, want %v", n, tx, want)
		}
	}

	wall, _ := got["wall"].([]any)
	if len(wall) != status.WallBlocks {
		t.Fatalf("%d wall blocks, want %d", len(wall), status.WallBlocks)
	}
	for i, raw := range wall {
		b := raw.(map[string]any)
		n := 300 - i
		wantShort := "0x1111…2222"
		if n%5 == 0 {
			wantShort = "0x0000…00AA"
		}
		expect(t, b, map[string]any{
			"number":     n,
			"time":       1_700_000_000 + 12*n,
			"minerShort": wantShort,
			"own":        n%5 == 0,
			"lostRace":   n == 298 || n == 290,
		})
		if len(b) != 5 {
			t.Errorf("wall block %d fields %v", n, b)
		}
	}

	// The next poll reads the latest blocks only.
	calls := func() int {
		n := 0
		for _, c := range fake.Calls() {
			if c == "eth_getBlockByNumber" {
				n++
			}
		}
		return n
	}
	before := calls()
	got = getPanel(t, h, "/api/network")
	if reads := calls() - before; reads != status.RecentBlocks+1 {
		t.Errorf("second poll read %d blocks, want %d", reads, status.RecentBlocks+1)
	}
	if wall, _ := got["wall"].([]any); len(wall) != status.WallBlocks {
		t.Errorf("second poll: %d wall blocks", len(wall))
	}

	// A lost race the node logs later shows on the next poll.
	f, err := os.OpenFile(filepath.Join(dir, "node.log"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("INFO [10-28|10:00:05.000] block lost number=299 hash=0x4\n")
	_ = f.Close()
	got = getPanel(t, h, "/api/network")
	if b := got["wall"].([]any)[1].(map[string]any); b["number"] != 299.0 || b["lostRace"] != true {
		t.Errorf("block 299 = %v, want a lost race", b)
	}

	// The rewards panel lists the lost races that name their block.
	got = getPanel(t, h, "/api/rewards")
	if s := jsonOf(got["lostRaceBlocks"]); !strings.Contains(s, `"number":299`) || strings.Count(s, "number") != 3 {
		t.Errorf("lostRaceBlocks %s, want 299, 290 and 298", s)
	}
	expect(t, got, map[string]any{"lostRaces": 4})
}

func TestMiningTrendEndpoint(t *testing.T) {
	var mu sync.Mutex
	rate := uint64(1000)
	_, node := startFake(t, map[string]ipctest.Handler{
		"web3_clientVersion": ipctest.Value("Ghost/test"),
		"eth_mining":         ipctest.Value(true),
		"eth_coinbase":       ipctest.Value(ownAddress),
		"eth_hashrate": func(json.RawMessage) (any, *ipctest.Error) {
			mu.Lock()
			defer mu.Unlock()
			return "0x" + strconv.FormatUint(rate, 16), nil
		},
	})
	h := panelHandler(t, node, panelOpts{})
	setRate := func(r uint64) {
		mu.Lock()
		rate = r
		mu.Unlock()
	}

	rates := func(got map[string]any) []float64 {
		t.Helper()
		trend, ok := got["hashRateTrend"].([]any)
		if !ok {
			t.Fatalf("hashRateTrend = %v, want a list", got["hashRateTrend"])
		}
		var out []float64
		for _, s := range trend {
			out = append(out, s.(map[string]any)["hashRate"].(float64))
		}
		return out
	}
	// A poll where the node reports no hash rate (0) adds nothing.
	for i, c := range []struct {
		rate uint64
		want []float64
	}{
		{1000, []float64{1000}},
		{1500, []float64{1000, 1500}},
		{0, []float64{1000, 1500}},
		{2000, []float64{1000, 1500, 2000}},
	} {
		setRate(c.rate)
		if got := rates(getPanel(t, h, "/api/mining")); jsonOf(got) != jsonOf(c.want) {
			t.Errorf("poll %d: trend %v, want %v", i, got, c.want)
		}
	}

	// Without a node the trend is empty, not null.
	got := getPanel(t, panelHandler(t, nil, panelOpts{}), "/api/mining")
	if trend, ok := got["hashRateTrend"].([]any); !ok || len(trend) != 0 {
		t.Errorf("node down: hashRateTrend %v, want []", got["hashRateTrend"])
	}
}

func TestNewFieldsNeedKey(t *testing.T) {
	_, node := startFake(t, wonChain(10))
	h := panelHandler(t, node, panelOpts{})
	for _, target := range []string{"/api/rewards?before=10&limit=5", "/api/rewards?limit=0", "/api/network", "/api/mining"} {
		rec := do(h, http.MethodGet, target, goodHost, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without cookie: status %d, want 401", target, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "wall") || strings.Contains(rec.Body.String(), "nextBefore") {
			t.Errorf("%s without cookie answered with data", target)
		}
		rec = do(h, http.MethodGet, target, "evil.example:8580", withSession)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s on another host: status %d, want 403", target, rec.Code)
		}
	}
}
