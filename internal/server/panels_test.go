package server

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ghostcoin-core/ghost-node-dashboard/internal/ipc"
	"github.com/ghostcoin-core/ghost-node-dashboard/internal/ipc/ipctest"
	"github.com/ghostcoin-core/ghost-node-dashboard/internal/status"
)

const (
	ownAddress   = "0x00000000000000000000000000000000000000AA"
	otherAddress = "0x1111111111111111111111111111111111112222"
	testCores    = 8
)

type panelOpts struct {
	nodeDir    string
	autoUpdate *bool
	now        func() time.Time
}

// startFake runs a fake node and returns a client for it.
func startFake(t *testing.T, handlers map[string]ipctest.Handler) (*ipctest.Server, *ipc.Client) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "geth.ipc")
	fake, err := ipctest.Start(sock, handlers)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fake.Close)
	node := ipc.New(sock)
	t.Cleanup(func() { _ = node.Close() })
	return fake, node
}

func panelHandler(t *testing.T, node *ipc.Client, o panelOpts) http.Handler {
	t.Helper()
	if node == nil {
		node = ipc.New(filepath.Join(t.TempDir(), "data", "geth.ipc"))
	}
	if o.nodeDir == "" {
		o.nodeDir = t.TempDir()
	}
	return New(Config{
		Port:         testPort,
		Key:          testKey,
		Node:         node,
		NodeDir:      o.nodeDir,
		LogicalCores: testCores,
		AutoUpdate:   func() *bool { return o.autoUpdate },
		Now:          o.now,
	})
}

func getPanel(t *testing.T, h http.Handler, target string) map[string]any {
	t.Helper()
	cookie := signIn(t, h)
	rec := do(h, http.MethodGet, target, goodHost, func(r *http.Request) { r.AddCookie(cookie) })
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: %d %s", target, rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("%s: Content-Type %q", target, ct)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %s: %v", target, err)
	}
	return got
}

func expect(t *testing.T, got map[string]any, want map[string]any) {
	t.Helper()
	for k, v := range want {
		gv, ok := got[k]
		if !ok {
			t.Errorf("field %s missing", k)
			continue
		}
		if !sameJSON(gv, v) {
			t.Errorf("%s = %#v, want %#v", k, gv, v)
		}
	}
}

// sameJSON compares a decoded JSON value with a Go value, numbers by value.
func sameJSON(got, want any) bool {
	switch w := want.(type) {
	case int:
		return got == float64(w)
	case uint64:
		return got == float64(w)
	case float64:
		return got == w
	}
	return fmt.Sprint(got) == fmt.Sprint(want)
}

func expectDown(t *testing.T, got map[string]any, nullFields ...string) {
	t.Helper()
	if got["running"] != false {
		t.Errorf("running = %v, want false", got["running"])
	}
	if got["error"] != "node not running" {
		t.Errorf("error = %v, want node not running", got["error"])
	}
	for _, f := range nullFields {
		v, ok := got[f]
		if !ok {
			t.Errorf("field %s missing", f)
		} else if v != nil {
			t.Errorf("field %s = %v, want null", f, v)
		}
	}
}

func boolPtr(b bool) *bool { return &b }

func rpcErr(msg string) ipctest.Handler {
	return func(json.RawMessage) (any, *ipctest.Error) {
		return nil, &ipctest.Error{Code: -32000, Message: msg}
	}
}

func TestPanelsNeedKey(t *testing.T) {
	h := panelHandler(t, nil, panelOpts{})
	for _, target := range []string{"/api/node", "/api/mining", "/api/network"} {
		rec := do(h, http.MethodGet, target, goodHost, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without cookie: status %d, want 401", target, rec.Code)
		}
		rec = do(h, http.MethodGet, target, "evil.example:8580", withSession)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s on another host: status %d, want 403", target, rec.Code)
		}
	}
}

