# Telegram Operator Bot — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Telegram bot that lets the operator issue, revoke, extend and list access keys by chatting with it, restricted to an allowlist of admin users, as a thin front end over the loopback Admin API.

**Architecture:** A new `cmd/accessbot` command plus a small `tgbot` package. The bot long-polls the Telegram Bot API (`getUpdates`) and replies with `sendMessage`, using only stdlib `net/http`+`encoding/json` (no new module, pure Go). Everything is behind interfaces so the command parser, the authorization check, the Admin API client and the update→reply dispatcher are unit-tested with fakes; nothing in the test suite talks to Telegram or a live `accessd`. Payment is out of band — the bot only issues on an operator's command.

**Tech Stack:** Go (CGO_ENABLED=0, pure Go), stdlib HTTP/JSON. Consumes the Admin API documented in [`docs/api.md`](../../api.md).

**Spec:** [`docs/superpowers/specs/2026-10-16-access-key-service-design.md`](../specs/2026-10-16-access-key-service-design.md)

This is subsystem 3 of 4. It depends on Plan 1 (the Admin API) already in `main`. Plan 4 (client key field) remains separate.

## Global Constraints

- Pure Go only: `CGO_ENABLED=0 go build ./...` must pass; no cgo, no new third-party module (talk to the Telegram Bot API over stdlib HTTP).
- All logic is unit-tested against fakes; no test calls api.telegram.org or a live accessd.
- The bot is an **operator console only**: it never takes payment and never exposes any admin action to a non-allowlisted user.
- Never log the bot token or an issued key's secret / full `openflux://` key; the key is sent only to the requesting admin's chat.
- The Admin API is loopback; the bot runs on the same host and reaches it at `127.0.0.1:8099` by default.

## Review Focus

- **Non-admin sends a command** — the bot must refuse and make no Admin API call. Task 2 + Task 4.
- **Malformed command** (`/issue abc`, `/revoke` with no id) — a friendly usage reply, no Admin API call, no panic. Task 2 + Task 4.
- **Admin API error** (409 pool exhausted, 404 unknown id, connection refused) — surfaced to the operator as a readable message, never a crash or a silent drop. Task 3 + Task 4.
- **Update offset handling** — each Telegram update is processed exactly once; the poll offset advances past handled updates so none is replayed in a loop. Task 1 + Task 5.
- **Secret/token in logs** — issuing logs the key id only; the token and `openflux://` key never reach logs. Task 4.

---

### Task 1: Telegram Bot API client (interface + HTTP impl + fake)

**Files:**
- Create: `tgbot/client.go`
- Create: `tgbot/fake_client.go`
- Test: `tgbot/client_test.go`

**Interfaces:**
- Produces:
  - `type Update struct { UpdateID int; ChatID int64; UserID int64; Text string }`
  - `type Client interface { GetUpdates(offset int, timeoutSec int) ([]Update, error); SendMessage(chatID int64, text string) error }`
  - `func NewHTTPClient(token string, opts ...Option) Client` — calls `https://api.telegram.org/bot<token>/getUpdates` and `/sendMessage`; `WithBaseURL` overrides the endpoint for tests.
  - `type FakeClient struct { ... }` implementing `Client`: returns queued updates, records sent messages.

- [ ] **Step 1: Write failing tests**: an `httptest` server returning a getUpdates JSON payload → `NewHTTPClient(...).GetUpdates` parses `UpdateID/ChatID/UserID/Text` from `result[].update_id` / `message.chat.id` / `message.from.id` / `message.text`; `SendMessage` POSTs `chat_id`+`text` to `/sendMessage`. Plus `FakeClient` queues/records.
- [ ] **Step 2: Run — FAIL** (`go test ./tgbot/ -run Client`).
- [ ] **Step 3: Implement** the client and fake; `WithBaseURL` points at the httptest server.
- [ ] **Step 4: Run — PASS + `CGO_ENABLED=0 go build ./tgbot/`.**
- [ ] **Step 5: Commit** `feat(tgbot): Telegram Bot API client + fake`.

---

### Task 2: Command parsing + authorization (pure)

**Files:**
- Create: `tgbot/command.go`
- Test: `tgbot/command_test.go`

**Interfaces:**
- Produces:
  - `type Command struct { Kind string; ID string; Days int }` — Kind in `issue|revoke|extend|list|help|unknown`.
  - `func ParseCommand(text string) (Command, error)` — `/issue 30`, `/revoke <id>`, `/extend <id> 7`, `/list`, `/help`; error for malformed args.
  - `func IsAdmin(userID int64, allow []int64) bool`.

- [ ] **Step 1: Write failing tests**: `/issue 30`→{issue,Days:30}; `/issue`→default days (ruling: document the default); `/issue abc`→error; `/revoke K1`→{revoke,ID:K1}; `/revoke`→error; `/extend K1 7`→{extend,ID:K1,Days:7}; `/list`→{list}; `/help` and unknown text→{help}/{unknown}; `IsAdmin` true only for listed ids.
- [ ] **Step 2: Run — FAIL.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run — PASS.**
- [ ] **Step 5: Commit** `feat(tgbot): command parser + admin authorization`.

