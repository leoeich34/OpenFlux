# OpenFlux Access-Key Service — Design

Status: approved for planning (2026-10-16)
Scale target (initial): ~10 concurrent users, single Docker host.

## Intent and why

Sell access to OpenFlux as a paid product. A buyer receives an **access key**,
pastes it into the OpenFlux app, and the app connects — with no account, no
manual config, and crucially **without first contacting our server**. The
seller can issue, time-limit and instantly revoke keys from a Telegram bot.

The hard constraint that shapes everything: the client is, by definition, on a
network where the open internet is restricted — that is why they use OpenFlux.
So the client **cannot depend on reaching our server to connect**. Only the
transport Telemost itself relies on (Yandex endpoints) is assumed reachable.
Therefore the key must be self-sufficient for the first connection, and all
access control lives on the exit node, which always has real internet.

## How it works (the model that resolves the bootstrap problem)

Two things are deliberately separated:

1. **Connecting** needs no server. The key is a self-contained `openflux://`
   link (existing format, `share` package): it carries **one** Telemost call
   link, a per-user encryption secret, and a user id. The app joins that call
   and brings up the tunnel using only Yandex reachability.
   **Call-death recovery is in-session:** if the bound call degrades or dies
   while the tunnel is up, the exit hands the client a fresh call link from the
   pool over the existing tunnel and both migrate — seamless while the tunnel
   lives. If the call dies before migration completes (rare), the user
   reconnects; a reconnect may need a fresh key/link, which is the accepted
   cost of keeping the common path server-free.
2. **Access control** lives on the **exit node**, which has internet. The exit
   holds a live connection to the control server and refuses to carry traffic
   for a key that is expired or revoked. Enforcement happens on the side that
   has connectivity, never on the blocked client.

### End-to-end flow

- **Issue (seller, via bot):** control server allocates a call from the static
  pool, binds an exit container to that call, generates a per-user secret, and
  returns the `openflux://` key (+ QR).
- **Connect (buyer):** app reads the key, joins the single embedded call with
  the secret, brings up the tunnel. No contact with our server. If that call
  later fails, the exit migrates the client to a fresh call in-session.
- **Authorize (exit):** the exit bound to that call checks the arriving peer's
  user id / secret against the control server over its live connection; if the
  key is valid it carries traffic, otherwise it does not.
- **Revoke (seller, via bot):** control server pushes a revoke for that user id
  over the live connection; the exit tears the session down immediately
  (sub-second, bounded by RTT to the exit). The call returns to the pool.

## Components

1. **Control server** (Go, single process):
   - Key store: `id`, `secret`, `status` (active/revoked/expired), `expiresAt`,
     assigned `callLink`, assigned `exitContainer`.
   - Exit-facing live channel (WebSocket): pushes revoke, receives heartbeats,
     answers authorize checks.
   - Admin API (local/loopback or token-auth) consumed by the Telegram bot.
2. **Pool orchestrator** (same process):
   - Static, operator-provided list of ~12–15 pre-created Telemost call links
     in config (no programmatic conference creation at this scale).
   - Manages exit containers via the Docker API; keeps 2–3 warm (pre-joined to
     a call) because joining a call takes ~30–40 s.
   - Maps key ↔ call ↔ exit container; recycles calls/containers on
     revoke/expiry.
3. **Exit node** (modification of the existing OpenFlux `--role=exit`):
   - Opens and maintains a live connection to the control server.
   - Authorizes the peer's user id/secret before carrying traffic.
   - Instant teardown on a revoke push.
   - Self-disables if it loses the control connection beyond a heartbeat
     window (so an isolated, revoked exit cannot keep serving).
4. **Key generator:** builds the `openflux://` key (one call link + secret +
   user id) using the existing `share` / `--make-link` machinery.
5. **Telegram bot (operator console):** commands `issue` (with TTL), `revoke`,
   `list active`, `extend`. Talks only to the admin API. **No payment handling
   — payment is out of band.** The bot runs on normal internet; the blocked-
   network problem does not apply to it.
