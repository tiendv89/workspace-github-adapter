package domain_test

import (
	"strings"
	"testing"

	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

func TestSourceError_Error_WithPath(t *testing.T) {
	e := domain.SourceError{
		Code:      domain.ErrParserInvalidYAML,
		Message:   "unexpected EOF",
		Source:    domain.ErrorSourceParser,
		Retryable: false,
		Path:      "docs/features/foo/status.yaml",
	}
	got := e.Error()
	if !strings.Contains(got, "docs/features/foo/status.yaml") {
		t.Errorf("expected path in error string, got: %s", got)
	}
	if !strings.Contains(got, string(domain.ErrParserInvalidYAML)) {
		t.Errorf("expected code in error string, got: %s", got)
	}
}

func TestSourceError_Error_WithoutPath(t *testing.T) {
	e := domain.SourceError{
		Code:      domain.ErrGitHubRateLimit,
		Message:   "rate limited",
		Source:    domain.ErrorSourceGitHub,
		Retryable: true,
	}
	got := e.Error()
	if strings.Contains(got, "path:") {
		t.Errorf("expected no path in error string, got: %s", got)
	}
}

func TestNewGitHubRateLimitError(t *testing.T) {
	e := domain.NewGitHubRateLimitError("Try again in 30 minutes.")
	if e.Code != domain.ErrGitHubRateLimit {
		t.Errorf("expected code %s, got %s", domain.ErrGitHubRateLimit, e.Code)
	}
	if !e.Retryable {
		t.Error("expected Retryable=true")
	}
	if e.Source != domain.ErrorSourceGitHub {
		t.Errorf("expected source github, got %s", e.Source)
	}
	if !strings.Contains(e.Message, "Try again in 30 minutes.") {
		t.Errorf("expected retry hint in message, got: %s", e.Message)
	}
}

func TestNewGitHubRateLimitError_EmptyHint(t *testing.T) {
	e := domain.NewGitHubRateLimitError("")
	if !strings.Contains(e.Message, "rate limit") {
		t.Errorf("expected rate limit in message, got: %s", e.Message)
	}
}

func TestNewGitHubNotFoundError(t *testing.T) {
	e := domain.NewGitHubNotFoundError("https://github.com/acme/repo")
	if e.Code != domain.ErrGitHubNotFound {
		t.Errorf("expected code %s, got %s", domain.ErrGitHubNotFound, e.Code)
	}
	if e.Retryable {
		t.Error("expected Retryable=false for not-found")
	}
	if !strings.Contains(e.Message, "https://github.com/acme/repo") {
		t.Errorf("expected URL in message, got: %s", e.Message)
	}
}

func TestNewGitHubNetworkError(t *testing.T) {
	e := domain.NewGitHubNetworkError("connection reset")
	if e.Code != domain.ErrGitHubNetworkError {
		t.Errorf("expected code %s, got %s", domain.ErrGitHubNetworkError, e.Code)
	}
	if !e.Retryable {
		t.Error("expected Retryable=true for network error")
	}
}

func TestNewParserInvalidYAMLError(t *testing.T) {
	e := domain.NewParserInvalidYAMLError("docs/features/foo/tasks.yaml", "line 5: unexpected token")
	if e.Code != domain.ErrParserInvalidYAML {
		t.Errorf("expected code %s, got %s", domain.ErrParserInvalidYAML, e.Code)
	}
	if e.Path != "docs/features/foo/tasks.yaml" {
		t.Errorf("expected path in error, got: %s", e.Path)
	}
	if e.Retryable {
		t.Error("expected Retryable=false for parse error")
	}
}

func TestNewValidationError(t *testing.T) {
	e := domain.NewValidationError(domain.ErrValidationInvalidURL, "repo URL must begin with https://github.com")
	if e.Source != domain.ErrorSourceValidation {
		t.Errorf("expected source validation, got %s", e.Source)
	}
	if e.Retryable {
		t.Error("expected Retryable=false for validation error")
	}
}

func TestFromSourceError(t *testing.T) {
	src := domain.SourceError{
		Code:      domain.ErrGitHubRateLimit,
		Message:   "rate limited",
		Source:    domain.ErrorSourceGitHub,
		Retryable: true,
	}
	cached := map[string]string{"id": "ws-1"}
	api := domain.FromSourceError(src, cached)
	if api.Code != src.Code {
		t.Errorf("Code mismatch: got %s want %s", api.Code, src.Code)
	}
	if api.CachedData == nil {
		t.Error("expected CachedData to be set")
	}
}

func TestFromSourceError_NoCachedData(t *testing.T) {
	src := domain.SourceError{
		Code:    domain.ErrDatabaseConnection,
		Message: "connection refused",
		Source:  domain.ErrorSourceDatabase,
	}
	api := domain.FromSourceError(src, nil)
	if api.CachedData != nil {
		t.Error("expected CachedData to be nil")
	}
}