---

### Task 3: Admin API client

**Files:**
- Create: `tgbot/admin.go`
- Test: `tgbot/admin_test.go`

**Interfaces:**
- Consumes: the Admin API (`docs/api.md`).
- Produces:
  - `type AdminClient struct { ... }` with `func NewAdminClient(base string) *AdminClient`.
  - `Issue(days int) (id, key string, err error)`; `Revoke(id string) error`; `Extend(id string, days int) error`; `List() ([]KeyInfo, error)` where `KeyInfo{ID, Status, CallLink, ExpiresAt string}`.
  - Errors carry the HTTP status meaning (e.g. a sentinel/`error` string for 409 pool-exhausted and 404 not-found) so the dispatcher can phrase them.

- [ ] **Step 1: Write failing tests** (httptest stub of the Admin API): `Issue` POSTs `{ttlSeconds: days*86400}` and returns the stub's `id`+`key`; a 409 → a recognizable "pool exhausted" error; `Revoke` of a 404 → a "not found" error; `List` parses the array.
- [ ] **Step 2: Run — FAIL.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run — PASS.**
- [ ] **Step 5: Commit** `feat(tgbot): admin API client`.

---

### Task 4: Dispatcher (update → reply)

**Files:**
- Create: `tgbot/dispatch.go`
- Test: `tgbot/dispatch_test.go`

**Interfaces:**
- Consumes: `Command`+`IsAdmin` (Task 2), `AdminClient` (Task 3), `Client.SendMessage` (Task 1).
- Produces:
  - `type Bot struct { ... }` with `func NewBot(tg Client, admin *AdminClient, allow []int64) *Bot`.
  - `func (b *Bot) Handle(u Update)` — authorizes, parses, calls the Admin API, and sends exactly one reply. The reply for `issue` contains the id and the `openflux://` key with a "treat as a credential" note.

- [ ] **Step 1: Write failing tests** (FakeClient + httptest admin stub):
  - non-admin update → reply "not authorized", **zero** admin calls;
  - `/issue 30` from an admin → admin Issue called, reply contains the key;
  - `/issue abc` → usage reply, no admin call;
  - admin API 409 on issue → reply says pool exhausted;
  - exactly one SendMessage per update.
- [ ] **Step 2: Run — FAIL.**
- [ ] **Step 3: Implement.** Do not log the token or key; the issue reply is the only place the key appears, sent to the admin's chat.
- [ ] **Step 4: Run — PASS.**
- [ ] **Step 5: Commit** `feat(tgbot): update dispatcher with auth + admin calls`.

---

### Task 5: `cmd/accessbot` long-poll loop

**Files:**
- Create: `cmd/accessbot/main.go`
- Test: `cmd/accessbot/main_test.go`

**Interfaces:**
- Consumes: `tgbot.NewHTTPClient`, `tgbot.NewAdminClient`, `tgbot.NewBot`, and a pure `pollOnce` helper.
- Produces: a binary with flags `--token` (or `TELEGRAM_TOKEN` env), `--admin` (default `127.0.0.1:8099`), `--admins` (comma-separated user ids). Long-polls `GetUpdates(offset, 30)`, dispatches each update, and advances `offset` past the highest `UpdateID` handled.

- [ ] **Step 1: Write failing test** for the pure helper `nextOffset(updates []tgbot.Update, cur int) int` — returns `max(update_id)+1`, or `cur` when empty; proves no update is replayed.
- [ ] **Step 2: Run — FAIL.**
- [ ] **Step 3: Implement** `nextOffset` + the loop + flags; token/admins from env or flag; refuse to start with no token or no admins.
- [ ] **Step 4: Run — PASS + `CGO_ENABLED=0 go build ./cmd/accessbot`.**
- [ ] **Step 5: Commit** `feat(accessbot): long-poll loop over the operator bot`.

---

### Task 6: README

**Files:**
- Create: `tgbot/README.md`

- [ ] **Step 1:** Document BotFather setup (get a token), the flags/env, the commands (`/issue N`, `/revoke id`, `/extend id N`, `/list`, `/help`), the admin allowlist, and the reminder that payment is handled out of band and the issued key is a credential. Note the bot and `accessd` run on the same host.
- [ ] **Step 2: Commit** `docs(tgbot): operator bot setup + commands`.

---

## Notes for the executor

- No Telegram library: the Bot API is plain HTTPS+JSON, and `WithBaseURL` makes it testable against `httptest`. Keeping it dependency-free honors the pure-Go constraint.
- The bot never needs the Admin API's loopback exception relaxed: it runs on the same host as `accessd`.
- The issued key is a credential — it appears only in the reply to the requesting admin, never in logs.
