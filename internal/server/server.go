// Package server is the dashboard's HTTP handler. It only answers requests
// addressed to 127.0.0.1 or localhost on its own port, and only from a
// browser that opened the dashboard with the key.
package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"io"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ghostcoin-core/ghost-node-dashboard/internal/status"
	"github.com/ghostcoin-core/ghost-node-dashboard/web"
)

// CookieName is the session cookie set by /open.
const CookieName = "ghost_dash"

// StatusTimeout bounds how long /api/status waits for the node.
const StatusTimeout = 4 * time.Second

// Config is what the handler needs.
type Config struct {
	Port int
	Key  string
	Node status.Caller

	// NodeDir is the node's directory, where miner-threads lives.
	NodeDir string
	// LogicalCores is the computer's core count; 0 means runtime.NumCPU.
	LogicalCores int
	// AutoUpdate reports whether the update service is installed; nil
	// means status.AutoUpdateInstalled.
	AutoUpdate func() *bool
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

type handler struct {
	key     []byte
	session []byte
	hosts   map[string]bool
	origins map[string]bool
	node    status.Caller
	nodeEnv status.NodeEnv
	mining  status.MiningEnv
	rewards *status.Rewards
	nodeDir string
	mux     *http.ServeMux

	// control serialises the mining controls so two changes do not
	// interleave their node calls and file writes.
	control sync.Mutex
}

// New returns the dashboard handler.
func New(cfg Config) http.Handler {
	h := &handler{
		key:     []byte(cfg.Key),
		session: []byte(sessionValue(cfg.Key)),
		hosts:   map[string]bool{},
		origins: map[string]bool{},
		node:    cfg.Node,
		nodeEnv: status.NodeEnv{
			Uptime:     &status.Uptime{},
			AutoUpdate: cfg.AutoUpdate,
			Now:        cfg.Now,
		},
		mining:  status.MiningEnv{NodeDir: cfg.NodeDir, LogicalCores: cfg.LogicalCores},
		rewards: status.NewRewards(status.RewardsEnv{NodeDir: cfg.NodeDir, Now: cfg.Now}),
		nodeDir: cfg.NodeDir,
		mux:     http.NewServeMux(),
	}
	if h.nodeEnv.AutoUpdate == nil {
		h.nodeEnv.AutoUpdate = status.AutoUpdateInstalled
	}
	if h.mining.LogicalCores <= 0 {
		h.mining.LogicalCores = runtime.NumCPU()
	}
	if p, ok := cfg.Node.(interface{ Path() string }); ok {
		h.nodeEnv.IPCPath = p.Path()
	}
	for _, name := range []string{"127.0.0.1", "localhost"} {
		host := fmt.Sprintf("%s:%d", name, cfg.Port)
		h.hosts[host] = true
		h.origins["http://"+host] = true
	}

	static, err := fs.Sub(web.Files, "static")
	if err != nil {
		panic(err)
	}
	h.mux.HandleFunc("GET /{$}", h.index)
	h.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	h.mux.HandleFunc("GET /api/status", h.status)
	h.mux.HandleFunc("GET /api/node", h.nodePanel)
	h.mux.HandleFunc("GET /api/mining", h.miningPanel)
	h.mux.HandleFunc("GET /api/network", h.networkPanel)
	h.mux.HandleFunc("GET /api/rewards", h.rewardsPanel)
	h.mux.HandleFunc("GET /api/log", h.logPanel)
	h.mux.HandleFunc("POST /api/mining/cores", h.setCores)
	h.mux.HandleFunc("POST /api/mining/pause", h.pauseMining)
	h.mux.HandleFunc("POST /api/mining/resume", h.resumeMining)
	return h
}

// sessionValue is the cookie value: derived from the key so the key itself
// is not stored in the browser.
func sessionValue(key string) string {
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte("ghost-dash session v1"))
	return hex.EncodeToString(m.Sum(nil))
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hdr := w.Header()
	hdr.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("X-Frame-Options", "DENY")
	hdr.Set("Referrer-Policy", "no-referrer")
	hdr.Set("Cross-Origin-Resource-Policy", "same-origin")
	hdr.Set("Cache-Control", "no-store")

	// A page on another site can make the browser send requests here, and a
	// DNS name pointed at 127.0.0.1 would carry its own Host. Refuse both.
	if !h.hosts[strings.ToLower(r.Host)] {
		plain(w, http.StatusForbidden, "Forbidden: this dashboard only answers on 127.0.0.1 and localhost.")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if !h.origins[r.Header.Get("Origin")] {
			plain(w, http.StatusForbidden, "Forbidden: request did not come from the dashboard page.")
			return
		}
	}

	if r.URL.Path == "/open" {
		h.open(w, r)
		return
	}
	if !h.signedIn(r) {
		unauthorized(w)
		return
	}
	h.mux.ServeHTTP(w, r)
}

