package caddyhttp

import (
	"sync"
	"time"
)

// Short critical sections only copy scalar metadata. They never cover the
// underlying Read or logging/syscalls. Concurrent Reads are legal; if they
// overlap, an exact oldest pending age is unknown until the overlap drains.
type proxyReadState struct {
	mu                 sync.Mutex
	entered, completed uint64
	inFlight           int
	started, returned  time.Time
	ambiguous          bool
	lastBytes          int
	lastError          string
}

func (r *proxyReadState) begin() {
	r.mu.Lock()
	r.entered++
	if r.inFlight == 0 {
		r.started = time.Now()
		r.ambiguous = false
	} else {
		r.ambiguous = true
	}
	r.inFlight++
	r.mu.Unlock()
}

func (r *proxyReadState) complete(n int, class string) time.Time {
	r.mu.Lock()
	r.completed++
	r.inFlight--
	now := time.Now()
	r.returned = now
	r.lastBytes, r.lastError = n, class
	if r.inFlight == 0 {
		r.started = time.Time{}
		r.ambiguous = false
	}
	r.mu.Unlock()
	return now
}

func (r *proxyReadState) snapshot() map[string]any {
	r.mu.Lock()
	now := time.Now()
	entered, completed, inFlight := r.entered, r.completed, r.inFlight
	started, returned, ambiguous := r.started, r.returned, r.ambiguous
	lastBytes, lastError := r.lastBytes, r.lastError
	r.mu.Unlock()
	out := map[string]any{"unix_ms": now.UnixMilli(), "calls_entered": entered,
		"calls_completed": completed, "in_flight": inFlight,
		"pending_age_ms": nil, "pending_age_ambiguous": ambiguous,
		"last_return_age_ms": nil, "last_return_bytes": nil, "last_return_error_class": nil}
	if inFlight == 1 && !ambiguous && !started.IsZero() {
		out["pending_age_ms"] = float64(now.Sub(started)) / float64(time.Millisecond)
	}
	if !returned.IsZero() {
		out["last_return_age_ms"] = float64(now.Sub(returned)) / float64(time.Millisecond)
		out["last_return_bytes"], out["last_return_error_class"] = lastBytes, lastError
	}
	return out
}

func proxyTCPUnavailable(reason string) map[string]any {
	now := time.Now().UnixMilli()
	return map[string]any{"available": false, "unavailable_reason": reason,
		"bytes_received": nil, "receive_queue_bytes": nil,
		"sample_started_unix_ms": now, "sample_completed_unix_ms": now,
		"sample_duration_us": int64(0), "sample_sequence": nil}
}

// Existing event logs can burst at 12/s. Keep all TCP syscalls at <=1 sample/s
// per socket, except for one final pre-close refresh. The cached map is immutable
// after publication. A closed wrapper never reuses its cache for later events.
func (p *ProxyEvidence) tcpSample(final bool) (map[string]any, float64) {
	p.tcpMu.Lock()
	defer p.tcpMu.Unlock()
	if p.closed.Load() && !final {
		return proxyTCPUnavailable("connection_closed"), 0
	}
	if final || p.tcpCached == nil || time.Since(p.tcpCaptured) >= time.Second {
		p.tcpCached = proxyTCPSnapshot(p.Conn)
		p.tcpSequence++
		p.tcpCached["sample_sequence"] = p.tcpSequence
		p.tcpCaptured = time.Now()
	}
	return p.tcpCached, float64(time.Since(p.tcpCaptured)) / float64(time.Millisecond)
}
