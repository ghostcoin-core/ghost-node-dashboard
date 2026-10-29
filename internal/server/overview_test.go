package server

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// The Overview screen loads its own stylesheet, scoped to its root, and
// builds its markup without HTML strings or inline styles.
func TestOverviewScreenFiles(t *testing.T) {
	h := newHandler(t, nil)
	css := getBody(t, h, "/static/screens/overview.css")
	if rec := do(h, http.MethodGet, "/static/screens/overview.css", goodHost, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("overview.css without cookie: status %d, want 401", rec.Code)
	}

	// Every selector starts with #screen-overview. Drop comments, then read
	// each selector list in front of a "{" that is not an @media block.
	css = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")
	rules := regexp.MustCompile(`([^{}]+)\{`).FindAllStringSubmatch(css, -1)
	if len(rules) < 10 {
		t.Fatalf("overview.css has %d rules, want a full stylesheet", len(rules))
	}
	for _, r := range rules {
		list := strings.TrimSpace(r[1])
		if strings.HasPrefix(list, "@media") {
			continue
		}
		for _, sel := range strings.Split(list, ",") {
			if !strings.HasPrefix(strings.TrimSpace(sel), "#screen-overview") {
				t.Errorf("overview.css selector %q is not scoped to #screen-overview", strings.TrimSpace(sel))
			}
		}
	}
	for _, want := range []string{"#screen-overview { display: block; }", "repeat(30, minmax(0, 1fr))", "dashed var(--violet)"} {
		if !strings.Contains(css, want) {
			t.Errorf("overview.css lacks %q", want)
		}
	}

	js := getBody(t, h, "/static/screens/overview.js")
	// The words are split so this file does not itself contain them.
	for _, bad := range []string{"inner" + "HTML", "insertAdjacent" + "HTML", "document." + "write", `"style"`, "style=", "<" + "style"} {
		if strings.Contains(js, bad) {
			t.Errorf("overview.js uses %q", bad)
		}
	}
	for _, want := range []string{
		`"/static/screens/overview.css"`,
		`rel: "stylesheet"`,
		"document.head.appendChild",
		`role: "img"`,
		`"/api/node"`, `"/api/mining"`, `"/api/rewards"`, `"/api/network"`, `"/api/log"`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("overview.js lacks %q", want)
		}
	}
	// Settings owns these ids; Overview's own ids start with ov-.
	for _, id := range []string{"payout-input", "node-restart", "updates-run", "updates-on", "updates-off", "updates-output"} {
		if strings.Contains(js, `"`+id+`"`) {
			t.Errorf("overview.js reuses the Settings id %q", id)
		}
	}
	shared := map[string]bool{"cores-input": true, "mining-pause": true, "mining-resume": true}
	for _, m := range regexp.MustCompile(`id: "([^"]+)"`).FindAllStringSubmatch(js, -1) {
		if !shared[m[1]] && !strings.HasPrefix(m[1], "ov-") {
			t.Errorf("overview.js id %q lacks the ov- prefix", m[1])
		}
	}
}
