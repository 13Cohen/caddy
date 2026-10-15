//go:build !linux

package caddyhttp

import "net"

func proxyTCPSnapshot(net.Conn) map[string]any { return proxyTCPUnavailable("unsupported_platform") }
