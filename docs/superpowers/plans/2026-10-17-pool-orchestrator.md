# Pool Orchestrator — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the static call-pool of Plan 1 into a live Docker-managed pool: the control daemon starts, warms, and tears down exit containers bound to keys, and an exit whose Telemost call degrades migrates the client to a fresh call in-session over the existing tunnel.

**Architecture:** A new `orchestrator` package behind two interfaces so the logic is unit-testable without Docker or a live call: `ExitLauncher` (start/stop an exit bound to a call+key, with a Docker implementation and a fake for tests) and the existing `access.Store` pool. The daemon keeps a warm set of pre-started exits. In-session migration reuses the existing dynamic-transport control path (`control.SubtypeTransportStart` / `SubtypeTransportStop` handled by `manager.Manager.startTransport` → `session.AddTransportPostStart`): the exit adds a new `telemost` transport carrying a fresh conference URL, then stops the old one; the client follows without dropping TCP flows because the session spans both transports during the swing.

**Tech Stack:** Go (CGO_ENABLED=0, pure Go). Docker via its HTTP API over the local socket using the official `github.com/docker/docker/client` **only if** it stays cgo-free and builds static; otherwise shell out to the `docker` CLI behind the `ExitLauncher` interface (decided in Task 2). Reuses `access` (Plan 1), `transport/manager`, `transport/control`, `transport/yandex` (TelemostGroup).

**Spec:** `docs/superpowers/specs/2026-10-16-access-key-service-design.md`

Depends on Plan 1 (`docs/superpowers/plans/2026-10-16-access-key-control-plane.md`), which must be merged first. This is subsystem 2 of 4. Plan 3 (Telegram bot) and Plan 4 (client key field) remain separate.

## Global Constraints

- Pure Go, `CGO_ENABLED=0 go build ./...` must pass; no cgo.
- All orchestrator logic is tested against a fake `ExitLauncher`; no test requires a running Docker daemon or a live Telemost call. Docker-backed and live-migration paths are verified manually (a `docs`/script smoke, like Plan 1's).
- The control daemon still binds its admin API to loopback only (Plan 1 constraint).
- One key = one call = one exit container = one concurrent session (spec).
- Never log the key secret.
- Migration must not drop established TCP flows while both calls are briefly live (the session-spanning-transports property the manager already provides).

## Review Focus

- **Launcher failure on issue** — if starting/binding an exit container fails, `issue` must fail and free the reserved call link, never return a key whose exit never came up. Task 2/4.
- **Warm-pool exhaustion vs. cold start** — when no warm exit is ready, issue either waits for a cold start or returns a clear "try again" error; it must not hand out a key bound to a not-yet-joined call. Task 3.
- **Orphaned containers** — a daemon restart must reconcile: adopt or stop containers whose key is gone/expired/revoked, so none linger billing a call slot. Task 4.
- **Migration mid-transfer** — triggering migration while bulk data flows must not drop the TCP connection; the old call stops only after the new one carries traffic. Task 5.
- **Migration storm** — repeated degradation must not flap calls faster than a floor interval, exhausting the pool. Task 5.

---

### Task 1: ExitLauncher interface + fake

**Files:**
- Create: `orchestrator/launcher.go`
- Create: `orchestrator/fake_launcher.go`
- Test: `orchestrator/fake_launcher_test.go`

**Interfaces:**
- Produces:
  - `type ExitSpec struct { KeyID, CallLink, Secret, LicenseWS string }`
  - `type ExitHandle struct { ID string }` (opaque container/process id)
  - `type ExitLauncher interface { Launch(ExitSpec) (ExitHandle, error); Stop(ExitHandle) error; List() ([]ExitHandle, error) }`
  - `type FakeLauncher struct { ... }` implementing it in memory, recording launched/stopped specs for assertions.

- [ ] **Step 1: Write failing tests** for `FakeLauncher`: `Launch` returns a handle and records the spec; `Stop` removes it; `List` returns live handles; stopping an unknown handle errors.
- [ ] **Step 2: Run — FAIL** (`go test ./orchestrator/ -run Fake`).
- [ ] **Step 3: Implement** the interface and the fake.
- [ ] **Step 4: Run — PASS.**
- [ ] **Step 5: Commit** `feat(orchestrator): ExitLauncher interface + in-memory fake`.

---

### Task 2: Docker-backed launcher

**Files:**
- Create: `orchestrator/docker_launcher.go`
- Test: `orchestrator/docker_launcher_test.go` (unit-test only the argument/label construction, no daemon)

**Interfaces:**
- Consumes: `ExitSpec`, `ExitHandle`, `ExitLauncher` (Task 1).
- Produces: `func NewDockerLauncher(image string, opts ...DockerOption) (ExitLauncher, error)`. Launches the `openflux:local` image with `--role=exit --mode=l4 --transport=telemost --url=<CallLink> --encryption-key-file=... --license-server=<LicenseWS> --license-key-id=<KeyID>`, labels the container `openflux.keyID=<id>` for reconciliation, and returns its container id as the handle.

- [ ] **Step 1: Write failing test** `TestDockerArgsForSpec` — a pure helper `containerCreateArgs(ExitSpec, image) (labels map[string]string, cmd []string)` produces the exact flag list and the `openflux.keyID` label. (No daemon.)
- [ ] **Step 2: Run — FAIL.**
- [ ] **Step 3: Implement.** Decide Docker SDK vs CLI shim here: try `github.com/docker/docker/client` under `CGO_ENABLED=0 go build`; if it fails to build static or pulls cgo, implement `Launch/Stop/List` by shelling out to `docker run -d --label ... / docker rm -f / docker ps --filter label=openflux.keyID`. Record the choice as a ruling. The pure arg/label helper is tested either way.
- [ ] **Step 4: Run — PASS + `CGO_ENABLED=0 go build ./orchestrator/`.**
- [ ] **Step 5: Commit** `feat(orchestrator): docker-backed exit launcher`.

---

### Task 3: Warm pool + issue integration

**Files:**
- Create: `orchestrator/pool.go`
- Test: `orchestrator/pool_test.go`
- Modify: the control server's issue path to go through the pool (wire in Task 4's daemon; here the Pool is standalone and tested with the fake launcher + a Plan-1 `access.Store`).

