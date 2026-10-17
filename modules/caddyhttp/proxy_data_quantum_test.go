package caddyhttp

import "testing"

func TestProxyDataQuantumConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, latency, quantum string
		wantFactory, wantError bool
	}{
		{"default", "", "", false, false},
		{"explicit_zero_default", "", "0", false, false},
		{"current_proxy", "1", "", true, false},
		{"current_proxy_zero", "1", "0", true, false},
		{"bounded_proxy", "1", "16384", true, false},
		{"requires_proxy", "", "16384", false, true},
		{"disabled_proxy", "0", "16384", false, true},
		{"other_quantum", "1", "32768", false, true},
		{"negative", "1", "-1", false, true},
		{"invalid", "1", "bad", false, true},
		{"noncanonical", "1", "16384 ", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PCS_PROXY_LATENCY", tc.latency)
			t.Setenv("PCS_PROXY_DATA_QUANTUM", tc.quantum)
			factory, err := proxyWriteSchedulerFromEnv(tc.latency, tc.quantum)
			if (err != nil) != tc.wantError || (factory != nil) != tc.wantFactory {
				t.Fatalf("factory=%v err=%v", factory != nil, err)
			}
			if factory != nil {
				first, second := factory(), factory()
				if first == nil || second == nil || first == second {
					t.Fatal("each connection requires a distinct scheduler")
				}
			}
			// The real app's configuration validation must reject invalid knobs
			// before any listener starts, rather than silently falling back.
			if err := new(App).Validate(); (err != nil) != tc.wantError {
				t.Fatalf("app validation error=%v", err)
			}
		})
	}
}
