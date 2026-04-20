// Minimal Gin HTTP server demonstrating the pairing protocol end-to-end.
//
// Routes:
//
//	POST   /pair                 — client registers a new nonce, receives deep link
//	PUT    /pair/:nonce          — manager bot posts token (auth via Bearer shared secret)
//	DELETE /pair/:nonce          — client consumes; 200 once ready, 404 otherwise (atomic, one-time)
//
// DELETE rather than GET for the consume endpoint: the handler is
// destructive (FetchAndDelete) and a safe-method GET would let browsers,
// CDNs, or proxies speculatively retry and drain the token before the
// real client sees the response. See pairing/server/doc.go for the
// full RFC 9110 rationale.
//
// Run:
//
//	PAIRING_SECRET=dev-secret go run ./examples/minimal
//
// Then exercise the flow:
//
//	# 1. Register a nonce.
//	curl -s -X POST localhost:8080/pair | jq
//	# → {"nonce":"...","deep_link":"https://t.me/newbot/.../...?name=My+Alva"}
//
//	# 2. Simulate the manager bot arriving with the token.
//	curl -s -X PUT localhost:8080/pair/<nonce> \
//	  -H "Authorization: Bearer dev-secret" \
//	  -H "Content-Type: application/json" \
//	  -d '{"token":"123456789:FAKE","bot_username":"alva_<nonce>_bot"}'
//
//	# 3. Client consumes.
//	curl -s -X DELETE localhost:8080/pair/<nonce> | jq
package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/alva-ai/tg-managed-bot-go/link"
	"github.com/alva-ai/tg-managed-bot-go/nonce"
	"github.com/alva-ai/tg-managed-bot-go/pairing"
	"github.com/gin-gonic/gin"
)

const (
	managerBotUsername = "alva_manager_bot"
	pairingPrefix      = "alva"
	pairingTTL         = 15 * time.Minute
)

func main() {
	secret := os.Getenv("PAIRING_SECRET")
	if secret == "" {
		log.Fatal("PAIRING_SECRET env var is required")
	}

	store := pairing.NewMemoryStore()

	r := gin.Default()
	// Every pairing route returns or accepts one-time credentials. Intermediaries
	// MUST NOT cache: stale 404s strand clients on "waiting", and cached 200s
	// could leak tokens to later requests. Set on the whole group once.
	r.Use(noStore)

	r.POST("/pair", registerHandler(store))
	r.PUT("/pair/:nonce", completeHandler(store, secret))
	r.DELETE("/pair/:nonce", fetchHandler(store))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port
	log.Printf("pairing service listening on %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatal(err)
	}
}

// noStore is a middleware that disables caching of pairing responses in
// browsers, proxies, and CDNs. The tokens / nonces handled here are
// one-time bearer credentials.
func noStore(c *gin.Context) {
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate, private")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
	c.Next()
}

// registerHandler generates a nonce, registers it in the Store, and returns
// a deep link the caller can present to the user.
func registerHandler(store pairing.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		n, err := nonce.New()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if err := store.Put(c.Request.Context(), n, pairingTTL); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		childUsername, err := nonce.PackIntoUsername(pairingPrefix, n)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		deepLink, err := link.BuildNewBot(link.Options{
			ManagerBotUsername: managerBotUsername,
			SuggestedUsername:  childUsername,
			SuggestedName:      "My Alva",
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusCreated, gin.H{
			"nonce":      n,
			"deep_link":  deepLink,
			"expires_in": int(pairingTTL.Seconds()),
		})
	}
}

// completeHandler accepts a token+username from the manager bot and
// transitions the pairing entry to Ready. Protected by Bearer auth.
func completeHandler(store pairing.Store, secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if got := c.GetHeader("Authorization"); got != "Bearer "+secret {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid bearer"})
			return
		}

		var body struct {
			Token       string `json:"token" binding:"required"`
			BotUsername string `json:"bot_username" binding:"required"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		n := c.Param("nonce")
		if err := nonce.Validate(n); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		switch err := store.Complete(c.Request.Context(), n, body.Token, body.BotUsername); {
		case err == nil:
			c.JSON(http.StatusOK, gin.H{"ok": true})
		case errors.Is(err, pairing.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "nonce not found or expired"})
		case errors.Is(err, pairing.ErrInvalidState):
			c.JSON(http.StatusConflict, gin.H{"error": "nonce already completed"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
	}
}

// fetchHandler is the client-side consume. Returns 200 once the pairing
// is Ready (and atomically deletes the entry — one-time use). Returns 404
// otherwise; the body distinguishes "still waiting" from "never existed".
// Bound to DELETE so intermediaries do not speculatively retry a
// destructive read (the same reasoning as pairing/server.DeletePair).
func fetchHandler(store pairing.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		n := c.Param("nonce")
		if err := nonce.Validate(n); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		entry, err := store.FetchAndDelete(c.Request.Context(), n)
		switch {
		case err == nil:
			c.JSON(http.StatusOK, gin.H{
				"token":        entry.Token,
				"bot_username": entry.BotUsername,
				"completed_at": entry.CompletedAt.UTC().Format(time.RFC3339),
			})
		case errors.Is(err, pairing.ErrNotReady):
			c.JSON(http.StatusNotFound, gin.H{"status": "waiting"})
		case errors.Is(err, pairing.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"status": "not_found"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
	}
}
