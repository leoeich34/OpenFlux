# OpenFlux Access-Key Service — API Reference

This document describes every programmatic interface of the access-key
service (the control plane in [`access/`](../access) and the orchestrator in
[`orchestrator/`](../orchestrator)): the protocol, the inputs, the outputs,
and their formats. A machine-readable OpenAPI 3.0 spec for the HTTP endpoints
lives alongside this file at [`openapi.yaml`](openapi.yaml) and loads directly
in Swagger UI.

Design background: [`superpowers/specs/2026-10-16-access-key-service-design.md`](superpowers/specs/2026-10-16-access-key-service-design.md).

## Overview of surfaces

| Surface | Served by | Bind | Protocol | Audience |
|---|---|---|---|---|
| **Admin API** | `accessd` | loopback (`127.0.0.1:8099` by default) | HTTP/1.1 + JSON | the operator (via `accessctl` or the Telegram bot) |
| **Exit hub — control** | `accessd` | all interfaces (`:8098` by default) | WebSocket (JSON frames) | exit nodes enforcing a key |
| **Exit hub — migration** | `accessd` | same `:8098` | HTTP/1.1 + JSON, HMAC-signed | exit nodes migrating a call |
| **Access key** | issued by the Admin API | — | `openflux://` URI (see below) | the buyer's OpenFlux client |

All JSON uses UTF-8. Timestamps are RFC 3339 / ISO 8601 (`2026-10-17T12:34:56Z`),
Go's `time.Time` default marshaling. Durations in request bodies are integer
**seconds** (`ttlSeconds`). Unless stated otherwise, request bodies are
`application/json`.

