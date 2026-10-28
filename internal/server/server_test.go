package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghostcoin-core/ghost-node-dashboard/internal/ipc"
	"github.com/ghostcoin-core/ghost-node-dashboard/internal/ipc/ipctest"
)

const (
	testPort = 8580
	testKey  = "synthetic-test-key"
	goodHost = "127.0.0.1:8580"
)

func newHandler(t *testing.T, node *ipc.Client) http.Handler {
	t.Helper()
	if node == nil {
		node = ipc.New(filepath.Join(t.TempDir(), "absent.ipc"))
	}
	return New(Config{Port: testPort, Key: testKey, Node: node})
}

func do(h http.Handler, method, target, host string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.Host = host
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func withSession(r *http.Request) {
	r.AddCookie(&http.Cookie{Name: CookieName, Value: sessionValue(testKey)})
}

// signIn opens the dashboard with the key and returns the cookie it set.
func signIn(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	rec := do(h, http.MethodGet, "/open?key="+url.QueryEscape(testKey), goodHost, nil)
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			return c
		}
	}
	t.Fatalf("no %s cookie after /open (status %d)", CookieName, rec.Code)
	return nil
}

func TestHostRefused(t *testing.T) {
	h := newHandler(t, nil)
	for _, host := range []string{
		"evil.example:8580",
		"127.0.0.1:9999",
		"localhost:1234",
		"127.0.0.1",
		"localhost",
		"0.0.0.0:8580",
		"[::1]:8580",
		"127.0.0.1.evil.example:8580",
		"",
	} {
		for _, target := range []string{"/", "/api/status", "/open?key=" + testKey, "/static/app.js"} {
			rec := do(h, http.MethodGet, target, host, withSession)
			if rec.Code != http.StatusForbidden {
				t.Errorf("Host %q %s: status %d, want 403", host, target, rec.Code)
			}
			if len(rec.Result().Cookies()) != 0 {
				t.Errorf("Host %q %s: set a cookie", host, target)
			}
		}
	}
	for _, host := range []string{"127.0.0.1:8580", "localhost:8580", "LOCALHOST:8580"} {
		rec := do(h, http.MethodGet, "/", host, withSession)
		if rec.Code != http.StatusOK {
			t.Errorf("Host %q: status %d, want 200", host, rec.Code)
		}
	}
}

func TestKeyCookie(t *testing.T) {
	h := newHandler(t, nil)

	rec := do(h, http.MethodGet, "/open?key="+testKey, goodHost, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("/open with key: status %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("redirect to %q, want /", loc)
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no session cookie set")
	}
	if !cookie.HttpOnly {
		t.Error("cookie is not HttpOnly")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie SameSite = %v, want Strict", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Errorf("cookie path %q, want /", cookie.Path)
	}
	if strings.Contains(cookie.Value, testKey) {
		t.Error("cookie carries the raw key")
	}

	// The cookie opens the page and the API.
	for _, target := range []string{"/", "/api/status", "/static/app.js"} {
		rec := do(h, http.MethodGet, target, goodHost, func(r *http.Request) { r.AddCookie(cookie) })
		if rec.Code != http.StatusOK {
			t.Errorf("%s with cookie: status %d, want 200", target, rec.Code)
		}
	}

	// Wrong, partial, longer or missing keys get nothing.
	for _, bad := range []string{"", "wrong", testKey[:len(testKey)-1], testKey + "x", strings.ToUpper(testKey)} {
		rec := do(h, http.MethodGet, "/open?key="+url.QueryEscape(bad), goodHost, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("/open?key=%q: status %d, want 401", bad, rec.Code)
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Errorf("/open?key=%q set a cookie", bad)
		}
	}

	// A forged cookie value does not work.
	rec = do(h, http.MethodGet, "/api/status", goodHost, func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: CookieName, Value: testKey})
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("forged cookie: status %d, want 401", rec.Code)
	}

	// A handler with another key does not accept this cookie.
	other := New(Config{Port: testPort, Key: "another-test-key", Node: ipc.New(filepath.Join(t.TempDir(), "x.ipc"))})
	rec = do(other, http.MethodGet, "/", goodHost, func(r *http.Request) { r.AddCookie(cookie) })
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("cookie for another key: status %d, want 401", rec.Code)
	}
}

