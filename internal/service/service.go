// Package service restarts the node and manages its daily updates through
// the services the node installer set up: launchd agents on macOS and
// systemd user units on Linux.
//
// The names are read from the files the installer wrote, never assumed: the
// launchd agent or systemd unit whose program is <node-dir>/bin/geth is the
// node, and the one whose program is <node-dir>/update is the updater. Every
// command runs with fixed arguments, never through a shell.
package service

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Runner runs one program with arguments and returns its combined output.
// When the program ran but failed, the error has an ExitCode method, as
// *exec.ExitError does.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Exec is the Runner that runs programs on this computer.
func Exec(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// ErrUnsupported is returned on systems the installer does not set up
// services on.
var ErrUnsupported = errors.New("restarting the node and managing updates from the dashboard works on macOS and Linux; " +
	"on Windows the node starts from its login entry (geth service start), which the dashboard does not manage yet")

// Env is where the services are looked for and how commands run.
type Env struct {
	GOOS string
	Home string
	// ConfigHome is XDG_CONFIG_HOME; "" means <Home>/.config.
	ConfigHome string
	UID        int
	NodeDir    string
	Run        Runner
}

// FromOS is the Env of this computer for a node directory.
func FromOS(nodeDir string) Env {
	home, _ := os.UserHomeDir()
	return Env{
		GOOS:       runtime.GOOS,
		Home:       home,
		ConfigHome: os.Getenv("XDG_CONFIG_HOME"),
		UID:        os.Getuid(),
		NodeDir:    nodeDir,
		Run:        Exec,
	}
}

// Services are the node's services as the installer wrote them.
type Services struct {
	// Node is the launchd label or the systemd unit of the node.
	Node string
	// Update is the launchd label or the systemd timer of the updater; ""
	// when the updater is not installed.
	Update string
	// UpdatePlist is the updater's launchd agent file (macOS).
	UpdatePlist string
	// UpdateLog is where the updater's launchd agent writes its output
	// (macOS).
	UpdateLog string
}

// UpdateScript is the updater the installer writes in the node directory.
func (e Env) UpdateScript() string {
	return filepath.Join(e.NodeDir, "update")
}

func (e Env) nodeProgram() string {
	return filepath.Join(e.NodeDir, "bin", "geth")
}

// LaunchAgents is where the installer writes launchd agents (macOS).
func (e Env) LaunchAgents() string {
	return filepath.Join(e.Home, "Library", "LaunchAgents")
}

// UnitDir is where the installer writes systemd user units (Linux).
func (e Env) UnitDir() string {
	config := e.ConfigHome
	if config == "" {
		config = filepath.Join(e.Home, ".config")
	}
	return filepath.Join(config, "systemd", "user")
}

func (e Env) domain() string {
	return "gui/" + strconv.Itoa(e.UID)
}

// Find reads the installer's service files. It fails when the node's own
// service is not there.
func (e Env) Find() (Services, error) {
	switch e.GOOS {
	case "windows":
		return Services{}, ErrUnsupported
	case "darwin":
		return e.findLaunchd()
	default:
		return e.findSystemd()
	}
}

func (e Env) findLaunchd() (Services, error) {
	var s Services
	dir := e.LaunchAgents()
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return s, err
	}
	for _, ent := range entries {
		if !strings.HasSuffix(ent.Name(), ".plist") || !ent.Type().IsRegular() {
			continue
		}
		path := filepath.Join(dir, ent.Name())
		agent, err := readPlist(path)
		if err != nil || agent.Label == "" {
			continue
		}
		switch agent.Program {
		case e.nodeProgram():
			if s.Node == "" {
				s.Node = agent.Label
			}
		case e.UpdateScript():
			if s.Update == "" {
				s.Update, s.UpdatePlist, s.UpdateLog = agent.Label, path, agent.StdoutPath
			}
		}
	}
	if s.Node == "" {
		return s, fmt.Errorf("no launchd agent in %s runs %s; is the node installed with the installer?", dir, e.nodeProgram())
	}
	return s, nil
}

func (e Env) findSystemd() (Services, error) {
	var s Services
	dir := e.UnitDir()
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return s, err
	}
	for _, ent := range entries {
		name := ent.Name()
		if !strings.HasSuffix(name, ".service") || !ent.Type().IsRegular() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		switch execStart(string(b)) {
		case e.nodeProgram():
			if s.Node == "" {
				s.Node = name
			}
		case e.UpdateScript():
			timer := strings.TrimSuffix(name, ".service") + ".timer"
			if fi, err := os.Stat(filepath.Join(dir, timer)); err == nil && fi.Mode().IsRegular() && s.Update == "" {
				s.Update = timer
			}
		}
	}
	if s.Node == "" {
		return s, fmt.Errorf("no systemd user unit in %s runs %s; is the node installed with the installer?", dir, e.nodeProgram())
	}
	return s, nil
}

