package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all runtime configuration for adapter-service.
type Config struct {
	// HTTP server
	Port int

	// Database
	DatabaseURL string

	// Redis / asynq task queue
	RedisURL string

	// GitHub
	GitHubToken string

	// WebhookSecret is the shared secret used to verify GitHub webhook HMAC signatures.
	// Set via GITHUB_WEBHOOK_SECRET environment variable.
	WebhookSecret string

	// Sync staleness threshold
	StaleThreshold time.Duration
}

// Load reads configuration from environment variables.
// Required variables: DATABASE_URL.
func Load() (*Config, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	port := 8080
	if raw := os.Getenv("PORT"); raw != "" {
		p, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid PORT: %w", err)
		}
		port = p
	}

	staleThreshold := 30 * time.Minute
	if raw := os.Getenv("STALE_THRESHOLD_MINUTES"); raw != "" {
		m, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid STALE_THRESHOLD_MINUTES: %w", err)
		}
		staleThreshold = time.Duration(m) * time.Minute
	}

	return &Config{
		Port:           port,
		DatabaseURL:    dbURL,
		RedisURL:       os.Getenv("REDIS_URL"),
		GitHubToken:    os.Getenv("GITHUB_TOKEN"),
		WebhookSecret:  os.Getenv("GITHUB_WEBHOOK_SECRET"),
		StaleThreshold: staleThreshold,
	}, nil
}
