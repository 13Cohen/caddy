# Reliability fork

Maintained at https://github.com/13Cohen/caddy/tree/pcs/reliability
Upstream: https://github.com/caddyserver/caddy
Base commit: `6610e2f1bd8f54853006eefd3849c9965190e57f`

This branch preserves upstream Git history and license. The repository's original
default branch is retained for upstream tracking. PCS owns deployment and pins
this independent repository by full commit and archive SHA256; source development
does not belong in PCS. The former temporary PCS source branch is retired after
migration. Runtime secrets, real client IPs and certificates are never committed.

Upstream workflow files were removed from the initial experimental branch; this
branch does not publish upstream release artifacts. CI/deployment is controlled
by PCS. Contribution branches must start from current upstream, carry only the
relevant fix and regression test, and preserve the upstream workflow structure.

Integration versions: Caddy 2.11.1, Go 1.25.6, x/net 0.50.0.
Refer to PCS `caddy/OPERATIONS.md` for experiments, rollout and limitations.

Changes: retain PCS HTTP/2 limits and liveness controls (512 streams, 30 second
read idle PING, 15 second PONG deadline, 60 second stalled socket write limit).
The pinned Go/x/net combination already schedules streams round-robin; no new
cryptographic or frame scheduling implementation is introduced.


Transport evidence (pcsproxy3): `PCS_PROXY_EVIDENCE=1` wraps TCP below TLS.
Only an authenticated forwardproxy `diagnostics` observation enables per-connection
logging. `EnableProxyEvidence(ctx)` exposes an anonymous random connection ID and
`Begin(trace, phase)` for bounded slow-operation correlation. The wrapper preserves
TLS negotiation, deadlines and byte streams. A watchdog logs TCP_INFO every 15s,
and every second during a slow send; Linux socket snapshots are read-only.
Initial response/data send operations and raw socket writes share the connection
ID. Close records retain first read/write error class/errno/time and function names
(no stack arguments, addresses, headers, credentials or payloads). Close callsite
is a local path, not a claim about who physically caused a disconnect. An abnormal
server exit can prevent a close record; bounded logs are not an audit guarantee.

Normal operation is not logged per chunk. Per-connection output is limited to
12 events/s with a suppression counter; close records bypass that limit. Pending
operations are bounded at 2048, sampled slow examples at 8. x/net CountError is a
separate server-scoped category event (no connection ID), capped at 4/s. Exact
HTTP/2 flow windows are deliberately marked unobserved: a pending application
Flush without a pending socket Write is insufficient to prove a flow-control bug.
Kernel TCP snd_wnd is not an HTTP/2 window. TCP counters require per-connection
deltas; zero windows and retransmissions need contemporaneous correlation.

Tests: TestProxyEvidence exercises real TLS/HTTP2 reuse and TCP_INFO, no payload
logging, pending socket write / timeout / close, and rate-limit close retention.
The application listener installs this wrapper before tls.NewListener so Go and
x/net still see the expected *tls.Conn; it does not replace HTTP/2 or TLS code.

## pcsproxy4 candidate

Paired with an independent golang/net v0.50.0 fork: Observe/ObserveEnabled transfer
exact H2 metadata snapshots without logging on the serve goroutine. Auth activation
is required; normal snapshots are atomic latest values, lifecycle transitions use
an eight-entry nonblocking queue with explicit drops. Existing per-connection log
budget/rotation remain. The oldest eight pending streams and current frame stream
ID join schema-3 forwardproxy trace records. Every record includes snapshot age;
a delayed serve loop must not masquerade as a current zero window.

Process-wide Go runtime samples every 15s include heap/goroutines/GC cycles and
CPU counters, scheduler/GC pause cumulative histogram counts (upper-bound bins
1ms/10ms/100ms/1s/>1s). These repeat across connection records; never sum them as
per-connection resource consumption. No public debug endpoint is added.

PING reply timing starts after successful flush, with the existing 60s no-progress
write deadline preserved. The headers-first scheduler is separately controlled by
PCS_PROXY_LATENCY=1 and remains an experiment until whole-path validation passes.
Default environment leaves the scheduler unchanged.