// execStart is the program of a unit's first ExecStart line, unquoted as
// systemd reads it ("…" with \\ and \" escapes, %% for %).
func execStart(unit string) string {
	for _, line := range strings.Split(unit, "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "ExecStart=")
		if !ok {
			continue
		}
		rest = strings.TrimLeft(rest, "@-:+!")
		var word strings.Builder
		if strings.HasPrefix(rest, `"`) {
			for i := 1; i < len(rest); i++ {
				c := rest[i]
				if c == '\\' && i+1 < len(rest) {
					i++
					word.WriteByte(rest[i])
					continue
				}
				if c == '"' {
					break
				}
				word.WriteByte(c)
			}
		} else {
			if f := strings.Fields(rest); len(f) > 0 {
				word.WriteString(f[0])
			}
		}
		return strings.ReplaceAll(word.String(), "%%", "%")
	}
	return ""
}

type launchAgent struct {
	Label      string
	Program    string
	StdoutPath string
}

// readPlist reads the label, the first program argument and the output file
// of a launchd agent.
func readPlist(path string) (launchAgent, error) {
	var a launchAgent
	f, err := os.Open(path)
	if err != nil {
		return a, err
	}
	defer f.Close()
	dec := xml.NewDecoder(io.LimitReader(f, 1<<20))
	key, inArgs, depth := "", false, 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return a, nil
		}
		if err != nil {
			return a, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "dict":
				depth++
			case "key":
				var s string
				if err := dec.DecodeElement(&s, &t); err != nil {
					return a, err
				}
				if depth == 1 {
					key = s
				}
			case "array":
				inArgs = depth == 1 && key == "ProgramArguments"
			case "string":
				var s string
				if err := dec.DecodeElement(&s, &t); err != nil {
					return a, err
				}
				if depth != 1 {
					continue
				}
				switch {
				case inArgs:
					if a.Program == "" {
						a.Program = s
					}
				case key == "Label":
					a.Label = s
				case key == "Program" && a.Program == "":
					a.Program = s
				case key == "StandardOutPath":
					a.StdoutPath = s
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "dict":
				depth--
			case "array":
				inArgs = false
			}
		}
	}
}

// RestartCommand is the command that restarts the node.
func (e Env) RestartCommand(s Services) []string {
	if e.GOOS == "darwin" {
		return []string{"launchctl", "kickstart", "-k", e.domain() + "/" + s.Node}
	}
	return []string{"systemctl", "--user", "restart", s.Node}
}

// UpdatesCommand is the command that turns daily updates on or off. On
// macOS the agent is loaded from or unloaded with its plist kept in place.
func (e Env) UpdatesCommand(s Services, on bool) []string {
	switch {
	case e.GOOS == "darwin" && on:
		return []string{"launchctl", "bootstrap", e.domain(), s.UpdatePlist}
	case e.GOOS == "darwin":
		return []string{"launchctl", "bootout", e.domain() + "/" + s.Update}
	case on:
		return []string{"systemctl", "--user", "enable", "--now", s.Update}
	default:
		return []string{"systemctl", "--user", "disable", "--now", s.Update}
	}
}

// Result is what a service action returns.
type Result struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
	// Command is what was run, for the page to show.
	Command []string `json:"command,omitempty"`
	// Output is what the command printed (the end of it when long).
	Output string `json:"output,omitempty"`
}

// MaxOutput bounds how much command output a result keeps: the end, where
// the outcome is.
const MaxOutput = 64 << 10

func (e Env) run(ctx context.Context, argv []string) (string, error) {
	out, err := e.Run(ctx, argv[0], argv[1:]...)
	if len(out) > MaxOutput {
		out = append([]byte("…\n"), out[len(out)-MaxOutput:]...)
	}
	return string(out), err
}

// Restart restarts the node through its service.
func (e Env) Restart(ctx context.Context) Result {
	s, err := e.Find()
	if err != nil {
		return Result{Error: "Cannot restart the node: " + err.Error() + "."}
	}
	argv := e.RestartCommand(s)
	out, err := e.run(ctx, argv)
	res := Result{Command: argv, Output: out}
	if err != nil {
		res.Error = "The restart failed: " + describe(ctx, err) + "."
		return res
	}
	res.OK, res.Message = true, "Restart sent to "+s.Node+"."
	return res
}

