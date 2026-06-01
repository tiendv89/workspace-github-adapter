package domain

import "fmt"

// ErrorSource identifies the system that produced a SourceError.
type ErrorSource string

const (
	ErrorSourceGitHub     ErrorSource = "github"
	ErrorSourceDatabase   ErrorSource = "database"
	ErrorSourceParser     ErrorSource = "parser"
	ErrorSourceAdapter    ErrorSource = "adapter"
	ErrorSourceValidation ErrorSource = "validation"
)

// ErrorCode is a machine-readable error identifier.
type ErrorCode string

const (
	// GitHub errors.
	ErrGitHubRateLimit    ErrorCode = "GITHUB_RATE_LIMIT"
	ErrGitHubNotFound     ErrorCode = "GITHUB_NOT_FOUND"
	ErrGitHubUnauthorized ErrorCode = "GITHUB_UNAUTHORIZED"
	ErrGitHubNetworkError ErrorCode = "GITHUB_NETWORK_ERROR"
	ErrGitHubServerError  ErrorCode = "GITHUB_SERVER_ERROR"

	// Parser errors.
	ErrParserInvalidYAML  ErrorCode = "PARSER_INVALID_YAML"
	ErrParserMissingField ErrorCode = "PARSER_MISSING_FIELD"
	ErrParserUnexpected   ErrorCode = "PARSER_UNEXPECTED"

	// Database errors.
	ErrDatabaseConnection  ErrorCode = "DATABASE_CONNECTION"
	ErrDatabaseQuery       ErrorCode = "DATABASE_QUERY"
	ErrDatabaseTransaction ErrorCode = "DATABASE_TRANSACTION"
	ErrDatabaseNotFound    ErrorCode = "DATABASE_NOT_FOUND"
	ErrDatabaseConflict    ErrorCode = "DATABASE_CONFLICT"

	// Adapter errors.
	ErrAdapterInternal ErrorCode = "ADAPTER_INTERNAL"
	ErrAdapterTimeout  ErrorCode = "ADAPTER_TIMEOUT"

	// Validation errors.
	ErrValidationInvalidURL   ErrorCode = "VALIDATION_INVALID_URL"
	ErrValidationInvalidInput ErrorCode = "VALIDATION_INVALID_INPUT"
	ErrValidationMissingInput ErrorCode = "VALIDATION_MISSING_INPUT"
)

// SourceError is the normalized error shape for all backend errors.
// It carries a machine-readable code, a user-facing message, the error source,
// a retryability hint, and an optional path for file-level errors.
type SourceError struct {
	Code      ErrorCode   `json:"code"`
	Message   string      `json:"message"`
	Source    ErrorSource `json:"source"`
	Retryable bool        `json:"retryable"`
	Path      string      `json:"path,omitempty"`
}

func (e SourceError) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("[%s/%s] %s (path: %s)", e.Source, e.Code, e.Message, e.Path)
	}
	return fmt.Sprintf("[%s/%s] %s", e.Source, e.Code, e.Message)
}

// NewGitHubRateLimitError returns a SourceError for GitHub rate limit responses.
func NewGitHubRateLimitError(retryAfterMsg string) SourceError {
	msg := "GitHub API rate limit reached."
	if retryAfterMsg != "" {
		msg += " " + retryAfterMsg
	}
	return SourceError{
		Code:      ErrGitHubRateLimit,
		Message:   msg,
		Source:    ErrorSourceGitHub,
		Retryable: true,
	}
}

// NewGitHubNotFoundError returns a SourceError for inaccessible or missing repos.
func NewGitHubNotFoundError(repoURL string) SourceError {
	return SourceError{
		Code:      ErrGitHubNotFound,
		Message:   fmt.Sprintf("Repository not found or inaccessible: %s", repoURL),
		Source:    ErrorSourceGitHub,
		Retryable: false,
	}
}

// NewGitHubNetworkError returns a SourceError for transient network failures.
func NewGitHubNetworkError(detail string) SourceError {
	return SourceError{
		Code:      ErrGitHubNetworkError,
		Message:   fmt.Sprintf("GitHub API request failed: %s", detail),
		Source:    ErrorSourceGitHub,
		Retryable: true,
	}
}

// NewParserInvalidYAMLError returns a SourceError for YAML parse failures.
func NewParserInvalidYAMLError(path, detail string) SourceError {
	return SourceError{
		Code:      ErrParserInvalidYAML,
		Message:   fmt.Sprintf("Invalid YAML in %s: %s", path, detail),
		Source:    ErrorSourceParser,
		Retryable: false,
		Path:      path,
	}
}

// NewValidationError returns a SourceError for input validation failures.
func NewValidationError(code ErrorCode, message string) SourceError {
	return SourceError{
		Code:      code,
		Message:   message,
		Source:    ErrorSourceValidation,
		Retryable: false,
	}
}

// NewDatabaseError returns a SourceError for database failures.
func NewDatabaseError(code ErrorCode, detail string) SourceError {
	return SourceError{
		Code:      code,
		Message:   fmt.Sprintf("Database error: %s", detail),
		Source:    ErrorSourceDatabase,
		Retryable: true,
	}
}

// NewDatabaseConflictError returns a SourceError for data conflicts.
func NewDatabaseConflictError(detail string) SourceError {
	return SourceError{
		Code:      ErrDatabaseConflict,
		Message:   fmt.Sprintf("Database conflict: %s", detail),
		Source:    ErrorSourceDatabase,
		Retryable: false,
	}
}

// APIError is the HTTP response body for error responses.
type APIError struct {
	Code       ErrorCode   `json:"code"`
	Message    string      `json:"message"`
	Source     ErrorSource `json:"source"`
	Retryable  bool        `json:"retryable"`
	CachedData interface{} `json:"cached_data,omitempty"`
}

// FromSourceError converts a SourceError to an APIError.
func FromSourceError(e SourceError, cachedData interface{}) APIError {
	return APIError{
		Code:       e.Code,
		Message:    e.Message,
		Source:     e.Source,
		Retryable:  e.Retryable,
		CachedData: cachedData,
	}
}
