package tgapi

import (
	"net/http"
	"testing"
	"time"
)

func TestNewClient_Defaults(t *testing.T) {
	c := NewClient("123:ABC")

	if c.token != "123:ABC" {
		t.Errorf("token = %q, want %q", c.token, "123:ABC")
	}
	if c.base != DefaultBaseURL {
		t.Errorf("base = %q, want %q", c.base, DefaultBaseURL)
	}
	if c.http == nil {
		t.Fatal("http client is nil")
	}
	if c.http.Timeout != DefaultHTTPTimeout {
		t.Errorf("http.Timeout = %v, want %v", c.http.Timeout, DefaultHTTPTimeout)
	}
}

func TestNewClient_Options(t *testing.T) {
	custom := &http.Client{Timeout: time.Second}

	c := NewClient("x",
		WithBaseURL("http://test.invalid"),
		WithHTTPClient(custom),
	)

	if c.base != "http://test.invalid" {
		t.Errorf("WithBaseURL ignored: base = %q", c.base)
	}
	if c.http != custom {
		t.Errorf("WithHTTPClient ignored")
	}
}

func TestNewClient_OptionsOrder(t *testing.T) {
	// Later options should win on conflict.
	a := &http.Client{Timeout: time.Second}
	b := &http.Client{Timeout: 2 * time.Second}

	c := NewClient("x", WithHTTPClient(a), WithHTTPClient(b))
	if c.http != b {
		t.Errorf("last option should win")
	}
}
