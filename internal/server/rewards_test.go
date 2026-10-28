package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ghostcoin-core/ghost-node-dashboard/internal/ipc/ipctest"
	"github.com/ghostcoin-core/ghost-node-dashboard/internal/status"
)

func TestRewardsAndLogNeedKey(t *testing.T) {
	h := panelHandler(t, nil, panelOpts{})
	for _, target := range []string{"/api/rewards", "/api/log"} {
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

func TestRewardsPanelEndpoint(t *testing.T) {
	// Blocks 0 to 4; the node won block 2, which paid 3 coins.
	const coin = "de0b6b3a7640000"
	_, node := startFake(t, map[string]ipctest.Handler{
		"web3_clientVersion": ipctest.Value("Ghost/test"),
		"eth_coinbase":       ipctest.Value(ownAddress),
		"eth_blockNumber":    ipctest.Value("0x4"),
		"eth_getBlockByNumber": func(params json.RawMessage) (any, *ipctest.Error) {
			var args []any
			_ = json.Unmarshal(params, &args)
			tag, _ := args[0].(string)
			n, _ := strconv.ParseUint(strings.TrimPrefix(tag, "0x"), 16, 64)
			if n > 4 {
				return nil, nil
			}
			miner := otherAddress
			if n == 2 {
				miner = ownAddress
			}
			return map[string]string{
				"number":     tag,
				"hash":       "0xh" + tag,
				"parentHash": "0xh0x" + strconv.FormatUint(n-1, 16),
				"timestamp":  "0x" + strconv.FormatUint(1_700_000_000+n, 16),
				"miner":      miner,
			}, nil
		},
		"eth_getBalance": func(params json.RawMessage) (any, *ipctest.Error) {
			var args []string
			_ = json.Unmarshal(params, &args)
			if args[1] == "0x1" {
				return "0x" + coin, nil
			}
			// 4 coins from block 2 on: 1 before plus the 3 it paid.
			return "0x3782dace9d900000", nil
		},
	})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "node.log"), []byte("INFO 😱 block lost number=3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := getPanel(t, panelHandler(t, node, panelOpts{nodeDir: dir}), "/api/rewards")
	expect(t, got, map[string]any{
		"running":       true,
		"payoutAddress": ownAddress,
		"wonTotal":      1,
		"lostRaces":     1,
		"indexing":      nil,
	})
	blocks, _ := got["blocks"].([]any)
	if len(blocks) != 1 {
		t.Fatalf("blocks %v, want block 2", got["blocks"])
	}
	b := blocks[0].(map[string]any)
	if reward, _ := b["reward"].(map[string]any); b["number"] != 2.0 || reward["coins"] != "3" {
		t.Errorf("block won %v, want block 2 paying 3 coins", b)
	}
	if bal, _ := got["balance"].(map[string]any); bal["coins"] != "4" {
		t.Errorf("balance %v, want 4 coins", got["balance"])
	}
	if _, err := os.Stat(status.IndexPath(dir)); err != nil {
		t.Errorf("index not written: %v", err)
	}
}

func TestLogPanelEndpoint(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "node.log"), []byte("one\nINFO Looking for peers peercount=0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := getPanel(t, panelHandler(t, nil, panelOpts{nodeDir: dir}), "/api/log")
	if got["available"] != true {
		t.Fatalf("log panel %v", got)
	}
	lines, _ := got["lines"].([]any)
	if len(lines) != 2 {
		t.Fatalf("lines %v", got["lines"])
	}
	first := lines[0].(map[string]any)
	if first["text"] != "INFO Looking for peers peercount=0" || first["meaning"] == nil {
		t.Errorf("newest line %v, want the peers line with its meaning", first)
	}
}
