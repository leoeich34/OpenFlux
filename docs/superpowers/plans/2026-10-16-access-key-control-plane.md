# Access-Key Control Plane — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A control server that issues time-limited, per-user OpenFlux access keys from a static Telemost call pool, and an exit node that enforces TTL and instant revocation by tearing its tunnel down on command.

**Architecture:** One new Go package `access` (store, key builder, control server, exit-side license client) plus two commands: `cmd/accessd` (the control daemon: loopback admin HTTP API + exit-facing WebSocket hub) and `cmd/accessctl` (thin CLI over the admin API). The existing `openflux --role=exit` gains a license client that holds a WebSocket to the daemon and cleanly stops the transport on revoke, expiry, or lost connection (fail-closed). The client key is a plain `openflux://` link (no protocol change); the exit knows which key it serves from its own `--license-key-id` flag.

**Tech Stack:** Go (CGO_ENABLED=0, pure-Go only), `github.com/gorilla/websocket` (already a dependency), stdlib `net/http`, `encoding/json` for the file-backed store, the existing `share` package for key encoding.

**Spec:** `docs/superpowers/specs/2026-10-16-access-key-service-design.md`

This plan is subsystem 1 of 4 from that spec. The others become their own plans and build on this one:
- **Plan 2 — Pool orchestrator:** Docker API lifecycle for exit containers, warm pool, in-session call migration. Replaces this plan's static call-list seed with live management.
- **Plan 3 — Telegram bot:** operator console over this plan's admin HTTP API.
- **Plan 4 — Client key field:** paste-a-key UX in `mobile/` / `ios-app/`.

## Global Constraints

- Pure Go only: `CGO_ENABLED=0 go build` must succeed; no cgo dependencies.
- No new third-party module unless unavoidable; reuse `github.com/gorilla/websocket` and the stdlib.
- The client key is a standard `openflux://` link produced via `share.Encode`; this plan adds no field to `share.Config` and no wire-protocol change.
- Enforcement is **fail-closed**: an exit that cannot confirm its key is valid (lost control connection beyond the grace window, or startup check fails) must stop carrying traffic.
- One key = one call = one exit = one concurrent session (spec constraint).
- The key carries the encryption secret — never log it at any level.
- Admin HTTP API binds to loopback only (`127.0.0.1`); it is not exposed publicly.

## Review Focus

- **Lost control connection (exit ↔ daemon):** the exit must fail-closed — stop the tunnel after the heartbeat grace window, not keep serving. Covered in Task 4.
- **Expiry mid-session:** a key whose `ExpiresAt` passes while connected must be torn down, not only refused at next connect. Covered in Tasks 1 and 3.
- **Revoke of an unknown / already-revoked id:** admin revoke must be idempotent and return a clear not-found vs already-revoked result, never panic. Covered in Task 1.
- **Pool exhaustion:** issuing when every pooled call link is already assigned must fail with a clear error, not hand out a duplicate call. Covered in Task 1.
- **Secret/key leakage in logs:** issuing and serving must not write the secret or full key to logs. Covered in Task 2.

---

### Task 1: Key store (file-backed) with pool assignment

**Files:**
- Create: `access/store.go`
- Test: `access/store_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Status string` with `StatusActive Status = "active"`, `StatusRevoked Status = "revoked"`.
  - `type KeyRecord struct { ID, Secret, CallLink string; Status Status; IssuedAt, ExpiresAt time.Time }`
  - `func (r KeyRecord) Valid(now time.Time) bool` — `Status==StatusActive && now.Before(r.ExpiresAt)`.
  - `type Store struct { ... }`
  - `func Open(path string, pool []string) (*Store, error)` — loads/creates the JSON file; `pool` is the static list of Telemost call links to assign from.
  - `func (s *Store) Issue(ttl time.Duration, now time.Time) (KeyRecord, error)` — picks a free pooled call link, generates a random `ID` and `Secret`, persists, returns the record; error `ErrPoolExhausted` when no free call.
  - `func (s *Store) Get(id string) (KeyRecord, bool)`
  - `func (s *Store) Revoke(id string) error` — sets `StatusRevoked`, frees its call link; `ErrNotFound` if absent; idempotent if already revoked (no error).
  - `func (s *Store) Extend(id string, d time.Duration) error` — `ExpiresAt += d`; `ErrNotFound` if absent.
  - `func (s *Store) List() []KeyRecord`
  - Exported errors: `ErrPoolExhausted`, `ErrNotFound`.

- [ ] **Step 1: Write failing tests**

