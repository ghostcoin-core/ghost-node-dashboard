package status

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ghostcoin-core/ghost-node-dashboard/internal/ipc"
	"github.com/ghostcoin-core/ghost-node-dashboard/internal/ipc/ipctest"
)

func TestNodePanelUptimeFromFirstAnswer(t *testing.T) {
	// Without a socket file to date the node's start (a named pipe), the
	// uptime counts from the first answer after the node came up.
	sock := filepath.Join(t.TempDir(), "geth.ipc")
	fake, err := ipctest.Start(sock, map[string]ipctest.Handler{"web3_clientVersion": ipctest.Value("Ghost/test")})
	if err != nil {
		t.Fatal(err)
	}
	node := ipc.New(sock)
	defer node.Close()

	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	now := t0
	env := NodeEnv{Uptime: &Uptime{}, Now: func() time.Time { return now }}
	uptime := func() (int64, string) {
		t.Helper()
		p := FetchNode(context.Background(), node, env)
		if !p.Running || p.UptimeSeconds == nil {
			t.Fatalf("running = %v, uptime = %v", p.Running, p.UptimeSeconds)
		}
		return *p.UptimeSeconds, p.UptimeFrom
	}

	if s, from := uptime(); s != 0 || from != "first answer" {
		t.Errorf("first answer: %d s from %q, want 0 from first answer", s, from)
	}
	now = t0.Add(75 * time.Second)
	if s, _ := uptime(); s != 75 {
		t.Errorf("later: %d s, want 75", s)
	}

	// The node stops, then comes back: the count starts again.
	fake.Close()
	now = t0.Add(100 * time.Second)
	if p := FetchNode(context.Background(), node, env); p.Running || p.UptimeSeconds != nil {
		t.Fatalf("while down: running %v, uptime %v", p.Running, p.UptimeSeconds)
	}
	fake, err = ipctest.Start(sock, map[string]ipctest.Handler{"web3_clientVersion": ipctest.Value("Ghost/test")})
	if err != nil {
		t.Fatal(err)
	}
	defer fake.Close()
	now = t0.Add(200 * time.Second)
	if s, _ := uptime(); s != 0 {
		t.Errorf("after restart: %d s, want 0", s)
	}
	now = t0.Add(230 * time.Second)
	if s, _ := uptime(); s != 30 {
		t.Errorf("after restart and 30 s: %d s, want 30", s)
	}
}

func TestAutoUpdateFor(t *testing.T) {
	noTask := func(string) (bool, error) { return false, nil }
	noEnv := func(string) string { return "" }
	install := func(t *testing.T, path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	is := func(t *testing.T, got *bool, want bool) {
		t.Helper()
		if got == nil || *got != want {
			t.Errorf("got %v, want %v", got, want)
		}
	}

	t.Run("macOS", func(t *testing.T) {
		home := t.TempDir()
		is(t, autoUpdateFor("darwin", home, noEnv, noTask), false)
		install(t, filepath.Join(home, "Library", "LaunchAgents", "com.ghostcoin.node.update.plist"))
		is(t, autoUpdateFor("darwin", home, noEnv, noTask), true)
	})

	t.Run("Linux", func(t *testing.T) {
		home := t.TempDir()
		is(t, autoUpdateFor("linux", home, noEnv, noTask), false)
		install(t, filepath.Join(home, ".config", "systemd", "user", updateTimer))
		is(t, autoUpdateFor("linux", home, noEnv, noTask), true)

		// XDG_CONFIG_HOME moves the user units.
		xdg := t.TempDir()
		env := func(k string) string {
			if k == "XDG_CONFIG_HOME" {
				return xdg
			}
			return ""
		}
		is(t, autoUpdateFor("linux", home, env, noTask), false)
		install(t, filepath.Join(xdg, "systemd", "user", updateTimer))
		is(t, autoUpdateFor("linux", home, env, noTask), true)
	})

	t.Run("Windows", func(t *testing.T) {
		var asked string
		task := func(name string) (bool, error) { asked = name; return true, nil }
		is(t, autoUpdateFor("windows", t.TempDir(), noEnv, task), true)
		if asked != updateTaskWindow {
			t.Errorf("asked for task %q, want %q", asked, updateTaskWindow)
		}
		is(t, autoUpdateFor("windows", t.TempDir(), noEnv, noTask), false)
		failing := func(string) (bool, error) { return false, errors.New("no schtasks") }
		if got := autoUpdateFor("windows", t.TempDir(), noEnv, failing); got != nil {
			t.Errorf("schtasks failing: got %v, want unknown", *got)
		}
	})
}

func TestShortAddress(t *testing.T) {
	if got := ShortAddress("0x1234567890abcdef1234567890abcdef12345678"); got != "0x1234…5678" {
		t.Errorf("ShortAddress = %q", got)
	}
}
