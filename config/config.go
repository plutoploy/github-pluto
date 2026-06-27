package config

import (
	"os"
	"strconv"
)

type Config struct {
	Port           int
	WebhookSecret  string
	AppID          int64
	PrivateKey string
	BaseURL        string
	SmeeURL        string
}

func Load() *Config {
	port, _ := strconv.Atoi(getEnv("PORT", "8080"))
	appID, _ := strconv.ParseInt(getEnv("APP_ID", "0"), 10, 64)

	return &Config{
		Port:           port,
		WebhookSecret:  getEnv("WEBHOOK_SECRET", ""),
		AppID:          appID,
		PrivateKey:     getEnv("PRIVATE_KEY", ""),
		BaseURL:        getEnv("GITHUB_BASE_URL", "https://api.github.com"),
		SmeeURL:        getEnv("SMEE_URL", ""),
	}
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
