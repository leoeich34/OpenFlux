# Client Access-Key Field — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a buyer connect by pasting their `openflux://` access key into the OpenFlux client — one field, one Connect button — with no transport picker, URL, or credentials to fill in.

**Architecture:** The Go mobile core (`openflux-mobile` module) already parses keys and connects via a Session; the missing pieces are (1) a small, testable core helper that turns a pasted key into the exact `(specsJSON, secret)` the existing `StartSession` path takes, (2) a one-line cgo `//export` that wraps it so the iOS app can call it, and (3) the iOS SwiftUI "Access key" screen. Only (1) is unit-testable off-device; (2) needs the iOS toolchain (`build_ios.sh`) and (3) needs Xcode, so both are verified by building and running, documented in a runbook — exactly as Plan 2 deferred its Docker/live parts.

**Tech Stack:** Go (the `openflux-mobile` module and its cgo `mobile/ios` shim), Swift/SwiftUI (`ios-app/`). No new modules.

**Spec:** [`docs/superpowers/specs/2026-10-16-access-key-service-design.md`](../specs/2026-10-16-access-key-service-design.md)

Subsystem 4 of 4. Depends on the `openflux://` key format (Plan 1) and the
`share` / `mobile` packages already in `main`. Nothing else depends on it.

## Global Constraints

- The Go core helper is pure (no network) and unit-tested in the `openflux-mobile` module; `go test ./...` in `mobile/` must pass.
- No new third-party module.
- The access key is a credential: it is stored only in the app's own settings and never logged by the core or the app.
- Reuse the existing connect path (`mobile.StartSession` / `OpenFluxStartSession`); do not add a parallel tunnel implementation.
- The key field connects over the Session path (the key always carries a Session-capable config); classic single-transport still works because a Session speaks classic to a classic node.

## Review Focus

