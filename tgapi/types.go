package tgapi

// User is a minimal Telegram User object, carrying only the fields used
// by the Managed Bots workflow.
//
// Reference: https://core.telegram.org/bots/api#user
type User struct {
	// ID is the Telegram user / bot identifier (a snowflake int64).
	ID int64 `json:"id"`

	// IsBot is true when the User represents a bot account.
	IsBot bool `json:"is_bot"`

	// FirstName is the display name; required by Telegram.
	FirstName string `json:"first_name"`

	// Username is the @-handle without the leading '@'. Optional for
	// users but always present for bots.
	Username string `json:"username,omitempty"`

	// CanManageBots reports whether this bot has "Bot Management Mode"
	// enabled in BotFather. Present only in getMe responses for bots.
	CanManageBots bool `json:"can_manage_bots,omitempty"`

	// CanReadAllGroupMessages is true when Privacy Mode is DISABLED.
	// A true value means the bot receives every message in groups it
	// belongs to, not just commands and explicit @mentions.
	CanReadAllGroupMessages bool `json:"can_read_all_group_messages,omitempty"`

	// CanJoinGroups reports whether users can add this bot to groups.
	CanJoinGroups bool `json:"can_join_groups,omitempty"`
}

// WebhookInfo describes the current webhook configuration of a bot.
// Used by reconciliation to detect webhook hijacking (a manager-managed
// bot whose webhook has been changed outside the Alva-side runtime).
//
// Reference: https://core.telegram.org/bots/api#webhookinfo
type WebhookInfo struct {
	// URL is the currently configured webhook URL. Empty when the bot
	// is not using a webhook (i.e. the caller is expected to long-poll).
	URL string `json:"url"`

	// HasCustomCertificate is true when the webhook was configured with
	// a self-signed certificate.
	HasCustomCertificate bool `json:"has_custom_certificate"`

	// PendingUpdateCount is the number of updates awaiting delivery.
	PendingUpdateCount int `json:"pending_update_count"`

	// AllowedUpdates lists the update types the webhook is configured
	// to receive. Nil means "all except chat_member", matching the
	// Telegram default.
	AllowedUpdates []string `json:"allowed_updates,omitempty"`
}

// Update represents a single incoming Telegram update. Only the
// managed-bot-relevant fields are modeled; other update kinds
// (edited_message, callback_query, etc.) unmarshal into zero values.
//
// Reference: https://core.telegram.org/bots/api#update
type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message,omitempty"`
}

// Message is a narrow Telegram Message object carrying only the fields
// the Managed Bots workflow consults. Notably, [Message.ManagedBotCreated]
// is populated for the service message announcing a new managed bot.
//
// Reference: https://core.telegram.org/bots/api#message
type Message struct {
	MessageID int64 `json:"message_id"`

	// From is the sender; absent for messages sent via channels or
	// some service messages.
	From *User `json:"from,omitempty"`

	// Chat is the conversation the message belongs to.
	Chat *Chat `json:"chat,omitempty"`

	// Date is the Unix timestamp the message was sent at.
	Date int64 `json:"date"`

	// ManagedBotCreated is set on the service message delivered to a
	// manager bot when one of its users created a managed bot.
	//
	// Reference: https://core.telegram.org/bots/api#managedbotcreated
	ManagedBotCreated *ManagedBotCreated `json:"managed_bot_created,omitempty"`
}

// Chat is a minimal Telegram Chat object.
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// ManagedBotCreated is the payload of the managed_bot_created service
// message delivered to a manager bot when one of its users confirms a
// Managed Bot creation dialog.
//
// The Telegram client may alter the bot's username / display name during
// the confirmation dialog, so callers should treat [ManagedBotCreated.Bot]
// as the authoritative identity and must not assume it matches the
// suggested_username supplied in the original deep link.
//
// Reference: https://core.telegram.org/bots/api#managedbotcreated
type ManagedBotCreated struct {
	Bot User `json:"bot"`
}