6. **Client:** the existing OpenFlux client (`mobile/`, `ios-app/`). The only
   UX addition is a field to paste the access key; no server calls added.

## Goals

- Connect with zero dependence on our server reachability from the client.
- Per-user secret (not one shared key for everyone).
- TTL and **instant** revocation, enforced exit-side.
- Operator issues/revokes keys from Telegram.
- Runs on one Docker host for ~10 concurrent users.

## Non-goals (initial)

- Programmatic Telemost conference creation (use a static, hand-made pool).
- Automated payments / self-serve purchase in the bot.
- Multi-host / horizontal scaling, HA, load balancing.
- Transport abstraction beyond Telemost (see "left to the builder" for the
  recommended seam, but `direct`/VPS fallback is not built now).
- Changes to the throughput ceiling (the shipped slot fix stands; ~2 Mbit/s
  per stream is a separate, already-analyzed problem).

## Anti-goals (these make it a failure even if it "works")

- A client that cannot connect when our control server is down or unreachable.
- Revocation that only takes effect at the next connection (must be instant).
- One shared secret across users (no real revocation or per-user accounting).
- A ghost participant from a hard-killed exit blocking the next user's call.

## Constraints

- Client assumes only Yandex/Telemost reachability, nothing else.
- One call holds exactly one client↔exit pair; extra participants break
  pairing (observed). Therefore one key = one call = one concurrent session.
- Joining a Telemost call takes ~30–40 s → warm pool required.
- The key carries the encryption secret → it is sensitive, treat like a
  password; deliver only over the bot's private chat.
- Exit containers need the Docker API and run a gVisor L4 stack each.

## Decisions made during design (operator's calls)

- Scale: ~10 concurrent users, single Docker host.
- Static pre-created call pool (no Yandex-account automation now).
- Telegram bot is an operator console only; payment is manual/out-of-band.
- Revocation must be instant (live push to exit + heartbeat self-disable).
- Build in the fork `leoeich34/OpenFlux`, branch `access-key-service-design`.
- **One call link per key**, with in-session migration to a fresh pool call on
  call death (not multiple backup links in the key, not a server round-trip on
  reconnect).
- **Builder-check call (minor):** an exit container is pre-bound to one key at
  issue time, so it serves only the call+secret it was given. "Authorize" is
  therefore just "is my bound key still active/unexpired?" — the exit does not
  need to identify arbitrary users; the per-user secret is what lets only the
  intended client bring up the tunnel on that call.

## Open risks to verify before production (not blockers for building)

- **Yandex account limits:** whether one account's links sustain ~12–15
  concurrent conferences. Verify empirically; if limited, shrink the pool or
  spread across accounts.
- **Host load:** ~10 active sessions = up to ~10 calls, each with client+exit
  lanes (dozens of WebRTC peer connections + gVisor stacks). Measure CPU/
  bandwidth on the target box.
- **ToS exposure:** using Telemost as a bulk relay for paid traffic runs
  against Yandex's terms and can be throttled or cut at any time. A `direct`/
  VPS transport as the primary channel (Telemost as fallback) is the durable
  path; recommended but out of scope now.

## Left to the builder

- Exact control-server ↔ exit protocol (message shapes for authorize,
  heartbeat, revoke) and the heartbeat/self-disable timings.
- Admin API surface and auth between the bot and the control server.
- Persistence choice for the key store (SQLite/BoltDB/file) — small, single
  process; pick the simplest durable option.
- Docker API usage (library vs CLI shell-out) and container lifecycle details.
- Where exit-side authorization hooks into the existing session/transport
  startup, and how the exit learns the peer's user id (carried in the
  negotiated session vs. a control-channel handshake).
- The in-session call-migration protocol: how the exit signals a new call link
  to the client over the live tunnel, how both re-join without dropping TCP
  flows, and the trigger (loss/stall thresholds) for migrating.
- Whether to introduce a thin "transport provider" seam now so a future
  `direct`/VPS channel drops in without reworking the key format.
- Telegram bot framework and deployment.
