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
	"strings"
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
}

type handler struct {
	key     []byte
	session []byte
	hosts   map[string]bool
	origins map[string]bool
	node    status.Caller
	mux     *http.ServeMux
}

// New returns the dashboard handler.
func New(cfg Config) http.Handler {
	h := &handler{
		key:     []byte(cfg.Key),
		session: []byte(sessionValue(cfg.Key)),
		hosts:   map[string]bool{},
		origins: map[string]bool{},
		node:    cfg.Node,
		mux:     http.NewServeMux(),
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
	st := status.Fetch(ctx, h.node)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(st)
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
