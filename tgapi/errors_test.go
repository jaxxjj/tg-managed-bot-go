package tgapi

import (
	"errors"
	"fmt"
	"testing"
)

func TestAPIError_Error(t *testing.T) {
	cases := []struct {
		err  *APIError
		want string
	}{
		{&APIError{Code: 401, Description: "Unauthorized"}, "tgapi: 401 Unauthorized"},
		{&APIError{Code: 403, Description: "Forbidden: user is deactivated"}, "tgapi: 403 Forbidden: user is deactivated"},
		{&APIError{Code: 429, Description: "Too Many Requests: retry after 5"}, "tgapi: 429 Too Many Requests: retry after 5"},
		{&APIError{Code: 0, Description: ""}, "tgapi: 0 "},
	}
	for _, c := range cases {
		if got := c.err.Error(); got != c.want {
			t.Errorf("Error() = %q, want %q", got, c.want)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		code int
		desc string
		want error
	}{
		{"401 any description", 401, "Unauthorized", ErrUnauthorized},
		{"403 user is deactivated", 403, "Forbidden: user is deactivated", ErrBotDeactivated},
		{"403 deactivated lowercase", 403, "user is deactivated", ErrBotDeactivated},
		{"403 other kicked", 403, "Forbidden: bot was kicked from the channel", nil},
		{"403 other blocked", 403, "Forbidden: bot was blocked by the user", nil},
		{"429 rate limited", 429, "Too Many Requests: retry after 5", ErrTooManyRequests},
		{"400 no sentinel", 400, "Bad Request: chat not found", nil},
		{"500 no sentinel", 500, "Internal Server Error", nil},
		{"0 empty", 0, "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classify(c.code, c.desc)
			if !errors.Is(got, c.want) {
				t.Errorf("classify(%d, %q) = %v, want %v", c.code, c.desc, got, c.want)
			}
		})
	}
}

func TestIsPredicates(t *testing.T) {
	type probe struct {
		name  string
		err   error
		want  bool
		check func(error) bool
	}
	cases := []probe{
		{"IsUnauthorized direct", ErrUnauthorized, true, IsUnauthorized},
		{"IsUnauthorized wrapped", fmt.Errorf("call: %w", ErrUnauthorized), true, IsUnauthorized},
		{"IsUnauthorized unrelated", errors.New("x"), false, IsUnauthorized},
		{"IsUnauthorized nil", nil, false, IsUnauthorized},

		{"IsBotDeactivated direct", ErrBotDeactivated, true, IsBotDeactivated},
		{"IsBotDeactivated wrapped", fmt.Errorf("call: %w", ErrBotDeactivated), true, IsBotDeactivated},
		{"IsBotDeactivated unrelated", ErrUnauthorized, false, IsBotDeactivated},
		{"IsBotDeactivated nil", nil, false, IsBotDeactivated},

		{"IsTooManyRequests direct", ErrTooManyRequests, true, IsTooManyRequests},
		{"IsTooManyRequests wrapped", fmt.Errorf("call: %w", ErrTooManyRequests), true, IsTooManyRequests},
		{"IsTooManyRequests unrelated", ErrBotDeactivated, false, IsTooManyRequests},
		{"IsTooManyRequests nil", nil, false, IsTooManyRequests},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.check(c.err); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}
