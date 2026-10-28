package server

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// screenFiles are the per-screen scripts the page loads after app.js.
var screenFiles = []string{
	"/static/screens/overview.js",
	"/static/screens/rewards.js",
	"/static/screens/network.js",
	"/static/screens/settings.js",
}

var fontFiles = []string{
	"/static/fonts/instrument-sans-latin-400-normal.woff2",
	"/static/fonts/instrument-sans-latin-500-normal.woff2",
	"/static/fonts/instrument-sans-latin-600-normal.woff2",
	"/static/fonts/ibm-plex-mono-latin-400-normal.woff2",
	"/static/fonts/ibm-plex-mono-latin-500-normal.woff2",
	"/static/fonts/instrument-sans-OFL.txt",
	"/static/fonts/ibm-plex-mono-OFL.txt",
}

// getBody fetches target with the key cookie and fails unless it is served.
func getBody(t *testing.T, h http.Handler, target string) string {
	t.Helper()
	rec := do(h, http.MethodGet, target, goodHost, withSession)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d, want 200", target, rec.Code)
	}
	return rec.Body.String()
}

// screenJS is every screen file, one after the other.
func screenJS(t *testing.T, h http.Handler) string {
	t.Helper()
	var b strings.Builder
	for _, f := range screenFiles {
		b.WriteString(getBody(t, h, f))
		b.WriteString("\n")
	}
	return b.String()
}

func TestShellPage(t *testing.T) {
	h := newHandler(t, nil)
	page := getBody(t, h, "/")
	for _, want := range []string{
		`href="#/overview"`,
		`href="#/rewards"`,
		`href="#/network"`,
		`href="#/settings"`,
		`src="/static/app.js"`,
		`name="viewport"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, f := range screenFiles {
		if !strings.Contains(page, `src="`+f+`"`) {
			t.Errorf("page does not load %s", f)
		}
	}
	// app.js comes first, so the screens can register with GhostDash.
	core := strings.Index(page, `src="/static/app.js"`)
	for _, f := range screenFiles {
		if i := strings.Index(page, `src="`+f+`"`); i >= 0 && i < core {
			t.Errorf("%s is loaded before app.js", f)
		}
	}
	// The CSP refuses inline scripts and styles: every script has a src and
	// an empty body, and there is no <style> or style attribute.
	scripts := regexp.MustCompile(`(?is)<script([^>]*)>(.*?)</script>`).FindAllStringSubmatch(page, -1)
	if len(scripts) != 1+len(screenFiles) {
		t.Errorf("page has %d scripts, want %d", len(scripts), 1+len(screenFiles))
	}
	for _, s := range scripts {
		if !strings.Contains(s[1], "src=") || !strings.Contains(s[1], "defer") {
			t.Errorf("script without src or defer: %q", s[0])
		}
		if strings.TrimSpace(s[2]) != "" {
			t.Errorf("inline script body: %q", s[2])
		}
	}
	lower := strings.ToLower(page)
	if strings.Contains(lower, "<style") {
		t.Error("page has a <style> element")
	}
	if strings.Contains(lower, " style=") {
		t.Error("page has a style attribute")
	}
	if !strings.Contains(page, `<main`) {
		t.Error("page has no <main> for the screens")
	}

	css := getBody(t, h, "/static/style.css")
	for _, want := range []string{
		"--ink: #0E0D1A",
		"--violet: #6B4CFF",
		"--violet-tint: #EEEAFF",
		"--mist: #F5F5F8",
		"--line: #E4E3EC",
		"--body: #4A4860",
		"--quiet: #5F5D73",
		"--quiet-2: #6E6C82",
		"--mint: #4FF0C8",
		"--white: #FFFFFF",
		"@media (max-width",
		"min-height: 44px",
		"240px",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("style.css lacks %q", want)
		}
	}

	// Each screen registers itself with the core for its own route.
	js := getBody(t, h, "/static/app.js")
	if !strings.Contains(js, "GhostDash") {
		t.Error("app.js does not define GhostDash")
	}
	for _, name := range []string{"overview", "rewards", "network", "settings"} {
		body := getBody(t, h, "/static/screens/"+name+".js")
		if !strings.Contains(body, `GhostDash`) || !strings.Contains(body, `.screen("`+name+`"`) {
			t.Errorf("%s.js does not register the %s screen", name, name)
		}
	}
}

func TestScreenFilesServed(t *testing.T) {
	h := newHandler(t, nil)
	for _, f := range append([]string{"/static/app.js"}, screenFiles...) {
		rec := do(h, http.MethodGet, f, goodHost, withSession)
		if rec.Code != http.StatusOK {
			t.Errorf("%s with cookie: status %d, want 200", f, rec.Code)
		} else if rec.Body.Len() == 0 {
			t.Errorf("%s is empty", f)
		}
		rec = do(h, http.MethodGet, f, goodHost, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without cookie: status %d, want 401", f, rec.Code)
		}
	}
}

func TestFontsServedBehindKey(t *testing.T) {
	h := newHandler(t, nil)
	for _, f := range fontFiles {
		rec := do(h, http.MethodGet, f, goodHost, withSession)
		if rec.Code != http.StatusOK {
			t.Errorf("%s with cookie: status %d, want 200", f, rec.Code)
			continue
		}
		body := rec.Body.Bytes()
		if strings.HasSuffix(f, ".woff2") && !strings.HasPrefix(string(body), "wOF2") {
			t.Errorf("%s does not start with wOF2", f)
		}
		if strings.HasSuffix(f, "-OFL.txt") && !strings.Contains(string(body), "SIL Open Font License") {
			t.Errorf("%s is not the SIL Open Font License", f)
		}
		rec = do(h, http.MethodGet, f, goodHost, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without cookie: status %d, want 401", f, rec.Code)
		}
		rec = do(h, http.MethodGet, f, "evil.example:8580", withSession)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s for a wrong Host: status %d, want 403", f, rec.Code)
		}
	}
	// Every font the stylesheet names is one of the served files.
	css := getBody(t, h, "/static/style.css")
	for _, m := range regexp.MustCompile(`url\("([^"]+)"\)`).FindAllStringSubmatch(css, -1) {
		found := false
		for _, f := range fontFiles {
			if m[1] == f {
				found = true
			}
		}
		if !found {
			t.Errorf("style.css names %s, which is not served", m[1])
		}
	}
}

