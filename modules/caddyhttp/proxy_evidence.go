package caddyhttp

// Experimental, opt-in metadata-only transport evidence. The listener wrapper
// sits BELOW TLS; it never parses or logs bytes. Logging starts only after an
// authenticated forward-proxy handler calls EnableProxyEvidence.
import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"go.uber.org/zap"
	"golang.org/x/net/http2"
)

type proxyEvidenceKey struct{}

// x/net exposes error categories at server scope, without a connection ID.
// Keep that scope explicit, and bound unauthenticated protocol-error logging.
func proxyEvidenceH2ErrorCounter(logger *zap.Logger) func(string) {
	var mu sync.Mutex
	var second int64
	var used int
	var suppressed uint64
	return func(category string) {
		mu.Lock()
		now := time.Now().Unix()
		if second != now {
			second = now
			used = 0
		}
		if used >= 4 {
			suppressed++
			mu.Unlock()
			return
		}
		used++
		dropped := suppressed
		mu.Unlock()
		logger.Info("http2 connection error category", zap.String("event", "h2_error_category"), zap.String("category", category), zap.String("scope", "server"), zap.Uint64("suppressed_events", dropped))
	}
}

type proxyEvidenceListener struct {
	net.Listener
	logger *zap.Logger
}

func (l *proxyEvidenceListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return c, err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return c, nil
	}
	p := &ProxyEvidence{Conn: c, id: hex.EncodeToString(id[:]), start: time.Now(), logger: l.logger,
		done: make(chan struct{}), ops: make(map[uint64]proxyOperation), h2Events: make(chan http2.ServerObservation, 8)}
	return p, nil
}

type proxyOperation struct {
	Trace   string  `json:"trace_id"`
	Phase   string  `json:"phase"`
	StartMS float64 `json:"start_ms"`
	at      time.Time
}
type proxyIOError struct {
	Class string  `json:"class"`
	AtMS  float64 `json:"at_ms"`
	Errno int     `json:"errno,omitempty"`
}

// ProxyEvidence is request-accessible transport metadata for one outer TCP
// connection. Calls do not set deadlines, reset streams or change TLS behavior.
type ProxyEvidence struct {
	h2        atomic.Pointer[http2.ServerObservation]
	h2Events  chan http2.ServerObservation
	h2Dropped atomic.Uint64
	net.Conn
	id                    string
	start                 time.Time
	logger                *zap.Logger
	active                atomic.Bool
	closed                atomic.Bool
	writeStart            atomic.Int64
	lastRead              atomic.Int64
	lastWrite             atomic.Int64
	readBytes             atomic.Uint64
	writeBytes            atomic.Uint64
	done                  chan struct{}
	once                  sync.Once
	mu                    sync.Mutex
	ops                   map[uint64]proxyOperation
	next                  uint64
	readError, writeError *proxyIOError
	budgetSecond          int64
	budgetUsed            int
	suppressed            uint64
}

func proxyEvidenceFromConn(c net.Conn) *ProxyEvidence {
	for depth := 0; c != nil && depth < 8; depth++ {
		if p, ok := c.(*ProxyEvidence); ok {
			return p
		}
		switch v := c.(type) {
		case interface{ NetConn() net.Conn }:
			c = v.NetConn()
		case interface{ tlsNetConn() net.Conn }:
			c = v.tlsNetConn()
		case interface{ Unwrap() net.Conn }:
			c = v.Unwrap()
		default:
			return nil
		}
	}
	return nil
}

func withProxyEvidence(ctx context.Context, c net.Conn) context.Context {
	if p := proxyEvidenceFromConn(c); p != nil {
		return context.WithValue(ctx, proxyEvidenceKey{}, p)
	}
	return ctx
}

// EnableProxyEvidence must only be called after authentication. Nil means this
// transport is not instrumented (e.g. HTTP/3 or the environment opt-in is off).
func EnableProxyEvidence(ctx context.Context) *ProxyEvidence {
	p, _ := ctx.Value(proxyEvidenceKey{}).(*ProxyEvidence)
	if p != nil && p.active.CompareAndSwap(false, true) {
		startProxyRuntimeEvidence()
		p.emit("connection_observed", nil)
		go p.watch()
	}
	return p
}

func (p *ProxyEvidence) ID() string {
	if p == nil {
		return ""
	}
	return p.id
}