// UpdateState is what /api/updates returns.
type UpdateState struct {
	// Available is false where the dashboard cannot manage updates.
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
	// Installed is whether the installer set up the updater.
	Installed bool   `json:"installed"`
	Service   string `json:"service,omitempty"`
	// On is whether daily updates run; null when that cannot be told.
	On *bool `json:"on"`
	// LastRun is when the updater last ran, as the system tells it, and
	// LastRunTime the same in Unix seconds when known; both empty when it
	// has not run.
	LastRun     string `json:"lastRun,omitempty"`
	LastRunTime *int64 `json:"lastRunTime"`
	Script      string `json:"script"`
}

// Updates reads whether daily updates are on and when they last ran.
func (e Env) Updates(ctx context.Context) UpdateState {
	st := UpdateState{Script: e.UpdateScript()}
	s, err := e.Find()
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Available = true
	if s.Update == "" {
		st.Error = "the daily updater is not installed"
		return st
	}
	st.Installed, st.Service = true, s.Update

	if e.GOOS == "darwin" {
		_, err := e.run(ctx, []string{"launchctl", "print", e.domain() + "/" + s.Update})
		st.On = onFromExit(err)
		if s.UpdateLog != "" {
			if fi, err := os.Stat(s.UpdateLog); err == nil {
				t := fi.ModTime()
				secs := t.Unix()
				st.LastRun, st.LastRunTime = t.Format("2006-01-02 15:04:05 MST"), &secs
			}
		}
		return st
	}

	out, err := e.run(ctx, []string{"systemctl", "--user", "is-enabled", s.Update})
	switch strings.TrimSpace(firstLine(out)) {
	case "enabled", "enabled-runtime":
		on := true
		st.On = &on
	case "disabled", "masked", "masked-runtime":
		off := false
		st.On = &off
	default:
		if exitCode(err) >= 0 {
			off := false
			st.On = &off
		}
	}
	out, err = e.run(ctx, []string{"systemctl", "--user", "show", s.Update, "--property=LastTriggerUSec", "--value"})
	if last := strings.TrimSpace(firstLine(out)); err == nil && last != "" && last != "n/a" && last != "0" {
		st.LastRun = last
	}
	return st
}

// SetUpdates turns daily updates on or off. It does nothing when they are
// already so.
func (e Env) SetUpdates(ctx context.Context, on bool) (Result, UpdateState) {
	word := map[bool]string{true: "on", false: "off"}[on]
	s, err := e.Find()
	if err != nil {
		return Result{Error: "Cannot turn updates " + word + ": " + err.Error() + "."}, e.Updates(ctx)
	}
	if s.Update == "" {
		return Result{Error: "Cannot turn updates " + word + ": the daily updater is not installed. Run the node installer again to set it up."}, e.Updates(ctx)
	}
	if st := e.Updates(ctx); st.On != nil && *st.On == on {
		return Result{OK: true, Message: "Daily updates are already " + word + "."}, st
	}
	argv := e.UpdatesCommand(s, on)
	out, err := e.run(ctx, argv)
	res := Result{Command: argv, Output: out}
	st := e.Updates(ctx)
	switch {
	case err != nil:
		res.Error = "Turning updates " + word + " failed: " + describe(ctx, err) + "."
	case st.On != nil && *st.On != on:
		res.Error = "The command ran, but daily updates do not read as " + word + " yet."
	default:
		res.OK, res.Message = true, "Daily updates are "+word+"."
	}
	return res, st
}

// RunUpdate runs <node-dir>/update and returns its output.
func (e Env) RunUpdate(ctx context.Context) Result {
	if _, err := e.Find(); err != nil {
		return Result{Error: "Cannot update the node: " + err.Error() + "."}
	}
	script := e.UpdateScript()
	fi, err := os.Lstat(script)
	if err != nil {
		return Result{Error: "Cannot update the node: " + script + " not found. Run the node installer again to set it up."}
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm()&0o100 == 0 {
		return Result{Error: "Cannot update the node: " + script + " is not a program file the installer wrote."}
	}
	argv := []string{script}
	out, err := e.run(ctx, argv)
	res := Result{Command: argv, Output: out}
	if err != nil {
		res.Error = "The update failed: " + describe(ctx, err) + ". Its output is below."
		return res
	}
	res.OK, res.Message = true, "The update finished."
	return res
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// exitCode is the exit status of a command that ran and failed, 0 when it
// succeeded, or -1 when it could not be run.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit interface{ ExitCode() int }
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

// onFromExit reads "launchctl print": it succeeds for a loaded agent and
// fails with an exit status for one that is not.
func onFromExit(err error) *bool {
	code := exitCode(err)
	if code < 0 {
		return nil
	}
	on := code == 0
	return &on
}

func describe(ctx context.Context, err error) string {
	if ctx.Err() != nil {
		return "it took too long and was stopped"
	}
	if code := exitCode(err); code > 0 {
		return "it exited with status " + strconv.Itoa(code)
	}
	return err.Error()
}