func TestNodePanelInSync(t *testing.T) {
	_, node := startFake(t, map[string]ipctest.Handler{
		"web3_clientVersion": ipctest.Value("Ghost/v1.2.3-test/linux-amd64/go1.25"),
		"eth_chainId":        ipctest.Value("0x676c74"),
		"eth_blockNumber":    ipctest.Value("0x64"),
		"eth_syncing":        ipctest.Value(false),
		"admin_peers": ipctest.Value([]map[string]string{
			{"name": "Geth/v1.13.5-stable/linux-amd64/go1.21.4"},
			{"name": "Ghost/v1.2.3/darwin-arm64/go1.25"},
			{"name": "Geth/v1.13.5-stable/windows-amd64/go1.21.4"},
		}),
		"net_peerCount": ipctest.Value("0x9"),
	})
	h := panelHandler(t, node, panelOpts{
		autoUpdate: boolPtr(true),
		now:        func() time.Time { return time.Now().Add(90 * time.Second) },
	})
	got := getPanel(t, h, "/api/node")
	expect(t, got, map[string]any{
		"running":       true,
		"syncing":       false,
		"currentBlock":  100,
		"highestBlock":  100,
		"peers":         3,
		"chainId":       6777972,
		"networkName":   "Ghost test network",
		"autoUpdate":    true,
		"clientVersion": "Ghost/v1.2.3-test/linux-amd64/go1.25",
		"uptimeFrom":    "node start",
	})
	if _, ok := got["error"]; ok {
		t.Errorf("unexpected error: %v", got["error"])
	}
	clients, _ := json.Marshal(got["peerClients"])
	if want := `[{"count":2,"name":"Geth/v1.13.5-stable"},{"count":1,"name":"Ghost/v1.2.3"}]`; string(clients) != want {
		t.Errorf("peerClients = %s, want %s", clients, want)
	}
	// The socket was created just now and the clock is 90 s ahead.
	if up, _ := got["uptimeSeconds"].(float64); up < 90 || up > 150 {
		t.Errorf("uptimeSeconds = %v, want about 90", got["uptimeSeconds"])
	}
}

func TestNodePanelSyncing(t *testing.T) {
	_, node := startFake(t, map[string]ipctest.Handler{
		"web3_clientVersion": ipctest.Value("Ghost/test"),
		"eth_chainId":        ipctest.Value("0x1"),
		"eth_blockNumber":    ipctest.Value("0x2a"),
		"eth_syncing":        ipctest.Value(map[string]string{"startingBlock": "0x0", "currentBlock": "0x2a", "highestBlock": "0x3e8"}),
		// admin_peers is not offered: the count comes from net_peerCount.
		"net_peerCount": ipctest.Value("0x5"),
	})
	got := getPanel(t, panelHandler(t, node, panelOpts{autoUpdate: boolPtr(false)}), "/api/node")
	expect(t, got, map[string]any{
		"running":      true,
		"syncing":      true,
		"currentBlock": 42,
		"highestBlock": 1000,
		"peers":        5,
		"peerClients":  nil,
		"networkName":  "Chain ID 1",
		"autoUpdate":   false,
	})
}

func TestNodePanelNodeDown(t *testing.T) {
	got := getPanel(t, panelHandler(t, nil, panelOpts{autoUpdate: boolPtr(true)}), "/api/node")
	expectDown(t, got, "syncing", "currentBlock", "highestBlock", "peers", "peerClients",
		"clientVersion", "uptimeSeconds", "chainId", "networkName")
	// Whether updates are installed is read from this computer, not the node.
	if got["autoUpdate"] != true {
		t.Errorf("autoUpdate = %v, want true", got["autoUpdate"])
	}
}

func TestNodePanelNodeStops(t *testing.T) {
	fake, node := startFake(t, map[string]ipctest.Handler{"web3_clientVersion": ipctest.Value("Ghost/test")})
	h := panelHandler(t, node, panelOpts{})
	if got := getPanel(t, h, "/api/node"); got["running"] != true {
		t.Fatalf("running = %v before the node stops", got["running"])
	}
	fake.Close()
	expectDown(t, getPanel(t, h, "/api/node"), "clientVersion", "uptimeSeconds")
}

