package reconcile

import (
	"fmt"

	"github.com/alva-ai/tg-managed-bot-go/tgapi"
)

// State is the subset of a stored Managed Bot record [CheckDrift] compares
// against live observations. Callers populate this from their persistence
// layer (DB, cache, etc.).
type State struct {
	// BotID is the Telegram user-id of the managed bot. Included in
	// [Drift.Detail] strings for diagnostics; not used for comparison.
	BotID int64

	// ExpectedWebhookURL is the URL the caller set via setWebhook.
	// Empty means "don't check webhook drift".
	ExpectedWebhookURL string

	// ExpectPrivacyOff is the caller's desired privacy state:
	//   true  = Privacy Mode disabled  (bot reads all group messages)
	//   false = Privacy Mode enabled   (bot reads only mentions/commands)
	// Only compared when a successful getMe observation is available.
	ExpectPrivacyOff bool

	// LastKnownUsername is the most recently observed @-handle (without
	// '@'). Empty means "don't check username drift" (e.g. immediately
	// after creation before the first getMe).
	LastKnownUsername string
}

// Observed holds the results of live Bot API calls for one bot.
//
// A caller wiring this to [tgapi.Client] typically populates both the
// Me/GetMeErr and Webhook/WebhookErr pairs from GetMe and GetWebhookInfo.
// Either half may be omitted (Err non-nil and struct nil) without
// affecting the other half's drift detection.
type Observed struct {
	Me       *tgapi.User
	GetMeErr error

	Webhook    *tgapi.WebhookInfo
	WebhookErr error
}

// DriftKind enumerates the discrete drift conditions detectable from a
// (State, Observed) pair. The zero value [DriftNone] never appears in a
// [Drift] — it is reserved as a sentinel for "no drift".
type DriftKind int

// Drift kinds. See the package doc for the semantics of each.
const (
	DriftNone DriftKind = iota
	DriftDeleted
	DriftTokenRotated
	DriftUnreachable
	DriftWebhookHijacked
	DriftPrivacyRegression
	DriftUsernameChanged
)

// String returns a stable identifier for each kind, suitable for log
// labels and metric dimensions.
func (k DriftKind) String() string {
	switch k {
	case DriftNone:
		return "none"
	case DriftDeleted:
		return "deleted"
	case DriftTokenRotated:
		return "token_rotated"
	case DriftUnreachable:
		return "unreachable"
	case DriftWebhookHijacked:
		return "webhook_hijacked"
	case DriftPrivacyRegression:
		return "privacy_regression"
	case DriftUsernameChanged:
		return "username_changed"
	default:
		return fmt.Sprintf("drift(%d)", k)
	}
}

// Drift describes a single drift condition.
type Drift struct {
	Kind   DriftKind
	Detail string
}

// CheckDrift compares a stored [State] to the [Observed] Bot API values
// and returns zero or more [Drift] entries.
//
// The function is pure: no I/O, no side effects, no randomness. It may
// be called from any goroutine and from tests without setup. Output order
// is deterministic (identity, then webhook, then bot-metadata).
//
// Terminal getMe failures ([DriftDeleted], [DriftTokenRotated]) short-
// circuit the bot-metadata comparisons — there is nothing actionable
// to report after "bot deleted". Webhook detection still runs when
// possible because the webhook status is maintained separately by
// Telegram and the two can independently be broken.
func CheckDrift(state State, obs Observed) []Drift {
	var drifts []Drift

	// Stage 1: identity. A terminal identity failure means the rest of
	// the checks cannot produce new information.
	switch {
	case obs.GetMeErr == nil:
		// Healthy getMe; fall through to later stages.
	case tgapi.IsBotDeactivated(obs.GetMeErr):
		drifts = append(drifts, Drift{
			Kind:   DriftDeleted,
			Detail: fmt.Sprintf("bot %d deleted (%v)", state.BotID, obs.GetMeErr),
		})
		return drifts
	case tgapi.IsUnauthorized(obs.GetMeErr):
		drifts = append(drifts, Drift{
			Kind:   DriftTokenRotated,
			Detail: fmt.Sprintf("bot %d token unauthorized (%v)", state.BotID, obs.GetMeErr),
		})
		return drifts
	default:
		drifts = append(drifts, Drift{
			Kind:   DriftUnreachable,
			Detail: fmt.Sprintf("bot %d getMe failed (%v)", state.BotID, obs.GetMeErr),
		})
		// Note: we still fall through to webhook check — the two APIs
		// can fail independently and the webhook signal is worth having.
	}

	// Stage 2: webhook. Only compare when the caller set an expected URL
	// and we actually observed something.
	if state.ExpectedWebhookURL != "" {
		if obs.WebhookErr == nil && obs.Webhook != nil && obs.Webhook.URL != state.ExpectedWebhookURL {
			drifts = append(drifts, Drift{
				Kind: DriftWebhookHijacked,
				Detail: fmt.Sprintf("webhook %q != expected %q",
					obs.Webhook.URL, state.ExpectedWebhookURL),
			})
		}
	}

	// Stage 3: bot metadata. Requires a successful getMe — skip if we
	// already recorded DriftUnreachable from stage 1.
	if obs.Me == nil {
		return drifts
	}

	if obs.Me.CanReadAllGroupMessages != state.ExpectPrivacyOff {
		drifts = append(drifts, Drift{
			Kind: DriftPrivacyRegression,
			Detail: fmt.Sprintf("privacy_off observed=%v expected=%v",
				obs.Me.CanReadAllGroupMessages, state.ExpectPrivacyOff),
		})
	}

	if state.LastKnownUsername != "" && obs.Me.Username != state.LastKnownUsername {
		drifts = append(drifts, Drift{
			Kind: DriftUsernameChanged,
			Detail: fmt.Sprintf("username observed=%q last_known=%q",
				obs.Me.Username, state.LastKnownUsername),
		})
	}

	return drifts
}
