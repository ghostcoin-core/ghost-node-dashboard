package status

import (
	"context"
	"time"
)

// RestartPoll is how often WaitNodeBack asks the node whether it is back.
const RestartPoll = 500 * time.Millisecond

// EndpointStamp is when the node's IPC socket was created, or zero when
// there is no socket file (a stopped node, or a named pipe on Windows).
func EndpointStamp(ipcPath string) time.Time {
	t, _ := endpointCreated(ipcPath)
	return t
}

// WaitNodeBack waits until a restarted node answers again: after it has been
// seen down, or once its IPC socket is newer than before (the stamp taken
// before the restart). It returns the node's client version and latest
// block, or ok false when ctx ends first.
func WaitNodeBack(ctx context.Context, node Caller, ipcPath string, before time.Time) (version *string, head *uint64, ok bool) {
	sawDown := before.IsZero()
	for {
		v, err := probe(ctx, node)
		if err != nil {
			sawDown = true
		} else if stamp := EndpointStamp(ipcPath); sawDown || (!stamp.IsZero() && !stamp.Equal(before)) {
			h, _ := quantity(ctx, node, "eth_blockNumber")
			return v, h, true
		}
		select {
		case <-ctx.Done():
			return nil, nil, false
		case <-time.After(RestartPoll):
		}
	}
}