func TestMiningPanelMining(t *testing.T) {
	// The network makes 4000 H/s at this difficulty; the node makes 1000.
	difficulty := uint64(4000 * status.TargetBlockTime.Seconds())
	fake, node := startFake(t, map[string]ipctest.Handler{
		"web3_clientVersion": ipctest.Value("Ghost/test"),
		"eth_mining":         ipctest.Value(true),
		"randomx_hashrate":   ipctest.Value("0x3e8"),
		"eth_hashrate":       ipctest.Value("0x1"),
		"eth_coinbase":       ipctest.Value(ownAddress),
		"eth_blockNumber":    ipctest.Value("0x10"),
		"eth_getBlockByNumber": ipctest.Value(map[string]string{
			"number":     "0x10",
			"timestamp":  "0x6553f100",
			"miner":      otherAddress,
			"difficulty": "0x" + strconv.FormatUint(difficulty, 16),
		}),
	})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "miner-threads"), []byte("3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := getPanel(t, panelHandler(t, node, panelOpts{nodeDir: dir}), "/api/mining")
	expect(t, got, map[string]any{
		"running":          true,
		"mining":           true,
		"coresUsed":        3,
		"coresFromSetting": true,
		"logicalCores":     testCores,
		"hashRate":         1000,
		"payoutAddress":    ownAddress,
		"networkShare":     0.25,
	})
	wantWait := status.TargetBlockTime.Seconds() * 4
	if w, _ := got["expectedBlockSeconds"].(float64); math.Abs(w-wantWait) > 1e-9 {
		t.Errorf("expectedBlockSeconds = %v, want %v", got["expectedBlockSeconds"], wantWait)
	}
	if slices.Contains(fake.Calls(), "eth_hashrate") {
		t.Error("asked eth_hashrate although the RandomX API answered")
	}
}

func TestMiningPanelPaused(t *testing.T) {
	_, node := startFake(t, map[string]ipctest.Handler{
		"web3_clientVersion":   ipctest.Value("Ghost/test"),
		"eth_mining":           ipctest.Value(false),
		"eth_hashrate":         ipctest.Value("0x0"),
		"eth_coinbase":         rpcErr("etherbase must be explicitly specified"),
		"eth_blockNumber":      ipctest.Value("0x10"),
		"eth_getBlockByNumber": ipctest.Value(map[string]string{"number": "0x10", "timestamp": "0x1", "difficulty": "0x10"}),
	})
	got := getPanel(t, panelHandler(t, node, panelOpts{}), "/api/mining")
	expect(t, got, map[string]any{
		"running":              true,
		"mining":               false,
		"coresUsed":            testCores,
		"coresFromSetting":     false,
		"logicalCores":         testCores,
		"hashRate":             nil,
		"payoutAddress":        nil,
		"networkShare":         nil,
		"expectedBlockSeconds": nil,
	})
}

func TestMiningPanelEthHashrate(t *testing.T) {
	// Without the RandomX API the hash rate comes from eth_hashrate; a bad
	// miner-threads file falls back to every core.
	_, node := startFake(t, map[string]ipctest.Handler{
		"web3_clientVersion": ipctest.Value("Ghost/test"),
		"eth_mining":         ipctest.Value(true),
		"eth_hashrate":       ipctest.Value("0x7d0"),
		"eth_coinbase":       ipctest.Value(ownAddress),
	})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "miner-threads"), []byte("lots"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := getPanel(t, panelHandler(t, node, panelOpts{nodeDir: dir}), "/api/mining")
	expect(t, got, map[string]any{
		"hashRate":         2000,
		"coresUsed":        testCores,
		"coresFromSetting": false,
		// No block to read the difficulty from: no estimate.
		"networkShare": nil,
	})
}

