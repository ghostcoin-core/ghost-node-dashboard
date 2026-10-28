package status

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"
)

// NodePanel is what /api/node returns. A field is null when the node is not
// running or does not answer that method.
type NodePanel struct {
	Running       bool         `json:"running"`
	Error         string       `json:"error,omitempty"`
	Syncing       *bool        `json:"syncing"`
	CurrentBlock  *uint64      `json:"currentBlock"`
	HighestBlock  *uint64      `json:"highestBlock"`
	Peers         *uint64      `json:"peers"`
	PeerClients   []PeerClient `json:"peerClients"`
	ClientVersion *string      `json:"clientVersion"`
	UptimeSeconds *int64       `json:"uptimeSeconds"`
	// UptimeFrom says what the uptime counts from: "node start" (when the
	// node opened its IPC endpoint) or "first answer" (the first answer the
	// dashboard had after the node came up).
	UptimeFrom  string  `json:"uptimeFrom,omitempty"`
	ChainID     *uint64 `json:"chainId"`
	NetworkName *string `json:"networkName"`
	// AutoUpdate is whether the update service is installed; it is read from
	// this computer, so it is known even while the node is down.
	AutoUpdate *bool `json:"autoUpdate"`
}

// PeerClient is one client name among the peers and how many peers run it.
type PeerClient struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// NodeEnv is what the node panel reads besides the node itself.
type NodeEnv struct {
	// IPCPath is the node's IPC endpoint. When it is a socket file its time
	// is when the node started listening, which is used for the uptime.
	IPCPath    string
	Uptime     *Uptime
	AutoUpdate func() *bool
	Now        func() time.Time
}

// FetchNode reads the node panel. It never fails: a stopped node is reported
// in the result.
func FetchNode(ctx context.Context, node Caller, env NodeEnv) NodePanel {
	var p NodePanel
	if env.AutoUpdate != nil {
		p.AutoUpdate = env.AutoUpdate()
	}
	now := time.Now()
	if env.Now != nil {
		now = env.Now()
	}
	down := func(err error) NodePanel {
		env.Uptime.Seen(false, now)
		return NodePanel{Error: downMessage(err), AutoUpdate: p.AutoUpdate}
	}

	version, err := probe(ctx, node)
	if err != nil {
		return down(err)
	}
	p.Running = true
	p.ClientVersion = version

	since := env.Uptime.Seen(true, now)
	from := "first answer"
	if t, ok := endpointCreated(env.IPCPath); ok && !t.After(now) {
		since, from = t, "node start"
	}
	if !since.IsZero() {
		secs := int64(now.Sub(since) / time.Second)
		p.UptimeSeconds, p.UptimeFrom = &secs, from
	}

	if p.ChainID, err = quantity(ctx, node, "eth_chainId"); lost(err) {
		return down(err)
	}
	if p.ChainID != nil {
		name := NetworkName(*p.ChainID)
		p.NetworkName = &name
	}

	head, err := quantity(ctx, node, "eth_blockNumber")
	if lost(err) {
		return down(err)
	}

	// eth_syncing is false when in sync and an object of progress otherwise.
	var syncing json.RawMessage
	if err := node.Call(ctx, &syncing, "eth_syncing"); lost(err) {
		return down(err)
	} else if err == nil {
		var progress struct {
			CurrentBlock string `json:"currentBlock"`
			HighestBlock string `json:"highestBlock"`
		}
		if string(syncing) == "false" {
			v := false
			p.Syncing, p.CurrentBlock, p.HighestBlock = &v, head, head
		} else if json.Unmarshal(syncing, &progress) == nil && progress.HighestBlock != "" {
			v := true
			p.Syncing = &v
			if n, err := parseQuantity(progress.CurrentBlock); err == nil {
				p.CurrentBlock = &n
			}
			if n, err := parseQuantity(progress.HighestBlock); err == nil {
				p.HighestBlock = &n
			}
		}
	}

	var peers []struct {
		Name string `json:"name"`
	}
	if err := node.Call(ctx, &peers, "admin_peers"); lost(err) {
		return down(err)
	} else if err == nil {
		n := uint64(len(peers))
		p.Peers = &n
		names := make([]string, len(peers))
		for i, peer := range peers {
			names[i] = peer.Name
		}
		p.PeerClients = groupClients(names)
	} else if p.Peers, err = quantity(ctx, node, "net_peerCount"); lost(err) {
		return down(err)
	}

	return p
}

// groupClients counts peers by client name and version, dropping the
// platform and compiler parts ("Geth/v1.13.5-stable/linux-amd64/go1.21"
// becomes "Geth/v1.13.5-stable"). The most common client comes first.
func groupClients(names []string) []PeerClient {
	counts := map[string]int{}
	for _, name := range names {
		parts := strings.SplitN(name, "/", 3)
		short := parts[0]
		if len(parts) > 1 {
			short += "/" + parts[1]
		}
		if short == "" {
			short = "unknown"
		}
		counts[short]++
	}
	out := make([]PeerClient, 0, len(counts))
	for name, n := range counts {
		out = append(out, PeerClient{Name: name, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// endpointCreated is when the node created its IPC socket. A named pipe has
// no such file, so on Windows this reports false.
func endpointCreated(path string) (time.Time, bool) {
	if path == "" {
		return time.Time{}, false
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode()&os.ModeSocket == 0 {
		return time.Time{}, false
	}
	return fi.ModTime(), true
}