Transport note: HTTP/1.1 is plaintext here. Both the Admin API (loopback) and
the Exit hub are meant to run inside a trusted boundary — the host loopback and
the Docker network respectively. Do **not** expose `:8098` to the WAN; see
[Authentication](#authentication).

---

## 1. Admin API (loopback)

The operator's control surface. `accessd` refuses to start unless `--admin`
is a loopback address, so these endpoints are never reachable off-host. No
auth header is required (the loopback bind *is* the boundary). The
[`accessctl`](../cmd/accessctl) CLI is a thin client over these calls.

### POST `/admin/issue`

Issue a new access key. With the pool orchestrator enabled (`accessd
--docker-image …`) this also claims or launches the key's exit node.

Request body:

| Field | Type | Meaning |
|---|---|---|
| `ttlSeconds` | integer | validity window from now, in seconds |

```json
{ "ttlSeconds": 2592000 }
```

Response `200 OK`:

| Field | Type | Meaning |
|---|---|---|
| `id` | string | the key id (non-secret; used as `--license-key-id`) |
| `key` | string | the `openflux://` access key — **contains the secret, treat as a credential** |
| `callLink` | string | the Telemost conference URL bound to this key |
| `expiresAt` | string (RFC 3339) | when the key expires |

```json
{
  "id": "K5B2QX9P3A",
  "key": "openflux://v1/eJx…",
  "callLink": "https://telemost.yandex.ru/j/1234567890",
  "expiresAt": "2026-11-16T09:00:00Z"
}
```

Errors: `409 Conflict` — the call pool is exhausted (no free link / no warm
exit); `500` — store or launch failure.

### POST `/admin/revoke`

Revoke a key immediately. With the pool enabled this also stops the key's exit
container. A connected exit is torn down within a network round-trip (see the
WebSocket `revoke` frame).

Request body: `{ "id": "K5B2QX9P3A" }`

Response `200 OK`: `{ "status": "revoked" }`.
Errors: `404 Not Found` — unknown id; `500`.
Revoking an already-revoked key is idempotent (`200`).

### POST `/admin/extend`

Move a key's expiry out.

Request body:

| Field | Type | Meaning |
|---|---|---|
| `id` | string | the key id |
| `ttlSeconds` | integer | seconds to add to the current expiry |

Response `200 OK`: `{ "status": "extended" }`.
Errors: `404 Not Found` — unknown id; `500`.

### GET `/admin/list`

List all keys. The secret is **never** included.

Response `200 OK` — a JSON array:

```json
[
  {
    "id": "K5B2QX9P3A",
    "status": "active",
    "callLink": "https://telemost.yandex.ru/j/1234567890",
    "expiresAt": "2026-11-16T09:00:00Z"
  }
]
```

`status` is `active` or `revoked`. (An expired key keeps `status: active`
until reaped; use `expiresAt` to judge validity.)

---

## 2. Exit hub — control WebSocket

Each exit node holds one WebSocket to the hub for the key it serves. This is
how the control plane enforces TTL and instant revocation on the side that has
real internet (the exit), fail-closed.

### GET `/exit/ws?id=<keyID>`

| Part | Value |
|---|---|
| Protocol | WebSocket (RFC 6455), upgraded from HTTP/1.1 GET |
| Query | `id` — the key id the exit serves |
| Frames | JSON text frames, one object per frame |

Message object (both directions):

```json
{ "type": "<heartbeat|revoke>" }
```

- **exit → server**: `{"type":"heartbeat"}`, sent every `grace/2` (grace is the
  exit's `--license-server` tolerance, 10 s by default). Heartbeats keep the
  read side alive; a dropped connection is the exit's signal to fail-closed.
- **server → exit**: `{"type":"revoke"}`. Sent when the key becomes invalid —
  revoked via the Admin API, or expired (the hub re-checks every 200 ms) — and
  immediately on connect if the key is already unknown/expired/revoked. The
  server then closes the socket. On receiving it (or on losing the connection
  beyond the grace window), the exit stops carrying traffic.

There is no request/response body beyond these frames. The hub also tracks
which key ids currently hold a live socket, used as the pool's readiness
signal when issuing a key.

---

## 3. Exit hub — migration (HMAC-signed HTTP)

Used only by an exit running with `--migrate` when its Telemost call degrades:
it reserves a fresh call from the pool and, after moving traffic to it,
commits the swap. Both endpoints are **authenticated with a per-key proof**
(see [Authentication](#authentication)).

### GET `/exit/nextcall?id=<keyID>`

Reserve a fresh pool call link for this key's migration. The reserved link is
held (not reissued to anyone else) until committed or the key is released.

| Part | Value |
|---|---|
| Query | `id` — the key id |
| Header | `X-OpenFlux-Proof: <hex HMAC-SHA256(secret, id)>` (required) |

Response `200 OK`: `{ "callLink": "https://telemost.yandex.ru/j/9999999999" }`.
Errors: `403 Forbidden` — missing/invalid proof; `404` — unknown id;
`409 Conflict` — no free pool link.

### POST `/exit/commitcall?id=<keyID>`

Promote the reserved link to the key's current call (frees the old one). Called
after the new call is carrying traffic.

| Part | Value |
|---|---|
| Query | `id` — the key id |
| Header | `X-OpenFlux-Proof` (required) |
| Body | none |

Response `200 OK`: `{ "status": "committed" }`.
Errors: `403` — bad proof; `404` — unknown id; `500`.

---

## 4. The access key (`openflux://`)

The `key` returned by `/admin/issue` is a self-contained URI the buyer pastes
into their OpenFlux client. It needs no contact with our server to connect —
only reachability to Telemost.

```
openflux://v1/<base64url( DEFLATE( JSON ) )>
```

The JSON payload is a `share.Config` (see [`links.md`](links.md) and the
[`share`](../share) package). For an access key it carries a single Telemost
transport, the per-key secret, and the encryption context:

```json
{
  "secret": "<32-byte base64url per-user secret>",
  "context": "https://telemost.yandex.ru/j/1234567890",
  "transports": [
    { "type": "telemost", "url": "https://telemost.yandex.ru/j/1234567890" }
  ]
}
```

- `secret` — AES-256-GCM shared secret; both client and exit derive keys from
  it. The exit receives the same value as its `--encryption-key-file`.
- `context` — the encryption context; equals the call URL so both ends derive
  the same context without extra config.

Parse or build a key with the CLI:

```bash
./openflux --parse-link "openflux://v1/…"   # -> {"config":…} or {"error":…}
./openflux --make-link -                      # read share.Config JSON on stdin
```

---

## 5. Authentication

| Surface | Mechanism |
|---|---|
| Admin API | **loopback bind** — `accessd` refuses a non-loopback `--admin`. No header; the bind is the boundary. |
| Exit control WS | the key `id` in the query; the socket leaks nothing (no secret, cannot carry traffic) and a revoked/expired id is closed on connect. Keep `:8098` off the WAN. |
| Exit migration HTTP | **per-key proof** header `X-OpenFlux-Proof: hex(HMAC-SHA256(secret, id))`. The exit holds the secret (its `--encryption-key-file`); a party that knows only the (non-secret) `id` cannot forge the proof. The server recomputes it from the stored secret and compares in constant time. |

Compute the proof (Go): `access.ProofFor(secret, id)`.

Deployment requirement: the exit hub (`:8098`) binds all interfaces because
exit **containers** must dial it; keep it firewalled to the Docker network /
host, never WAN-exposed.

---

## 6. CLI: `accessctl`

A thin client over the Admin API. `--admin` defaults to `127.0.0.1:8099`.

| Command | Admin call |
|---|---|
| `accessctl issue --days N` | `POST /admin/issue {ttlSeconds: N*86400}` → prints `id` and the `openflux://` key |
| `accessctl revoke --id X` | `POST /admin/revoke {id: X}` |
| `accessctl extend --id X --days N` | `POST /admin/extend {id: X, ttlSeconds: N*86400}` |
| `accessctl list` | `GET /admin/list` |

---

## 7. End-to-end data flow

```
operator ──accessctl──▶ POST /admin/issue ──▶ accessd
                                               │ store.Issue + (pool) launch exit
                        ◀── {id, key, callLink, expiresAt} ──┘
operator ──(out of band)──▶ buyer : the openflux:// key

buyer's client ──reads key──▶ joins the Telemost call (no server contact)
exit node ──WS /exit/ws?id──▶ accessd      (holds the control socket)
            ◀── {"type":"revoke"} ── on revoke/expiry → exit stops traffic

(exit --migrate, on call stall)
exit ──GET /exit/nextcall (proof)──▶ accessd ── {callLink} ──▶ start new call
exit ──SubtypeTransportStart (session)──▶ client joins new call
exit ──POST /exit/commitcall (proof)──▶ accessd (promote, free old)
```
