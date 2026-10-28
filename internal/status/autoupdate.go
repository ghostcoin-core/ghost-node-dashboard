package status

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// The update service the installer sets up on each system.
const (
	updatePlist      = "com.ghostcoin.node.update.plist"
	updateTimer      = "ghost-node-update.timer"
	updateTaskWindow = "GhostNodeUpdate"
)

// AutoUpdateInstalled reports whether the node's update service is installed
// on this computer, or nil when that cannot be told.
func AutoUpdateInstalled() *bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return autoUpdateFor(runtime.GOOS, home, os.Getenv, windowsTaskExists)
}

func autoUpdateFor(goos, home string, getenv func(string) string, taskExists func(string) (bool, error)) *bool {
	var installed bool
	switch goos {
	case "darwin":
		installed = fileExists(filepath.Join(home, "Library", "LaunchAgents", updatePlist))
	case "windows":
		ok, err := taskExists(updateTaskWindow)
		if err != nil {
			return nil
		}
		installed = ok
	default:
		config := getenv("XDG_CONFIG_HOME")
		if config == "" {
			config = filepath.Join(home, ".config")
		}
		installed = fileExists(filepath.Join(config, "systemd", "user", updateTimer))
	}
	return &installed
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// windowsTaskExists asks the Task Scheduler whether a task is registered.
func windowsTaskExists(name string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := exec.CommandContext(ctx, "schtasks", "/Query", "/TN", name).Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && ctx.Err() == nil {
		return false, nil
	}
	return err == nil, err
}
