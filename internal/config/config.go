// Package config reads the dashboard's command line and environment.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultPort is used when neither --port nor GHOST_DASH_PORT is set.
const DefaultPort = 8580

// PortEnv overrides the default port.
const PortEnv = "GHOST_DASH_PORT"

// Config is the parsed command line.
type Config struct {
	Command string // "" to serve, "open" to print and open the link
	NodeDir string
	Port    int
}

// Parse reads args (without the program name). getenv and home stand in for
// os.Getenv and os.UserHomeDir.
func Parse(args []string, getenv func(string) string, home func() (string, error), out io.Writer) (Config, error) {
	defaultPort := DefaultPort
	if v := strings.TrimSpace(getenv(PortEnv)); v != "" {
		p, err := parsePort(v)
		if err != nil {
			return Config{}, fmt.Errorf("%s: %w", PortEnv, err)
		}
		defaultPort = p
	}

	homeDir, homeErr := home()
	defaultDir := ""
	if homeErr == nil {
		defaultDir = filepath.Join(homeDir, ".ghost-node")
	}

	fs := flag.NewFlagSet("ghost-dash", flag.ContinueOnError)
	fs.SetOutput(out)
	nodeDir := fs.String("node-dir", defaultDir, "the node's directory")
	port := fs.Int("port", defaultPort, "port to listen on at 127.0.0.1 (or set "+PortEnv+")")
	fs.Usage = func() {
		fmt.Fprintln(out, "Usage: ghost-dash [flags]        serve the dashboard on 127.0.0.1")
		fmt.Fprintln(out, "       ghost-dash open [flags]   print and open the dashboard link")
		fmt.Fprintln(out)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	var cfg Config
	if rest := fs.Args(); len(rest) > 0 {
		switch rest[0] {
		case "open":
			cfg.Command = "open"
			if err := fs.Parse(rest[1:]); err != nil {
				return Config{}, err
			}
			if fs.NArg() > 0 {
				return Config{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
			}
		default:
			return Config{}, fmt.Errorf("unknown command %q", rest[0])
		}
	}

	if *port < 1 || *port > 65535 {
		return Config{}, fmt.Errorf("--port %d is out of range", *port)
	}
	cfg.Port = *port

	dir := *nodeDir
	if dir == "~" || strings.HasPrefix(dir, "~/") || strings.HasPrefix(dir, `~\`) {
		if homeErr != nil {
			return Config{}, fmt.Errorf("find home directory: %w", homeErr)
		}
		dir = filepath.Join(homeDir, dir[1:])
	}
	if dir == "" {
		if homeErr != nil {
			return Config{}, fmt.Errorf("find home directory (or pass --node-dir): %w", homeErr)
		}
		return Config{}, errors.New("--node-dir is empty")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Config{}, err
	}
	cfg.NodeDir = abs
	return cfg, nil
}

// FromOS parses os.Args with the real environment.
func FromOS() (Config, error) {
	return Parse(os.Args[1:], os.Getenv, os.UserHomeDir, os.Stderr)
}

func parsePort(s string) (int, error) {
	p, err := strconv.Atoi(s)
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("%q is not a port number", s)
	}
	return p, nil
}
