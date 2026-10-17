# access — OpenFlux access-key control plane

Issue time-limited, per-user OpenFlux access keys from a static Telemost
call pool, and enforce TTL + instant revocation on the exit node.

Spec: [`docs/superpowers/specs/2026-10-16-access-key-service-design.md`](../docs/superpowers/specs/2026-10-16-access-key-service-design.md).
This package is subsystem 1 of 4 (control plane + exit enforcement + key
issuance). The pool orchestrator, Telegram bot and client key-field are
separate plans.

**API reference:** [`docs/api.md`](../docs/api.md) documents every endpoint
(inputs, outputs, formats, auth) and the WebSocket + `openflux://` key
protocols; [`docs/openapi.yaml`](../docs/openapi.yaml) is the OpenAPI 3.0 spec
for the HTTP endpoints (loads in Swagger UI).

## Pieces

- `access` — key store, `openflux://` key builder, control server (admin API
  + exit WebSocket hub), fail-closed exit license client.
- `cmd/accessd` — the control daemon.
- `cmd/accessctl` — a CLI over the admin API.

## Run it

1. Create a pool file, one Telemost call link per line (`#` comments and
   blank lines are ignored):

   ```
   # pool.txt
   https://telemost.yandex.ru/j/1111111111
   https://telemost.yandex.ru/j/2222222222
   ```

2. Start the daemon:

   ```bash
   go run ./cmd/accessd --store accessd-store.json --pool pool.txt \
     --admin 127.0.0.1:8099 --exit :8098
   ```

   The admin API binds to loopback only; the exit hub listens on `:8098` for
   exit nodes.

3. Issue a key (prints the `openflux://` link — it carries the encryption
   secret, so treat it like a password):

   ```bash
   go run ./cmd/accessctl issue --admin 127.0.0.1:8099 --days 30
   ```

4. Start an exit node bound to that key id (its id is printed by `issue`):

   ```bash
   openflux --role=exit --mode=l4 --transport=telemost \
     --url="<the pool call link assigned to this key>" \
     --encryption-key-file=channel.key \
     --license-server=ws://127.0.0.1:8098/exit/ws --license-key-id=<id>
   ```

   The exit holds a WebSocket to the daemon and stops carrying traffic the
   moment the key is revoked or expires, or if it loses the control
   connection (fail-closed).

5. Revoke or extend:

   ```bash
   go run ./cmd/accessctl revoke --admin 127.0.0.1:8099 --id <id>
   go run ./cmd/accessctl extend --admin 127.0.0.1:8099 --id <id> --days 7
   go run ./cmd/accessctl list   --admin 127.0.0.1:8099
   ```

The buyer pastes the `openflux://` key into the OpenFlux client, which joins
the call and connects with no contact to this server.

## Tests

```bash
go test ./access/...                       # unit tests
go test -tags smoke ./access/testdata/     # issue → connect → revoke flow (no live Telemost)
```

Running the full tunnel in Docker against a live Telemost call — warm pool,
container lifecycle, in-session call migration — is covered by the pool
orchestrator (Plan 2), not this subsystem.