**Interfaces:**
- Consumes: `ExitLauncher` (Task 1), `*access.Store` (Plan 1).
- Produces:
  - `type Pool struct { ... }`
  - `func NewPool(store *access.Store, launcher ExitLauncher, licenseWS string, warm int) *Pool`
  - `func (p *Pool) Issue(ttl time.Duration) (access.KeyRecord, string, error)` — reserves a call link via the store, launches (or claims a warm) exit bound to it, returns the record and the `openflux://` key; on launch failure it revokes the just-issued record (freeing the link) and returns the error.
  - `func (p *Pool) Release(keyID string) error` — stops the key's exit and revokes it.
  - `func (p *Pool) EnsureWarm()` — tops the warm set back up to `warm`.

- [ ] **Step 1: Write failing tests** (fake launcher): issue launches an exit bound to the issued key's call link and returns a decodable key; a launcher that errors on the next launch makes `Issue` return the error AND leaves the call link free (re-issue succeeds); `Release` stops the exit and the link frees; `EnsureWarm` keeps `warm` idle exits.
- [ ] **Step 2: Run — FAIL.**
- [ ] **Step 3: Implement** `Pool`. Reserve-then-launch-then-rollback ordering so a launch failure never yields a live key with no exit.
- [ ] **Step 4: Run — PASS.**
- [ ] **Step 5: Commit** `feat(orchestrator): warm pool with reserve-launch-rollback issue`.

---

### Task 4: Daemon wiring + reconcile on restart

**Files:**
- Modify: `cmd/accessd/main.go` (new flags `--docker-image`, `--warm`, `--license-ws`; build a `Pool` and route `/admin/issue` and `/admin/revoke` through it).
- Create: `orchestrator/reconcile.go`
- Test: `orchestrator/reconcile_test.go`

**Interfaces:**
- Consumes: `Pool`, `ExitLauncher.List`, `*access.Store`.
- Produces: `func Reconcile(store *access.Store, launcher ExitLauncher, now time.Time) (stopped []string, err error)` — stops every launched exit whose key is unknown, revoked, or expired; returns the handles it stopped.

