package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

const (
	apiBase        = "https://api.github.com"
	defaultTimeout = 30 * time.Second
)

// client is an authenticated GitHub API client.
type client struct {
	http  *http.Client
	token string
}

func newClient(token string) *client {
	return &client{
		http:  &http.Client{Timeout: defaultTimeout},
		token: token,
	}
}

// do executes an HTTP request and returns the response body bytes.
// It handles GitHub error responses and maps them to domain.SourceError.
func (c *client) do(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, domain.SourceError{
			Code:      domain.ErrAdapterInternal,
			Message:   fmt.Sprintf("failed to build request: %v", err),
			Source:    domain.ErrorSourceAdapter,
			Retryable: false,
		}
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, domain.SourceError{
				Code:      domain.ErrAdapterTimeout,
				Message:   "request cancelled or timed out",
				Source:    domain.ErrorSourceAdapter,
				Retryable: true,
			}
		}
		return nil, domain.NewGitHubNetworkError(err.Error())
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, domain.NewGitHubNetworkError("failed to read response body: " + err.Error())
	}

	switch resp.StatusCode {
	case http.StatusOK:
		return body, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		if resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0" {
			resetAt := resp.Header.Get("X-RateLimit-Reset")
			msg := ""
			if resetAt != "" {
				if ts, err := strconv.ParseInt(resetAt, 10, 64); err == nil {
					msg = fmt.Sprintf("Try again after %s.", time.Unix(ts, 0).UTC().Format(time.RFC3339))
				}
			}
			return nil, domain.NewGitHubRateLimitError(msg)
		}
		return nil, domain.SourceError{
			Code:      domain.ErrGitHubUnauthorized,
			Message:   "GitHub API request unauthorized — check GITHUB_TOKEN",
			Source:    domain.ErrorSourceGitHub,
			Retryable: false,
		}
	case http.StatusNotFound:
		return nil, domain.SourceError{
			Code:      domain.ErrGitHubNotFound,
			Message:   fmt.Sprintf("GitHub resource not found: %s", url),
			Source:    domain.ErrorSourceGitHub,
			Retryable: false,
		}
	case http.StatusTooManyRequests:
		retryAfter := resp.Header.Get("Retry-After")
		return nil, domain.NewGitHubRateLimitError("Retry-After: " + retryAfter)
	default:
		if resp.StatusCode >= 500 {
			return nil, domain.SourceError{
				Code:      domain.ErrGitHubServerError,
				Message:   fmt.Sprintf("GitHub API server error (HTTP %d) for %s", resp.StatusCode, url),
				Source:    domain.ErrorSourceGitHub,
				Retryable: true,
			}
		}
		// Include the URL and a body snippet — GitHub's 4xx body (e.g. a 422 "No
		// commit found for ...", or an oversized-file message) and the URL say
		// which call failed and why.
		snippet := string(body)
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		return nil, domain.SourceError{
			Code:      domain.ErrGitHubNetworkError,
			Message:   fmt.Sprintf("GitHub API unexpected response (HTTP %d) for %s: %s", resp.StatusCode, url, snippet),
			Source:    domain.ErrorSourceGitHub,
			Retryable: false,
		}
	}
}

// treeEntry is a single entry returned by the Git Trees API.
type treeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"` // "blob" or "tree"
	SHA  string `json:"sha"`
	Size int    `json:"size,omitempty"`
}

// gitTreeResponse is the response from the Git Trees API.
type gitTreeResponse struct {
	SHA       string      `json:"sha"`
	Tree      []treeEntry `json:"tree"`
	Truncated bool        `json:"truncated"`
}

// contentResponse is the response from the Contents API.
type contentResponse struct {
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
	SHA      string `json:"sha"`
}

// getTree fetches the full recursive file tree for a repo using the given commit SHA.
// The caller is responsible for resolving the ref to a SHA via getCommitSHA before calling this.
func (c *client) getTree(ctx context.Context, owner, repo, sha string) (*gitTreeResponse, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/git/trees/%s?recursive=1", apiBase, owner, repo, sha)
	body, err := c.do(ctx, url)
	if err != nil {
		return nil, err
	}

	var tree gitTreeResponse
	if err := json.Unmarshal(body, &tree); err != nil {
		return nil, domain.SourceError{
			Code:      domain.ErrAdapterInternal,
			Message:   "failed to parse git tree response: " + err.Error(),
			Source:    domain.ErrorSourceAdapter,
			Retryable: false,
		}
	}
	if tree.Truncated {
		return nil, domain.SourceError{
			Code:      domain.ErrGitHubServerError,
			Message:   fmt.Sprintf("GitHub tree response truncated for %s/%s at %s", owner, repo, sha),
			Source:    domain.ErrorSourceGitHub,
			Retryable: true,
		}
	}
	return &tree, nil
}

