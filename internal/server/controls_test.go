package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/ghostcoin-core/ghost-node-dashboard/internal/ipc/ipctest"
	"github.com/ghostcoin-core/ghost-node-dashboard/internal/status"
)

const goodOrigin = "http://127.0.0.1:8580"

// fakeMiner is a node whose miner_start and miner_stop change what
// eth_mining reports, recording the thread counts miner_start was given.
type fakeMiner struct {
	mu      sync.Mutex
	mining  bool
	started []json.RawMessage
}

func (m *fakeMiner) handlers() map[string]ipctest.Handler {
	return map[string]ipctest.Handler{
		"miner_start": func(params json.RawMessage) (any, *ipctest.Error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.mining = true
			m.started = append(m.started, params)
			return nil, nil
		},
		"miner_stop": func(json.RawMessage) (any, *ipctest.Error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.mining = false
			return nil, nil
		},
		"eth_mining": func(json.RawMessage) (any, *ipctest.Error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			return m.mining, nil
		},
		"web3_clientVersion": ipctest.Value("Ghost/test"),
		"eth_hashrate":       ipctest.Value("0x0"),
		"eth_coinbase":       ipctest.Value(ownAddress),
	}
}

func (m *fakeMiner) starts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, p := range m.started {
		out = append(out, string(p))
	}
	return out
}

