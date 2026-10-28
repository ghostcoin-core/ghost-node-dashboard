package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// exitErr is a command that ran and failed, as *exec.ExitError reports it.
type exitErr int

func (e exitErr) Error() string { return "exit status" }
func (e exitErr) ExitCode() int { return int(e) }

// fakeSystem stands in for launchctl and systemctl: it records every
// command and keeps whether the updater is loaded or enabled.
type fakeSystem struct {
	mu       sync.Mutex
	commands [][]string
	updateOn bool
	// fail makes the command with this program name fail.
	fail   string
	output map[string]string
}

func (f *fakeSystem) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	argv := append([]string{name}, args...)
	f.commands = append(f.commands, argv)
	if name == f.fail {
		return []byte("synthetic failure output\n"), exitErr(3)
	}
	line := strings.Join(argv, " ")
	switch {
	case slices.Contains(args, "print"):
		if !f.updateOn {
			return []byte("Could not find service\n"), exitErr(113)
		}
		return []byte("state = not running\n"), nil
	case slices.Contains(args, "is-enabled"):
		if !f.updateOn {
			return []byte("disabled\n"), exitErr(1)
		}
		return []byte("enabled\n"), nil
	case slices.Contains(args, "bootstrap"), slices.Contains(args, "enable"):
		f.updateOn = true
	case slices.Contains(args, "bootout"), slices.Contains(args, "disable"):
		f.updateOn = false
	}
	return []byte(f.output[line]), nil
}

// actions lists the commands that change something: the state reads are
// left out.
func (f *fakeSystem) actions() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]string
	for _, c := range f.commands {
		if slices.Contains(c, "print") || slices.Contains(c, "is-enabled") || slices.Contains(c, "show") {
			continue
		}
		out = append(out, c)
	}
	return out
}

func (f *fakeSystem) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = nil
}

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

// unitWord quotes a path as the installer's unit_word does.
func unitWord(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "%", "%%")
	return `"` + s + `"`
}

// xmlText escapes a path as the installer's xml_text does.
func xmlText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func plist(label string, args []string, log string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + label + `</string>
	<key>ProgramArguments</key>
	<array>
`)
	for _, a := range args {
		b.WriteString("\t\t<string>" + xmlText(a) + "</string>\n")
	}
	b.WriteString(`	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>StandardOutPath</key>
	<string>` + xmlText(log) + `</string>
</dict>
</plist>
`)
	return b.String()
}

// installDarwin writes what the installer writes on macOS, plus an
// unrelated agent that must be ignored.
func installDarwin(t *testing.T, home, nodeDir string) {
	agents := filepath.Join(home, "Library", "LaunchAgents")
	write(t, filepath.Join(agents, "com.example.other.plist"),
		plist("com.example.other", []string{"/usr/bin/true"}, "/tmp/other.log"), 0o644)
	write(t, filepath.Join(agents, "com.ghostcoin.node.plist"),
		plist("com.ghostcoin.node", []string{filepath.Join(nodeDir, "bin", "geth"), "--config", filepath.Join(nodeDir, "config.toml"), "--mine"},
			filepath.Join(nodeDir, "node.log")), 0o644)
	write(t, filepath.Join(agents, "com.ghostcoin.node.update.plist"),
		plist("com.ghostcoin.node.update", []string{filepath.Join(nodeDir, "update")}, filepath.Join(nodeDir, "update.log")), 0o644)
}

// installLinux writes what the installer writes on Linux, plus an
// unrelated unit that must be ignored.
func installLinux(t *testing.T, unitDir, nodeDir string) {
	write(t, filepath.Join(unitDir, "other.service"), "[Service]\nExecStart=/usr/bin/true\n", 0o644)
	write(t, filepath.Join(unitDir, "ghost-node.service"), "[Unit]\nDescription=Ghost node\n\n[Service]\nExecStart="+
		unitWord(filepath.Join(nodeDir, "bin", "geth"))+" --config "+unitWord(filepath.Join(nodeDir, "config.toml"))+
		" --mine --miner.threads 4\nRestart=on-failure\n\n[Install]\nWantedBy=default.target\n", 0o644)
	write(t, filepath.Join(unitDir, "ghost-node-update.service"), "[Service]\nType=oneshot\nExecStart="+
		unitWord(filepath.Join(nodeDir, "update"))+"\nNice=10\n", 0o644)
	write(t, filepath.Join(unitDir, "ghost-node-update.timer"),
		"[Timer]\nOnCalendar=daily\nRandomizedDelaySec=1h\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n", 0o644)
}

