package server

import (
	"regexp"
	"strings"
	"testing"
)

// The Settings screen keeps its looks in its own stylesheet, scoped to the
// screen's root, and loads it itself. Ids other than the ones the server
// tests name carry the st- prefix, so they never clash with the Overview's.
func TestSettingsScreenFiles(t *testing.T) {
	h := newHandler(t, nil)
	js := getBody(t, h, "/static/screens/settings.js")
	css := getBody(t, h, "/static/screens/settings.css")

	for _, want := range []string{
		`"/static/screens/settings.css"`, `rel: "stylesheet"`,
		`"/api/mining"`, `"/api/node"`, `"/api/updates"`,
		`"/api/mining/cores"`, `"/api/mining/pause"`, `"/api/mining/resume"`, `"/api/mining/payout"`,
		`"/api/node/restart"`, `"/api/updates/run"`, `"/api/updates/on"`, `"/api/updates/off"`,
		`role: "switch"`, `"aria-checked"`, `"aria-live": "polite"`,
		`/^0x[0-9a-fA-F]{40}$/`,
		`id: "st-cores"`, `"st-mine"`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("settings.js lacks %s", want)
		}
	}
	// Markup is built with GhostDash.h and textContent; no style attribute.
	for _, bad := range []string{"HTML", `"style"`, "style=", ".style"} {
		if strings.Contains(js, bad) {
			t.Errorf("settings.js uses %s", bad)
		}
	}
	// The Overview owns these.
	for _, bad := range []string{`"cores-input"`, `"mining-pause"`, `"mining-resume"`} {
		if strings.Contains(js, bad) {
			t.Errorf("settings.js uses the Overview's id %s", bad)
		}
	}
	shared := map[string]bool{"payout-input": true, "node-restart": true, "updates-run": true, "updates-on": true, "updates-output": true}
	for _, m := range regexp.MustCompile(`id: "([^"]+)"`).FindAllStringSubmatch(js, -1) {
		if !shared[m[1]] && !strings.HasPrefix(m[1], "st-") {
			t.Errorf("settings.js id %q lacks the st- prefix", m[1])
		}
	}

	// Every selector, outside and inside @media, starts with #screen-settings.
	plain := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")
	selectors := 0
	for _, m := range regexp.MustCompile(`([^{}]+)\{`).FindAllStringSubmatch(plain, -1) {
		sel := strings.TrimSpace(m[1])
		if strings.HasPrefix(sel, "@media") {
			continue
		}
		for _, part := range strings.Split(sel, ",") {
			selectors++
			if !strings.HasPrefix(strings.TrimSpace(part), "#screen-settings") {
				t.Errorf("settings.css selector %q is not scoped to #screen-settings", part)
			}
		}
	}
	if selectors == 0 {
		t.Error("settings.css has no rules")
	}
	if strings.Contains(css, "import") || strings.Contains(css, "url(") {
		t.Error("settings.css loads another file")
	}
	for _, want := range []string{"#screen-settings { display: block; }", "minmax(min(100%, 420px), 1fr)", `[aria-checked="true"]`, "min-height: 44px"} {
		if !strings.Contains(css, want) {
			t.Errorf("settings.css lacks %q", want)
		}
	}
}
