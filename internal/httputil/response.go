package httputil

import (
	"encoding/json"
	"net/http"

	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

// WriteOK encodes value as JSON and writes it with the given HTTP status.
func WriteOK(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// WriteSourceError maps a domain.SourceError to the appropriate HTTP status and writes the JSON body.
func WriteSourceError(w http.ResponseWriter, se domain.SourceError) {
	status := sourceErrorStatus(se)
	WriteOK(w, status, domain.FromSourceError(se, nil))
}

// WriteAnyError writes a SourceError if err is one, otherwise wraps it in a generic database error.
func WriteAnyError(w http.ResponseWriter, err error) {
	var se domain.SourceError
	if isSourceError(err, &se) {
		WriteSourceError(w, se)
		return
	}
	WriteSourceError(w, domain.NewDatabaseError(domain.ErrDatabaseQuery, err.Error()))
}

func isSourceError(err error, target *domain.SourceError) bool {
	if err == nil {
		return false
	}
	se, ok := err.(domain.SourceError)
	if ok {
		*target = se
		return true
	}
	return false
}

func sourceErrorStatus(se domain.SourceError) int {
	switch se.Source {
	case domain.ErrorSourceValidation:
		return http.StatusBadRequest
	case domain.ErrorSourceGitHub:
		switch se.Code {
		case domain.ErrGitHubNotFound:
			return http.StatusNotFound
		case domain.ErrGitHubUnauthorized:
			return http.StatusUnauthorized
		case domain.ErrGitHubRateLimit:
			return http.StatusTooManyRequests
		default:
			return http.StatusBadGateway
		}
	case domain.ErrorSourceDatabase:
		switch se.Code {
		case domain.ErrDatabaseNotFound:
			return http.StatusNotFound
		case domain.ErrDatabaseConflict:
			return http.StatusConflict
		}
	}
	return http.StatusInternalServerError
}