// Begin observes an application send operation. Slow operations are reported
// both while pending (bounded connection snapshots) and when they complete.
func (p *ProxyEvidence) Begin(trace, phase string) func(error) {
	if p == nil {
		return func(error) {}
	}
	now := time.Now()
	p.mu.Lock()
	p.next++
	key := p.next
	op := proxyOperation{Trace: trace, Phase: phase, StartMS: float64(now.Sub(p.start)) / float64(time.Millisecond), at: now}
	tracked := len(p.ops) < 2048
	if tracked {
		p.ops[key] = op
	} else {
		p.suppressed++
	}
	p.mu.Unlock()
	return func(err error) {
		elapsed := time.Since(now)
		p.mu.Lock()
		if tracked {
			delete(p.ops, key)
		}
		p.mu.Unlock()
		if elapsed >= time.Second {
			p.emit("operation_slow_complete", []zap.Field{zap.String("trace_id", trace), zap.String("phase", phase), zap.Float64("operation_start_ms", op.StartMS), zap.Float64("operation_duration_ms", float64(elapsed)/float64(time.Millisecond)), zap.String("error_class", proxyEvidenceError(err))})
		}
	}
}

func proxyEvidenceError(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, io.EOF):
		return "eof"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, syscall.ECONNRESET):
		return "peer_reset"
	case errors.Is(err, syscall.EPIPE):
		return "broken_pipe"
	case errors.Is(err, net.ErrClosed):
		return "closed"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	return "other"
}

func proxyEvidenceErrno(err error) int {
	var code syscall.Errno
	if errors.As(err, &code) {
		return int(code)
	}
	return 0
}

func (p *ProxyEvidence) Read(b []byte) (int, error) {
	n, err := p.Conn.Read(b)
	if n > 0 {
		p.readBytes.Add(uint64(n))
		p.lastRead.Store(time.Now().UnixNano())
	}
	if err != nil {
		p.mu.Lock()
		if p.readError == nil {
			p.readError = &proxyIOError{proxyEvidenceError(err), float64(time.Since(p.start)) / float64(time.Millisecond), proxyEvidenceErrno(err)}
		}
		p.mu.Unlock()
	}
	return n, err
}

func (p *ProxyEvidence) Write(b []byte) (int, error) {
	now := time.Now()
	p.writeStart.Store(now.UnixNano())
	n, err := p.Conn.Write(b)
	p.writeStart.Store(0)
	if n > 0 {
		p.writeBytes.Add(uint64(n))
		p.lastWrite.Store(time.Now().UnixNano())
	}
	if err != nil {
		p.mu.Lock()
		if p.writeError == nil {
			p.writeError = &proxyIOError{proxyEvidenceError(err), float64(time.Since(p.start)) / float64(time.Millisecond), proxyEvidenceErrno(err)}
		}
		p.mu.Unlock()
	}
	if p.active.Load() && (time.Since(now) >= time.Second || err != nil) {
		p.emit("socket_write_complete", []zap.Field{zap.Float64("operation_start_ms", float64(now.Sub(p.start))/float64(time.Millisecond)), zap.Float64("operation_duration_ms", float64(time.Since(now))/float64(time.Millisecond)), zap.Int("written_bytes", n), zap.String("error_class", proxyEvidenceError(err))})
	}
	return n, err
}

func (p *ProxyEvidence) Close() error {
	p.once.Do(func() {
		p.closed.Store(true)
		close(p.done)
		if p.active.Load() {
			var pcs [16]uintptr
			n := runtime.Callers(2, pcs[:])
			frames := runtime.CallersFrames(pcs[:n])
			stack := make([]string, 0, n)
			for {
				f, more := frames.Next()
				stack = append(stack, f.Function)
				if !more {
					break
				}
			}
			callsite := "unclassified_local_close"
			for _, name := range stack {
				if strings.Contains(name, "handlePingTimer") {
					callsite = "h2_ping_timeout"
					break
				}
				if strings.Contains(name, "wroteFrame") {
					callsite = "h2_frame_write_error"
					break
				}
				if strings.Contains(name, "serverConn).serve") {
					callsite = "h2_serve_exit"
				}
			}
			p.emit("connection_close_called", []zap.Field{zap.Strings("close_callers", stack), zap.String("close_callsite", callsite)})
		}
	})
	return p.Conn.Close()
}

func (p *ProxyEvidence) watch() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	ticks := 0
	for {
		select {
		case <-p.done:
			return
		case snapshot := <-p.h2Events:
			p.emit("h2_lifecycle", []zap.Field{zap.Any("h2_transition", snapshot)})
		case now := <-ticker.C:
			ticks++
			pending := p.writeStart.Load()
			slow := pending > 0 && now.Sub(time.Unix(0, pending)) >= time.Second
			p.mu.Lock()
			for _, op := range p.ops {
				if now.Sub(op.at) >= 2*time.Second {
					slow = true
					break
				}
			}
			p.mu.Unlock()
			if slow || ticks%15 == 0 {
				p.emit("connection_sample", nil)
			}
		}
	}
}

