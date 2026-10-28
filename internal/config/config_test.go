package config

import (
	"io"
	"path/filepath"
	"testing"
)

func parse(t *testing.T, env map[string]string, args ...string) (Config, error) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	return Parse(args, func(k string) string { return env[k] }, func() (string, error) { return home, nil }, io.Discard)
}

func TestDefaults(t *testing.T) {
	cfg, err := parse(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 8580 {
		t.Errorf("port %d, want 8580", cfg.Port)
	}
	if filepath.Base(cfg.NodeDir) != ".ghost-node" || filepath.Base(filepath.Dir(cfg.NodeDir)) != "home" {
		t.Errorf("node dir %q, want <home>/.ghost-node", cfg.NodeDir)
	}
	if cfg.Command != "" {
		t.Errorf("command %q", cfg.Command)
	}
}

func TestPortFromEnvAndFlag(t *testing.T) {
	env := map[string]string{PortEnv: "9100"}
	cfg, err := parse(t, env)
	if err != nil || cfg.Port != 9100 {
		t.Fatalf("env port: %d, %v", cfg.Port, err)
	}
	cfg, err = parse(t, env, "--port", "9200")
	if err != nil || cfg.Port != 9200 {
		t.Fatalf("flag port: %d, %v", cfg.Port, err)
	}
	if _, err := parse(t, map[string]string{PortEnv: "nope"}); err == nil {
		t.Error("bad env port accepted")
	}
	if _, err := parse(t, nil, "--port", "70000"); err == nil {
		t.Error("out of range port accepted")
	}
}

func TestOpenCommand(t *testing.T) {
	dir := t.TempDir()
	cfg, err := parse(t, nil, "open", "--node-dir", dir, "--port", "9300")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Command != "open" || cfg.NodeDir != dir || cfg.Port != 9300 {
		t.Errorf("got %+v", cfg)
	}
	cfg, err = parse(t, nil, "--port", "9301", "open")
	if err != nil || cfg.Command != "open" || cfg.Port != 9301 {
		t.Errorf("flags before command: %+v, %v", cfg, err)
	}
	if _, err := parse(t, nil, "launch"); err == nil {
		t.Error("unknown command accepted")
	}
}

func TestTildeNodeDir(t *testing.T) {
	cfg, err := parse(t, nil, "--node-dir", "~/node")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(cfg.NodeDir) != "node" || filepath.Base(filepath.Dir(cfg.NodeDir)) != "home" {
		t.Errorf("node dir %q", cfg.NodeDir)
	}
}
