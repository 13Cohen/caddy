package caddyhttp

import (
	"context"
	"golang.org/x/net/http2"
	"io"
	"net"
	"testing"
)

type receiveGateConn struct {
	net.Conn
	entered chan struct{}
	release chan struct{}
}

func (c *receiveGateConn) Read(p []byte) (int, error) {
	c.entered <- struct{}{}
	<-c.release
	return 0, io.EOF
}

func TestProxyEvidenceReadPendingConcurrentUnknown(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	c := &receiveGateConn{Conn: a, entered: make(chan struct{}), release: make(chan struct{})}
	p, logs := testEvidence(c)
	defer p.Close()
	done := make(chan struct{}, 2)
	go func() { p.Read(make([]byte, 1)); done <- struct{}{} }()
	<-c.entered
	one := p.reads.snapshot()
	if one["in_flight"] != 1 || one["pending_age_ms"] == nil || one["calls_entered"] != uint64(1) {
		t.Fatalf("single pending read not observed: %+v", one)
	}
	go func() { p.Read(make([]byte, 1)); done <- struct{}{} }()
	<-c.entered
	two := p.reads.snapshot()
	if two["in_flight"] != 2 || two["pending_age_ms"] != nil || two["pending_age_ambiguous"] != true {
		t.Fatalf("overlapping read age must be unknown: %+v", two)
	}
	c.release <- struct{}{}
	<-done
	if p.reads.snapshot()["pending_age_ms"] != nil {
		t.Fatal("remaining overlap retained an unprovable oldest age")
	}
	c.release <- struct{}{}
	<-done
	end := p.reads.snapshot()
	if end["in_flight"] != 0 || end["calls_completed"] != uint64(2) || end["last_return_error_class"] != "eof" || end["pending_age_ms"] != nil {
		t.Fatalf("completion evidence changed: %+v", end)
	}
	if logs.Len() != 0 {
		t.Fatal("read tracking logged before authentication")
	}
}

func TestProxyEvidenceReadStateRegistrationDoesNotAuthenticate(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	p, logs := testEvidence(a)
	defer p.Close()
	ctx := context.WithValue(context.Background(), proxyEvidenceKey{}, p)
	state := new(http2.ServerReadState)
	proxyObserveH2ReadState(ctx, state)
	if p.h2Reader.Load() != state || p.active.Load() || logs.Len() != 0 {
		t.Fatal("reader registration authenticated or emitted data")
	}
	proxyObserveH2ReadState(ctx, new(http2.ServerReadState))
	if p.h2Reader.Load() != state {
		t.Fatal("reader handle changed after registration")
	}
	proxyObserveH2ReadState(context.Background(), state)
}