// getCommitSHA resolves a branch/tag/ref to its commit SHA.
func (c *client) getCommitSHA(ctx context.Context, owner, repo, ref string) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/commits/%s", apiBase, owner, repo, ref)
	body, err := c.do(ctx, url)
	if err != nil {
		return "", err
	}

	var commit struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(body, &commit); err != nil {
		return "", domain.SourceError{
			Code:      domain.ErrAdapterInternal,
			Message:   "failed to parse commit response: " + err.Error(),
			Source:    domain.ErrorSourceAdapter,
			Retryable: false,
		}
	}
	return commit.SHA, nil
}

// compareCommits lists files changed between base and head via the Compare API.
// Returns changed (added/modified/renamed) and removed paths separately so the
// caller can fall back to a full reconciliation when files were deleted.
// complete=false when the diff is too large to trust (GitHub caps the files
// list at 300), again signalling a full sync.
func (c *client) compareCommits(ctx context.Context, owner, repo, base, head string) (changed, removed []string, complete bool, err error) {
	url := fmt.Sprintf("%s/repos/%s/%s/compare/%s...%s?per_page=100", apiBase, owner, repo, base, head)
	body, err := c.do(ctx, url)
	if err != nil {
		return nil, nil, false, err
	}
	var cmp struct {
		Files []struct {
			Filename         string `json:"filename"`
			PreviousFilename string `json:"previous_filename"`
			Status           string `json:"status"`
		} `json:"files"`
	}
	if err := json.Unmarshal(body, &cmp); err != nil {
		return nil, nil, false, domain.SourceError{
			Code:      domain.ErrAdapterInternal,
			Message:   "failed to parse compare response: " + err.Error(),
			Source:    domain.ErrorSourceAdapter,
			Retryable: false,
		}
	}
	// GitHub returns at most 300 files in compare; if we hit that, the list may
	// be truncated — signal incomplete so the caller does a full sync.
	if len(cmp.Files) >= 300 {
		return nil, nil, false, nil
	}
	for _, f := range cmp.Files {
		if f.Status == "removed" {
			removed = append(removed, f.Filename)
			continue
		}
		changed = append(changed, f.Filename)
		if f.PreviousFilename != "" {
			// Renamed: the old path effectively disappeared — treat as removal
			// so a feature-dir rename triggers a full reconciliation.
			removed = append(removed, f.PreviousFilename)
		}
	}
	return changed, removed, true, nil
}

// getFileContent fetches and decodes the content of a single file via the Contents API.
// Returns nil content (not an error) when the file is not found (404).
func (c *client) getFileContent(ctx context.Context, owner, repo, path, ref string) ([]byte, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/contents/%s?ref=%s", apiBase, owner, repo, path, ref)
	body, err := c.do(ctx, url)
	if err != nil {
		// Not found is treated as "file absent" — caller decides if required.
		if se, ok := err.(domain.SourceError); ok && se.Code == domain.ErrGitHubNotFound {
			return nil, nil
		}
		return nil, err
	}

	var cr contentResponse
	err = json.Unmarshal(body, &cr)
	if err != nil {
		return nil, domain.SourceError{
			Code:      domain.ErrAdapterInternal,
			Message:   fmt.Sprintf("failed to parse contents response for %s: %v", path, err),
			Source:    domain.ErrorSourceAdapter,
			Retryable: false,
		}
	}

	if cr.Encoding != "base64" {
		return nil, domain.SourceError{
			Code:      domain.ErrAdapterInternal,
			Message:   fmt.Sprintf("unexpected encoding %q for %s", cr.Encoding, path),
			Source:    domain.ErrorSourceAdapter,
			Retryable: false,
		}
	}

	// GitHub wraps base64 content with newlines — strip them before decoding.
	stripped := removeNewlines(cr.Content)
	decoded, err := base64.StdEncoding.DecodeString(stripped)
	if err != nil {
		return nil, domain.SourceError{
			Code:      domain.ErrAdapterInternal,
			Message:   fmt.Sprintf("failed to base64-decode content for %s: %v", path, err),
			Source:    domain.ErrorSourceAdapter,
			Retryable: false,
		}
	}
	return decoded, nil
}

func removeNewlines(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\n' && s[i] != '\r' {
			out = append(out, s[i])
		}
	}
	return string(out)
}
