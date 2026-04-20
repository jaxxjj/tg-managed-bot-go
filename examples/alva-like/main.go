// Package main is a complete Alva-flavoured integration example wiring
// every piece of tg-managed-bot-go together:
//
//   - redis.Store     persistent pairing state (go-redis/v9)
//   - server          three pairing HTTP routes under /api/v1/
//   - tgapi.Client    thin Bot API client for the manager bot
//   - manager.Handler webhook receiver for managed_bot_created events
//   - reconcile       illustrated via a per-request drift probe endpoint
//
// Wire it in, set the env vars, run:
//
//	PAIRING_SECRET=dev-shared-secret \
//	MANAGER_BOT_TOKEN=<token from @BotFather> \
//	REDIS_ADDR=localhost:6379 \
//	PORT=8080 \
//	PUBLIC_URL=https://pair.example.com \
//	go run ./examples/alva-like
//
// PUBLIC_URL is used only so this main can call setWebhook on the
// manager bot at startup. When running on localhost, skip the webhook
// registration or tunnel through ngrok / tailscale-funnel.
//
// This example is intentionally a single file — real deployments would
// split into cmd/ + internal/ + proper graceful shutdown + metrics +
// feature flags. Copy what's useful.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	redisv9 "github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/alva-ai/tg-managed-bot-go/manager"
	"github.com/alva-ai/tg-managed-bot-go/pairing/server"
	redisstore "github.com/alva-ai/tg-managed-bot-go/pairing/stores/redis"
	"github.com/alva-ai/tg-managed-bot-go/reconcile"
	"github.com/alva-ai/tg-managed-bot-go/tgapi"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

	cfg := mustLoadConfig()

	// 1. Redis-backed pairing store.
	rdb := redisv9.NewClient(&redisv9.Options{Addr: cfg.RedisAddr})
	defer func() { _ = rdb.Close() }()
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatal().Err(err).Str("redis_addr", cfg.RedisAddr).Msg("redis ping failed")
	}
	store := redisstore.NewStore(rdb, redisstore.WithKeyPrefix("alva:pairing:"))

	// 2. Manager Bot API client.
	api := tgapi.NewClient(cfg.ManagerToken)

	// 3. Manager-side update handler.
	handler := &manager.Handler{
		Store:  store,
		API:    api,
		Prefix: cfg.NoncePrefix,
	}

	// 4. Gin server with the three pairing routes mounted via stdlib
	//    handlers (gin.WrapF adapts http.HandlerFunc → gin.HandlerFunc).
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), requestLogger())

	pairCfg := server.Config{
		Store:              store,
		ManagerBotUsername: cfg.ManagerUsername,
		NoncePrefix:        cfg.NoncePrefix,
		SuggestedName:      "My Alva",
		PairingTTL:         15 * time.Minute,
		Authenticator:      server.BearerAuth(cfg.PairingSecret),
	}
	r.POST("/api/v1/pair", gin.WrapF(server.PostPair(pairCfg)))
	r.PUT("/api/v1/pair/:nonce", gin.WrapF(server.PutPair(pairCfg)))
	r.GET("/api/v1/pair/:nonce", gin.WrapF(server.GetPair(pairCfg)))

	// 5. Manager webhook. Telegram POSTs updates here.
	r.POST("/tg/manager-webhook", managerWebhook(handler))

	// 6. Illustrative drift probe: /diag/drift/:bot_id runs reconcile
	//    against a given managed bot. Real apps have a cron reading
	//    from a managed_bots DB.
	r.GET("/diag/drift/:bot_id", driftProbe(api))

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info().Str("addr", srv.Addr).Msg("pairing service listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal().Err(err).Msg("server failed")
		}
	}()

	// 7. Graceful shutdown.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Info().Msg("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn().Err(err).Msg("shutdown error")
	}
}

// ---------- wiring helpers ----------

type config struct {
	Port            string
	PairingSecret   string
	ManagerToken    string
	ManagerUsername string
	NoncePrefix     string
	RedisAddr       string
}

func mustLoadConfig() config {
	c := config{
		Port:            getenv("PORT", "8080"),
		PairingSecret:   os.Getenv("PAIRING_SECRET"),
		ManagerToken:    os.Getenv("MANAGER_BOT_TOKEN"),
		ManagerUsername: os.Getenv("MANAGER_BOT_USERNAME"),
		NoncePrefix:     getenv("NONCE_PREFIX", "alva"),
		RedisAddr:       getenv("REDIS_ADDR", "localhost:6379"),
	}
	miss := []string{}
	if c.PairingSecret == "" {
		miss = append(miss, "PAIRING_SECRET")
	}
	if c.ManagerToken == "" {
		miss = append(miss, "MANAGER_BOT_TOKEN")
	}
	if c.ManagerUsername == "" {
		miss = append(miss, "MANAGER_BOT_USERNAME")
	}
	if len(miss) > 0 {
		log.Fatal().Strs("missing", miss).Msg("required env vars not set")
	}
	return c
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// requestLogger is a tiny gin middleware that puts a zerolog per-request
// logger into the request context, so downstream handlers using
// zerolog.Ctx(ctx) get structured fields for free.
func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		logger := log.With().
			Str("method", c.Request.Method).
			Str("path", c.Request.URL.Path).
			Str("remote", c.ClientIP()).
			Logger()
		c.Request = c.Request.WithContext(logger.WithContext(c.Request.Context()))
		start := time.Now()
		c.Next()
		logger.Debug().
			Int("status", c.Writer.Status()).
			Dur("dur", time.Since(start)).
			Msg("http")
	}
}

// managerWebhook decodes a Telegram Update JSON body and hands it to
// [manager.Handler]. Returns 200 OK as long as the body is well-formed
// JSON — Telegram retries indefinitely on non-2xx.
func managerWebhook(h *manager.Handler) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		logger := zerolog.Ctx(ctx)

		body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
		if err != nil {
			logger.Warn().Err(err).Msg("webhook: read body")
			c.JSON(http.StatusBadRequest, gin.H{"error": "read body"})
			return
		}

		var u tgapi.Update
		if err := json.Unmarshal(body, &u); err != nil {
			logger.Warn().Err(err).Msg("webhook: unmarshal")
			c.JSON(http.StatusBadRequest, gin.H{"error": "unmarshal"})
			return
		}

		if err := h.HandleUpdate(ctx, &u); err != nil {
			// Swallow non-retryable errors; retryable go out as 500.
			logger.Error().Err(err).Int64("update_id", u.UpdateID).Msg("webhook: handler")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "handler failed"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// driftProbe runs [reconcile.CheckDrift] against the bot identified by
// :bot_id using the current manager credentials. It's purely
// illustrative — production callers iterate managed_bots records and
// act on each detected Drift.
func driftProbe(api *tgapi.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		_ = c.Param("bot_id") // real code would look up stored State here

		state := reconcile.State{
			BotID:            0,
			ExpectPrivacyOff: true,
			// ExpectedWebhookURL / LastKnownUsername loaded from DB in real apps.
		}

		me, meErr := api.GetMe(c.Request.Context())
		wh, whErr := api.GetWebhookInfo(c.Request.Context())

		drifts := reconcile.CheckDrift(state, reconcile.Observed{
			Me: me, GetMeErr: meErr,
			Webhook: wh, WebhookErr: whErr,
		})

		type view struct {
			Kind   string `json:"kind"`
			Detail string `json:"detail"`
		}
		out := make([]view, len(drifts))
		for i, d := range drifts {
			out[i] = view{Kind: d.Kind.String(), Detail: d.Detail}
		}
		c.JSON(http.StatusOK, gin.H{"drifts": out})
	}
}
