//go:build !windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"syscall"
)

// pipePath is empty on unix: the endpoint lives in the node directory.
const pipePath = ""

func dialEndpoint(ctx context.Context, path string) (Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			return nil, fmt.Errorf("%w: %s", ErrNodeNotRunning, path)
		}
		return nil, fmt.Errorf("ipc dial: %w", err)
	}
	return conn, nil
}