func (p *ProxyEvidence) emit(event string, extra []zap.Field) {
	now := time.Now()
	p.mu.Lock()
	// Bound work/log volume during a mass stream failure. The close record is
	// never rate-limited; suppressed event totals remain visible.
	if p.budgetSecond != now.Unix() {
		p.budgetSecond = now.Unix()
		p.budgetUsed = 0
	}
	if event != "connection_close_called" && p.budgetUsed >= 12 {
		p.suppressed++
		p.mu.Unlock()
		return
	}
	p.budgetUsed++
	slow := make([]proxyOperation, 0, 8)
	counts := map[string]int{}
	for _, op := range p.ops {
		if now.Sub(op.at) >= time.Second {
			counts[op.Phase]++
			if len(slow) < 8 {
				slow = append(slow, op)
			}
		}
	}
	readErr, writeErr := p.readError, p.writeError
	pendingOps := len(p.ops)
	suppressed := p.suppressed + p.h2Dropped.Load()
	p.mu.Unlock()
	fields := []zap.Field{zap.Int("evidence_version", 1), zap.String("event", event), zap.String("connection_id", p.id), zap.Float64("connection_started_unix_ms", float64(p.start.UnixNano())/1e6), zap.Float64("connection_age_ms", float64(now.Sub(p.start))/float64(time.Millisecond)), zap.Bool("close_called", p.closed.Load()), zap.Uint64("socket_read_bytes", p.readBytes.Load()), zap.Uint64("socket_written_bytes", p.writeBytes.Load()), zap.Int("pending_operations", pendingOps), zap.Any("slow_operation_counts", counts), zap.Any("slow_operation_examples", slow), zap.Any("first_read_error", readErr), zap.Any("first_write_error", writeErr), zap.Uint64("suppressed_events", suppressed), zap.String("h2_flow_window", "observed_when_h2_snapshot_present")}
	if start := p.writeStart.Load(); start > 0 {
		fields = append(fields, zap.Float64("socket_write_pending_ms", float64(now.Sub(time.Unix(0, start)))/float64(time.Millisecond)))
	}
	for _, v := range []struct {
		name  string
		stamp int64
	}{{"last_socket_read_age_ms", p.lastRead.Load()}, {"last_socket_write_age_ms", p.lastWrite.Load()}} {
		if v.stamp > 0 {
			fields = append(fields, zap.Float64(v.name, float64(now.Sub(time.Unix(0, v.stamp)))/float64(time.Millisecond)))
		}
	}
	if snapshot := p.h2.Load(); snapshot != nil {
		fields = append(fields, zap.Any("h2", snapshot), zap.Int64("h2_snapshot_age_ms", now.UnixMilli()-snapshot.UnixMS))
	}
	if state := proxyRuntimeLatest.Load(); state != nil {
		fields = append(fields, zap.Any("go_runtime", *state))
	}
	fields = append(fields, zap.Any("tcp", proxyTCPSnapshot(p.Conn)))
	p.logger.Info("proxy transport evidence", append(fields, extra...)...)
}

// proxyObserveH2 never logs on the HTTP2 serve goroutine. Snapshot ownership is
// transferred; all fields are copied scalars and a bounded immutable slice.
func proxyObserveH2(ctx context.Context, snapshot http2.ServerObservation) {
	p, _ := ctx.Value(proxyEvidenceKey{}).(*ProxyEvidence)
	if p == nil || !p.active.Load() {
		return
	}
	p.h2.Store(&snapshot)
	if snapshot.Event != "sample" {
		select {
		case p.h2Events <- snapshot:
		default:
			p.h2Dropped.Add(1)
		}
	}
}

// Stage records bounded metadata from a DNS/dial operation, never an address.
func (p *ProxyEvidence) Stage(trace, phase string, elapsed time.Duration, err error, fields ...zap.Field) {
	if p == nil {
		return
	}
	extra := []zap.Field{zap.String("trace_id", trace), zap.String("phase", phase), zap.Float64("stage_duration_ms", float64(elapsed)/float64(time.Millisecond)), zap.String("error_class", proxyEvidenceError(err)), zap.Int("errno", proxyEvidenceErrno(err))}
	p.emit("proxy_stage_complete", append(extra, fields...))
}

func proxyH2ObservationEnabled(ctx context.Context) bool {
	p, _ := ctx.Value(proxyEvidenceKey{}).(*ProxyEvidence)
	return p != nil && p.active.Load()
}
