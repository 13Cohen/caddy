package caddyhttp

import (
	"runtime/metrics"
	"sync"
	"sync/atomic"
	"time"
)

var proxyRuntimeOnce sync.Once
var proxyRuntimeLatest atomic.Pointer[map[string]any]

func startProxyRuntimeEvidence() {
	proxyRuntimeOnce.Do(func() {
		go func() {
			names := []string{"/sched/goroutines:goroutines", "/gc/heap/live:bytes", "/gc/heap/goal:bytes", "/gc/cycles/total:gc-cycles", "/cpu/classes/gc/total:cpu-seconds", "/cpu/classes/gc/mark/assist:cpu-seconds", "/sched/latencies:seconds", "/gc/pauses:seconds"}
			samples := make([]metrics.Sample, len(names))
			for i, n := range names {
				samples[i].Name = n
			}
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for {
				metrics.Read(samples)
				snapshot := map[string]any{"unix_ms": time.Now().UnixMilli(), "scope": "process", "histogram_counters": "cumulative"}
				for _, s := range samples {
					switch s.Value.Kind() {
					case metrics.KindUint64:
						snapshot[s.Name] = s.Value.Uint64()
					case metrics.KindFloat64:
						snapshot[s.Name] = s.Value.Float64()
					case metrics.KindFloat64Histogram:
						h := s.Value.Float64Histogram()
						counts := make([]uint64, 5)
						// Fixed upper-bound latency bins: <=1ms, <=10ms, <=100ms, <=1s, >1s.
						// Runtime's bucket boundary may straddle these limits; classify by upper bound.
						for i, n := range h.Counts {
							upper := h.Buckets[i+1]
							bin := 4
							for j, b := range []float64{.001, .01, .1, 1} {
								if upper <= b {
									bin = j
									break
								}
							}
							counts[bin] += n
						}
						snapshot[s.Name] = counts
					}
				}
				proxyRuntimeLatest.Store(&snapshot)
				<-ticker.C
			}
		}()
	})
}
