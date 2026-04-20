package manager

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog"

	"github.com/alva-ai/tg-managed-bot-go/nonce"
	"github.com/alva-ai/tg-managed-bot-go/pairing"
	"github.com/alva-ai/tg-managed-bot-go/tgapi"
)

// Client is the subset of Telegram Bot API methods [Handler] needs.
//
// Any implementation satisfying this interface works — a real
// [*tgapi.Client], a retry/metrics wrapper around one, or a test mock.
// Accepting an interface (rather than *tgapi.Client) keeps the
// package mockable and composable without forcing callers to import
// tgapi for mere type-satisfaction.
type Client interface {
	GetManagedBotToken(ctx context.Context, botID int64) (string, error)
	SendMessage(ctx context.Context, chatID int64, text string) error
}

// Handler processes incoming Telegram updates for the manager bot.
//
// A zero-value Handler is not usable; callers populate Store, API, and
// Prefix before invoking [Handler.HandleUpdate].
type Handler struct {
	// Store is the pairing backend (typically the same instance the
	// pairing HTTP server writes to via Put).
	Store pairing.Store

	// API is the Bot API client used to fetch child tokens and send
	// fallback DMs. Must not be nil.
	API Client

	// Prefix is the nonce-in-username prefix; it must match the value
	// used by [github.com/alva-ai/tg-managed-bot-go/pairing/server.Config.NoncePrefix]
	// or nonce extraction fails and every pairing falls back.
	Prefix string

	// OnFallback is invoked when a managed_bot_created event cannot be
	// correlated to a live pairing — either because the user edited
	// the suggested username or because the pairing entry expired
	// before the event arrived.
	//
	// If nil, [Handler] falls back to sending a plain-text DM via
	// API.SendMessage using the default English message. Callers
	// needing localized or silent fallback should set this.
	//
	// The returned error propagates out of HandleUpdate; a nil return
	// means the fallback was handled successfully.
	OnFallback func(ctx context.Context, creatorTgID int64, bot tgapi.User) error
}

// HandleUpdate processes a single Telegram update. Updates other than
// a managed_bot_created service message are no-ops (return nil); this
// lets callers route every inbound update to HandleUpdate without
// pre-filtering.
//
// Transport-level failures from API.GetManagedBotToken or Store.Complete
// return a wrapped error so callers can retry on their own cadence.
// Soft failures — nonce mismatch, expired pairing, bot-already-deleted
// — invoke [Handler.OnFallback] and return its error (nil on success).
//
// Idempotency. Telegram retries webhook deliveries until it receives a
// 2xx, and some deployments run the handler in multiple replicas.
// HandleUpdate is safe to call on the same update more than once:
// [pairing.ErrInvalidState] from Store.Complete (the pairing was already
// Ready) is treated as success, not as a fallback trigger — emitting a
// fallback DM in that case would confuse the user whose first delivery
// succeeded.
func (h *Handler) HandleUpdate(ctx context.Context, u *tgapi.Update) error {
	if u == nil || u.Message == nil || u.Message.ManagedBotCreated == nil {
		return nil
	}

	event := u.Message.ManagedBotCreated.Bot
	var creatorTgID int64
	if u.Message.From != nil {
		creatorTgID = u.Message.From.ID
	}

	logger := zerolog.Ctx(ctx).With().
		Int64("bot_id", event.ID).
		Str("bot_username", event.Username).
		Int64("creator_tg_id", creatorTgID).
		Logger()

	// Stage 1: extract the nonce from the suggested-turned-confirmed username.
	n, ok := nonce.Extract(event.Username, h.Prefix)
	if !ok {
		logger.Info().Msg("manager: nonce extraction failed (user edited username); running fallback")
		return h.runFallback(ctx, creatorTgID, event)
	}

	// Stage 2: fetch the child token.
	token, err := h.API.GetManagedBotToken(ctx, event.ID)
	switch {
	case err == nil:
		// Continue.
	case tgapi.IsBotDeactivated(err):
		// Rare but possible: user confirmed then deleted immediately.
		// Nothing to Complete with; tell them to retry.
		logger.Warn().Err(err).Msg("manager: child deactivated before token fetch; running fallback")
		return h.runFallback(ctx, creatorTgID, event)
	default:
		logger.Error().Err(err).Msg("manager: getManagedBotToken failed")
		return fmt.Errorf("manager: getManagedBotToken(%d): %w", event.ID, err)
	}

	// Stage 3: complete the pairing.
	completeErr := h.Store.Complete(ctx, n, token, event.Username)
	switch {
	case completeErr == nil:
		logger.Info().Str("nonce", n).Msg("manager: pairing completed")
		return nil
	case errors.Is(completeErr, pairing.ErrInvalidState):
		// Pairing is already Ready. Happens when Telegram retries a
		// webhook (no 2xx was observed in time) or two replicas of the
		// handler see the same update. The first delivery succeeded;
		// a fallback DM here would confuse a user who has already been
		// handed their token. Log and return nil — idempotent success.
		logger.Info().Str("nonce", n).Msg("manager: pairing already completed (duplicate delivery); treating as idempotent success")
		return nil
	case errors.Is(completeErr, pairing.ErrNotFound):
		// Pairing expired or was never registered — tell the user to
		// restart the flow. Distinct from ErrInvalidState above: this
		// one genuinely has no prior successful handoff to dedupe against.
		logger.Warn().Err(completeErr).Str("nonce", n).Msg("manager: pairing not found or expired; running fallback")
		return h.runFallback(ctx, creatorTgID, event)
	default:
		logger.Error().Err(completeErr).Str("nonce", n).Msg("manager: store.Complete failed")
		return fmt.Errorf("manager: store.Complete: %w", completeErr)
	}
}

// runFallback dispatches to OnFallback if set, else to the default.
func (h *Handler) runFallback(ctx context.Context, creatorTgID int64, bot tgapi.User) error {
	if h.OnFallback != nil {
		return h.OnFallback(ctx, creatorTgID, bot)
	}
	return h.defaultFallback(ctx, creatorTgID, bot)
}

// defaultFallback sends a generic English DM. Returns nil without doing
// anything when creatorTgID is zero (the Update carried no [tgapi.Message.From]).
func (h *Handler) defaultFallback(ctx context.Context, creatorTgID int64, bot tgapi.User) error {
	if creatorTgID == 0 {
		return nil
	}
	msg := fmt.Sprintf(
		"Your bot @%s was created but we couldn't link it to your session. Please retry the connection flow and the new link will succeed.",
		bot.Username,
	)
	return h.API.SendMessage(ctx, creatorTgID, msg)
}
