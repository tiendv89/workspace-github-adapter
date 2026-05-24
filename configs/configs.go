package configs

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds all runtime configuration for both adapter binaries.
type Config struct {
	Log      LogConfig      `mapstructure:"log"`
	Server   ServerConfig   `mapstructure:"server"`
	Database DatabaseConfig `mapstructure:"database"`
	Redis    RedisConfig    `mapstructure:"redis"`
	GitHub   GitHubConfig   `mapstructure:"github"`
	Sync     SyncConfig     `mapstructure:"sync"`
}

// LogConfig controls zerolog output.
type LogConfig struct {
	Level string `mapstructure:"level"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Port int `mapstructure:"port"`
}

// DatabaseConfig holds PostgreSQL connection settings.
type DatabaseConfig struct {
	URL string `mapstructure:"url"`
}

// RedisConfig holds Redis connection settings.
type RedisConfig struct {
	URL string `mapstructure:"url"`
}

// GitHubConfig holds GitHub API credentials.
type GitHubConfig struct {
	Token         string `mapstructure:"token"`
	WebhookSecret string `mapstructure:"webhook_secret"`
}

// SyncConfig holds workspace sync settings.
type SyncConfig struct {
	StaleThresholdMinutes int `mapstructure:"stale_threshold_minutes"`
}

// StaleThreshold returns the configured duration for stale threshold.
func (c *Config) StaleThreshold() time.Duration {
	m := c.Sync.StaleThresholdMinutes
	if m <= 0 {
		return 30 * time.Minute
	}
	return time.Duration(m) * time.Minute
}

// Load reads configuration from the YAML file at path and applies environment-variable overrides.
// If the file does not exist, env-var values and defaults are used directly.
// Environment variables use underscores as separators; e.g. SERVER_PORT overrides server.port.
func Load(path string) (*Config, error) {
	v := viper.New()

	v.SetDefault("log.level", "info")
	v.SetDefault("server.port", 8080)
	v.SetDefault("sync.stale_threshold_minutes", 30)

	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		// Allow a missing config file; env vars + defaults are sufficient.
		if !isNotFound(err) {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	if cfg.Database.URL == "" {
		return nil, fmt.Errorf("database.url is required")
	}
	return &cfg, nil
}

// isNotFound returns true when viper reports that the config file was not found.
func isNotFound(err error) bool {
	_, ok := err.(viper.ConfigFileNotFoundError)
	return ok || (err != nil && strings.Contains(err.Error(), "no such file"))
}