func (h *handler) signedIn(r *http.Request) bool {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), h.session) == 1
}

func (h *handler) open(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		plain(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	given := []byte(r.URL.Query().Get("key"))
	if len(h.key) == 0 || subtle.ConstantTimeCompare(given, h.key) != 1 {
		unauthorized(w)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    string(h.session),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	// Redirect so the key leaves the address bar and the history entry.
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *handler) index(w http.ResponseWriter, r *http.Request) {
	page, err := web.Files.ReadFile("index.html")
	if err != nil {
		http.Error(w, "page missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}

func (h *handler) status(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), StatusTimeout)
	defer cancel()
	writeJSON(w, status.Fetch(ctx, h.node))
}

func (h *handler) nodePanel(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), StatusTimeout)
	defer cancel()
	writeJSON(w, status.FetchNode(ctx, h.node, h.nodeEnv))
}

func (h *handler) miningPanel(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), StatusTimeout)
	defer cancel()
	writeJSON(w, status.FetchMining(ctx, h.node, h.mining))
}

func (h *handler) networkPanel(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), StatusTimeout)
	defer cancel()
	writeJSON(w, status.FetchNetwork(ctx, h.node))
}

func (h *handler) rewardsPanel(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), StatusTimeout)
	defer cancel()
	writeJSON(w, h.rewards.Fetch(ctx, h.node))
}

func (h *handler) logPanel(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, status.ReadLog(h.nodeDir))
}

// maxControlBody bounds a control request's body.
const maxControlBody = 1 << 10

// setCores takes {"cores": n}. n must be a whole number from 1 to the
// computer's logical core count.
func (h *handler) setCores(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxControlBody))
	if err != nil {
		h.badCores(w)
		return
	}
	var req struct {
		Cores json.RawMessage `json:"cores"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		h.badCores(w)
		return
	}
	// Parse the raw text so 2.5, 4.0, 1e1 and "4" are all refused.
	n, err := strconv.Atoi(string(req.Cores))
	if err != nil || !status.ValidCores(n, h.mining.LogicalCores) {
		h.badCores(w)
		return
	}
	h.control.Lock()
	defer h.control.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), StatusTimeout)
	defer cancel()
	writeJSON(w, status.SetCores(ctx, h.node, h.mining, n))
}

func (h *handler) badCores(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(status.ControlResult{
		Error:     fmt.Sprintf("Choose a whole number of cores from 1 to %d.", h.mining.LogicalCores),
		CoresUsed: status.SavedCores(h.mining),
		Paused:    status.MiningPaused(h.nodeDir),
	})
}

func (h *handler) pauseMining(w http.ResponseWriter, r *http.Request) {
	h.control.Lock()
	defer h.control.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), StatusTimeout)
	defer cancel()
	writeJSON(w, status.Pause(ctx, h.node, h.mining))
}

func (h *handler) resumeMining(w http.ResponseWriter, r *http.Request) {
	h.control.Lock()
	defer h.control.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), StatusTimeout)
	defer cancel()
	writeJSON(w, status.Resume(ctx, h.node, h.mining))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

const unauthorizedPage = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Ghost Node Dashboard</title></head>
<body>
<h1>Open the dashboard from its link</h1>
<p>This browser is not signed in to the dashboard. Open the dashboard from the link the installer printed, or run <code>ghost-dash open</code> on this computer.</p>
</body>
</html>
`

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(unauthorizedPage))
}

func plain(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(msg + "\n"))
}
