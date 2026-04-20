// Package server implements the HTTP surface of the Managed Bot pairing
// protocol — three stdlib-net/http handlers that plug into any
// [*http.ServeMux] (Go 1.22+) or any router that accepts
// [http.HandlerFunc]:
//
//	POST   /pair              client registers a nonce, receives deep link
//	PUT    /pair/{nonce}      manager bot posts token (requires auth)
//	DELETE /pair/{nonce}      client consumes the token; atomic + one-time
//
// The consume endpoint is DELETE rather than GET (a departure from
// the hermes-agent Cloudflare Worker reference implementation) because
// RFC 9110 defines GET as a safe, cacheable, idempotently-retriable
// method. Consuming a one-time bearer token is none of those — a
// browser, CDN, or proxy that speculatively retries a GET would
// destroy the token before the real client sees the 200. DELETE has
// the right HTTP semantics: intermediaries do not retry, the caller
// gets exactly one successful response, and second DELETE calls
// return 404 as expected.
//
// Every response carries Cache-Control: no-store — the tokens and nonces
// flowing through this endpoint are single-use bearer credentials and
// must not be cached by browsers, proxies, or CDNs.
//
// # Dependencies
//
// The package depends only on the standard library and
// [github.com/rs/zerolog]. It uses Go 1.22+ [*http.ServeMux] path
// patterns; no third-party router is required.
//
// # Usage
//
//	mux := http.NewServeMux()
//	err := server.Mount(mux, server.Config{
//	    Store:              redisStore,
//	    ManagerBotUsername: "alva_manager_bot",
//	    NoncePrefix:        "alva",
//	    SuggestedName:      "My Alva",
//	    PairingTTL:         15 * time.Minute,
//	    Authenticator:      server.BearerAuth("secret"),
//	}, "/api/v1")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	http.ListenAndServe(":8080", mux)
//
// # Observability
//
// Handlers emit structured log events via [zerolog.Ctx]. Callers wire
// their global or per-request logger via context (see zerolog docs on
// context-based loggers). If no logger is set in the request context,
// the global [zerolog.DefaultContextLogger] is used.
package server
