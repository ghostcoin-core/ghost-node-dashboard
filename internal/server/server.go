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

	"github.com/ghostcoin-core/ghost-node-dashboard/internal/address"
	"github.com/ghostcoin-core/ghost-node-dashboard/internal/service"
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
	// Services finds and drives the node's services; nil means this
	// computer's (service.FromOS).
	Services *service.Env
	// RestartWait bounds how long a restart waits for the node to answer
	// again; 0 means DefaultRestartWait.
	RestartWait time.Duration
}

// DefaultRestartWait is how long a restart waits for the node to come back.
const DefaultRestartWait = 60 * time.Second

// UpdateTimeout bounds one "Update now" run.
const UpdateTimeout = 15 * time.Minute

// ServiceTimeout bounds the service commands other than the update itself.
const ServiceTimeout = 20 * time.Second

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

	services    service.Env
	restartWait time.Duration
	// busy is held while a restart or an update command runs; a second one
	// is refused rather than queued.
	busy sync.Mutex
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

		restartWait: cfg.RestartWait,
	}
	if cfg.Services != nil {
		h.services = *cfg.Services
	} else {
		h.services = service.FromOS(cfg.NodeDir)
	}
	if h.restartWait <= 0 {
		h.restartWait = DefaultRestartWait
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
	h.mux.HandleFunc("POST /api/mining/payout", h.setPayout)
	h.mux.HandleFunc("POST /api/node/restart", h.restartNode)
	h.mux.HandleFunc("GET /api/updates", h.updatesPanel)
	h.mux.HandleFunc("POST /api/updates/run", h.runUpdate)
	h.mux.HandleFunc("POST /api/updates/on", h.updatesOn)
	h.mux.HandleFunc("POST /api/updates/off", h.updatesOff)
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

// setPayout takes {"address": "0x…"}: 40 hex characters, with the mixed-case
// checksum checked when given.
func (h *handler) setPayout(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxControlBody))
	var req struct {
		Address *string `json:"address"`
	}
	if err == nil {
		err = json.Unmarshal(body, &req)
	}
	if err != nil || req.Address == nil {
		badRequest(w, status.PayoutResult{Error: "Enter the payout address: " + address.ErrFormat.Error() + "."})
		return
	}
	addr, err := address.Parse(*req.Address)
	if err != nil {
		badRequest(w, status.PayoutResult{Error: "That is not a payout address: " + err.Error() + "."})
		return
	}
	h.control.Lock()
	defer h.control.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), StatusTimeout)
	defer cancel()
	writeJSON(w, status.SetPayout(ctx, h.node, h.nodeDir, addr))
}

// RestartResult is what a restart returns: the command's result and whether
// the node answered again afterwards.
type RestartResult struct {
	service.Result
	Back          bool    `json:"back"`
	ClientVersion *string `json:"clientVersion"`
	BlockNumber   *uint64 `json:"blockNumber"`
}

// lockService takes the service lock, or answers 409 when a restart or an
// update is already running.
func (h *handler) lockService(w http.ResponseWriter) bool {
	if h.busy.TryLock() {
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(service.Result{Error: "A restart or an update is already running. Wait for it to finish."})
	return false
}

// extendWrite lets a long action answer after the server's usual write
// timeout.
func extendWrite(w http.ResponseWriter, d time.Duration) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d + 10*time.Second))
}

func (h *handler) restartNode(w http.ResponseWriter, r *http.Request) {
	if !h.lockService(w) {
		return
	}
	defer h.busy.Unlock()
	extendWrite(w, ServiceTimeout+h.restartWait)

	before := status.EndpointStamp(h.nodeEnv.IPCPath)
	ctx, cancel := context.WithTimeout(r.Context(), ServiceTimeout)
	res := RestartResult{Result: h.services.Restart(ctx)}
	cancel()
	if !res.OK {
		writeJSON(w, res)
		return
	}
	wait, cancel := context.WithTimeout(r.Context(), h.restartWait)
	defer cancel()
	res.ClientVersion, res.BlockNumber, res.Back = status.WaitNodeBack(wait, h.node, h.nodeEnv.IPCPath, before)
	if res.Back {
		res.Message = "The node restarted and is answering again."
		if res.BlockNumber != nil {
			res.Message += fmt.Sprintf(" Latest block: %d.", *res.BlockNumber)
		}
	} else {
		res.Message = fmt.Sprintf("Restart sent, but the node has not answered in %s. The Your node panel shows it when it is back.", h.restartWait)
	}
	writeJSON(w, res)
}

func (h *handler) updatesPanel(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), ServiceTimeout)
	defer cancel()
	writeJSON(w, h.services.Updates(ctx))
}

// UpdatesResult is what turning updates on or off and "Update now" return:
// the command's result and the update state read afterwards.
type UpdatesResult struct {
	service.Result
	Updates service.UpdateState `json:"updates"`
}

func (h *handler) runUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.lockService(w) {
		return
	}
	defer h.busy.Unlock()
	extendWrite(w, UpdateTimeout)
	ctx, cancel := context.WithTimeout(r.Context(), UpdateTimeout)
	res := h.services.RunUpdate(ctx)
	cancel()
	ctx, cancel = context.WithTimeout(r.Context(), ServiceTimeout)
	defer cancel()
	writeJSON(w, UpdatesResult{Result: res, Updates: h.services.Updates(ctx)})
}

func (h *handler) updatesOn(w http.ResponseWriter, r *http.Request)  { h.setUpdates(w, r, true) }
func (h *handler) updatesOff(w http.ResponseWriter, r *http.Request) { h.setUpdates(w, r, false) }

func (h *handler) setUpdates(w http.ResponseWriter, r *http.Request, on bool) {
	if !h.lockService(w) {
		return
	}
	defer h.busy.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), ServiceTimeout)
	defer cancel()
	res, st := h.services.SetUpdates(ctx, on)
	writeJSON(w, UpdatesResult{Result: res, Updates: st})
}

func badRequest(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(v)
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
