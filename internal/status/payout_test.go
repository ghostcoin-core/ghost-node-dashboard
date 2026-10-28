package status

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const newPayout = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"

func TestConfigEtherbaseRewrite(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			name: "installer config",
			in: "[Eth]\nNetworkId = 6777972\nSyncMode = \"snap\"\n\n[Eth.Miner]\n" +
				"Etherbase = \"0x00000000000000000000000000000000000000aa\"\nGasPrice = 1\n\n[Node]\nDataDir = \"/x\"\n",
			want: "[Eth]\nNetworkId = 6777972\nSyncMode = \"snap\"\n\n[Eth.Miner]\n" +
				"Etherbase = \"" + newPayout + "\"\nGasPrice = 1\n\n[Node]\nDataDir = \"/x\"\n",
		},
		{
			name: "windows line endings and no final newline",
			in:   "[Eth.Miner]\r\nEtherbase = \"0xabc\"\r\n# Etherbase = \"comment kept\"\r\nRecommit = 3000000000",
			want: "[Eth.Miner]\r\nEtherbase = \"" + newPayout + "\"\r\n# Etherbase = \"comment kept\"\r\nRecommit = 3000000000",
		},
		{
			name: "indented, other spacing",
			in:   "[Eth.Miner]\n  Etherbase=\"0xabc\"\nEtherbaseX = 1\n",
			want: "[Eth.Miner]\n  Etherbase = \"" + newPayout + "\"\nEtherbaseX = 1\n",
		},
		{
			name: "section without the line",
			in:   "[Eth]\nNetworkId = 1\n\n[Eth.Miner]\nGasPrice = 1\n",
			want: "[Eth]\nNetworkId = 1\n\n[Eth.Miner]\nEtherbase = \"" + newPayout + "\"\nGasPrice = 1\n",
		},
		{
			name: "no section, as the installer appends it",
			in:   "[Eth]\nNetworkId = 1",
			want: "[Eth]\nNetworkId = 1\n\n[Eth.Miner]\nEtherbase = \"" + newPayout + "\"\n",
		},
	}
	for _, c := range cases {
		if got := RewriteEtherbase(c.in, newPayout); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}

	// On disk: the file is replaced in one step, keeps its permissions and
	// leaves no temporary file behind.
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFile)
	if err := os.WriteFile(path, []byte(cases[0].in), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveEtherbase(dir, newPayout); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != cases[0].want {
		t.Errorf("config.toml = %q", b)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("config.toml mode %v, want 0600 as before", fi.Mode().Perm())
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("files left in the node folder: %v", entries)
	}

	// A missing config is not created; a link is not followed.
	empty := t.TempDir()
	if err := SaveEtherbase(empty, newPayout); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(empty, ConfigFile)); !os.IsNotExist(err) {
		t.Errorf("missing config was created (%v)", err)
	}
	linked := t.TempDir()
	if err := os.Symlink(path, filepath.Join(linked, ConfigFile)); err != nil {
		t.Fatal(err)
	}
	if err := SaveEtherbase(linked, "0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb"); err == nil {
		t.Error("a linked config.toml was rewritten")
	}
	if b, _ := os.ReadFile(path); string(b) != cases[0].want {
		t.Errorf("the link's target changed: %q", b)
	}
}
