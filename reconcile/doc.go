// Package reconcile detects drift between a stored Managed Bot record
// and the bot's actual state as reported by the Telegram Bot API.
//
// The package is pure: no I/O, no locking, no logging. Callers are
// responsible for:
//
//   - Gathering [Observed] data by calling the bot's getMe and
//     getWebhookInfo endpoints (typically through
//     [github.com/alva-ai/tg-managed-bot-go/tgapi.Client]).
//   - Loading the corresponding [State] from their persistence layer.
//   - Acting on each returned [Drift] (notify the user, mark deleted,
//     reset webhook, etc.).
//
// This separation keeps [CheckDrift] trivially testable via tables and
// lets callers reuse the logic regardless of where they run it:
// cron job, request-time check, admin tool, etc.
//
// # Drift semantics
//
// Drift is reported for the following conditions:
//
//   - [DriftDeleted]     bot was deleted via @BotFather (ErrBotDeactivated)
//   - [DriftTokenRotated] token is invalid (ErrUnauthorized) — typically
//     because the user ran /token in @BotFather or
//     [tgapi.Client.ReplaceManagedBotToken] was called elsewhere
//   - [DriftUnreachable] transport-level failure; state is unknown
//   - [DriftWebhookHijacked] actual webhook differs from the expected
//     URL configured by the caller
//   - [DriftPrivacyRegression] bot's group-privacy setting differs from
//     expected — typically the user re-enabled privacy via @BotFather
//   - [DriftUsernameChanged] bot's @-handle changed (user ran /setusername)
//
// When getMe fails with a terminal signal ([DriftDeleted] or
// [DriftTokenRotated]), subsequent checks are skipped because they
// cannot produce actionable information.
package reconcile
