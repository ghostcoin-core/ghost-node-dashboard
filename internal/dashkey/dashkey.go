// Package dashkey loads the dashboard key from the node directory, creating
// it with owner-only permissions the first time.
package dashkey

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FileName is the key file inside the node directory.
const FileName = "dashboard-key"

// Path is the key file for a node directory.
func Path(nodeDir string) string {
	return filepath.Join(nodeDir, FileName)
}

// LoadOrCreate returns the key stored in nodeDir, creating the directory and
// a new random key if there is none yet.
func LoadOrCreate(nodeDir string) (string, error) {
	path := Path(nodeDir)
	key, err := load(path)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}

	if err := os.MkdirAll(nodeDir, 0o700); err != nil {
		return "", fmt.Errorf("create node directory: %w", err)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate dashboard key: %w", err)
	}
	key = hex.EncodeToString(buf)

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		// Another process made it between our read and create; use theirs.
		return load(path)
	}
	if err != nil {
		return "", fmt.Errorf("create dashboard key: %w", err)
	}
	if _, err := f.WriteString(key + "\n"); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("write dashboard key: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("write dashboard key: %w", err)
	}
	return key, nil
}

func load(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(b))
	if key == "" {
		return "", fmt.Errorf("dashboard key file %s is empty", path)
	}
	return key, nil
}
