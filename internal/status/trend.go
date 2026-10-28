package status

import (
	"sync"
	"time"
)

// TrendSamples is how many hash rate samples the trend keeps.
const TrendSamples = 60

// HashSample is the node's hash rate at one poll.
type HashSample struct {
	// Time is a unix time in seconds.
	Time int64 `json:"time"`
	// HashRate is in hashes per second.
	HashRate float64 `json:"hashRate"`
}

// HashTrend keeps the latest TrendSamples hash rates, one per mining poll, in
// memory only. The zero value is ready to use and safe for concurrent use.
type HashTrend struct {
	mu      sync.Mutex
	samples [TrendSamples]HashSample
	next    int
	n       int
}

// Record adds the panel's hash rate at now when the node is running and
// reports one, and sets the panel's trend.
func (h *HashTrend) Record(p *MiningPanel, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if p.Running && p.HashRate != nil {
		h.samples[h.next] = HashSample{Time: now.Unix(), HashRate: *p.HashRate}
		h.next = (h.next + 1) % TrendSamples
		h.n = min(h.n+1, TrendSamples)
	}
	p.HashRateTrend = h.list()
}

// Samples is the trend, oldest first; empty, not nil, when there are none.
func (h *HashTrend) Samples() []HashSample {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.list()
}

func (h *HashTrend) list() []HashSample {
	out := make([]HashSample, 0, h.n)
	start := (h.next - h.n + TrendSamples) % TrendSamples
	for i := range h.n {
		out = append(out, h.samples[(start+i)%TrendSamples])
	}
	return out
}