```go
func TestIssueAssignsFreeCallUniqueIDAndSecret(t *testing.T) // two Issues get different IDs, secrets, and the two distinct pool links
func TestIssuePoolExhausted(t *testing.T)                    // pool of 1, second Issue returns ErrPoolExhausted
func TestValidTrueThenFalseAfterRevoke(t *testing.T)          // Valid(now)==true; after Revoke, Get shows revoked and Valid==false
func TestRevokeFreesCallLinkForReuse(t *testing.T)           // pool of 1: Issue, Revoke, Issue again succeeds with same link
func TestRevokeUnknownIsErrNotFound_AlreadyRevokedIsNoError(t *testing.T)
func TestExpiryMakesInvalid(t *testing.T)                     // ttl=1h; Valid(now+2h)==false
func TestExtendMovesExpiry(t *testing.T)
func TestPersistenceRoundTrip(t *testing.T)                   // Open, Issue, close; re-Open same path -> record present
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./access/ -run 'Issue|Valid|Revoke|Expiry|Extend|Persistence' -v`
Expected: FAIL (undefined symbols).

- [ ] **Step 3: Implement `access/store.go`**

Random `ID` and `Secret` from `crypto/rand` (ID: short base32, e.g. 10 chars; Secret: 32 bytes base64url). Persist the whole map to the JSON file under a `sync.Mutex` on every mutation (write temp file + rename for atomicity). Track free vs assigned call links by scanning active records against `pool`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./access/ -run 'Issue|Valid|Revoke|Expiry|Extend|Persistence' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add access/store.go access/store_test.go
git commit -m "feat(access): file-backed key store with pool assignment"
```

---

### Task 2: Key builder (openflux:// from a record)

**Files:**
- Create: `access/key.go`
- Test: `access/key_test.go`

**Interfaces:**
- Consumes: `KeyRecord` (Task 1); `share.Config`, `share.Encode`, `share.Decode` (existing).
- Produces:
  - `func BuildKey(callLink, secret string) (string, error)` — returns an `openflux://` link for a single `telemost` transport carrying `callLink`, with `Secret=secret` and `Context=callLink`.

- [ ] **Step 1: Write failing test**

```go
func TestBuildKeyRoundTrips(t *testing.T)
// link, err := BuildKey("https://telemost.yandex.ru/j/123", "s3cr3t")
// cfg, err := share.Decode(link)
// assert: one transport, Type=="telemost", URL==callLink, cfg.Secret=="s3cr3t", cfg.Context==callLink
func TestBuildKeyIsValidShareConfig(t *testing.T) // cfg.Validate() returns nil
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./access/ -run BuildKey -v`
Expected: FAIL (undefined `BuildKey`).

- [ ] **Step 3: Implement `BuildKey` in `access/key.go`**

