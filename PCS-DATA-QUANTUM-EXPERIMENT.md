# Optional fixed DATA quantum integration

**HOLD: experimental source, not approved for production.** Based on `deb6904f5976647b233ea0842d7f4883cbabc679`; not deployed. It requires the independently reviewed net experiment based on `ca43f763` which exports `NewProxyWriteSchedulerWithDataQuantum`. No dependency version or replace directive is changed by this source worktree; the root-coordinated build must resolve the exact accepted net source and record its hash.

The environment is validated in both App.Validate and App.Start:

| PCS_PROXY_LATENCY | PCS_PROXY_DATA_QUANTUM | Policy |
| --- | --- | --- |
| unset / not 1 | unset / 0 | Existing default scheduler selection |
| 1 | unset / 0 | Existing fair proxy policy, original DATA turn |
| 1 | 16384 | Same fair proxy policy, 16384-byte maximum DATA turn |
| not 1 | 16384 | Configuration error |
| any | any other value | Configuration error |

The feature defaults off. A/B runs keep PCS_PROXY_LATENCY=1 in both variants and vary only the quantum. Invalid inputs fail clearly rather than silently selecting a different scheduler. Every outer connection gets a fresh scheduler. Existing HTTP2 concurrency, PING/deadline settings, observation, TLS, authentication, ACL and protocol configuration remain unchanged.

The integration test covers the actual App.Validate path, factory presence and per-connection isolation. Enabled behavior, fragments, flow accounting and lifecycle are covered in the independent net tests; full tagged runtime and client-receipt acceptance must use the exact integrated binary. Passing forwardproxy tests that manually configure a separate HTTP2 server does not itself exercise this App environment switch.

A 16 KiB turn cannot preempt a blocked TLS/TCP Write. The experiment's main outcome is complete payload arrival at the client, with bulk delivery and CPU per decimal MB as guardrails. The frozen pre-execution CPU rule fails only if both the relative increase exceeds 20% and the absolute increase exceeds 0.01 CPU-seconds per client-received decimal MB; this rule was not changed after measurement. The separate net document `docs/pcs-data-quantum-20261017.md` details hypotheses, cancellation boundaries and the conditional TLS record-alignment follow-up. No adaptive quantum or 16375-byte option is part of this revision.

## Completed v1 acceptance and remaining work

The full v1 matrix completed 28 runs across six profiles with exact payload verification. The main eight-run comparison used the median of four per-run nearest-rank small-response P95 values per quantum: 7.511113 seconds at q0 versus 5.659346 seconds at q16K, a 24.65% reduction. This misses the predeclared 30% minimum improvement even though the absolute reduction was 1.851767 seconds and three of four paired directions improved. Main bulk goodput was 0.034110 versus 0.033861 decimal MB/s; CPU was 0.082256 versus 0.089579 CPU-seconds per client-received MB. These are isolated shaped-network measurements, not production-user speed. **The candidate remains HOLD and is not deployed.**

The verified-TLS, explicitly quantum-enabled single-outer lifecycle test passed normally (0.443 seconds) and under race (2.695 seconds): an incomplete large stream is canceled, a concurrent and subsequent exact small response both complete on the same outer connection, and outer close releases another pending large handler. Full HTTP2/HPACK, targeted scheduler race, Caddy proxy tests and corrected-environment forwardproxy off/on tests also passed. The initial forwardproxy ACL failure caused by missing offline host fixtures is preserved; it was not a production-source defect or a silently discarded run.

A first-wave set-level analysis still showed 3.228–3.999 seconds (roughly 3.5 seconds) after all small-response Flush calls returned before the earliest client completion. Fresh H2 samples no longer contained those pending small requests, but TCP queues remained and ACKs advanced. This is not a per-stream causal join and does not establish packet-loss origin. The prepared v2 2×2 plan crosses q0/q16384 with isolated-namespace notsent-lowat 65536/16384 and adds exact synthetic lane correlation. **Its benefit is not yet proved.** It does not change this branch's default or production settings.

## Tested runtime identity

The actual v1 binary SHA256 is `ee3b8c4c8baad0ce3658ca9187f00e0039d810a8be8898f4796f284036555781`. Runtime Go files in this review commit exactly match the recorded build-source manifest:

| File | SHA256 |
| --- | --- |
| `modules/caddyhttp/app.go` | `6dddba96067f37ac02491707a2a5cc65130fdff04d6fe80e714c399c8e221ded` |
| `modules/caddyhttp/proxy_data_quantum.go` | `081962880d24bdff3dd0c13a8286e563cbb3b5eb0adcfa3e38bfefe10d24a617` |
| Companion net `http2/writesched_roundrobin.go` | `af1a17d431827eb8eedf6f26edc98813a42591e0cd99c7366a99c94422a51f0f` |

The companion net review commit is [`fb6fa2d0c533aeb7eb7d61b40372d8b547c1ac87`](https://github.com/justinchen13/net/commit/fb6fa2d0c533aeb7eb7d61b40372d8b547c1ac87); this is a source reference, not a dependency-pin or production update.

The root retains `loop-20261017T1753Z/candidate-source-sha256.json`, `functional-validation.json`, all 28 v1 run records and the TLS lifecycle logs. This commit only publishes the experiment source, tests and review documentation. It does not update dependency pins, PCS deployment manifests, production images or the default branch. No tests or load experiments were rerun for this documentation/publish step.