func post(t *testing.T, h http.Handler, target, body string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Host = goodHost
	req.Header.Set("Content-Type", "application/json")
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func signedPost(r *http.Request) {
	withSession(r)
	r.Header.Set("Origin", goodOrigin)
}

func decodeResult(t *testing.T, rec *httptest.ResponseRecorder) status.ControlResult {
	t.Helper()
	var res status.ControlResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return res
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSetCores(t *testing.T) {
	miner := &fakeMiner{}
	fake, node := startFake(t, miner.handlers())
	dir := t.TempDir()
	threads := filepath.Join(dir, status.MinerThreadsFile)
	if err := os.WriteFile(threads, []byte("8\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(threads, 0o640); err != nil {
		t.Fatal(err)
	}
	h := panelHandler(t, node, panelOpts{nodeDir: dir})

	rec := post(t, h, "/api/mining/cores", `{"cores": 3}`, signedPost)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	res := decodeResult(t, rec)
	if !res.OK || res.Error != "" {
		t.Errorf("result %+v, want ok", res)
	}
	if !strings.Contains(res.Message, "3 cores") {
		t.Errorf("message %q does not name 3 cores", res.Message)
	}
	if res.Mining == nil || !*res.Mining || res.CoresUsed != 3 {
		t.Errorf("read back mining %v, cores %d", res.Mining, res.CoresUsed)
	}

	if got := miner.starts(); !slices.Equal(got, []string{"[3]"}) {
		t.Errorf("miner_start params %v, want [[3]]", got)
	}
	calls := fake.Calls()
	if i := slices.Index(calls, "miner_start"); i < 0 || !slices.Contains(calls[i:], "eth_mining") {
		t.Errorf("calls %v: eth_mining not read back after miner_start", calls)
	}

	if got := readFile(t, threads); got != "3\n" {
		t.Errorf("miner-threads = %q, want 3", got)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(threads)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o640 {
			t.Errorf("miner-threads mode %v, want 0640 as before", fi.Mode().Perm())
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temporary file %s left behind", e.Name())
		}
	}

	// The mining panel now shows the new count.
	got := getPanel(t, h, "/api/mining")
	if got["coresUsed"] != float64(3) || got["coresFromSetting"] != true {
		t.Errorf("panel cores %v (from setting %v), want 3", got["coresUsed"], got["coresFromSetting"])
	}
}

func TestSetCoresNewFile(t *testing.T) {
	miner := &fakeMiner{}
	_, node := startFake(t, miner.handlers())
	dir := t.TempDir()
	h := panelHandler(t, node, panelOpts{nodeDir: dir})

	rec := post(t, h, "/api/mining/cores", `{"cores": 8}`, signedPost)
	if res := decodeResult(t, rec); !res.OK {
		t.Fatalf("result %+v, want ok", res)
	}
	if got := readFile(t, filepath.Join(dir, status.MinerThreadsFile)); got != "8\n" {
		t.Errorf("miner-threads = %q, want 8", got)
	}
}

func TestSetCoresOutOfRange(t *testing.T) {
	miner := &fakeMiner{}
	fake, node := startFake(t, miner.handlers())
	dir := t.TempDir()
	threads := filepath.Join(dir, status.MinerThreadsFile)
	if err := os.WriteFile(threads, []byte("2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := panelHandler(t, node, panelOpts{nodeDir: dir})

	for _, body := range []string{
		`{"cores": 0}`,
		`{"cores": -1}`,
		`{"cores": 9}`,
		`{"cores": 100000000000000000000}`,
		`{"cores": 2.5}`,
		`{"cores": 4.0}`,
		`{"cores": 1e1}`,
		`{"cores": "4"}`,
		`{"cores": null}`,
		`{"cores": true}`,
		`{}`,
		`4`,
		`not json`,
		``,
	} {
		rec := post(t, h, "/api/mining/cores", body, signedPost)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: status %d, want 400", body, rec.Code)
			continue
		}
		res := decodeResult(t, rec)
		if res.OK || !strings.Contains(res.Error, "from 1 to 8") {
			t.Errorf("body %q: result %+v, want an error naming 1 to 8", body, res)
		}
	}
	if calls := fake.Calls(); slices.Contains(calls, "miner_start") {
		t.Errorf("refused counts still called the node: %v", calls)
	}
	if got := readFile(t, threads); got != "2\n" {
		t.Errorf("miner-threads changed to %q", got)
	}
}

func TestSetCoresNodeDown(t *testing.T) {
	dir := t.TempDir()
	h := panelHandler(t, nil, panelOpts{nodeDir: dir})

	res := decodeResult(t, post(t, h, "/api/mining/cores", `{"cores": 4}`, signedPost))
	if res.OK || !strings.Contains(res.Error, "not running") {
		t.Errorf("result %+v, want an error saying the node is not running", res)
	}
	if res.Mining != nil {
		t.Errorf("mining = %v, want null", *res.Mining)
	}
	// The choice still sticks for the next start.
	if got := readFile(t, filepath.Join(dir, status.MinerThreadsFile)); got != "4\n" {
		t.Errorf("miner-threads = %q, want 4", got)
	}
}

func TestSetCoresNodeRefuses(t *testing.T) {
	handlers := (&fakeMiner{}).handlers()
	handlers["miner_start"] = func(json.RawMessage) (any, *ipctest.Error) {
		return nil, &ipctest.Error{Code: -32000, Message: "etherbase missing"}
	}
	_, node := startFake(t, handlers)
	h := panelHandler(t, node, panelOpts{})

	res := decodeResult(t, post(t, h, "/api/mining/cores", `{"cores": 2}`, signedPost))
	if res.OK || !strings.Contains(res.Error, "etherbase missing") {
		t.Errorf("result %+v, want the node's error in the message", res)
	}
}

func TestPauseResume(t *testing.T) {
	miner := &fakeMiner{mining: true}
	fake, node := startFake(t, miner.handlers())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, status.MinerThreadsFile), []byte("5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	paused := filepath.Join(dir, status.MiningPausedFile)
	h := panelHandler(t, node, panelOpts{nodeDir: dir})

	rec := post(t, h, "/api/mining/pause", "", signedPost)
	if rec.Code != http.StatusOK {
		t.Fatalf("pause: status %d: %s", rec.Code, rec.Body.String())
	}
	res := decodeResult(t, rec)
	if !res.OK || !res.Paused || res.Mining == nil || *res.Mining {
		t.Errorf("pause result %+v, want ok, paused and not mining", res)
	}
	if !strings.Contains(res.Message, "keeps syncing") {
		t.Errorf("pause message %q", res.Message)
	}
	if !slices.Contains(fake.Calls(), "miner_stop") {
		t.Errorf("pause did not call miner_stop: %v", fake.Calls())
	}
	if _, err := os.Stat(paused); err != nil {
		t.Errorf("mining-paused not written: %v", err)
	}
	if got := getPanel(t, h, "/api/mining"); got["paused"] != true || got["mining"] != false {
		t.Errorf("panel after pause: paused %v, mining %v", got["paused"], got["mining"])
	}

	// Choosing cores while paused saves them without restarting the miner.
	res = decodeResult(t, post(t, h, "/api/mining/cores", `{"cores": 6}`, signedPost))
	if !res.OK || !res.Paused || res.CoresUsed != 6 {
		t.Errorf("cores while paused %+v", res)
	}
	if got := miner.starts(); len(got) != 0 {
		t.Errorf("cores while paused called miner_start %v", got)
	}

	rec = post(t, h, "/api/mining/resume", "", signedPost)
	if rec.Code != http.StatusOK {
		t.Fatalf("resume: status %d: %s", rec.Code, rec.Body.String())
	}
	res = decodeResult(t, rec)
	if !res.OK || res.Paused || res.Mining == nil || !*res.Mining {
		t.Errorf("resume result %+v, want ok, not paused and mining", res)
	}
	if got := miner.starts(); !slices.Equal(got, []string{"[6]"}) {
		t.Errorf("resume miner_start params %v, want the saved [6]", got)
	}
	if _, err := os.Stat(paused); !os.IsNotExist(err) {
		t.Errorf("mining-paused still there after resume (%v)", err)
	}
	if got := getPanel(t, h, "/api/mining"); got["paused"] != false || got["mining"] != true {
		t.Errorf("panel after resume: paused %v, mining %v", got["paused"], got["mining"])
	}
}

func TestPauseNodeStillMining(t *testing.T) {
	handlers := (&fakeMiner{}).handlers()
	handlers["eth_mining"] = ipctest.Value(true)
	_, node := startFake(t, handlers)
	h := panelHandler(t, node, panelOpts{})

	res := decodeResult(t, post(t, h, "/api/mining/pause", "", signedPost))
	if res.OK || !strings.Contains(res.Error, "still mining") {
		t.Errorf("result %+v, want an error saying it is still mining", res)
	}
}

func TestControlsRefused(t *testing.T) {
	miner := &fakeMiner{mining: true}
	fake, node := startFake(t, miner.handlers())
	dir := t.TempDir()
	h := panelHandler(t, node, panelOpts{nodeDir: dir})

	type tc struct {
		name   string
		mutate func(*http.Request)
		want   int
	}
	cases := []tc{
		{"no key", func(r *http.Request) { r.Header.Set("Origin", goodOrigin) }, http.StatusUnauthorized},
		{"forged cookie", func(r *http.Request) {
			r.Header.Set("Origin", goodOrigin)
			r.AddCookie(&http.Cookie{Name: CookieName, Value: testKey})
		}, http.StatusUnauthorized},
		{"no origin", withSession, http.StatusForbidden},
	}
	for _, origin := range []string{"http://evil.example", "null", "http://127.0.0.1:9999", "https://127.0.0.1:8580"} {
		origin := origin
		cases = append(cases, tc{"origin " + origin, func(r *http.Request) {
			withSession(r)
			r.Header.Set("Origin", origin)
		}, http.StatusForbidden})
	}
	for _, target := range []string{"/api/mining/cores", "/api/mining/pause", "/api/mining/resume"} {
		for _, c := range cases {
			rec := post(t, h, target, `{"cores": 2}`, c.mutate)
			if rec.Code != c.want {
				t.Errorf("%s %s: status %d, want %d", target, c.name, rec.Code, c.want)
			}
		}
		// GET is not a way in either.
		rec := do(h, http.MethodGet, target, goodHost, withSession)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: status %d, want 405", target, rec.Code)
		}
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("refused requests reached the node: %v", calls)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("refused requests wrote files: %v", entries)
	}
}

func TestPageHasControls(t *testing.T) {
	h := newHandler(t, nil)
	page := do(h, http.MethodGet, "/", goodHost, withSession).Body.String()
	for _, want := range []string{`id="cores-input"`, `type="range"`, `id="mining-pause"`, `id="mining-resume"`, "mining-paused", "restart resumes mining"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	js := do(h, http.MethodGet, "/static/app.js", goodHost, withSession).Body.String()
	for _, api := range []string{"/api/mining/cores", "/api/mining/pause", "/api/mining/resume"} {
		if !strings.Contains(js, `"`+api+`"`) {
			t.Errorf("app.js does not post to %s", api)
		}
	}
}
