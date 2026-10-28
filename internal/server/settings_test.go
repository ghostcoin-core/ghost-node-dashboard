package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ghostcoin-core/ghost-node-dashboard/internal/ipc"
	"github.com/ghostcoin-core/ghost-node-dashboard/internal/ipc/ipctest"
	"github.com/ghostcoin-core/ghost-node-dashboard/internal/service"
	"github.com/ghostcoin-core/ghost-node-dashboard/internal/status"
)

const (
	checksummedPayout = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"
	installerConfig   = "[Eth]\nNetworkId = 6777972\n\n[Eth.Miner]\nEtherbase = \"" + ownAddress + "\"\n\n[Node]\nDataDir = \"data\"\n"
)

// payoutNode is a node whose miner_setEtherbase changes eth_coinbase.
type payoutNode struct {
	mu       sync.Mutex
	coinbase string
	set      []string
}

func (p *payoutNode) handlers() map[string]ipctest.Handler {
	return map[string]ipctest.Handler{
		"web3_clientVersion": ipctest.Value("Ghost/test"),
		"eth_mining":         ipctest.Value(true),
		"eth_hashrate":       ipctest.Value("0x0"),
		"eth_blockNumber":    ipctest.Value("0x10"),
		"eth_coinbase": func(json.RawMessage) (any, *ipctest.Error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			return p.coinbase, nil
		},
		"miner_setEtherbase": func(params json.RawMessage) (any, *ipctest.Error) {
			var args []string
			if err := json.Unmarshal(params, &args); err != nil || len(args) != 1 {
				return nil, &ipctest.Error{Code: -32602, Message: "bad params"}
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			p.coinbase = strings.ToLower(args[0])
			p.set = append(p.set, args[0])
			return true, nil
		},
	}
}

func decodePayout(t *testing.T, rec *httptest.ResponseRecorder) status.PayoutResult {
	t.Helper()
	var res status.PayoutResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return res
}

func TestPayoutAddressControl(t *testing.T) {
	miner := &payoutNode{coinbase: strings.ToLower(ownAddress)}
	_, node := startFake(t, miner.handlers())
	dir := t.TempDir()
	config := filepath.Join(dir, status.ConfigFile)
	if err := os.WriteFile(config, []byte(installerConfig), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(config, 0o640); err != nil {
		t.Fatal(err)
	}
	h := panelHandler(t, node, panelOpts{nodeDir: dir})

	// Entered in lower case, saved in checksum form.
	rec := post(t, h, "/api/mining/payout", `{"address": "`+strings.ToLower(checksummedPayout)+`"}`, signedPost)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	res := decodePayout(t, rec)
	if !res.OK || !res.Saved || res.PayoutAddress == nil || !strings.EqualFold(*res.PayoutAddress, checksummedPayout) {
		t.Errorf("result %+v, want ok with the new address read back", res)
	}
	if !slices.Equal(miner.set, []string{checksummedPayout}) {
		t.Errorf("miner_setEtherbase got %v", miner.set)
	}
	want := strings.Replace(installerConfig, ownAddress, checksummedPayout, 1)
	if got := readFile(t, config); got != want {
		t.Errorf("config.toml = %q, want %q", got, want)
	}
	fi, err := os.Stat(config)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o640 {
		t.Errorf("config.toml mode %v, want 0640 as before", fi.Mode().Perm())
	}

	// The mining panel shows it, and the blocks-won index counts for it.
	if got := getPanel(t, h, "/api/mining"); !strings.EqualFold(got["payoutAddress"].(string), checksummedPayout) {
		t.Errorf("mining panel payout %v", got["payoutAddress"])
	}
	if got := getPanel(t, h, "/api/rewards"); !strings.EqualFold(got["payoutAddress"].(string), checksummedPayout) {
		t.Errorf("rewards panel payout %v", got["payoutAddress"])
	}

	// Refused addresses change nothing.
	for _, bad := range []string{
		`{"address": "0x5aaeb6053F3E94C9b9A09f33669435E7Ef1BeAed"}`,
		`{"address": "0x1234"}`,
		`{"address": "5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"}`,
		`{"address": "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed\"\nInjected = 1"}`,
		`{"address": 5}`,
		`{}`,
		`not json`,
	} {
		rec := post(t, h, "/api/mining/payout", bad, signedPost)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: status %d, want 400", bad, rec.Code)
		}
		if res := decodePayout(t, rec); res.OK || res.Error == "" {
			t.Errorf("body %s: result %+v", bad, res)
		}
	}
	if len(miner.set) != 1 {
		t.Errorf("refused addresses reached the node: %v", miner.set)
	}
	if got := readFile(t, config); got != want {
		t.Errorf("refused addresses changed config.toml: %q", got)
	}
}

func TestPayoutAddressNodeDown(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, status.ConfigFile)
	if err := os.WriteFile(config, []byte(installerConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	h := panelHandler(t, nil, panelOpts{nodeDir: dir})
	res := decodePayout(t, post(t, h, "/api/mining/payout", `{"address": "`+checksummedPayout+`"}`, signedPost))
	if !res.OK || !res.Saved || !strings.Contains(res.Message, "not running") {
		t.Errorf("result %+v, want saved for the next start", res)
	}
	if !strings.Contains(readFile(t, config), checksummedPayout) {
		t.Error("config.toml not rewritten")
	}

	// Without a config.toml nothing is created and the node is not asked.
	empty := t.TempDir()
	h = panelHandler(t, nil, panelOpts{nodeDir: empty})
	res = decodePayout(t, post(t, h, "/api/mining/payout", `{"address": "`+checksummedPayout+`"}`, signedPost))
	if res.OK || res.Saved || !strings.Contains(res.Error, "not found") {
		t.Errorf("result %+v, want the missing config reported", res)
	}
}

// fakeRunner records commands; restart restarts the fake node.
type fakeRunner struct {
	mu       sync.Mutex
	commands []string
	onRun    func(argv []string)
}

func (f *fakeRunner) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	argv := append([]string{name}, args...)
	f.mu.Lock()
	f.commands = append(f.commands, strings.Join(argv, " "))
	f.mu.Unlock()
	if f.onRun != nil {
		f.onRun(argv)
	}
	if slices.Contains(args, "is-enabled") {
		return []byte("enabled\n"), nil
	}
	return []byte("synthetic output\n"), nil
}

func (f *fakeRunner) ran() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.commands...)
}

