package dashkey

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCreatesOwnerOnlyKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".ghost-node")
	key, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 64 {
		t.Errorf("key length %d, want 64 hex characters", len(key))
	}
	info, err := os.Stat(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("key file mode %v, want 0600", info.Mode().Perm())
	}

	again, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again != key {
		t.Error("second load returned a different key")
	}
}

func TestReadsExistingKey(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(Path(dir), []byte("  installer-test-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if key != "installer-test-key" {
		t.Errorf("key = %q", key)
	}
}

func TestEmptyKeyRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(Path(dir), []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(dir); err == nil {
		t.Fatal("empty key file accepted")
	}
}
