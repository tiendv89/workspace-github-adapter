package urlutil

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

// ParseGitHubRepo extracts owner and repo name from a GitHub repo URL.
func ParseGitHubRepo(raw string) (string, string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", "", domain.NewValidationError(domain.ErrValidationInvalidURL, "invalid GitHub repo URL: "+raw)
	}
	if !strings.EqualFold(u.Host, "github.com") {
		return "", "", domain.NewValidationError(domain.ErrValidationInvalidURL, "unsupported GitHub host: "+u.Host)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", domain.NewValidationError(domain.ErrValidationInvalidURL, "invalid GitHub repo URL path: "+raw)
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git"), nil
}

// Slugify converts a string to a lowercase hyphen-separated slug.
func Slugify(name string) string {
	lower := strings.ToLower(name)
	re := regexp.MustCompile(`[^a-z0-9]+`)
	slug := re.ReplaceAllString(lower, "-")
	return strings.Trim(slug, "-")
}