// serviceHandler is a dashboard on a fake Linux install whose units the
// installer wrote.
func serviceHandler(t *testing.T, node *ipc.Client, runner *fakeRunner) (http.Handler, string) {
	t.Helper()
	home := t.TempDir()
	nodeDir := filepath.Join(home, ".ghost-node")
	units := filepath.Join(home, ".config", "systemd", "user")
	files := map[string]string{
		filepath.Join(units, "ghost-node.service"):        "[Service]\nExecStart=\"" + filepath.Join(nodeDir, "bin", "geth") + "\" --mine\n",
		filepath.Join(units, "ghost-node-update.service"): "[Service]\nType=oneshot\nExecStart=\"" + filepath.Join(nodeDir, "update") + "\"\n",
		filepath.Join(units, "ghost-node-update.timer"):   "[Timer]\nOnCalendar=daily\n",
		filepath.Join(nodeDir, "update"):                  "#!/bin/sh\n",
		filepath.Join(nodeDir, status.ConfigFile):         installerConfig,
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if node == nil {
		node = ipc.New(filepath.Join(t.TempDir(), "data", "geth.ipc"))
	}
	env := &service.Env{GOOS: "linux", Home: home, UID: 1000, NodeDir: nodeDir, Run: runner.run}
	return New(Config{
		Port:         testPort,
		Key:          testKey,
		Node:         node,
		NodeDir:      nodeDir,
		LogicalCores: testCores,
		AutoUpdate:   func() *bool { return nil },
		Services:     env,
		RestartWait:  3 * time.Second,
	}), nodeDir
}

func TestRestartShowsNodeBack(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "geth.ipc")
	handlers := map[string]ipctest.Handler{
		"web3_clientVersion": ipctest.Value("Ghost/test"),
		"eth_blockNumber":    ipctest.Value("0x2a"),
	}
	fake, err := ipctest.Start(sock, handlers)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		fake.Close()
	})
	node := ipc.New(sock)
	t.Cleanup(func() { _ = node.Close() })
	// Connect first, as the page's refreshes would have.
	if err := node.Call(context.Background(), nil, "web3_clientVersion"); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{}
	runner.onRun = func(argv []string) {
		if !slices.Contains(argv, "restart") {
			return
		}
		// The node goes down and comes back a little later on a new socket.
		mu.Lock()
		fake.Close()
		mu.Unlock()
		go func() {
			time.Sleep(700 * time.Millisecond)
			mu.Lock()
			defer mu.Unlock()
			fake, _ = ipctest.Start(sock, handlers)
		}()
	}
	h, _ := serviceHandler(t, node, runner)

	rec := post(t, h, "/api/node/restart", "", signedPost)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var res RestartResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.OK || !res.Back || res.BlockNumber == nil || *res.BlockNumber != 42 {
		t.Errorf("result %+v, want the node back at block 42", res)
	}
	if got := runner.ran(); !slices.Equal(got, []string{"systemctl --user restart ghost-node.service"}) {
		t.Errorf("ran %q", got)
	}
}

