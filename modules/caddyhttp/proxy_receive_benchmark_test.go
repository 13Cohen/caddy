package caddyhttp

import (
	"net"
	"testing"
)

type receiveImmediateConn struct{ net.Conn }

func (receiveImmediateConn) Read(p []byte) (int, error) { return len(p), nil }

// Run this same benchmark file against baseline and candidate wrappers. It
// measures instrumentation cost with no I/O, so it is not a throughput claim.
func BenchmarkProxyEvidenceRead(b *testing.B) {
	p, _ := testEvidence(receiveImmediateConn{})
	buf := make([]byte, 16<<10)
	b.ReportAllocs()
	b.SetBytes(int64(len(buf)))
	for i := 0; i < b.N; i++ {
		p.Read(buf)
	}
}
