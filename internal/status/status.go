// Package status reads a summary of the node over IPC.
package status

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ghostcoin-core/ghost-node-dashboard/internal/ipc"
)

// Status is what /api/status returns. A field is null when the node is not
// running or does not answer that method.
type Status struct {
	Running       bool    `json:"running"`
	Error         string  `json:"error,omitempty"`
	ChainID       *uint64 `json:"chainId"`
	BlockNumber   *uint64 `json:"blockNumber"`
	PeerCount     *uint64 `json:"peerCount"`
	Syncing       *bool   `json:"syncing"`
	Mining        *bool   `json:"mining"`
	PayoutAddress *string `json:"payoutAddress"`
	ClientVersion *string `json:"clientVersion"`
}

// Caller is the part of the IPC client status needs.
type Caller interface {
	Call(ctx context.Context, result any, method string, params ...any) error
}

// Fetch asks the node for its status. It never fails: a stopped node or a
// broken connection is reported in the result.
func Fetch(ctx context.Context, node Caller) Status {
	var st Status

	var version string
	if err := node.Call(ctx, &version, "web3_clientVersion"); err != nil {
		var rpcErr *ipc.RPCError
		if !errors.As(err, &rpcErr) {
			return notRunning(err)
		}
	} else {
		st.ClientVersion = &version
	}
	st.Running = true

	quantity := func(method string) (*uint64, error) {
		var hex string
		if err := node.Call(ctx, &hex, method); err != nil {
			return nil, err
		}
		n, err := parseQuantity(hex)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", method, err)
		}
		return &n, nil
	}

	var err error
	if st.ChainID, err = quantity("eth_chainId"); lost(err) {
		return notRunning(err)
	}
	if st.BlockNumber, err = quantity("eth_blockNumber"); lost(err) {
		return notRunning(err)
	}
	if st.PeerCount, err = quantity("net_peerCount"); lost(err) {
		return notRunning(err)
	}

	// eth_syncing is false when in sync and an object of progress otherwise.
	var syncing json.RawMessage
	if err := node.Call(ctx, &syncing, "eth_syncing"); err == nil {
		v := len(syncing) > 0 && string(syncing) != "false" && string(syncing) != "null"
		st.Syncing = &v
	} else if lost(err) {
		return notRunning(err)
	}

	var mining bool
	if err := node.Call(ctx, &mining, "eth_mining"); err == nil {
		st.Mining = &mining
	} else if lost(err) {
		return notRunning(err)
	}

	var coinbase string
	if err := node.Call(ctx, &coinbase, "eth_coinbase"); err == nil && coinbase != "" {
		st.PayoutAddress = &coinbase
	} else if lost(err) {
		return notRunning(err)
	}

	return st
}

// lost reports whether err means the connection to the node failed, rather
// than one method being unavailable or returning something odd.
func lost(err error) bool {
	if err == nil {
		return false
	}
	var rpcErr *ipc.RPCError
	if errors.As(err, &rpcErr) {
		return false
	}
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) || errors.Is(err, errBadQuantity) {
		return false
	}
	return true
}

func notRunning(err error) Status {
	return Status{Running: false, Error: downMessage(err)}
}

// downMessage is the error a panel shows when the node did not answer.
func downMessage(err error) string {
	if errors.Is(err, ipc.ErrNodeNotRunning) {
		return ipc.ErrNodeNotRunning.Error()
	}
	return "cannot reach the node: " + err.Error()
}

var errBadQuantity = errors.New("not a hex quantity")

func parseQuantity(s string) (uint64, error) {
	if !strings.HasPrefix(s, "0x") || len(s) < 3 {
		return 0, fmt.Errorf("%w: %q", errBadQuantity, s)
	}
	n, err := strconv.ParseUint(s[2:], 16, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", errBadQuantity, s)
	}
	return n, nil
}