func TestMiningPanelNodeDown(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "miner-threads"), []byte("2"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := getPanel(t, panelHandler(t, nil, panelOpts{nodeDir: dir}), "/api/mining")
	expectDown(t, got, "mining", "hashRate", "payoutAddress", "networkShare", "expectedBlockSeconds")
	expect(t, got, map[string]any{"coresUsed": 2, "logicalCores": testCores})
}

// chain is a fake chain whose block n was mined at base+12n, by the node
// itself when n is a multiple of 5.
func chain(height uint64) map[string]ipctest.Handler {
	const base = 1_700_000_000
	return map[string]ipctest.Handler{
		"web3_clientVersion": ipctest.Value("Ghost/test"),
		"eth_blockNumber":    ipctest.Value("0x" + strconv.FormatUint(height, 16)),
		"eth_coinbase":       ipctest.Value(strings.ToLower(ownAddress)),
		"eth_getBlockByNumber": func(params json.RawMessage) (any, *ipctest.Error) {
			var args []any
			if err := json.Unmarshal(params, &args); err != nil || len(args) != 2 {
				return nil, &ipctest.Error{Code: -32602, Message: "bad params"}
			}
			tag, _ := args[0].(string)
			n, err := strconv.ParseUint(strings.TrimPrefix(tag, "0x"), 16, 64)
			if err != nil || n > height {
				return nil, nil
			}
			miner := otherAddress
			if n%5 == 0 {
				miner = ownAddress
			}
			return map[string]string{
				"number":     tag,
				"timestamp":  "0x" + strconv.FormatUint(base+12*n, 16),
				"miner":      miner,
				"difficulty": "0x" + strconv.FormatUint(1_000_000+n, 16),
			}, nil
		},
	}
}

func TestNetworkPanel(t *testing.T) {
	fake, node := startFake(t, chain(150))
	got := getPanel(t, panelHandler(t, node, panelOpts{}), "/api/network")
	expect(t, got, map[string]any{
		"running":             true,
		"height":              150,
		"averageBlockSeconds": 12,
		"averageOverBlocks":   100,
		"difficulty":          1_000_150,
		"targetBlockSeconds":  status.TargetBlockTime.Seconds(),
	})
	wantRate := 1_000_150 / status.TargetBlockTime.Seconds()
	if r, _ := got["networkHashRate"].(float64); math.Abs(r-wantRate) > 1e-6 {
		t.Errorf("networkHashRate = %v, want %v", got["networkHashRate"], wantRate)
	}

	blocks, _ := got["blocks"].([]any)
	if len(blocks) != 20 {
		t.Fatalf("%d blocks, want 20", len(blocks))
	}
	for i, raw := range blocks {
		b := raw.(map[string]any)
		n := 150 - i
		own := n%5 == 0
		wantMiner, wantShort := otherAddress, "0x1111…2222"
		if own {
			wantMiner, wantShort = ownAddress, "0x0000…00AA"
		}
		expect(t, b, map[string]any{
			"number":     n,
			"time":       1_700_000_000 + 12*n,
			"miner":      wantMiner,
			"minerShort": wantShort,
			"own":        own,
		})
	}
	if !slices.Contains(fake.Calls(), "eth_getBlockByNumber") {
		t.Error("blocks were not read over IPC")
	}
}

func TestNetworkPanelYoungChain(t *testing.T) {
	_, node := startFake(t, chain(5))
	got := getPanel(t, panelHandler(t, node, panelOpts{}), "/api/network")
	expect(t, got, map[string]any{
		"height":              5,
		"averageBlockSeconds": 12,
		"averageOverBlocks":   5,
	})
	if blocks, _ := got["blocks"].([]any); len(blocks) != 6 {
		t.Errorf("%d blocks, want 6 (blocks 5 to 0)", len(blocks))
	}
}

func TestNetworkPanelNodeDown(t *testing.T) {
	got := getPanel(t, panelHandler(t, nil, panelOpts{}), "/api/network")
	expectDown(t, got, "height", "averageBlockSeconds", "difficulty", "networkHashRate", "blocks")
}
