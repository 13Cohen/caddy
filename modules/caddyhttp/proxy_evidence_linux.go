//go:build linux

package caddyhttp

import (
	"net"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

func proxyTCPSnapshot(conn net.Conn) map[string]any {
	started := time.Now()
	out := proxyTCPUnavailable("not_syscall_conn")
	defer func() {
		out["sample_started_unix_ms"] = started.UnixMilli()
		out["sample_completed_unix_ms"] = time.Now().UnixMilli()
		out["sample_duration_us"] = time.Since(started).Microseconds()
	}()
	c, ok := conn.(syscall.Conn)
	if !ok {
		return out
	}
	raw, err := c.SyscallConn()
	if err != nil {
		out["unavailable_reason"] = "syscall_conn_unavailable"
		return out
	}
	err = raw.Control(func(fd uintptr) {
		info, size, infoErr := proxyTCPInfo(int(fd))
		out["tcp_info_length"] = size
		if infoErr == nil {
			out["available"] = true
			out["unavailable_reason"] = nil
			if size >= uint32(unsafe.Offsetof(info.Bytes_received)+unsafe.Sizeof(info.Bytes_received)) {
				out["bytes_received"] = info.Bytes_received
			}
			out["state"] = info.State
			out["rtt_us"] = info.Rtt
			out["rttvar_us"] = info.Rttvar
			out["snd_cwnd"] = info.Snd_cwnd
			out["unacked_segments"] = info.Unacked
			out["lost_segments"] = info.Lost
			out["total_retrans_segments"] = info.Total_retrans
			out["bytes_retrans"] = info.Bytes_retrans
			out["bytes_sent"] = info.Bytes_sent
			out["bytes_acked"] = info.Bytes_acked
			out["notsent_bytes"] = info.Notsent_bytes
			out["snd_wnd_bytes"] = info.Snd_wnd
			out["rwnd_limited_us"] = info.Rwnd_limited
			out["sndbuf_limited_us"] = info.Sndbuf_limited
		} else {
			out["unavailable_reason"] = "tcp_info_unavailable"
		}
		if q, err := unix.IoctlGetInt(int(fd), unix.TIOCOUTQ); err == nil {
			out["send_queue_bytes"] = q
		}
		// TIOCINQ is Linux FIONREAD: unread raw TCP bytes, below TLS.
		if q, err := unix.IoctlGetInt(int(fd), unix.TIOCINQ); err == nil {
			out["receive_queue_bytes"] = q
		}
	})
	if err != nil {
		out = proxyTCPUnavailable("raw_control_unavailable")
	}
	return out
}

// Preserve the kernel-returned structure length: an older kernel omitting a
// tail field must report unknown, not a fabricated zero counter.
func proxyTCPInfo(fd int) (*unix.TCPInfo, uint32, error) {
	var info unix.TCPInfo
	size := uint32(unsafe.Sizeof(info))
	_, _, errno := unix.Syscall6(unix.SYS_GETSOCKOPT, uintptr(fd), unix.IPPROTO_TCP,
		unix.TCP_INFO, uintptr(unsafe.Pointer(&info)), uintptr(unsafe.Pointer(&size)), 0)
	if errno != 0 {
		return nil, size, errno
	}
	return &info, size, nil
}
