package httputil

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

// WriteOK writes value as JSON with the given HTTP status.
func WriteOK(c *gin.Context, status int, value interface{}) {
	c.JSON(status, value)
}

// WriteSourceError maps a domain.SourceError to the appropriate HTTP status and writes the JSON body.
func WriteSourceError(c *gin.Context, se domain.SourceError) {
	c.AbortWithStatusJSON(sourceErrorStatus(se), domain.FromSourceError(se, nil))
}

// WriteAnyError writes a SourceError if err is one, otherwise wraps it in a generic database error.
func WriteAnyError(c *gin.Context, err error) {
	var srcErr domain.SourceError
	if errors.As(err, &srcErr) {
		WriteSourceError(c, srcErr)
		return
	}
	WriteSourceError(c, domain.NewDatabaseError(domain.ErrDatabaseQuery, err.Error()))
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
