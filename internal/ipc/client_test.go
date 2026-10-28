package ipc_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ghostcoin-core/ghost-node-dashboard/internal/ipc"
	"github.com/ghostcoin-core/ghost-node-dashboard/internal/ipc/ipctest"
)

func TestCallResultAndError(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "geth.ipc")
	fake, err := ipctest.Start(sock, map[string]ipctest.Handler{
		"eth_blockNumber": ipctest.Value("0x10"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer fake.Close()

	c := ipc.New(sock)
	defer c.Close()
	var block string
	if err := c.Call(context.Background(), &block, "eth_blockNumber"); err != nil {
		t.Fatal(err)
	}
	if block != "0x10" {
		t.Errorf("block = %q", block)
	}

	err = c.Call(context.Background(), nil, "eth_nothing")
	var rpcErr *ipc.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32601 {
		t.Fatalf("missing method: err = %v, want RPCError -32601", err)
	}
	// An RPC error keeps the connection usable.
	if err := c.Call(context.Background(), &block, "eth_blockNumber"); err != nil {
		t.Fatalf("after rpc error: %v", err)
	}
}

func TestNodeNotRunningError(t *testing.T) {
	c := ipc.New(filepath.Join(t.TempDir(), "data", "geth.ipc"))
	err := c.Call(context.Background(), nil, "web3_clientVersion")
	if !errors.Is(err, ipc.ErrNodeNotRunning) {
		t.Fatalf("err = %v, want ErrNodeNotRunning", err)
	}
}

func TestReconnectAfterRestart(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "geth.ipc")
	first, err := ipctest.Start(sock, map[string]ipctest.Handler{"web3_clientVersion": ipctest.Value("first")})
	if err != nil {
		t.Fatal(err)
	}
	c := ipc.New(sock)
	defer c.Close()

	var v string
	if err := c.Call(context.Background(), &v, "web3_clientVersion"); err != nil || v != "first" {
		t.Fatalf("first call: %q, %v", v, err)
	}

	// The node stops: calls report it is not running.
	first.Close()
	if err := c.Call(context.Background(), &v, "web3_clientVersion"); !errors.Is(err, ipc.ErrNodeNotRunning) {
		t.Fatalf("while stopped: err = %v, want ErrNodeNotRunning", err)
	}

	// The node comes back on the same socket: the client reconnects.
	second, err := ipctest.Start(sock, map[string]ipctest.Handler{"web3_clientVersion": ipctest.Value("second")})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := c.Call(context.Background(), &v, "web3_clientVersion"); err != nil || v != "second" {
		t.Fatalf("after restart: %q, %v", v, err)
	}
}

func TestReconnectWithoutGap(t *testing.T) {
	// The node restarts between two calls; the client's old connection is
	// dead but the next call succeeds without reporting an error.
	sock := filepath.Join(t.TempDir(), "geth.ipc")
	first, err := ipctest.Start(sock, map[string]ipctest.Handler{"web3_clientVersion": ipctest.Value("first")})
	if err != nil {
		t.Fatal(err)
	}
	c := ipc.New(sock)
	defer c.Close()
	var v string
	if err := c.Call(context.Background(), &v, "web3_clientVersion"); err != nil {
		t.Fatal(err)
	}
	first.Close()
	second, err := ipctest.Start(sock, map[string]ipctest.Handler{"web3_clientVersion": ipctest.Value("second")})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := c.Call(context.Background(), &v, "web3_clientVersion"); err != nil || v != "second" {
		t.Fatalf("after restart: %q, %v", v, err)
	}
}