- **Malformed / non-key text pasted** — a clear, worded error (reusing `share.Read`'s codes), no crash, no connect attempt. Task 1 + Task 3.
- **Valid key, connect** — the core turns the key into the same specs+secret the manual Session path uses; connecting from a key reaches the same place as the equivalent manual config. Task 1.
- **Key with a bare single transport** (e.g. one telemost) — still connects (Session speaks classic to a one-carrier node), not rejected as "needs several transports". Task 1.
- **Secret never logged** — the helper and the cgo export never write the secret or full key to logs. Task 1 + Task 2.
- **Empty / whitespace input** — the Connect button is disabled / the call returns a worded error, never an empty-config connect. Task 1 + Task 3.

---

### Task 1: Core helper — key → (specs, secret)

**Files:**
- Create: `mobile/key.go`
- Test: `mobile/key_test.go`

**Interfaces:**
- Consumes: `share.Decode`, `share.Read`, the existing `sessionSpecsOf` / `ShareSessionSpecs` (mobile/share.go).
- Produces:
  - `func KeySpecs(link string) (specsJSON string, secret string, err error)` — decodes the `openflux://` key, returns the Session specs JSON (context + transports, via `sessionSpecsOf`) and the key's secret. A malformed link returns an error whose text matches what `share.Read` reports.
  - `func ValidateKey(link string) error` — nil for a connectable key, else a worded error (for the UI to show before connecting).

- [ ] **Step 1: Write failing tests** (reuse a known-good key built with `share.Encode`/`MakeShareLink` in the test):
  - `KeySpecs` on a telemost key returns a specsJSON that `json.Unmarshal`s to a context + one telemost transport, and the secret equals the config's secret;
  - `KeySpecs("not a link")` returns a non-nil error;
  - `ValidateKey` nil on a good key, error on garbage and on empty string.
- [ ] **Step 2: Run — FAIL** (`cd mobile && go test ./ -run Key`).
- [ ] **Step 3: Implement** `KeySpecs` + `ValidateKey` in `mobile/key.go`.
- [ ] **Step 4: Run — PASS + `cd mobile && go test ./...`.**
- [ ] **Step 5: Commit** `feat(mobile): KeySpecs/ValidateKey — connect from an access key`.

---

### Task 2: cgo export `OpenFluxStartFromKey`

**Files:**
- Modify: `mobile/ios/proxy.go` (add the export next to `OpenFluxStartSession`).

**Interfaces:**
- Consumes: `mobile.KeySpecs` (Task 1), the existing session-start path behind `OpenFluxStartSession`.
- Produces: `//export OpenFluxStartFromKey` — `func OpenFluxStartFromKey(link, socksAddr *C.char) C.int` — calls `KeySpecs`, then the same internal start the `OpenFluxStartSession` body uses, with the key's secret. Returns the same `C.int` status codes (and a distinct code for a bad key) as the other start exports; never logs the key or secret.

- [ ] **Step 1:** Add the export, reusing the existing `recoverStart`, status codes, and the `OpenFluxStartSession` start path (factor its body into a shared helper if needed so this does not duplicate it).
- [ ] **Step 2: Verify it compiles for the host** (`CGO_ENABLED=1 go build ./mobile/ios/` — cgo but no iOS SDK needed to type-check) and **build the iOS static lib**: `./build_ios.sh` produces `output/ios/liboflux.a` and a refreshed `liboflux.h` containing `OpenFluxStartFromKey`. *(Requires Xcode; runbook.)*
- [ ] **Step 3: Commit** `feat(mobile/ios): OpenFluxStartFromKey export`.

> No unit test: cgo `//export` functions are built, not `go test`ed. The logic they wrap (`KeySpecs`) is unit-tested in Task 1; this task's gate is a clean `build_ios.sh`.

---

### Task 3: iOS "Access key" screen

**Files:**
- Modify: `ios-app/OpenFlux/ContentView.swift` (add an `accessKey` transport kind + its field; wire Connect to the new export).
- Modify: the Swift transport enum (`TransportKind`) wherever it is defined.
- Modify: `ios-app/OpenFlux/TunnelController.swift` and/or `VPNController.swift` (call `OpenFluxStartFromKey` for the new kind).

**Interfaces:**
- Consumes: `OpenFluxStartFromKey` (Task 2), the existing `OpenFluxShareDecode` for pre-connect validation.
- Produces: an "Access key" option in the transport Picker whose `connectionFields` is a single multi-line paste field bound to an `@AppStorage("accessKey")`; `canStart` is true only for a non-empty, valid key (validated via `OpenFluxShareDecode`); Connect calls `OpenFluxStartFromKey(key, socksAddr)`. A bad key shows the worded error, not a silent failure.

- [ ] **Step 1:** Implement the SwiftUI changes: add the enum case, the field, the validation, and the connect wiring; persist the key in the app's settings only.
- [ ] **Step 2: Build and run** the app (Xcode, or this repo's iOS Simulator tooling): paste a known-good key → Connect → it tunnels; paste garbage → the field shows the error and Connect stays disabled. *(Requires Xcode / a Mac; runbook.)*
- [ ] **Step 3: Commit** `feat(ios): connect from a pasted access key`.

> No unit test: SwiftUI + NetworkExtension are built and run, not `go test`ed. The gate is the manual build-and-run in Task 4's runbook.

---

### Task 4: Runbook

**Files:**
- Create: `ios-app/ACCESS_KEY.md`

- [ ] **Step 1:** Document the end-to-end client flow and how to verify it (requires a Mac + Xcode): build the core with `./build_ios.sh`, open `ios-app` (XcodeGen `project.yml`), run on a device/simulator, pick "Access key", paste a key issued by `accessctl`/the Telegram bot, Connect, and confirm traffic flows; then paste a malformed key and confirm the worded error. Note which Review-Focus items this manual run covers that Task 1's unit tests cannot (the actual cgo export, the SwiftUI validation, and the live tunnel from a key).
- [ ] **Step 2: Commit** `docs(ios): access-key connect runbook`.

---

## Notes for the executor

- Only Task 1 is unit-testable off-device; Tasks 2–4 are build/runbook-verified because the cgo export needs the iOS toolchain and the UI needs Xcode — the same split Plan 2 used for its Docker and live-migration parts. Keep all branching logic in `KeySpecs`/`ValidateKey` (tested) so the export and the Swift view stay thin.
- The secret lives inside the key; do not surface it in the UI or logs. The key field is a password-grade input.
- Reuse `OpenFluxShareDecode` for the Swift-side pre-connect validation so the app and the core agree on what a valid key is.
