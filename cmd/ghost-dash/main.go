// Command ghost-dash serves the Ghost node dashboard on 127.0.0.1 and reads
// the node through its IPC endpoint.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/ghostcoin-core/ghost-node-dashboard/internal/config"
	"github.com/ghostcoin-core/ghost-node-dashboard/internal/dashkey"
	"github.com/ghostcoin-core/ghost-node-dashboard/internal/ipc"
	"github.com/ghostcoin-core/ghost-node-dashboard/internal/server"
)

func main() {
	cfg, err := config.FromOS()
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ghost-dash:", err)
		os.Exit(2)
	}

	key, err := dashkey.LoadOrCreate(cfg.NodeDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ghost-dash:", err)
		os.Exit(1)
	}

	switch cfg.Command {
	case "open":
		err = openLink(cfg.Port, key)
	default:
		err = serve(cfg, key)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ghost-dash:", err)
		os.Exit(1)
	}
}

func link(port int, key string) string {
	return "http://127.0.0.1:" + strconv.Itoa(port) + "/open?key=" + key
}

func openLink(port int, key string) error {
	url := link(port, key)
	fmt.Println(url)

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		// start is built into cmd; the empty argument is the window title.
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not open a browser (%v); open the link above yourself", err)
	}
	go func() { _ = cmd.Wait() }()
	// Give the opener a moment to hand the link over before we exit.
	time.Sleep(500 * time.Millisecond)
	return nil
}

func serve(cfg config.Config, key string) error {
	node := ipc.New(ipc.DefaultPath(cfg.NodeDir))
	defer node.Close()

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           server.New(server.Config{Port: cfg.Port, Key: key, Node: node, NodeDir: cfg.NodeDir}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	log.Printf("dashboard on http://%s (node: %s)", addr, node.Path())
	log.Printf("open it with: ghost-dash open --port %d", cfg.Port)
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
