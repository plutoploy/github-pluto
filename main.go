package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/palantir/go-githubapp/githubapp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"plutoploy/plutoploy-gh-bot/config"
	"plutoploy/plutoploy-gh-bot/store"
	"plutoploy/plutoploy-gh-bot/webhook"
)

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	cfg := config.Load()

	installationStore, err := store.NewFileStore(cfg.StorePath)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load installations")
	}

	// Shared client creator: caches installation transports/tokens.
	clientCreator, err := githubapp.NewDefaultCachingClientCreator(cfg.GitHub)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create GitHub client creator")
	}

	handler := webhook.NewHandler(clientCreator, installationStore)

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()

	// Global middleware
	router.Use(gin.LoggerWithWriter(os.Stderr))
	router.Use(gin.CustomRecovery(func(c *gin.Context, recovered interface{}) {
		log.Error().
			Interface("error", recovered).
			Str("path", c.Request.URL.Path).
			Msg("Panic recovered")
		c.AbortWithStatus(http.StatusInternalServerError)
	}))
	router.Use(corsMiddleware())

	// Webhook endpoint for GitHub events
	webhookDispatcher := githubapp.NewDefaultEventDispatcher(cfg.GitHub, handler.EventHandlers()...)
	router.Any("/webhook", gin.WrapH(webhookDispatcher))

	// API endpoints
	api := router.Group("/api")
	{
		api.GET("/repos", handler.FetchAllRepos)
		api.GET("/workflow-runs", handler.GetWorkflowRuns)
		api.GET("/workflow-logs", handler.GetWorkflowLogs)
		api.GET("/workflow-status", handler.GetWorkflowStatus)
		api.POST("/inject", handler.InjectFile)
		api.GET("/installations", handler.ListInstallations)
		api.GET("/events", handler.ServeEvents)
	}

	// Health check
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "healthy"})
	})

	// Determine the webhook URL to register in the GitHub App.
	// Priority: PUBLIC_URL (reverse proxy) > localhost.
	webhookURL := fmt.Sprintf("http://localhost:%d/webhook", cfg.Port)
	if cfg.PublicURL != "" {
		webhookURL = cfg.PublicURL + "/webhook"
	}

	log.Info().
		Int("port", cfg.Port).
		Str("webhook_url", webhookURL).
		Str("store_path", cfg.StorePath).
		Msg("Starting Plutoploy GH Bot")
	log.Info().
		Str("webhook_url", webhookURL).
		Msg("Register this URL in your GitHub App settings (Webhook URL)")

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Graceful shutdown: wait for all active connections to drain.
	var wg sync.WaitGroup
	wg.Add(1)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		defer wg.Done()
		sig := <-sigCh
		log.Info().Str("signal", sig.String()).Msg("Shutting down...")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error().Err(err).Msg("Server shutdown error")
		}
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal().Err(err).Msg("Server failed")
	}

	wg.Wait()
	log.Info().Msg("Server exited cleanly")
}

// corsMiddleware adds permissive CORS headers for browser clients.
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusOK)
			return
		}
		c.Next()
	}
}
