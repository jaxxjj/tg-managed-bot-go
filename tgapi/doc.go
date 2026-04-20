// Package tgapi is a minimal Telegram Bot API client scoped to the
// Managed Bots workflow introduced in Bot API 9.6.
//
// # Scope
//
// Only five Bot API methods are implemented, each chosen because it is
// required to operate a Managed Bot pairing + reconciliation loop:
//
//   - [Client.GetMe] — verify token & read bot metadata
//     (e.g. CanReadAllGroupMessages for privacy drift detection)
//   - [Client.GetManagedBotToken] — retrieve a child bot's token
//     from its manager
//   - [Client.ReplaceManagedBotToken] — rotate a managed bot's token
//   - [Client.GetWebhookInfo] — detect webhook hijacking during reconcile
//   - [Client.SendMessage] — DM fallback when a manager cannot match a
//     freshly-created child bot to its originating pairing
//
// For general Bot API coverage (sending photos, inline queries, keyboards,
// etc.) use a full client library such as go-telegram/bot, telegraf, or
// pengrad/telegram-bot-api. This package is deliberately narrow so that
// importing it adds no transitive Telegram-SDK dependency.
//
// # Non-goals
//
//   - Long-polling loops. Callers plug [Update] bytes in themselves.
//   - Webhook server. See package
//     [github.com/alva-ai/tg-managed-bot-go/pairing/server] for the
//     pairing-specific HTTP surface.
//   - Parsing or dispatching updates beyond the types required for
//     Managed Bot events. [Update] and [Message] deliberately expose
//     only the fields consumed by the Managed Bot flow.
//
// # Example
//
//	c := tgapi.NewClient(os.Getenv("MANAGER_BOT_TOKEN"))
//	me, err := c.GetMe(ctx)
//	if err != nil {
//	    return err
//	}
//	if !me.CanManageBots {
//	    return errors.New("manager bot mode is not enabled in @BotFather")
//	}
package tgapi