func TestMissingCookie(t *testing.T) {
	h := newHandler(t, nil)
	for _, target := range []string{"/", "/api/status", "/api/anything", "/static/app.js", "/nope"} {
		rec := do(h, http.MethodGet, target, goodHost, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without cookie: status %d, want 401", target, rec.Code)
			continue
		}
		body := rec.Body.String()
		if !strings.Contains(body, "link the installer printed") {
			t.Errorf("%s: 401 page does not point to the installer link: %q", target, body)
		}
		if strings.Contains(body, "chainId") || strings.Contains(body, "status-title") {
			t.Errorf("%s: 401 leaked dashboard content", target)
		}
	}
}

func TestPostOriginChecked(t *testing.T) {
	h := newHandler(t, nil)
	for _, origin := range []string{"", "null", "http://evil.example", "http://127.0.0.1:9999", "https://127.0.0.1:8580", "http://127.0.0.1:8580.evil.example"} {
		rec := do(h, http.MethodPost, "/api/status", goodHost, func(r *http.Request) {
			withSession(r)
			if origin != "" {
				r.Header.Set("Origin", origin)
			}
		})
		if rec.Code != http.StatusForbidden {
			t.Errorf("POST with Origin %q: status %d, want 403", origin, rec.Code)
		}
	}
	for _, origin := range []string{"http://127.0.0.1:8580", "http://localhost:8580"} {
		rec := do(h, http.MethodPost, "/api/status", goodHost, func(r *http.Request) {
			withSession(r)
			r.Header.Set("Origin", origin)
		})
		// The Origin is accepted; the route itself only allows GET.
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST with Origin %q: status %d, want 405", origin, rec.Code)
		}
	}
	// A matching Origin does not replace the cookie.
	rec := do(h, http.MethodPost, "/api/status", goodHost, func(r *http.Request) {
		r.Header.Set("Origin", "http://127.0.0.1:8580")
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("POST without cookie: status %d, want 401", rec.Code)
	}
}

func TestNoCORSHeader(t *testing.T) {
	h := newHandler(t, nil)
	cases := []struct {
		method, target string
		session        bool
	}{
		{http.MethodGet, "/", true},
		{http.MethodGet, "/api/status", true},
		{http.MethodGet, "/api/status", false},
		{http.MethodGet, "/open?key=" + testKey, false},
		{http.MethodOptions, "/api/status", true},
		{http.MethodPost, "/api/status", true},
	}
	for _, c := range cases {
		rec := do(h, c.method, c.target, goodHost, func(r *http.Request) {
			r.Header.Set("Origin", "http://evil.example")
			r.Header.Set("Access-Control-Request-Method", "GET")
			if c.session {
				withSession(r)
			}
		})
		for name := range rec.Header() {
			if strings.HasPrefix(strings.ToLower(name), "access-control-") {
				t.Errorf("%s %s: sent %s", c.method, c.target, name)
			}
		}
		if c.method == http.MethodOptions && rec.Code < 400 {
			t.Errorf("OPTIONS preflight answered %d", rec.Code)
		}
	}
}

func getStatus(t *testing.T, h http.Handler) map[string]any {
	t.Helper()
	cookie := signIn(t, h)
	rec := do(h, http.MethodGet, "/api/status", goodHost, func(r *http.Request) { r.AddCookie(cookie) })
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/status: %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type %q", ct)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	return got
}

func TestNodeNotRunning(t *testing.T) {
	h := newHandler(t, ipc.New(filepath.Join(t.TempDir(), "data", "geth.ipc")))
	got := getStatus(t, h)
	if got["running"] != false {
		t.Errorf("running = %v, want false", got["running"])
	}
	if got["error"] != "node not running" {
		t.Errorf("error = %v, want node not running", got["error"])
	}
	for _, f := range []string{"chainId", "blockNumber", "peerCount", "syncing", "mining", "payoutAddress", "clientVersion"} {
		v, ok := got[f]
		if !ok {
			t.Errorf("field %s missing", f)
		} else if v != nil {
			t.Errorf("field %s = %v, want null", f, v)
		}
	}
}

