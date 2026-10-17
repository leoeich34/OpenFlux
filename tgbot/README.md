# tgbot — Telegram operator bot

`accessbot` lets the operator run the access-key service from Telegram: issue,
revoke, extend and list keys by chatting with the bot. It is a thin front end
over the loopback [Admin API](../docs/api.md) — it performs no action a
non-allowlisted user asks for, and takes **no payment** (payment is handled out
of band; you issue a key once a buyer has paid you however you like).

Spec: [`docs/superpowers/specs/2026-10-16-access-key-service-design.md`](../docs/superpowers/specs/2026-10-16-access-key-service-design.md).
Subsystem 3 of 4. Pure Go, no Telegram library.

## Setup

1. **Create the bot** with [@BotFather](https://t.me/BotFather) (`/newbot`) and
   copy the token.
2. **Find your admin user id** — message [@userinfobot](https://t.me/userinfobot)
   (or any "what's my id" bot). Collect every operator's numeric id.
3. **Run the bot on the same host as `accessd`** (it reaches the Admin API on
   loopback):

   ```bash
   go run ./cmd/accessbot \
     --token "123456:ABC-DEF..." \
     --admin 127.0.0.1:8099 \
     --admins 11111111,22222222
   ```

   `--token` can come from `TELEGRAM_TOKEN` and `--admins` from
   `ACCESSBOT_ADMINS` instead of flags. The bot refuses to start without a
   token or without at least one admin id.

## Commands

| Command | Action |
|---|---|
| `/issue [days]` | issue a new key (default 30 days); replies with the id and the `openflux://` key |
| `/revoke <id>` | revoke a key immediately |
| `/extend <id> <days>` | extend a key's validity |
| `/list` | list all keys (id, status, expiry) |
| `/help` | show the command list |

A non-admin gets "not authorized" and no action runs. A malformed command gets
a usage hint and makes no Admin API call.

## Security notes

- **The issued key is a credential** — it contains the per-user secret. The bot
  sends it only to the requesting admin's chat and never logs it; forward it to
  the buyer over a channel you trust.
- Keep the bot token secret; pass it via env or a flag, not in a committed file.
- The bot talks to `accessd` on loopback — do not expose the Admin API off-host.

## Tests

```bash
go test ./tgbot/... ./cmd/accessbot/...
```

All unit-tested against fakes and `httptest`; no test contacts Telegram or a
live `accessd`.
