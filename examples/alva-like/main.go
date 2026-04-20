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
	"strconv"
	"strings"
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

	// 2b. Register the manager bot's webhook, if a public URL is
	//     configured. When PUBLIC_URL is empty, the operator is
	//     expected to run setWebhook out-of-band (handy for local dev
	//     behind ngrok/tailscale where the tunneled URL varies).
	if cfg.PublicURL != "" {
		hookURL := strings.TrimRight(cfg.PublicURL, "/") + "/tg/manager-webhook"
		registerManagerWebhook(api, hookURL)
	} else {
		log.Warn().Msg("PUBLIC_URL unset — run setWebhook out-of-band for manager bot")
	}

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
	// gin.WrapF hands the handler a bare *http.Request — Gin's own route
	// parameters live in gin.Context.Params and are NOT populated into
	// http.Request. pairing/server handlers extract :nonce via
	// r.PathValue("nonce") by default, so wrap through ginAdapt which
	// copies gin params into the request via SetPathValue before dispatch.
	// (Alternative: set pairCfg.NonceFromRequest to a custom extractor.)
	r.POST("/api/v1/pair", ginAdapt(server.PostPair(pairCfg)))
	r.PUT("/api/v1/pair/:nonce", ginAdapt(server.PutPair(pairCfg)))
	r.DELETE("/api/v1/pair/:nonce", ginAdapt(server.DeletePair(pairCfg)))

	// 5. Manager webhook. Telegram POSTs updates here.
	r.POST("/tg/manager-webhook", managerWebhook(handler))

	// 6. Illustrative drift probe: /diag/drift/:bot_id runs reconcile
	//    against a given managed bot. Real apps have a cron reading
	//    from a managed_bots DB and keyed by stored bot_id/token.
	//    This example accepts the managed bot's token via the manager
	//    client so the demo is self-contained.
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

	// PublicURL, when non-empty, is used at startup to register the
	// manager bot's webhook via tgapi.Client.SetWebhook. Leave empty
	// to run setWebhook out-of-band (curl, BotFather etc.); handy for
	// local development behind ngrok/tailscale.
	PublicURL string
}

func mustLoadConfig() config {
	c := config{
		Port:            getenv("PORT", "8080"),
		PairingSecret:   os.Getenv("PAIRING_SECRET"),
		ManagerToken:    os.Getenv("MANAGER_BOT_TOKEN"),
		ManagerUsername: os.Getenv("MANAGER_BOT_USERNAME"),
		NoncePrefix:     getenv("NONCE_PREFIX", "alva"),
		RedisAddr:       getenv("REDIS_ADDR", "localhost:6379"),
		PublicURL:       os.Getenv("PUBLIC_URL"),
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

// registerManagerWebhook installs hookURL as the manager bot's webhook.
// Extracted so the 10-second context can be scoped via defer cancel()
// and the happy-path log fires only when SetWebhook succeeds.
func registerManagerWebhook(api *tgapi.Client, hookURL string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := api.SetWebhook(ctx, hookURL, []string{"message"}); err != nil {
		log.Fatal().Err(err).Str("webhook", hookURL).Msg("SetWebhook failed")
	}
	log.Info().Str("webhook", hookURL).Msg("manager bot webhook registered")
}

// ginAdapt converts a stdlib http.HandlerFunc into a gin.HandlerFunc
// and copies gin's named path parameters into the *http.Request via
// SetPathValue (Go 1.22+). That lets the wrapped handler call
// r.PathValue("nonce") and get the expected string, even though the
// outer router is Gin rather than the Go 1.22 ServeMux the package
// defaults to.
func ginAdapt(h http.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		r := c.Request
		for _, p := range c.Params {
			r.SetPathValue(p.Key, p.Value)
		}
		h(c.Writer, r)
	}
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

// driftProbe runs [reconcile.CheckDrift] against a managed child bot
// (not the manager). Production callers would:
//
//  1. Load the stored State (expected webhook / privacy / username) and
//     the child's encrypted token from a managed_bots DB keyed by
//     :bot_id.
//  2. Build a [tgapi.Client] scoped to the child token.
//  3. Call GetMe + GetWebhookInfo through that client.
//  4. Feed the results to CheckDrift and act on each returned Drift.
//
// This demo accepts the child's token out-of-band:
//
//	GET /diag/drift/:bot_id  (header) X-Bot-Token: <child token>
//
// so the example stays self-contained without a persistence layer.
// driftProbe parameter is named mgrAPI rather than "manager" so it does
// not shadow the imported github.com/alva-ai/tg-managed-bot-go/manager
// package (reachable from this file's broader scope even though this
// function does not itself use it).
func driftProbe(mgrAPI *tgapi.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		botIDStr := c.Param("bot_id")
		botID, err := strconv.ParseInt(botIDStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad bot_id"})
			return
		}

		// Pull the child token out-of-band via header for the demo.
		// Real apps look this up in the managed_bots DB; the only reason
		// mgrAPI is passed in is to show the alternative: you could
		// instead call mgrAPI.GetManagedBotToken(ctx, botID) for a
		// fresh copy every time (and pay the extra round-trip).
		childToken := c.GetHeader("X-Bot-Token")
		if childToken == "" {
			fresh, err := mgrAPI.GetManagedBotToken(c.Request.Context(), botID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "no X-Bot-Token header and getManagedBotToken failed: " + err.Error(),
				})
				return
			}
			childToken = fresh
		}
		child := tgapi.NewClient(childToken)

		// Demo state. Real apps load this from DB.
		state := reconcile.State{
			BotID:              botID,
			ExpectPrivacyOff:   true,
			ExpectedWebhookURL: os.Getenv("PUBLIC_WEBHOOK_URL_FOR_" + botIDStr), // illustrative
		}

		me, meErr := child.GetMe(c.Request.Context())
		wh, whErr := child.GetWebhookInfo(c.Request.Context())

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
