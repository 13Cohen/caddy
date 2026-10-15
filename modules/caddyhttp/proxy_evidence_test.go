package caddyhttp

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"golang.org/x/net/http2"
)

func testEvidence(c net.Conn) (*ProxyEvidence, *observer.ObservedLogs) {
	core, logs := observer.New(zap.InfoLevel)
	return &ProxyEvidence{Conn: c, id: "anonymous-test", start: time.Now(), logger: zap.New(core), done: make(chan struct{}), ops: make(map[uint64]proxyOperation)}, logs
}

func TestProxyEvidenceTLSHTTP2CorrelationAndPrivacy(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := EnableProxyEvidence(r.Context())
		if p == nil {
			t.Error("connection metadata lost through TLS")
			return
		}
		if p.h2Reader.Load() == nil {
			t.Error("independent reader handle missing through TLS context")
		}
		if r.ProtoMajor != 2 {
			t.Error("test did not use h2")
		}
		w.Header().Set("Test-Connection-ID", p.ID())
		w.Write([]byte("payload-must-not-be-logged"))
	}))
	s.Listener = &proxyEvidenceListener{Listener: s.Listener, logger: zap.New(core)}
	s.Config.ConnContext = withProxyEvidence
	if err := http2.ConfigureServer(s.Config, &http2.Server{ObserveReadState: proxyObserveH2ReadState}); err != nil {
		t.Fatal(err)
	}
	s.EnableHTTP2 = true
	s.StartTLS()
	defer s.Close()
	tr := &http2.Transport{TLSClientConfig: s.Client().Transport.(*http.Transport).TLSClientConfig}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	ids := []string{}
	for i := 0; i < 2; i++ {
		r, err := client.Get(s.URL)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		ids = append(ids, r.Header.Get("Test-Connection-ID"))
	}
	if ids[0] == "" || ids[0] != ids[1] {
		t.Fatal("requests not linked to one connection", ids)
	}
	entries := logs.FilterMessage("proxy transport evidence").All()
	if len(entries) != 1 {
		t.Fatalf("expected activation once, got %d", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["h2_reader"] == nil || fields["socket_read"] == nil {
		t.Fatal("receive fields absent from authenticated event")
	}
	if fields["connection_id"] != ids[0] {
		t.Fatal("connection ID mismatch")
	}
	if tcp := fields["tcp"].(map[string]any); tcp["available"] != true {
		t.Fatal("real TCP snapshot unavailable", tcp)
	}
	b, _ := json.Marshal(fields)
	for _, secret := range []string{"payload-must-not-be-logged", "127.0.0.1", "Authorization"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("leaked %s", secret)
		}
	}
	tr.CloseIdleConnections()
}

func TestProxyEvidenceCapturesPendingSocketWriteAndTimeout(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	p, logs := testEvidence(a)
	defer p.Close()
	ctx := context.WithValue(context.Background(), proxyEvidenceKey{}, p)
	if logs.Len() != 0 {
		t.Fatal("logged before authentication")
	}
	EnableProxyEvidence(ctx)
	done := p.Begin("trace-test", "response_flush")
	p.SetWriteDeadline(time.Now().Add(2400 * time.Millisecond))
	_, err := p.Write([]byte("not-logged"))
	done(err)
	if proxyEvidenceError(err) != "timeout" {
		t.Fatal(err)
	}
	p.Close()
	var pending, timeout, closed bool
	for _, entry := range logs.All() {
		f := entry.ContextMap()
		if f["event"] == "connection_sample" && f["socket_write_pending_ms"] != nil {
			pending = true
		}
		if f["event"] == "socket_write_complete" && f["error_class"] == "timeout" {
			timeout = true
		}
		if f["event"] == "connection_close_called" {
			closed = true
			if f["first_write_error"] == nil {
				t.Fatal("close lost initiating error")
			}
		}
	}
	if !pending || !timeout || !closed {
		t.Fatalf("missing evidence pending=%v timeout=%v closed=%v", pending, timeout, closed)
	}
}

func TestProxyEvidenceRateLimitRetainsClose(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	p, logs := testEvidence(a)
	p.active.Store(true)
	for i := 0; i < 100; i++ {
		p.emit("operation_slow_complete", nil)
	}
	p.Close()
	last := logs.All()[logs.Len()-1].ContextMap()
	if last["event"] != "connection_close_called" || last["suppressed_events"].(uint64) == 0 {
		t.Fatal("close/suppression missing", last)
	}
}

func TestProxyEvidenceErrorClassification(t *testing.T) {
	for err, want := range map[error]string{io.EOF: "eof", syscall.ECONNRESET: "peer_reset", syscall.EPIPE: "broken_pipe", net.ErrClosed: "closed"} {
		if got := proxyEvidenceError(err); got != want {
			t.Fatalf("%v: %s", err, got)
		}
	}
}

func TestProxyEvidenceH2ObserverNonblockingAndAuthentication(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	p, _ := testEvidence(a)
	defer p.Close()
	p.h2Events = make(chan http2.ServerObservation, 1)
	ctx := context.WithValue(context.Background(), proxyEvidenceKey{}, p)
	proxyObserveH2(ctx, http2.ServerObservation{Event: "ping_queued"})
	if p.h2.Load() != nil {
		t.Fatal("observed unauthenticated connection")
	}
	p.active.Store(true)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			proxyObserveH2(ctx, http2.ServerObservation{Event: "ping_ack", Sequence: uint64(i)})
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("observer blocked serve loop")
	}
	if p.h2.Load().Sequence != 999 || p.h2Dropped.Load() != 999 {
		t.Fatal("latest snapshot or suppression lost")
	}
}
