# PCS source fork

Upstream: https://github.com/caddyserver/caddy
Base commit: `6610e2f1bd8f54853006eefd3849c9965190e57f`

This source fork preserves upstream Git history and license. It is hosted on the
`source/caddy-reliability` branch of the private `13Cohen/PCS` repository because
the connected GitHub interface cannot create a separate fork repository. The
branch can be pushed into a standalone repository later without losing history.
Upstream workflow files are removed from this source branch; PCS owns deployment.

The production build uses the pinned upstream module plus an audited patch in
PCS `caddy/`, so Docker builds need no credentials for private Git repositories.
Runtime secrets, real client IPs and certificates are never stored in this fork.

Integration versions: Caddy 2.11.1, Go 1.25.6, x/net 0.50.0.
Refer to PCS `caddy/OPERATIONS.md` for experiments, rollout and limitations.

Changes: retain PCS HTTP/2 limits and liveness controls (512 streams, 30 second
read idle PING, 15 second PONG deadline, 60 second stalled socket write limit).
The pinned Go/x/net combination already schedules streams round-robin; no new
cryptographic or frame scheduling implementation is introduced.