func TestRestartNodeNotBack(t *testing.T) {
	runner := &fakeRunner{}
	h, _ := serviceHandler(t, nil, runner)
	start := time.Now()
	rec := post(t, h, "/api/node/restart", "", signedPost)
	var res RestartResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Back || !strings.Contains(res.Message, "has not answered") {
		t.Errorf("result %+v, want the node reported not back", res)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("restart waited %s", time.Since(start))
	}
}

func TestUpdateControls(t *testing.T) {
	runner := &fakeRunner{}
	h, nodeDir := serviceHandler(t, nil, runner)

	got := getPanel(t, h, "/api/updates")
	if got["available"] != true || got["installed"] != true || got["on"] != true || got["service"] != "ghost-node-update.timer" {
		t.Errorf("updates panel %v", got)
	}

	rec := post(t, h, "/api/updates/run", "", signedPost)
	var res UpdatesResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Output != "synthetic output\n" || !slices.Equal(res.Command, []string{filepath.Join(nodeDir, "update")}) {
		t.Errorf("update now %+v", res)
	}

	runner.commands = nil
	rec = post(t, h, "/api/updates/off", "", signedPost)
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(runner.ran(), "systemctl --user disable --now ghost-node-update.timer") {
		t.Errorf("off ran %q", runner.ran())
	}
}

func TestServiceControlsRefused(t *testing.T) {
	miner := &payoutNode{coinbase: ownAddress}
	fake, node := startFake(t, miner.handlers())
	runner := &fakeRunner{}
	h, nodeDir := serviceHandler(t, node, runner)

	cases := []struct {
		name   string
		mutate func(*http.Request)
		want   int
	}{
		{"no key", func(r *http.Request) { r.Header.Set("Origin", goodOrigin) }, http.StatusUnauthorized},
		{"forged cookie", func(r *http.Request) {
			r.Header.Set("Origin", goodOrigin)
			r.AddCookie(&http.Cookie{Name: CookieName, Value: testKey})
		}, http.StatusUnauthorized},
		{"no origin", withSession, http.StatusForbidden},
		{"other origin", func(r *http.Request) {
			withSession(r)
			r.Header.Set("Origin", "http://evil.example")
		}, http.StatusForbidden},
	}
	targets := []string{"/api/mining/payout", "/api/node/restart", "/api/updates/run", "/api/updates/on", "/api/updates/off"}
	for _, target := range targets {
		for _, c := range cases {
			rec := post(t, h, target, `{"address": "`+checksummedPayout+`"}`, c.mutate)
			if rec.Code != c.want {
				t.Errorf("%s %s: status %d, want %d", target, c.name, rec.Code, c.want)
			}
		}
		if rec := do(h, http.MethodGet, target, goodHost, withSession); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: status %d, want 405", target, rec.Code)
		}
	}
	if rec := do(h, http.MethodGet, "/api/updates", goodHost, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("updates panel without key: status %d", rec.Code)
	}
	if got := runner.ran(); len(got) != 0 {
		t.Errorf("refused requests ran %q", got)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("refused requests reached the node: %v", calls)
	}
	if got := readFile(t, filepath.Join(nodeDir, status.ConfigFile)); got != installerConfig {
		t.Errorf("refused requests changed config.toml: %q", got)
	}
}

func TestPageHasSettings(t *testing.T) {
	h := newHandler(t, nil)
	page := do(h, http.MethodGet, "/", goodHost, withSession).Body.String()
	for _, want := range []string{`id="payout-input"`, `id="node-restart"`, `id="updates-run"`, `id="updates-on"`, `id="updates-off"`, `id="updates-output"`} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	js := do(h, http.MethodGet, "/static/app.js", goodHost, withSession).Body.String()
	for _, api := range []string{"/api/mining/payout", "/api/node/restart", "/api/updates", "/api/updates/run", "/api/updates/on", "/api/updates/off"} {
		if !strings.Contains(js, `"`+api+`"`) {
			t.Errorf("app.js does not use %s", api)
		}
	}
}
