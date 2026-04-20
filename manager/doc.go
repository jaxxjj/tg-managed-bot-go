// Package manager drives the manager-bot half of the Managed Bots
// pairing protocol. It owns a single entry point, [Handler.HandleUpdate],
// that consumes a [tgapi.Update] and — if the update is a
// managed_bot_created service message — completes the pairing by:
//
//  1. Extracting the nonce from the newly-created bot's username.
//  2. Fetching the child bot's token via [Client.GetManagedBotToken].
//  3. Writing it into the pairing [pairing.Store] via Complete.
//
// When nonce correlation fails (user edited the suggested username) or
// the pairing entry has expired, [Handler] falls back to DMing the
// creator so they can retry.
//
// # Dependencies
//
//   - github.com/alva-ai/tg-managed-bot-go/pairing   (Store interface)
//   - github.com/alva-ai/tg-managed-bot-go/nonce     (Extract)
//   - github.com/alva-ai/tg-managed-bot-go/tgapi     (Update / ManagedBotCreated types)
//   - github.com/rs/zerolog                          (structured logs)
//
// The [Client] interface — rather than a concrete [tgapi.Client] —
// is used so callers can plug in their own implementation (retries,
// metrics, rate limits, mocks).
//
// # Typical wiring
//
//	h := &manager.Handler{
//	    Store:  store,
//	    API:    tgapi.NewClient(os.Getenv("MANAGER_BOT_TOKEN")),
//	    Prefix: "alva",
//	}
//	for update := range incoming {
//	    if err := h.HandleUpdate(ctx, &update); err != nil {
//	        log.Err(err).Msg("handle update failed")
//	    }
//	}
package manager