func TestServiceActions(t *testing.T) {
	ctx := context.Background()
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			home := t.TempDir()
			// A node folder with a space, & and % in it checks the
			// installer's quoting is read back, not just plain paths.
			nodeDir := filepath.Join(home, "Ghost & co 100%", ".ghost-node")
			write(t, filepath.Join(nodeDir, "update"), "#!/bin/sh\necho updated\n", 0o700)
			sys := &fakeSystem{output: map[string]string{filepath.Join(nodeDir, "update"): "synthetic update output\n"}}
			env := Env{GOOS: goos, Home: home, UID: 501, NodeDir: nodeDir, Run: sys.run}
			if goos == "darwin" {
				installDarwin(t, home, nodeDir)
			} else {
				env.ConfigHome = filepath.Join(home, "xdg config")
				installLinux(t, env.UnitDir(), nodeDir)
			}

			want := map[string][][]string{
				"darwin restart": {{"launchctl", "kickstart", "-k", "gui/501/com.ghostcoin.node"}},
				"darwin on": {{"launchctl", "bootstrap", "gui/501",
					filepath.Join(home, "Library", "LaunchAgents", "com.ghostcoin.node.update.plist")}},
				"darwin off":    {{"launchctl", "bootout", "gui/501/com.ghostcoin.node.update"}},
				"linux restart": {{"systemctl", "--user", "restart", "ghost-node.service"}},
				"linux on":      {{"systemctl", "--user", "enable", "--now", "ghost-node-update.timer"}},
				"linux off":     {{"systemctl", "--user", "disable", "--now", "ghost-node-update.timer"}},
			}
			check := func(action string) {
				t.Helper()
				got := sys.actions()
				w := want[goos+" "+action]
				if action == "update" {
					w = [][]string{{filepath.Join(nodeDir, "update")}}
				}
				if !slices.EqualFunc(got, w, slices.Equal[[]string]) {
					t.Errorf("%s ran %q, want %q", action, got, w)
				}
				sys.reset()
			}

			res := env.Restart(ctx)
			if !res.OK || res.Error != "" {
				t.Errorf("restart %+v", res)
			}
			check("restart")

			st := env.Updates(ctx)
			if !st.Available || !st.Installed || st.On == nil || *st.On {
				t.Errorf("state before on %+v, want installed and off", st)
			}

			res, st = env.SetUpdates(ctx, true)
			if !res.OK || st.On == nil || !*st.On {
				t.Errorf("on: %+v, state %+v", res, st)
			}
			check("on")

			// Already on: nothing runs.
			if res, _ = env.SetUpdates(ctx, true); !res.OK || !strings.Contains(res.Message, "already on") {
				t.Errorf("on again: %+v", res)
			}
			check("none")

			res, st = env.SetUpdates(ctx, false)
			if !res.OK || st.On == nil || *st.On {
				t.Errorf("off: %+v, state %+v", res, st)
			}
			check("off")

			res = env.RunUpdate(ctx)
			if !res.OK || res.Output != "synthetic update output\n" {
				t.Errorf("update now %+v, want ok with the script's output", res)
			}
			check("update")

			if goos == "darwin" {
				// The update plist is kept while updates are off.
				if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", "com.ghostcoin.node.update.plist")); err != nil {
					t.Errorf("update plist gone: %v", err)
				}
			}

			// A failing command is reported with its output.
			sys.fail = map[string]string{"darwin": "launchctl", "linux": "systemctl"}[goos]
			res = env.Restart(ctx)
			if res.OK || !strings.Contains(res.Error, "status 3") || !strings.Contains(res.Output, "synthetic failure") {
				t.Errorf("failed restart %+v", res)
			}
			sys.reset()
		})
	}

	t.Run("windows", func(t *testing.T) {
		sys := &fakeSystem{}
		env := Env{GOOS: "windows", Home: t.TempDir(), NodeDir: t.TempDir(), Run: sys.run}
		for name, res := range map[string]Result{
			"restart": env.Restart(ctx),
			"update":  env.RunUpdate(ctx),
		} {
			if res.OK || !strings.Contains(res.Error, "Windows") {
				t.Errorf("%s on windows: %+v, want refused", name, res)
			}
		}
		if res, st := env.SetUpdates(ctx, true); res.OK || st.Available {
			t.Errorf("updates on windows: %+v %+v", res, st)
		}
		if len(sys.commands) != 0 {
			t.Errorf("windows ran %q", sys.commands)
		}
	})
}

