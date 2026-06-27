package config

import (
	"encoding/pem"
	"os"
	"strconv"
	"strings"

	"github.com/palantir/go-githubapp/githubapp"
	"github.com/rs/zerolog/log"
)

type Config struct {
	Port       int
	SmeeURL    string
	PublicURL  string // external base URL when behind a reverse proxy

	// GitHub holds the GitHub App credentials and API endpoints used by
	// go-githubapp's client creator and event dispatcher.
	GitHub githubapp.Config
}

func Load() *Config {
	port, _ := strconv.Atoi(getEnv("PORT", "8080"))
	appID, _ := strconv.ParseInt(getEnv("APP_ID", "0"), 10, 64)

	var gh githubapp.Config
	gh.V3APIURL = getEnv("GITHUB_BASE_URL", "https://api.github.com")
	gh.App.IntegrationID = appID
	gh.App.WebhookSecret = getEnv("WEBHOOK_SECRET", "")
	gh.App.PrivateKey = loadPrivateKey(getEnv("PRIVATE_KEY", ""))

	return &Config{
		Port:      port,
		SmeeURL:   getEnv("SMEE_URL", ""),
		PublicURL: strings.TrimRight(getEnv("PUBLIC_URL", ""), "/"),
		GitHub:    gh,
	}
}

// loadPrivateKey accepts a PEM key path or the PEM contents directly and
// returns the PEM contents, which is what githubapp.Config expects. It also
// repairs keys that were flattened with literal "\n" escapes (common when a
// multi-line PEM is stored in a single-line env var) and validates that the
// result is a parseable PEM block so misconfiguration fails fast.
func loadPrivateKey(keyOrPath string) string {
	if keyOrPath == "" {
		return ""
	}

	key := keyOrPath
	if data, err := os.ReadFile(keyOrPath); err == nil {
		key = string(data)
	} else {
		log.Debug().Msg("PRIVATE_KEY is not a file path; using value as key contents")
	}

	// Restore real newlines if the PEM was provided with escaped "\n".
	if strings.Contains(key, `\n`) {
		key = strings.ReplaceAll(key, `\n`, "\n")
	}
	key = strings.TrimSpace(key)

	if block, _ := pem.Decode([]byte(key)); block == nil {
		log.Fatal().Msg("PRIVATE_KEY is not a valid PEM key (need a path or full PEM contents with real newlines, e.g. -----BEGIN RSA PRIVATE KEY-----)")
	}

	return key
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
