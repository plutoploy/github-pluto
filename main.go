package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"plutoploy/plutoploy-gh-bot/config"
	"plutoploy/plutoploy-gh-bot/store"
	"plutoploy/plutoploy-gh-bot/webhook"
	"plutoploy/plutoploy-gh-bot/webhook/smee"
)

// loggingMiddleware logs every request with method, path, and duration.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Str("remote", r.RemoteAddr).
			Msg("Request started")
		next.ServeHTTP(w, r)
		log.Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Dur("duration", time.Since(start)).
			Msg("Request completed")
	})
}

// recoveryMiddleware catches panics and returns a 500 error.
func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Error().
					Interface("error", err).
					Str("path", r.URL.Path).
					Str("stack", string(debug.Stack())).
					Msg("Panic recovered")
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// corsMiddleware adds permissive CORS headers for browser clients.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	cfg := config.Load()

	installationStore, err := store.NewFileStore("installations.json")
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load installations")
	}

	handler := webhook.NewHandler(cfg, installationStore)

	mux := http.NewServeMux()

	// Webhook endpoint for GitHub events
	mux.HandleFunc("/webhook", handler.HandleWebhook)

	// API endpoints
	mux.HandleFunc("/api/repos", handler.FetchAllRepos)
	mux.HandleFunc("/api/workflow-runs", handler.GetWorkflowRuns)
	mux.HandleFunc("/api/workflow-logs", handler.GetWorkflowLogs)
	mux.HandleFunc("/api/workflow-status", handler.GetWorkflowStatus)
	mux.HandleFunc("/api/inject", handler.InjectFile)

	// Installation management
	mux.HandleFunc("/api/installations", handler.ListInstallations)

	// SSE endpoint for real-time events
	mux.HandleFunc("/api/events", handler.ServeEvents)

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status": "healthy"}`)
	})

	// Wrap with middleware (outermost = panic recovery)
	wrappedHandler := recoveryMiddleware(corsMiddleware(loggingMiddleware(mux)))

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: wrappedHandler,
	}

	// Start smee client if configured
	if cfg.SmeeURL != "" {
		targetURL := fmt.Sprintf("http://localhost:%d/webhook", cfg.Port)
		smeeClient := smee.NewClient(cfg.SmeeURL, targetURL)
		go func() {
			if err := smeeClient.Start(); err != nil {
				log.Error().Err(err).Msg("Smee client failed")
			}
		}()
	}

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Info().Msg("Shutting down...")
		server.Close()
	}()

	log.Info().
		Int("port", cfg.Port).
		Msg("Starting Plutoploy GH Bot")

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal().Err(err).Msg("Server failed")
	}
}
