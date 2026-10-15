//go:build linux

package caddyhttp

import (
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

func proxyTCPSnapshot(conn net.Conn) map[string]any {
	out := map[string]any{"available": false}
	c, ok := conn.(syscall.Conn)
	if !ok {
		return out
	}
	raw, err := c.SyscallConn()
	if err != nil {
		return out
	}
	err = raw.Control(func(fd uintptr) {
		info, err := unix.GetsockoptTCPInfo(int(fd), unix.IPPROTO_TCP, unix.TCP_INFO)
		if err != nil {
			return
		}
		out["available"] = true
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
		if q, err := unix.IoctlGetInt(int(fd), unix.TIOCOUTQ); err == nil {
			out["send_queue_bytes"] = q
		}
	})
	if err != nil {
		out["available"] = false
	}
	return out
}