- [ ] **Step 1: Write failing test** (fake launcher seeded with handles for: a valid key, an expired key, a revoked key, an unknown key): `Reconcile` stops exactly the expired/revoked/unknown ones and leaves the valid one.
- [ ] **Step 2: Run — FAIL.**
- [ ] **Step 3: Implement** `Reconcile`; call it from `accessd` on startup and periodically. Keep admin loopback-gating.
- [ ] **Step 4: Run — PASS + `CGO_ENABLED=0 go build ./cmd/accessd`.**
- [ ] **Step 5: Commit** `feat(accessd): route issue/revoke through the pool; reconcile on restart`.

---

### Task 5: In-session call migration

**Files:**
- Modify: `transport/yandex/` (a health signal: when a lane's forwarded-RTP stalls / loss stays above a threshold for a floor interval, report "call unhealthy") — extend the existing per-lane stats in `telemost.go`/`striped` rather than adding a new mechanism.
- Modify: the exit run path (`main.go` exit case / a small `migrate.go`) to, on an unhealthy signal, request a fresh call from the control server and drive migration via the manager.
- Create: `access` admin/exit endpoint `/exit/nextcall?id=<keyID>` returning a fresh pool call link (server side, Plan-1 package) — the exit asks for it over its existing control connection, not a new trust path.
- Test: `transport/.../migrate_test.go` for the pure decision logic; the manager swap itself is covered by existing manager tests plus a manual docker smoke.

**Interfaces:**
- Consumes: `manager.Manager` dynamic-transport path (`SubtypeTransportStart` with a `control.TransportConfig{Type:"telemost", URL:<fresh call>}`, then `SubtypeTransportStop` for the old), the control server (new `/exit/nextcall`).
- Produces:
  - `func shouldMigrate(stall time.Duration, lossFrac float64, sinceLast time.Duration) (bool, string)` — pure policy: migrate when stalled > stallFloor OR loss > lossFloor, AND sinceLast > minMigrateInterval (anti-flap).
  - the migration driver that calls it and, on true, fetches `/exit/nextcall` and adds/stops transports.

- [ ] **Step 1: Write failing tests** for `shouldMigrate`: healthy → false; high loss but within min interval → false (anti-flap); stalled beyond floor and interval elapsed → true with a reason.
- [ ] **Step 2: Run — FAIL.**
- [ ] **Step 3: Implement** `shouldMigrate` and wire the driver to the manager's dynamic-transport calls and `/exit/nextcall`. Add `/exit/nextcall` to `access.Server` (returns a free pool link, or 409 if none), reusing `Store.freeLink`.
- [ ] **Step 4: Run — PASS + build.**
- [ ] **Step 5: Commit** `feat(migrate): in-session call migration on lane degradation`.

---

### Task 6: Manual smoke runbook (docker + live call)

**Files:**
- Create: `orchestrator/README.md`

- [ ] **Step 1:** Document the end-to-end manual verification (requires Docker + live Telemost pool): start `accessd --docker-image openflux:local --warm 2 --pool pool.txt --license-ws ws://host:8098/exit/ws`; `accessctl issue`; paste the key into a client; confirm traffic; `accessctl revoke` and confirm the container stops and the call frees; kill a call and confirm the client migrates without the download dropping. Note which Review-Focus items this manual run covers that the unit tests cannot (actual Docker lifecycle, actual migration over a live SFU).
- [ ] **Step 2: Commit** `docs(orchestrator): manual docker + live-call smoke runbook`.

---

## Notes for the executor

- The hard, un-unit-testable parts are the Docker daemon calls (Task 2) and the live migration swing (Task 5). Keep both behind interfaces/pure policy functions so the logic is tested in isolation; prove the integration with the Task 6 manual runbook, exactly as Plan 1 deferred its live Telemost run.
- Migration reuses the manager's existing dynamic-transport protocol — do NOT invent a new client/exit signaling channel. The one new server endpoint, `/exit/nextcall`, hands out a pool link; it is the only addition to Plan 1's `access` package.
- Anti-flap (`minMigrateInterval`) matters: without it a lane that is merely slow would churn the whole pool.
