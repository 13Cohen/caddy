package caddyhttp

import (
	"fmt"

	"golang.org/x/net/http2"
)

// The DATA experiment is intentionally available only with the existing proxy
// policy selected. A/B runs keep PCS_PROXY_LATENCY=1 and change only quantum.
// Empty/zero preserves the previous scheduler selection; unsupported values
// fail validation rather than silently selecting a different experiment.
func proxyWriteSchedulerFromEnv(latency, quantum string) (func() http2.WriteScheduler, error) {
	switch quantum {
	case "", "0":
		if latency == "1" {
			return http2.NewProxyWriteScheduler, nil
		}
		return nil, nil
	case "16384":
		if latency != "1" {
			return nil, fmt.Errorf("PCS_PROXY_DATA_QUANTUM=16384 requires PCS_PROXY_LATENCY=1")
		}
		return func() http2.WriteScheduler {
			// The constant was validated above; the constructor cannot fail.
			ws, _ := http2.NewProxyWriteSchedulerWithDataQuantum(16384)
			return ws
		}, nil
	default:
		return nil, fmt.Errorf("PCS_PROXY_DATA_QUANTUM must be empty, 0 or 16384")
	}
}