Construct `share.Config{Secret: secret, Context: callLink, Transports: []share.Transport{{Type: "telemost", URL: callLink}}}` and return `share.Encode(cfg)`. A single transport needs no `Negotiate` (see `share.Validate`).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./access/ -run BuildKey -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add access/key.go access/key_test.go
git commit -m "feat(access): build openflux:// key from a call link + secret"
```

---

### Task 3: Control server — admin API + exit WebSocket hub

**Files:**
- Create: `access/server.go`
- Test: `access/server_test.go`

**Interfaces:**
- Consumes: `Store` (Task 1), `BuildKey` (Task 2), `github.com/gorilla/websocket`.
- Produces:
  - `type Server struct { ... }`
  - `func NewServer(s *Store) *Server`
  - `func (srv *Server) AdminHandler() http.Handler` — routes:
    - `POST /admin/issue` body `{"ttlSeconds":N}` → `{"id","key","callLink","expiresAt"}` (key from `BuildKey`).
    - `POST /admin/revoke` body `{"id":"..."}` → 200 `{"status":"revoked"}`, 404 if not found. Also pushes revoke to a connected exit (below).
    - `POST /admin/extend` body `{"id":"...","ttlSeconds":N}` → 200, 404 if not found.
    - `GET /admin/list` → `[{"id","status","callLink","expiresAt"}]` (never the secret).
  - `func (srv *Server) ExitHandler() http.Handler` — `GET /exit/ws?id=<keyID>`: upgrades to WebSocket. On connect, if the key is unknown or already invalid, send `{"type":"revoke"}` and close. While valid, read client heartbeats; when the key becomes invalid (revoked via admin, or `ExpiresAt` passes — server runs a ticker), send `{"type":"revoke"}` and close.
  - Message type (both directions use JSON): `type Msg struct { Type string \`json:"type"\` }` with `"heartbeat"` and `"revoke"`.

- [ ] **Step 1: Write failing tests** (use `httptest.NewServer` with `AdminHandler`/`ExitHandler`, and a `gorilla/websocket` dial)

```go
func TestAdminIssueReturnsDecodableKey(t *testing.T)      // POST /admin/issue -> body.key decodes via share.Decode to a telemost transport
func TestAdminListOmitsSecret(t *testing.T)               // /admin/list JSON contains no "secret" field and not the secret value
func TestAdminRevokeUnknownIs404(t *testing.T)
func TestExitWSGetsRevokeAfterAdminRevoke(t *testing.T)   // issue, dial /exit/ws?id=, admin revoke -> ws receives {"type":"revoke"}
func TestExitWSRevokeOnConnectIfExpired(t *testing.T)     // issue ttl=0 -> dial -> immediately receives revoke
func TestExitWSRevokeOnMidSessionExpiry(t *testing.T)     // issue ttl=1s, dial while valid, keep socket open -> ws receives revoke after the key expires (ticker-driven)
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./access/ -run 'Admin|ExitWS' -v`
Expected: FAIL.

- [ ] **Step 3: Implement `access/server.go`**

Keep a map `id -> chan struct{}` of connected exits so admin-revoke can wake the right socket; also run a `time.Ticker` (e.g. 1s) that scans for newly-expired active keys and signals their sockets. Guard with a mutex. Do not log secrets or keys.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./access/ -run 'Admin|ExitWS' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add access/server.go access/server_test.go
git commit -m "feat(access): control server admin API + exit WebSocket hub"
```

---

### Task 4: Exit-side license client (fail-closed)

**Files:**
- Create: `access/licenseclient.go`
- Test: `access/licenseclient_test.go`

**Interfaces:**
- Consumes: `github.com/gorilla/websocket`; the server's `Msg` shape (Task 3).
- Produces:
  - `type LicenseClient struct { ... }`
  - `func Dial(wsURL, keyID string, grace time.Duration, onStop func(reason string)) (*LicenseClient, error)` — connects to `wsURL?id=keyID`, sends a `heartbeat` every `grace/2`, and reads. Calls `onStop` exactly once on any of: a `revoke` message; the read loop ending (connection lost) with no reconnect within `grace`; `Close`-initiated shutdown is NOT a stop.
  - `func (c *LicenseClient) Close() error` — stops the client without firing `onStop`.

- [ ] **Step 1: Write failing tests** (stand up a fake WS server with `httptest` + `gorilla/websocket` upgrader)

```go
func TestOnStopFiresOnRevokeMessage(t *testing.T)       // server sends {"type":"revoke"} -> onStop called once with reason "revoked"
func TestOnStopFiresOnConnectionLoss(t *testing.T)      // server closes socket -> onStop called within grace with reason "connection lost"
func TestCloseDoesNotFireOnStop(t *testing.T)           // Close() -> onStop never called
func TestOnStopFiresOnce(t *testing.T)                  // revoke then close -> onStop called exactly once
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./access/ -run 'OnStop|Close' -v`
Expected: FAIL.

- [ ] **Step 3: Implement `access/licenseclient.go`**

A read goroutine and a heartbeat ticker; a `sync.Once` guards `onStop`. On read error, attempt one reconnect; if it fails for `grace`, fire `onStop("connection lost")` (fail-closed).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./access/ -run 'OnStop|Close' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add access/licenseclient.go access/licenseclient_test.go
git commit -m "feat(access): fail-closed exit-side license client"
```

---

### Task 5: `cmd/accessd` daemon

**Files:**
- Create: `cmd/accessd/main.go`
- Test: `cmd/accessd/main_test.go`

**Interfaces:**
- Consumes: `access.Open`, `access.NewServer`, `Server.AdminHandler`, `Server.ExitHandler`.
- Produces: a binary with flags `--store=<path>` (JSON file), `--pool=<file>` (newline-separated call links), `--admin=127.0.0.1:PORT`, `--exit=:PORT` (exit WS listener). Serves `AdminHandler` on `--admin` (loopback-checked) and `ExitHandler` on `--exit`.

- [ ] **Step 1: Write failing test**

```go
func TestLoadPoolFileTrimsAndSkipsBlanks(t *testing.T) // a helper loadPool(path) []string: trims whitespace, skips blank/`#` lines
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/accessd/ -v`
Expected: FAIL.

- [ ] **Step 3: Implement `cmd/accessd/main.go`**

Flag parsing, `loadPool`, wire the two listeners. Refuse to start if `--admin` is not a loopback address.

- [ ] **Step 4: Run test + build**

Run: `go test ./cmd/accessd/ -v && CGO_ENABLED=0 go build ./cmd/accessd`
Expected: PASS and a built binary.

- [ ] **Step 5: Commit**

```bash
git add cmd/accessd/
git commit -m "feat(accessd): control daemon wiring admin + exit listeners"
```

---

### Task 6: `cmd/accessctl` CLI

**Files:**
- Create: `cmd/accessctl/main.go`
- Test: `cmd/accessctl/main_test.go`

**Interfaces:**
- Consumes: the admin HTTP API (Task 3) via `net/http`.
- Produces: a binary with subcommands `issue --days=N`, `revoke --id=X`, `extend --id=X --days=N`, `list`, each hitting `--admin=127.0.0.1:PORT`. `issue` prints the `openflux://` key and a note that it is sensitive.

- [ ] **Step 1: Write failing test**

```go
func TestIssueCommandHitsAdminAPIAndPrintsKey(t *testing.T) // httptest admin stub returns a key; run issue; stdout contains the openflux:// link
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/accessctl/ -v`
Expected: FAIL.

- [ ] **Step 3: Implement `cmd/accessctl/main.go`**

- [ ] **Step 4: Run test + build**

Run: `go test ./cmd/accessctl/ -v && CGO_ENABLED=0 go build ./cmd/accessctl`
Expected: PASS and a built binary.

- [ ] **Step 5: Commit**

```bash
git add cmd/accessctl/
git commit -m "feat(accessctl): CLI over the control admin API"
```

---

### Task 7: Wire the license client into the exit node

**Files:**
- Modify: `main.go` (flag registration near the other flags ~line 269–337; exit startup in the `case roleExit:` block ~line 1136–1190, before `runExit`).
- Test: `license_flags_test.go` (follow the existing `logging_flags_test.go` pattern for flag-parsing tests).

**Interfaces:**
- Consumes: `access.Dial` (Task 4), the existing `trans.Stop()` clean-shutdown path.
- Produces: new flags `--license-server=ws://host:port/exit/ws` and `--license-key-id=<id>`; when both are set and role is exit, dial the license client before `runExit`, with `onStop` that logs the reason, calls `trans.Stop()`, and `os.Exit(0)` (the same clean leave the Telemost signal handler already uses).

- [ ] **Step 1: Write failing test**

```go
func TestLicenseFlagsParse(t *testing.T) // --license-server and --license-key-id populate their vars; both-or-neither validation helper returns error if only one set
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run TestLicenseFlags -v`
Expected: FAIL.

- [ ] **Step 3: Implement the flags + wiring**

Register the two flags; add a helper `licenseEnabled() (bool, error)` enforcing both-or-neither. In `case roleExit:`, when enabled, `access.Dial(server, keyID, grace, onStop)` before `runExit`; store the client so a normal signal shutdown calls `Close()` (no false stop).

- [ ] **Step 4: Run test + build**

Run: `go test . -run TestLicenseFlags -v && CGO_ENABLED=0 go build .`
Expected: PASS and a built binary.

- [ ] **Step 5: Commit**

```bash
git add main.go license_flags_test.go
git commit -m "feat(exit): enforce access keys via the license client"
```

---

### Task 8: End-to-end smoke test (docs + script, no live call)

**Files:**
- Create: `access/README.md`
- Create: `access/testdata/smoke_test.go` (a Go test gated behind `-tags smoke`, using loopback listeners and a stub exit `onStop`, exercising issue → exit-ws-connect → revoke → onStop, with no real Telemost).

**Interfaces:**
- Consumes: Tasks 1–4.

- [ ] **Step 1: Write the smoke test**

```go
//go:build smoke
func TestIssueConnectRevokeFlow(t *testing.T)
// Open store(pool of 2); NewServer; httptest admin+exit; accessctl-style issue;
// access.Dial with the issued id and an onStop that signals a channel;
// admin revoke; assert onStop fires within grace.
```

- [ ] **Step 2: Run it**

Run: `go test -tags smoke ./access/testdata/ -run IssueConnectRevokeFlow -v`
Expected: PASS.

- [ ] **Step 3: Write `access/README.md`**

How to run `accessd` with a pool file, issue a key with `accessctl`, and start an exit with `--license-server/--license-key-id`; a one-paragraph note that the full docker run against a live Telemost call is covered by Plan 2 (orchestrator).

- [ ] **Step 4: Commit**

```bash
git add access/README.md access/testdata/
git commit -m "test(access): end-to-end issue/connect/revoke smoke test + README"
```

---

## Notes for the executor

- The static call pool (`--pool` file) is a stand-in for Plan 2's orchestrator; keep `Store.Issue` taking its links from that seed so Plan 2 can swap the source without changing the store's API.
- The exit learns only its own `--license-key-id`; it never identifies arbitrary clients. The per-user secret in the key is what lets only the intended client bring up the tunnel on that call (spec, builder-check call).
- In-session call migration and the Docker warm pool are explicitly Plan 2 — do not build them here.
