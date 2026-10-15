# Receive-side evidence candidate

Base: Caddy `5197d3d35c058f353af467a5b46ec8f6ea0581d6`, with the matching
`13Cohen/net` receive-evidence branch and unchanged forwardproxy
`b1e4e257f5f3ca9774cb10744f86684e532f0d2a`.

This adds observation, not a stall fix. The motivating production incident had
connection send credit exhausted without new WINDOW_UPDATE frames. Existing
logs could not distinguish no raw bytes reaching TCP from bytes arriving but
waiting below TLS/HTTP/2 processing. No peer fault is inferred.

## Activation and bounds

The existing `PCS_PROXY_EVIDENCE=1` switch enables the wrapper and HTTP/2 hooks.
It remains disabled by default. Reader metadata may be registered and updated
before proxy authentication, but does not activate logging; existing successful
proxy authentication remains required. There are no payloads, destinations,
accounts or additional network identifiers in these fields.

No read/frame emits a log. Raw Read uses short scalar-only mutex sections before
and after the underlying call; no lock covers I/O. HTTP/2 reader phase tracking
uses atomic scalars, without per-frame allocations or serve-loop callbacks.
Existing event rate limits remain. TCP_INFO and queue ioctls are cached for one
second per connection, with one final pre-close refresh. This also bounds syscall
cost when multiple existing slow-completion events arrive together.

## Fields and time semantics

- `tcp.bytes_received`: cumulative kernel TCP byte counter; null if unsupported
  or the returned TCP_INFO structure is too short. Delta only within the same
  connection and increasing unique sample sequence. It is not application data.
- `tcp.receive_queue_bytes`: Linux TIOCINQ/FIONREAD unread bytes in the raw TCP
  socket below TLS. It excludes bytes already buffered in TLS or the HTTP/2
  framer. For userspace TLS these queued bytes are ciphertext. Linux 6.1's
  [tcp_ioctl](https://github.com/torvalds/linux/blob/v6.1/net/ipv4/tcp.c) routes
  SIOCINQ to tcp_inq; Caddy's evidence wrapper is outside the TLS connection.
- `tcp.sample_sequence`: per-wrapper number, incremented only for an actual
  refresh. Cached events retain it. The final refresh gets a new sequence even
  when two samples share the same millisecond. Unknown closed-socket snapshots
  use null and never reuse an old valid-looking sample.
- `tcp.sample_started_unix_ms`, `sample_completed_unix_ms`, `sample_duration_us`
  describe this syscall group, not the enclosing event. TCP_INFO and queue
  readings are sequential best-effort observations of the same protected FD.
  `tcp_snapshot_age_ms` is monotonic cache age. The final sample is taken before
  the underlying close. RawConn.Control guards FD lifetime against reuse.
- `socket_read`: independently sampled raw Read entry/completion counts,
  in-flight count, pending age, and last return age/bytes/error class. Overlapping
  Reads make the pending age unknown until all overlap drains; null is not zero.
  Counters may include the TLS handshake; they are per-wrapper lifetime values.
- `h2_reader`: independent snapshot with `unix_ms`, `phase`, `phase_age_ms`,
  `frames_decoded`, `best_effort`. It does not reuse the serve-loop snapshot's
  timestamp. Unknown/unregistered readers are null. Frames are cumulative.

Reader phases are `starting`, `reading_frame`, `handoff`, `await_serve`, and
`stopped`. A reading_frame wait includes possible TCP, TLS decryption/buffering,
or an incomplete HTTP/2 frame. Handoff and await_serve can include scheduling.
Phase and start offset share one atomic value; frame count is independently
sampled. None of these states alone establishes a root cause. These samples
are correlated by the existing connection ID; no new socket identifier is added.

## Validation

The deterministic tests cover unread kernel bytes versus no entered Read,
partial frame input, a reader blocked on handoff without a running serve loop,
a reader waiting for processing acknowledgement, overlapping Reads, cache
sequence/time preservation, final close sampling, unsupported sockets, and
pre-authentication silence. Existing PING and evidence/privacy tests also run.

The performance acceptance comparison is old enabled observer versus new enabled
observer, with default-off checked separately. Thresholds: throughput regression
at most 5%, CPU per effective byte increase at most 20%, mixed small-request P95
increase at most 20%. Quota throttling or unstable measurements cannot establish
a pass. This document will be supplemented with exact validation results.

Current candidate validation (2026-10-15 UTC): focused net tests (6 top-level)
and Caddy evidence tests (9 top-level) passed during 17:32:40–17:35:29.
This includes actual TLS/H2 hook registration and privacy behavior. The segment
then stopped during binary build at its shared-host scheduling boundary. The
earlier 17:23:47–17:26:40 race segment was only cold compilation; it does not
establish a race pass. Race, full protocol regression and A/B performance are
release gates still pending at this source commit.
