//go:build linux

package caddyhttp

import (
	"net"
	"reflect"
	"testing"
	"time"
)

func TestProxyEvidenceTCPReceiveQueueAndCache(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	client, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	p, _ := testEvidence(server)
	defer p.Close()
	before := proxyTCPSnapshot(server)
	if before["bytes_received"] == nil {
		t.Fatal("supported kernel must expose bytes_received")
	}
	if _, err := client.Write(make([]byte, 64)); err != nil {
		t.Fatal(err)
	}
	var queued map[string]any
	deadline := time.Now().Add(time.Second)
	for {
		queued = proxyTCPSnapshot(server)
		if queued["receive_queue_bytes"] == 64 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queued bytes not observed: %+v", queued)
		}
		time.Sleep(time.Millisecond)
	}
	if queued["bytes_received"].(uint64)-before["bytes_received"].(uint64) != 64 || p.reads.snapshot()["in_flight"] != 0 {
		t.Fatal("kernel received data must be distinguishable from an entered Read")
	}
	cached, _ := p.tcpSample(false)
	if n, err := p.Read(make([]byte, 16)); n != 16 || err != nil {
		t.Fatal(n, err)
	}
	again, age := p.tcpSample(false)
	if !reflect.DeepEqual(cached, again) || age < 0 || again["sample_sequence"] != uint64(1) {
		t.Fatal("cache lost its original sample/time")
	}
	// Expire the cache without a wall-clock sleep or extra sampling service.
	p.tcpMu.Lock()
	p.tcpCaptured = p.tcpCaptured.Add(-time.Second)
	p.tcpMu.Unlock()
	fresh, _ := p.tcpSample(false)
	if fresh["receive_queue_bytes"] != 48 || fresh["bytes_received"] != queued["bytes_received"] || fresh["sample_sequence"] != uint64(2) {
		t.Fatalf("read-vs-kernel distinction lost: %+v", fresh)
	}
	p.closed.Store(true)
	final, _ := p.tcpSample(true)
	if final["available"] != true || final["sample_sequence"] != uint64(3) {
		t.Fatal("final sample must precede underlying close")
	}
	server.Close()
	closed, _ := p.tcpSample(false)
	if closed["available"] != false || closed["receive_queue_bytes"] != nil || closed["bytes_received"] != nil {
		t.Fatal("closed wrapper reused a valid-looking cached socket sample")
	}
}

func TestProxyEvidenceTCPUnavailableIsUnknown(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	o := proxyTCPSnapshot(a)
	if o["available"] != false || o["bytes_received"] != nil || o["receive_queue_bytes"] != nil {
		t.Fatal("unsupported socket fabricated zero receive counters")
	}
}