func TestNoExternalURLs(t *testing.T) {
	h := newHandler(t, nil)
	files := append([]string{"/", "/static/style.css", "/static/app.js"}, screenFiles...)
	scheme := regexp.MustCompile(`(?i)https?://`)
	for _, f := range files {
		body := strings.ReplaceAll(getBody(t, h, f), "http://www.w3.org/2000/svg", "")
		if loc := scheme.FindStringIndex(body); loc != nil {
			end := loc[1] + 40
			if end > len(body) {
				end = len(body)
			}
			t.Errorf("%s has an outside URL: %q", f, body[loc[0]:end])
		}
		if strings.Contains(body, "//fonts.") || strings.Contains(body, `src="//`) || strings.Contains(body, `href="//`) {
			t.Errorf("%s has a scheme-relative outside URL", f)
		}
	}
}

func TestFontSrcCSP(t *testing.T) {
	h := newHandler(t, nil)
	for _, target := range []string{"/", "/static/style.css"} {
		rec := do(h, http.MethodGet, target, goodHost, withSession)
		csp := rec.Header().Get("Content-Security-Policy")
		for _, want := range []string{"font-src 'self'", "default-src 'none'", "script-src 'self'", "style-src 'self'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: CSP %q lacks %q", target, csp, want)
			}
		}
		if strings.Contains(csp, "unsafe-inline") {
			t.Errorf("%s: CSP allows unsafe-inline", target)
		}
	}
}