func TestServiceNotInstalled(t *testing.T) {
	ctx := context.Background()
	for _, goos := range []string{"darwin", "linux"} {
		sys := &fakeSystem{}
		home := t.TempDir()
		nodeDir := filepath.Join(home, ".ghost-node")
		env := Env{GOOS: goos, Home: home, UID: 501, NodeDir: nodeDir, Run: sys.run}
		// A unit or agent for another node folder is not this node's.
		other := filepath.Join(home, "elsewhere")
		if goos == "darwin" {
			installDarwin(t, home, other)
		} else {
			installLinux(t, env.UnitDir(), other)
		}
		if res := env.Restart(ctx); res.OK || !strings.Contains(res.Error, "installer") {
			t.Errorf("%s restart without the node's service: %+v", goos, res)
		}
		if st := env.Updates(ctx); st.Available || st.On != nil {
			t.Errorf("%s updates without the node's service: %+v", goos, st)
		}
		if len(sys.commands) != 0 {
			t.Errorf("%s ran %q", goos, sys.commands)
		}
	}
}

func TestServiceUpdaterMissing(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	nodeDir := filepath.Join(home, ".ghost-node")
	sys := &fakeSystem{}
	env := Env{GOOS: "linux", Home: home, NodeDir: nodeDir, Run: sys.run}
	installLinux(t, env.UnitDir(), nodeDir)
	if err := os.Remove(filepath.Join(env.UnitDir(), "ghost-node-update.timer")); err != nil {
		t.Fatal(err)
	}
	st := env.Updates(ctx)
	if !st.Available || st.Installed || st.On != nil {
		t.Errorf("state %+v, want available but not installed", st)
	}
	if res, _ := env.SetUpdates(ctx, true); res.OK {
		t.Errorf("on without the timer: %+v", res)
	}
	// No update script: refused without running anything.
	if res := env.RunUpdate(ctx); res.OK || !strings.Contains(res.Error, "not found") {
		t.Errorf("update without the script: %+v", res)
	}
	// A link in place of the script is refused too.
	if err := os.MkdirAll(nodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/bin/true", filepath.Join(nodeDir, "update")); err != nil {
		t.Fatal(err)
	}
	if res := env.RunUpdate(ctx); res.OK || !strings.Contains(res.Error, "not a program file") {
		t.Errorf("update through a link: %+v", res)
	}
	if len(sys.actions()) != 0 {
		t.Errorf("ran %q", sys.actions())
	}
}

func TestUpdatesLastRun(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	nodeDir := filepath.Join(home, ".ghost-node")
	sys := &fakeSystem{updateOn: true}
	env := Env{GOOS: "linux", Home: home, NodeDir: nodeDir, Run: sys.run}
	installLinux(t, env.UnitDir(), nodeDir)
	sys.output = map[string]string{
		"systemctl --user show ghost-node-update.timer --property=LastTriggerUSec --value": "Wed 2026-10-28 03:12:00 UTC\n",
	}
	if st := env.Updates(ctx); st.LastRun != "Wed 2026-10-28 03:12:00 UTC" || st.On == nil || !*st.On {
		t.Errorf("linux state %+v", st)
	}
	sys.output = nil
	if st := env.Updates(ctx); st.LastRun != "" {
		t.Errorf("never run: last run %q", st.LastRun)
	}

	// macOS: the time of the update log the agent writes.
	env.GOOS = "darwin"
	installDarwin(t, home, nodeDir)
	write(t, filepath.Join(nodeDir, "update.log"), "updated\n", 0o644)
	st := env.Updates(ctx)
	if st.LastRunTime == nil || st.LastRun == "" {
		t.Errorf("darwin state %+v, want the log's time", st)
	}
}

func TestExecStart(t *testing.T) {
	for _, c := range [][2]string{
		{"ExecStart=/a/b --x", "/a/b"},
		{`ExecStart="/a b/c\"d\\e" --x`, `/a b/c"d\e`},
		{`ExecStart="/100%%/geth"`, "/100%/geth"},
		{"[Service]\n  ExecStart=-/a/b\nExecStart=/c", "/a/b"},
		{"ExecStart=", ""},
		{"NoExec=/a", ""},
	} {
		if got := execStart(c[0]); got != c[1] {
			t.Errorf("execStart(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

func TestExitCode(t *testing.T) {
	if exitCode(nil) != 0 || exitCode(exitErr(4)) != 4 || exitCode(errors.New("no such program")) != -1 {
		t.Error("exitCode misreads errors")
	}
}
