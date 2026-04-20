package tgapi

import (
	"net/http"
	"time"
)

// DefaultBaseURL is the standard Telegram Bot API endpoint. Override
// with [WithBaseURL] for self-hosted Bot API servers
// (https://github.com/tdlib/telegram-bot-api) or tests.
const DefaultBaseURL = "https://api.telegram.org"

// DefaultHTTPTimeout is the timeout applied to the default *http.Client.
// Chosen to absorb the occasional slow round-trip from Telegram while
// still bounding tail latency on reconciliation sweeps.
const DefaultHTTPTimeout = 30 * time.Second

// Client is a minimal, goroutine-safe Telegram Bot API client scoped to
// the methods needed for Managed Bot operation. See the package doc for
// the full scope.
//
// A zero-value Client is not usable; construct one via [NewClient].
//
// Sensitive data handling: the bot token is embedded in every request
// URL as "/bot<TOKEN>/<method>" per the Telegram Bot API contract.
// Callers installing a custom [http.Client] via [WithHTTPClient] — for
// logging, tracing, or metrics middleware — should scrub the URL
// before emitting it anywhere durable (logs, spans) or a token leak
// becomes trivial.
type Client struct {
	token string
	base  string
	http  *http.Client
}

// Option configures a [Client] at construction. See [WithBaseURL] and
// [WithHTTPClient].
type Option func(*Client)

// WithBaseURL overrides the Telegram Bot API base URL. Useful for
// self-hosted Bot API servers and for tests (point at an httptest.Server).
//
// The supplied url must not have a trailing slash; the client appends
// "/bot<token>/<method>" internally.
func WithBaseURL(url string) Option {
	return func(c *Client) { c.base = url }
}

// WithHTTPClient replaces the default *http.Client. Use this to install
// proxies, retry middleware, custom transports, or tighter timeouts.
// The client's Timeout field is respected as-is — the package does not
// impose its own per-request context deadline beyond what the passed
// context.Context carries.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// NewClient constructs a [Client]. The token is the bot authentication
// token, as obtained from @BotFather (for a manager bot) or from a
// previous call to [Client.GetManagedBotToken] (for a managed child bot).
//
// The returned client targets [DefaultBaseURL] and uses an *http.Client
// with [DefaultHTTPTimeout]; override either via options.
func NewClient(token string, opts ...Option) *Client {
	c := &Client{
		token: token,
		base:  DefaultBaseURL,
		http:  &http.Client{Timeout: DefaultHTTPTimeout},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}
