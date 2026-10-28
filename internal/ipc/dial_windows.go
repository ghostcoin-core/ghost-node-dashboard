//go:build windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
	"time"
)

// pipePath is the named pipe the node listens on under Windows.
const pipePath = `\\.\pipe\geth.ipc`

const errorPipeBusy = syscall.Errno(231)

func dialEndpoint(ctx context.Context, path string) (Conn, error) {
	for {
		// Overlapped mode lets the runtime poll the pipe, so deadlines work.
		f, err := os.OpenFile(path, os.O_RDWR|syscall.FILE_FLAG_OVERLAPPED, 0)
		if err == nil {
			return f, nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNodeNotRunning, path)
		}
		if !errors.Is(err, errorPipeBusy) {
			return nil, fmt.Errorf("ipc dial: %w", err)
		}
		// Every pipe instance is in use; wait briefly for one to free up.
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("ipc dial: %w", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}
