# orchestrator — pool of exit nodes for access keys

Turns Plan 1's static call pool into live Docker-managed exits: `accessd`
launches an exit per issued key, keeps a warm set pre-joined to calls, stops an
exit when its key is released, reconciles orphans on restart, and (opt-in)
migrates a key's call in-session when it degrades.

Spec: [`docs/superpowers/specs/2026-10-16-access-key-service-design.md`](../docs/superpowers/specs/2026-10-16-access-key-service-design.md).
Plan: [`docs/superpowers/plans/2026-10-17-pool-orchestrator.md`](../docs/superpowers/plans/2026-10-17-pool-orchestrator.md).
This is subsystem 2 of 4; it builds on the `access` control plane (Plan 1).

## Pieces

- `launcher.go` / `fake_launcher.go` — `ExitLauncher` interface + in-memory fake.
- `docker_launcher.go` — launches exits as containers of the OpenFlux image via
  the `docker` CLI; the key secret is a 0600 file bind-mounted at
  `/run/secrets/channel.key`.
- `pool.go` — warm pool; `Issue` claims a warm exit or cold-launches one
  (waiting for it to join, via the readiness gate), `Release` stops it.
- `reconcile.go` — stops exits whose key is unknown / revoked / expired.
- `migrate.go` / `migrate_live.go` — `shouldMigrate` policy, the
  `MigrationController` sequence, and the live manager-wired deps.

## Unit tests (no Docker, no live call)

```bash
go test -race ./orchestrator/... ./access/...
```

Covers: launcher fake, docker arg/label/secret construction, warm-pool
issue/claim/rollback, readiness gate, reconcile, migration policy +
controller sequence, store call reservation, and the hub endpoints.

## Manual runbook (requires Docker + a live Telemost pool)

These steps verify what unit tests cannot: the real Docker lifecycle and the
in-session call migration over a live SFU.

1. **Build the image** (from Plan 1's compose or directly):
   ```bash
   docker build -t openflux:local .
   ```
2. **Pool file** `pool.txt` — ~12–15 pre-created Telemost call links, one per
   line.
3. **Channel key** — the shared `channel.key` used by Plan 1.
4. **Start the daemon with the pool enabled:**
   ```bash
   go run ./cmd/accessd \
     --store accessd-store.json --pool pool.txt \
     --admin 127.0.0.1:8099 --exit :8098 \
     --docker-image openflux:local --warm 2 \
     --license-ws ws://host.docker.internal:8098/exit/ws
   ```
   Expect: `pool enabled`, and after ~30–40s two warm containers
   (`docker ps` shows `openflux-exit-*`).
5. **Issue a key:**
   ```bash
   go run ./cmd/accessctl issue --admin 127.0.0.1:8099 --days 30
   ```
   Expect: an `openflux://` key; a warm container is now bound to it (no new
   30–40s wait).
6. **Connect** the key in an OpenFlux client; confirm traffic flows.
7. **Revoke** and confirm the exit stops and the call frees:
   ```bash
   go run ./cmd/accessctl revoke --admin 127.0.0.1:8099 --id <id>
   ```
   Expect: the container exits within a second; `docker ps` no longer lists it.
8. **Reconcile on restart:** kill `accessd`, leave a container running, restart
   `accessd` — it logs `reconcile stopped N orphaned exit(s)` for any whose key
   is gone/expired.
9. **In-session migration (opt-in):** start the exit (or warm exits) with
   `--migrate`; while a download runs, break the current Telemost call (close
   it in the Yandex UI). Expect the exit to log `[migrate] migrated to a fresh
   call` and the download to continue without dropping the connection.

Covered only here (not by unit tests): actual container create/stop, the
manager's dynamic-transport swing over a real session, and byte-stall health
from a real SFU.