func TestStatusFromIPC(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "geth.ipc")
	fake, err := ipctest.Start(sock, map[string]ipctest.Handler{
		"web3_clientVersion": ipctest.Value("Ghost/v1.2.3-test/linux-amd64/go1.25"),
		"eth_chainId":        ipctest.Value("0x1e61"),
		"eth_blockNumber":    ipctest.Value("0x2a"),
		"net_peerCount":      ipctest.Value("0x5"),
		"eth_syncing":        ipctest.Value(map[string]string{"currentBlock": "0x2a", "highestBlock": "0x64"}),
		"eth_mining":         ipctest.Value(true),
		"eth_coinbase":       ipctest.Value("0x00000000000000000000000000000000000000aa"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer fake.Close()

	node := ipc.New(sock)
	defer node.Close()
	got := getStatus(t, newHandler(t, node))

	want := map[string]any{
		"running":       true,
		"chainId":       float64(0x1e61),
		"blockNumber":   float64(42),
		"peerCount":     float64(5),
		"syncing":       true,
		"mining":        true,
		"payoutAddress": "0x00000000000000000000000000000000000000aa",
		"clientVersion": "Ghost/v1.2.3-test/linux-amd64/go1.25",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %#v, want %#v", k, got[k], v)
		}
	}
	if _, ok := got["error"]; ok {
		t.Errorf("unexpected error field: %v", got["error"])
	}
}

func TestStatusMethodsMissing(t *testing.T) {
	// A node that is in sync, has no payout address and lacks eth_mining
	// still reports as running with those fields null or false.
	sock := filepath.Join(t.TempDir(), "geth.ipc")
	fake, err := ipctest.Start(sock, map[string]ipctest.Handler{
		"web3_clientVersion": ipctest.Value("Ghost/test"),
		"eth_chainId":        ipctest.Value("0x1"),
		"eth_blockNumber":    ipctest.Value("0x0"),
		"net_peerCount":      ipctest.Value("0x0"),
		"eth_syncing":        ipctest.Value(false),
		"eth_coinbase": func(json.RawMessage) (any, *ipctest.Error) {
			return nil, &ipctest.Error{Code: -32000, Message: "etherbase must be explicitly specified"}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer fake.Close()

	got := getStatus(t, newHandler(t, ipc.New(sock)))
	if got["running"] != true {
		t.Fatalf("running = %v, want true", got["running"])
	}
	if got["syncing"] != false {
		t.Errorf("syncing = %v, want false", got["syncing"])
	}
	if got["blockNumber"] != float64(0) {
		t.Errorf("blockNumber = %v, want 0", got["blockNumber"])
	}
	if got["mining"] != nil {
		t.Errorf("mining = %v, want null", got["mining"])
	}
	if got["payoutAddress"] != nil {
		t.Errorf("payoutAddress = %v, want null", got["payoutAddress"])
	}
}

func TestPageBranding(t *testing.T) {
	h := newHandler(t, nil)
	page := do(h, http.MethodGet, "/", goodHost, withSession).Body.String()
	for _, want := range []string{
		`viewBox="0 0 100 100"`,
		`M37 50a13 13 0 0 1 26 0v13l-6.5-5.2-6.5 5.2-6.5-5.2-6.5 5.2z`,
		`#6B4CFF`,
		`#0E0D1A`,
		`name="viewport"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if n := strings.Count(page, `width="26" height="26"`); n != 8 {
		t.Errorf("logo has %d squares, want 8", n)
	}
	css := do(h, http.MethodGet, "/static/style.css", goodHost, withSession).Body.String()
	for _, want := range []string{`"Instrument Sans", system-ui, sans-serif`, `"IBM Plex Mono", ui-monospace, monospace`, "#FFFFFF"} {
		if !strings.Contains(css, want) {
			t.Errorf("style.css lacks %q", want)
		}
	}
	// The only files the stylesheet loads are the dashboard's own fonts.
	urls := strings.Split(css, "url(")
	if len(urls) < 2 {
		t.Error("style.css loads no fonts")
	}
	for _, rest := range urls[1:] {
		if !strings.HasPrefix(strings.TrimLeft(rest, `"'`), "/static/fonts/") {
			t.Errorf("style.css loads a file outside /static/fonts/: url(%.40s", rest)
		}
	}
	core := do(h, http.MethodGet, "/static/app.js", goodHost, withSession).Body.String()
	if !strings.Contains(core, "5000") {
		t.Error("app.js does not refresh every 5 seconds")
	}
	js := core + screenJS(t, h)
	for _, api := range []string{"/api/node", "/api/mining", "/api/network"} {
		if !strings.Contains(js, `"`+api+`"`) {
			t.Errorf("app.js and the screen files do not poll %s", api)
		}
	}
}
