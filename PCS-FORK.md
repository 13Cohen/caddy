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
