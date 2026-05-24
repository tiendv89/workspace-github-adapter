package configs_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tiendv89/workspace-github-adapter/configs"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

func TestLoad_HappyPath(t *testing.T) {
	// Env overrides take precedence; pin values so test is deterministic regardless of host env.
	t.Setenv("GITHUB_TOKEN", "ghp_test")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "secret")

	path := writeConfig(t, `
log:
  level: debug
server:
  port: 9090
database:
  url: "postgres://localhost/test"
redis:
  url: "redis://localhost:6379"
github:
  token: "ghp_yaml"
  webhook_secret: "yaml_secret"
sync:
  stale_threshold_minutes: 60
`)
	cfg, err := configs.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("log.level: got %q, want %q", cfg.Log.Level, "debug")
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("server.port: got %d, want 9090", cfg.Server.Port)
	}
	if cfg.Database.URL != "postgres://localhost/test" {
		t.Errorf("database.url: got %q", cfg.Database.URL)
	}
	// Env override should win over YAML value.
	if cfg.GitHub.Token != "ghp_test" {
		t.Errorf("github.token: got %q, want %q", cfg.GitHub.Token, "ghp_test")
	}
	if cfg.GitHub.WebhookSecret != "secret" {
		t.Errorf("github.webhook_secret: got %q, want %q", cfg.GitHub.WebhookSecret, "secret")
	}
	if cfg.StaleThreshold() != 60*time.Minute {
		t.Errorf("stale threshold: got %v, want 60m", cfg.StaleThreshold())
	}
}

func TestLoad_MissingDatabaseURL(t *testing.T) {
	path := writeConfig(t, `
log:
  level: info
server:
  port: 8080
`)
	_, err := configs.Load(path)
	if err == nil {
		t.Fatal("expected error for missing database.url")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := configs.Load(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_Defaults(t *testing.T) {
	path := writeConfig(t, `
database:
  url: "postgres://localhost/test"
`)
	cfg, err := configs.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Log.Level != "info" {
		t.Errorf("default log.level: got %q, want %q", cfg.Log.Level, "info")
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("default server.port: got %d, want 8080", cfg.Server.Port)
	}
	if cfg.StaleThreshold() != 30*time.Minute {
		t.Errorf("default stale threshold: got %v, want 30m", cfg.StaleThreshold())
	}
}

func TestLoad_EnvOverride(t *testing.T) {
	path := writeConfig(t, `
database:
  url: "postgres://localhost/test"
server:
  port: 8080
`)
	t.Setenv("SERVER_PORT", "7777")
	cfg, err := configs.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.Port != 7777 {
		t.Errorf("env override server.port: got %d, want 7777", cfg.Server.Port)
	}
}
