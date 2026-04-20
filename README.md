# tg-managed-bot-go

> Go toolkit for Telegram **Bot API 9.6 Managed Bots** — zero-copy-paste bot
> provisioning via the `t.me/newbot/...` deep link protocol.

[![Go Reference](https://pkg.go.dev/badge/github.com/alva-ai/tg-managed-bot-go.svg)](https://pkg.go.dev/github.com/alva-ai/tg-managed-bot-go)
![Status](https://img.shields.io/badge/status-alpha-orange)
![Go](https://img.shields.io/badge/go-1.25-blue)

**Status: alpha, v0.0.1.** API may change before v0.1.

## What this is

A narrow-scope toolkit for the **pairing + drift-detection** parts of a
Telegram Managed Bots integration. You bring your own Bot API client
(we don't force a choice — works with
[go-telegram/bot](https://github.com/go-telegram/bot),
[mymmrac/telego](https://github.com/mymmrac/telego),
[pengrad/telegram-bot-api](https://github.com/pengrad/telegram-bot-api),
or raw `net/http`).

## What this is not

- Not a general Bot API client. Go already has several excellent ones.
- Not a full multi-tenant bot runtime. The storage, crypto, webhook
  routing, and scheduling bits are deliberately left as integration
  seams — implement them against your own infra.

## Why Managed Bots?

Telegram Bot API 9.6 (April 2026) introduced
[Managed Bots](https://core.telegram.org/bots/features#managed-bots): a
manager bot can programmatically mint child bots on a user's behalf. The
flow is:

```
User taps deep link  →  Telegram shows "Create Bot" confirm dialog
                    →  child bot is created, owned by the user
                    →  manager bot receives managed_bot_created update
                    →  manager bot calls getManagedBotToken
                    →  manager bot forwards token back to the originating client
```

No `@BotFather /newbot`, no token copy-paste. This package implements the
**nonce-based handoff protocol** first demonstrated by
[hermes-agent #10591](https://github.com/NousResearch/hermes-agent/issues/10591).

## Packages

| Package | Purpose |
|---|---|
| [`link`](./link) | Build `https://t.me/newbot/...` deep links; validate & sanitize bot usernames. Pure functions, no I/O. |
| [`nonce`](./nonce) | Generate and parse pairing nonces (Crockford base32). Pure functions. |
| [`pairing`](./pairing) | HTTP-less `Store` interface for the token handoff protocol, plus an in-memory implementation for tests. Bring your own Redis / KV / Postgres. |
| `manager` | *(planned)* Handler for `managed_bot_created` updates: extract nonce → fetch token → push to pairing store → DM fallback. |
| `tgapi` | *(planned)* Minimal Bot API client covering just `getManagedBotToken`, `replaceManagedBotToken`, `sendMessage`, `getMe`, `getWebhookInfo`. |
| `reconcile` | *(planned)* Pure drift-detection helpers: given `stored BotState` + `current BotInfo`, return list of `Drift` events (`TokenRotated`, `WebhookHijacked`, `PrivacyRegression`, `Deleted`). |

## Quick sketch

```go
import (
    "context"
    "time"

    "github.com/alva-ai/tg-managed-bot-go/link"
    "github.com/alva-ai/tg-managed-bot-go/nonce"
    "github.com/alva-ai/tg-managed-bot-go/pairing"
)

func handleConnectRequest(ctx context.Context, store pairing.Store) (string, error) {
    // 1. Generate a nonce and stash it in the pairing store.
    n, err := nonce.New()
    if err != nil {
        return "", err
    }
    if err := store.Put(ctx, n, 15*time.Minute); err != nil {
        return "", err
    }

    // 2. Return a deep link with the nonce embedded in the suggested username.
    return link.BuildNewBot(link.Options{
        ManagerBotUsername: "alva_manager_bot",
        SuggestedUsername:  "alva_" + n + "_bot",
        SuggestedName:      "My Alva",
    })
}

// Later, in your manager bot's managed_bot_created handler:
func onManagedBotCreated(ctx context.Context, store pairing.Store,
    botUsername, childToken string) {

    n, ok := nonce.Extract(botUsername, "alva")
    if !ok {
        // User edited the suggested username; fall back to DMing them.
        return
    }
    _ = store.Complete(ctx, n, childToken, botUsername)
}

// Client polls:
//   entry, err := store.FetchAndDelete(ctx, n)
//   // entry.Token is the child bot token, retrievable exactly once.
```

## Design notes

- **Storage is pluggable.** `pairing.Store` is an interface with three
  methods (`Put`, `Complete`, `FetchAndDelete`). Implement it against
  whatever you have — Redis, Cloudflare KV, Postgres, in-memory, DynamoDB.
- **`FetchAndDelete` must be atomic.** The token is a bearer credential;
  concurrent fetches must return exactly one success and the rest must
  miss. The in-memory impl satisfies this via `sync.Mutex`.
- **Nonce alphabet is Crockford base32.** No `I/L/O/U` to avoid
  visual ambiguity. Generated nonces are lowercase. 12 chars ≈ 60 bits
  of entropy.
- **Nonce is embedded in the bot username.** This avoids a separate
  lookup table. If the user edits the suggested username during the
  confirm dialog, `nonce.Extract` returns `ok=false` — handle this with
  a DM fallback (see `manager` package, planned).

## Running tests

```
go test ./...
go test -cover ./...
```

Current coverage:
- `link` — 81%
- `nonce` — 89%
- `pairing` — 100%

## Project status & roadmap

**v0.0.1 (this release):** link + nonce + pairing.Store interface + memory impl.

**Planned v0.1:**
- `pairing/server` — HTTP handler for `POST /pair`, `PUT /pair/{nonce}`,
  `GET /pair/{nonce}` (can be mounted on any Go HTTP mux)
- `pairing/stores/redis` — Redis-backed implementation using Lua script
  for atomic `FetchAndDelete`
- `tgapi` — minimal Bot API client (3 methods)
- `manager` — update handler + pluggable `Pusher` interface
- `reconcile` — drift detection state machine

**Phase 2 (nice-to-have):**
- Cloudflare Workers / Deno Deploy compatible pairing server
- Prometheus metrics adapter
- Webhook signature verification helpers

## Credits

The pairing protocol is modeled on
[NousResearch/hermes-agent #10591](https://github.com/NousResearch/hermes-agent/issues/10591)
(teknium1). This package is a Go port of the same design, generalized to
be embeddable in any Go application (not just CLI tools).

## License

MIT — see [LICENSE](./LICENSE).
