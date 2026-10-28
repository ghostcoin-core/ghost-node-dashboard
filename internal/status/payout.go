package status

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ghostcoin-core/ghost-node-dashboard/internal/ipc"
)

// ConfigFile is the node's settings file in the node directory, written by
// the installer. Its Etherbase line is the payout address the node starts
// with.
const ConfigFile = "config.toml"

// PayoutResult is what the payout address control returns.
type PayoutResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
	// Saved is true when config.toml has the new address.
	Saved bool `json:"saved"`
	// PayoutAddress is eth_coinbase read back after the change; null when
	// the node did not answer it.
	PayoutAddress *string `json:"payoutAddress"`
}

// RewriteEtherbase sets the Etherbase line of a config.toml to addr and
// leaves every other line as it was, line endings included. Like the
// installer, it replaces each line that sets Etherbase; when there is none
// it adds one under [Eth.Miner], or adds that section at the end.
func RewriteEtherbase(text, addr string) string {
	lines := strings.SplitAfter(text, "\n")
	setting := "Etherbase = \"" + addr + "\""
	replaced := false
	miner := -1
	for i, line := range lines {
		body, end := splitEnding(line)
		trimmed := strings.TrimLeft(body, " \t")
		if trimmed == "[Eth.Miner]" && miner < 0 {
			miner = i
		}
		key, _, ok := strings.Cut(trimmed, "=")
		if !ok || strings.TrimRight(key, " \t") != "Etherbase" {
			continue
		}
		indent := body[:len(body)-len(trimmed)]
		lines[i] = indent + setting + end
		replaced = true
	}
	if replaced {
		return strings.Join(lines, "")
	}
	if miner >= 0 {
		_, end := splitEnding(lines[miner])
		if end == "" {
			end = "\n"
			lines[miner] += end
		}
		lines = append(lines[:miner+1], append([]string{setting + end}, lines[miner+1:]...)...)
		return strings.Join(lines, "")
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text + "\n[Eth.Miner]\n" + setting + "\n"
}

// splitEnding splits a line from its "\n" or "\r\n".
func splitEnding(line string) (string, string) {
	if strings.HasSuffix(line, "\r\n") {
		return line[:len(line)-2], "\r\n"
	}
	if strings.HasSuffix(line, "\n") {
		return line[:len(line)-1], "\n"
	}
	return line, ""
}

// SaveEtherbase rewrites the Etherbase line of <nodeDir>/config.toml in one
// step, keeping the file's permissions. A missing config is not created and
// a config that links elsewhere is refused.
func SaveEtherbase(nodeDir, addr string) error {
	if nodeDir == "" {
		return errors.New("no node directory")
	}
	path := filepath.Join(nodeDir, ConfigFile)
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s not found; is the node installed in this folder?", path)
	}
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a plain file", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return WriteSetting(nodeDir, ConfigFile, RewriteEtherbase(string(b), addr))
}

// SetPayout saves addr (already checked) in config.toml, so restarts keep
// it, and asks the node to pay to it at once (miner_setEtherbase).
func SetPayout(ctx context.Context, node Caller, nodeDir, addr string) PayoutResult {
	var res PayoutResult
	if err := SaveEtherbase(nodeDir, addr); err != nil {
		res.Error = "Could not save the payout address: " + err.Error()
		res.PayoutAddress = readCoinbase(ctx, node)
		return res
	}
	res.Saved = true
	if err := node.Call(ctx, nil, "miner_setEtherbase", addr); err != nil {
		res.PayoutAddress = readCoinbase(ctx, node)
		if errors.Is(err, ipc.ErrNodeNotRunning) {
			res.OK = true
			res.Message = "Saved " + addr + ". The node is not running; it will pay to this address when it starts."
			return res
		}
		res.Error = "Saved " + addr + " for the next start, but the node did not take it now: " + nodeProblem(err) + "."
		return res
	}
	res.PayoutAddress = readCoinbase(ctx, node)
	switch {
	case res.PayoutAddress == nil:
		res.Error = "Saved " + addr + ". The node took it but did not say which address it pays to now."
	case !sameAddress(*res.PayoutAddress, addr):
		res.Error = "Saved " + addr + ", but the node still reports " + *res.PayoutAddress + "."
	default:
		res.OK = true
		res.Message = "Rewards now go to " + addr + ". Saved for restarts and updates."
	}
	return res
}

func readCoinbase(ctx context.Context, node Caller) *string {
	addr, err := coinbase(ctx, node)
	if err != nil || addr == "" {
		return nil
	}
	return &addr
}
