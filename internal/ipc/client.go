// Package ipc is a small JSON-RPC 2.0 client for the node's IPC endpoint: a
// unix socket on macOS and Linux, a named pipe on Windows.
package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"
)

// ErrNodeNotRunning is returned when the IPC endpoint is absent or refuses
// the connection, which is what a stopped node looks like.
var ErrNodeNotRunning = errors.New("node not running")

// DefaultTimeout bounds one call when the context has no earlier deadline.
const DefaultTimeout = 5 * time.Second

// RPCError is an error the node returned for a call. The connection is fine;
// the method failed.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// Conn is what a dialer returns: a net.Conn on unix, a pipe file on Windows.
type Conn interface {
	io.ReadWriteCloser
	SetDeadline(time.Time) error
}

// Dialer opens a connection to the endpoint at path. It returns an error
// wrapping ErrNodeNotRunning when nothing is listening there.
type Dialer func(ctx context.Context, path string) (Conn, error)

// Client holds at most one connection and redials it when the node restarts.
// It is safe for concurrent use; calls are serialised on the connection.
type Client struct {
	path string
	dial Dialer

	mu   sync.Mutex
	conn Conn
	dec  *json.Decoder
	id   uint64
}

// New returns a client for the endpoint at path using the platform dialer.
func New(path string) *Client {
	return &Client{path: path, dial: dialEndpoint}
}

// NewWithDialer returns a client that connects with dial instead of the
// platform dialer.
func NewWithDialer(path string, dial Dialer) *Client {
	return &Client{path: path, dial: dial}
}

// Path is the endpoint the client connects to.
func (c *Client) Path() string { return c.path }

// DefaultPath is the node's IPC endpoint for a node directory.
func DefaultPath(nodeDir string) string {
	if pipePath != "" {
		return pipePath
	}
	return filepath.Join(nodeDir, "data", "geth.ipc")
}

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type response struct {
	ID     *uint64         `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *RPCError       `json:"error"`
}

// Call invokes method with params and decodes the result into result (which
// may be nil). If the existing connection has gone away, for example because
// the node restarted, it reconnects once and retries.
func (c *Client) Call(ctx context.Context, result any, method string, params ...any) error {
	if params == nil {
		params = []any{}
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	fresh := false
	for attempt := 0; attempt < 2; attempt++ {
		if c.conn == nil {
			conn, err := c.dial(ctx, c.path)
			if err != nil {
				return err
			}
			c.conn = conn
			c.dec = json.NewDecoder(conn)
			fresh = true
		}
		raw, err := c.roundTrip(ctx, method, params)
		if err == nil {
			if result == nil || len(raw) == 0 {
				return nil
			}
			return json.Unmarshal(raw, result)
		}
		var rpcErr *RPCError
		if errors.As(err, &rpcErr) {
			return err
		}
		c.closeLocked()
		if fresh || ctx.Err() != nil {
			return err
		}
	}
	return errors.New("ipc: unreachable")
}

func (c *Client) roundTrip(ctx context.Context, method string, params []any) (json.RawMessage, error) {
	deadline, _ := ctx.Deadline()
	// A pipe or socket without deadline support still works; it just can't
	// be cut short.
	_ = c.conn.SetDeadline(deadline)

	c.id++
	id := c.id
	body, err := json.Marshal(request{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	if _, err := c.conn.Write(body); err != nil {
		return nil, fmt.Errorf("ipc write: %w", err)
	}
	for {
		var resp response
		if err := c.dec.Decode(&resp); err != nil {
			return nil, fmt.Errorf("ipc read: %w", err)
		}
		// Skip anything that is not the answer to this request, such as a
		// late answer to a call that timed out on a previous connection.
		if resp.ID == nil || *resp.ID != id {
			continue
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

func (c *Client) closeLocked() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.conn = nil
	c.dec = nil
}

// Close drops the current connection. The client can still be used; the next
// call dials again.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeLocked()
	return nil
}
