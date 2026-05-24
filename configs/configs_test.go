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
	t.Setenv("GITHUB_TOKEN", "ghp_test")
	t.Setenv("GITHUB_WEBHOOK_SECRETS", "secret")

	path := writeConfig(t, `
log:
  level: debug
api:
  http:
    address: ":9090"
    mode: debug
  auth:
    admin_api_key: "key123"
db:
  host: localhost
  port: 5432
  db_name: testdb
  user: postgres
  password: postgres
redis:
  init_address:
    - localhost:6379
github:
  token: "ghp_yaml"
  webhook_secrets: "yaml_secret"
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
	if cfg.API.HTTP.Address != ":9090" {
		t.Errorf("api.http.address: got %q, want %q", cfg.API.HTTP.Address, ":9090")
	}
	if cfg.DB.Host != "localhost" {
		t.Errorf("db.host: got %q", cfg.DB.Host)
	}
	if cfg.Redis.Addr() != "localhost:6379" {
		t.Errorf("redis addr: got %q", cfg.Redis.Addr())
	}
	if cfg.GitHub.Token != "ghp_test" {
		t.Errorf("github.token: got %q, want %q", cfg.GitHub.Token, "ghp_test")
	}
	if cfg.GitHub.WebhookSecrets != "secret" {
		t.Errorf("github.webhook_secrets: got %q, want %q", cfg.GitHub.WebhookSecrets, "secret")
	}
	if cfg.StaleThreshold() != 60*time.Minute {
		t.Errorf("stale threshold: got %v, want 60m", cfg.StaleThreshold())
	}
}

func TestLoad_WebhookSecrets_Multiple(t *testing.T) {
	path := writeConfig(t, `
db:
  host: localhost
  port: 5432
  db_name: testdb
  user: postgres
  password: postgres
github:
  webhook_secrets: "s1,s2,s3"
`)
	cfg, err := configs.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.GitHub.WebhookSecrets != "s1,s2,s3" {
		t.Errorf("github.webhook_secrets: got %q, want %q", cfg.GitHub.WebhookSecrets, "s1,s2,s3")
	}
}

func TestLoad_WebhookSecrets_Empty(t *testing.T) {
	// Empty webhook_secrets is loadable; startup guard lives in api.go, not Load.
	path := writeConfig(t, `
db:
  host: localhost
  port: 5432
  db_name: testdb
  user: postgres
  password: postgres
`)
	cfg, err := configs.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.GitHub.WebhookSecrets != "" {
		t.Errorf("github.webhook_secrets: got %q, want empty string", cfg.GitHub.WebhookSecrets)
	}
}

func TestLoad_MissingDBHost(t *testing.T) {
	path := writeConfig(t, `
log:
  level: info
`)
	_, err := configs.Load(path)
	if err == nil {
		t.Fatal("expected error for missing db.host")
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
db:
  host: localhost
  port: 5432
  db_name: testdb
  user: postgres
  password: postgres
`)
	cfg, err := configs.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Log.Level != "info" {
		t.Errorf("default log.level: got %q, want %q", cfg.Log.Level, "info")
	}
	if cfg.API.HTTP.Address != ":8080" {
		t.Errorf("default api.http.address: got %q, want %q", cfg.API.HTTP.Address, ":8080")
	}
	if cfg.StaleThreshold() != 30*time.Minute {
		t.Errorf("default stale threshold: got %v, want 30m", cfg.StaleThreshold())
	}
}

func TestLoad_EnvOverride(t *testing.T) {
	path := writeConfig(t, `
db:
  host: localhost
  port: 5432
  db_name: testdb
  user: postgres
  password: postgres
`)
	t.Setenv("API_HTTP_ADDRESS", ":7777")
	cfg, err := configs.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.API.HTTP.Address != ":7777" {
		t.Errorf("env override api.http.address: got %q, want %q", cfg.API.HTTP.Address, ":7777")
	}
}

func TestDBConfig_DSN(t *testing.T) {
	cfg := configs.DBConfig{
		Host:     "localhost",
		Port:     5432,
		DBName:   "mydb",
		User:     "admin",
		Password: "pass",
	}
	want := "postgres://admin:pass@localhost:5432/mydb?sslmode=disable"
	if got := cfg.DSN(); got != want {
		t.Errorf("DSN: got %q, want %q", got, want)
	}
}

func TestRedisConfig_Addr(t *testing.T) {
	cfg := configs.RedisConfig{InitAddress: []string{"redis-host:6380"}}
	if got := cfg.Addr(); got != "redis-host:6380" {
		t.Errorf("Addr: got %q, want %q", got, "redis-host:6380")
	}

	empty := configs.RedisConfig{}
	if got := empty.Addr(); got != "127.0.0.1:6379" {
		t.Errorf("empty Addr: got %q, want default", got)
	}
}
