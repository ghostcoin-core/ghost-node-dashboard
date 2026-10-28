package status

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/ghostcoin-core/ghost-node-dashboard/internal/ipc"
)

// MiningPausedFile is the file in the node directory that marks mining as
// paused from the dashboard.
const MiningPausedFile = "mining-paused"

// defaultFileMode is used for a settings file that did not exist before.
const defaultFileMode fs.FileMode = 0o644

// ControlResult is what a mining control returns: what was done and what the
// node reports afterwards, in plain words.
type ControlResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
	// Mining is eth_mining read back after the change; null when the node
	// did not answer it.
	Mining    *bool `json:"mining"`
	CoresUsed int   `json:"coresUsed"`
	Paused    bool  `json:"paused"`
}

// MiningPaused reports whether <node-dir>/mining-paused exists.
func MiningPaused(nodeDir string) bool {
	if nodeDir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(nodeDir, MiningPausedFile))
	return err == nil
}

// SavedCores is the core count in <node-dir>/miner-threads, else every
// logical core.
func SavedCores(env MiningEnv) int {
	if n, ok := minerThreads(env.NodeDir); ok {
		return n
	}
	return env.LogicalCores
}

// ValidCores reports whether n is a core count the dashboard accepts.
func ValidCores(n, logical int) bool {
	return n >= 1 && n <= logical
}

// The node's miner_start takes no parameters and its miner API has no way to
// set the thread count, so a new core count only reaches the node through
// <node-dir>/miner-threads when the node next starts.

// SetCores saves n to <node-dir>/miner-threads, which applies at the next node
// restart, and, unless mining is paused, makes sure the miner is running.
func SetCores(ctx context.Context, node Caller, env MiningEnv, n int) ControlResult {
	if !ValidCores(n, env.LogicalCores) {
		return ControlResult{
			Error:     fmt.Sprintf("Choose a whole number of cores from 1 to %d.", env.LogicalCores),
			CoresUsed: SavedCores(env),
			Paused:    MiningPaused(env.NodeDir),
		}
	}
	res := ControlResult{CoresUsed: SavedCores(env), Paused: MiningPaused(env.NodeDir)}
	if err := WriteSetting(env.NodeDir, MinerThreadsFile, strconv.Itoa(n)+"\n"); err != nil {
		res.Error = "Could not save the core count: " + err.Error()
		return res
	}
	res.CoresUsed = n
	if res.Paused {
		res.OK = true
		res.Message = fmt.Sprintf("Saved %d cores. They apply at the next node restart. Mining is paused.", n)
		res.Mining = readMining(ctx, node)
		return res
	}
	if err := node.Call(ctx, nil, "miner_start"); err != nil {
		res.Error = fmt.Sprintf("Saved %d cores for the next node restart, but the node did not start mining: %s.", n, nodeProblem(err))
		res.Mining = readMining(ctx, node)
		return res
	}
	res.Mining = readMining(ctx, node)
	res.OK, res.Message = miningOutcome(res.Mining, true, fmt.Sprintf("Saved %d cores. They apply at the next node restart; until then the node keeps its current core count.", n))
	if !res.OK {
		res.Error, res.Message = res.Message, ""
	}
	return res
}

// Pause stops the miner and writes <node-dir>/mining-paused. The node keeps
// syncing.
func Pause(ctx context.Context, node Caller, env MiningEnv) ControlResult {
	res := ControlResult{CoresUsed: SavedCores(env), Paused: MiningPaused(env.NodeDir)}
	if err := WriteSetting(env.NodeDir, MiningPausedFile, "paused from the dashboard\n"); err != nil {
		res.Error = "Could not save the pause: " + err.Error()
		return res
	}
	res.Paused = true
	if err := node.Call(ctx, nil, "miner_stop"); err != nil {
		res.Error = "Saved the pause, but the node did not stop mining: " + nodeProblem(err) + "."
		res.Mining = readMining(ctx, node)
		return res
	}
	res.Mining = readMining(ctx, node)
	res.OK, res.Message = miningOutcome(res.Mining, false, "Mining paused. The node keeps syncing.")
	if !res.OK {
		res.Error, res.Message = res.Message, ""
	}
	return res
}

// Resume removes <node-dir>/mining-paused and starts the miner. The node mines
// on the core count it started with; a saved count applies at its next restart.
func Resume(ctx context.Context, node Caller, env MiningEnv) ControlResult {
	cores := SavedCores(env)
	res := ControlResult{CoresUsed: cores, Paused: MiningPaused(env.NodeDir)}
	if env.NodeDir != "" {
		err := os.Remove(filepath.Join(env.NodeDir, MiningPausedFile))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			res.Error = "Could not clear the pause: " + err.Error()
			return res
		}
	}
	res.Paused = false
	if err := node.Call(ctx, nil, "miner_start"); err != nil {
		res.Error = "Cleared the pause, but the node did not start mining: " + nodeProblem(err) + "."
		res.Mining = readMining(ctx, node)
		return res
	}
	res.Mining = readMining(ctx, node)
	res.OK, res.Message = miningOutcome(res.Mining, true, fmt.Sprintf("Mining resumed. The saved %d cores apply at the next node restart.", cores))
	if !res.OK {
		res.Error, res.Message = res.Message, ""
	}
	return res
}

// readMining reads eth_mining back from the node.
func readMining(ctx context.Context, node Caller) *bool {
	var mining bool
	if err := node.Call(ctx, &mining, "eth_mining"); err != nil {
		return nil
	}
	return &mining
}

// miningOutcome compares what the node reports with what was asked.
func miningOutcome(mining *bool, want bool, done string) (bool, string) {
	switch {
	case mining == nil:
		return false, "The node took the change but did not say whether it is mining."
	case *mining == want:
		return true, done
	case want:
		return false, "The node took the change but reports it is not mining."
	default:
		return false, "The node took the change but reports it is still mining."
	}
}

// nodeProblem describes a failed call in plain words.
func nodeProblem(err error) string {
	var rpcErr *ipc.RPCError
	if errors.As(err, &rpcErr) {
		return "it answered “" + rpcErr.Message + "”"
	}
	if errors.Is(err, ipc.ErrNodeNotRunning) {
		return "the node is not running"
	}
	return "cannot reach the node (" + err.Error() + ")"
}

// WriteSetting replaces <nodeDir>/<name> with content atomically: it writes a
// temporary file next to it and renames it over the old one, keeping the old
// file's permissions.
func WriteSetting(nodeDir, name, content string) error {
	if nodeDir == "" {
		return errors.New("no node directory")
	}
	path := filepath.Join(nodeDir, name)
	mode := defaultFileMode
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(nodeDir, "."+name+"-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	err = f.Chmod(mode)
	if err == nil {
		_, err = f.WriteString(content)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
