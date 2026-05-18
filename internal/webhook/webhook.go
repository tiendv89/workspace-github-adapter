// Package webhook implements GitHub webhook signature verification, push event
// parsing, and branch routing for adapter-service.
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// PushEvent is the subset of a GitHub push event payload that adapter-service needs.
type PushEvent struct {
	Ref        string   `json:"ref"`
	Repository Repository `json:"repository"`
	Commits    []Commit   `json:"commits"`
}

// Repository holds repository identity from the push payload.
type Repository struct {
	FullName string `json:"full_name"`
	HTMLURL  string `json:"html_url"`
	CloneURL string `json:"clone_url"`
}

// Commit holds the changed file lists from a single push commit.
type Commit struct {
	Added    []string `json:"added"`
	Modified []string `json:"modified"`
	Removed  []string `json:"removed"`
}

// BranchKind classifies the branch from a push event.
type BranchKind int

const (
	BranchIgnored     BranchKind = iota
	BranchBase                   // base branch (e.g. "main")
	BranchFeature                // feature/<feature-id>
	BranchTask                   // feature/<feature-id>-T<n>
)

// BranchInfo contains the parsed branch information.
type BranchInfo struct {
	Kind      BranchKind
	Branch    string
	FeatureID string
	TaskID    string
}

// featureBranchRe matches "feature/<feature-id>" (no task suffix).
var featureBranchRe = regexp.MustCompile(`^feature/([^/]+)$`)

// taskBranchRe matches "feature/<feature-id>-T<n>".
var taskBranchRe = regexp.MustCompile(`^feature/(.+)-(T\d+)$`)

// featurePathRe matches docs/features/<feature-id>/ path prefixes.
var featurePathRe = regexp.MustCompile(`^docs/features/([^/]+)/`)

// VerifySignature checks the X-Hub-Signature-256 header against the request body.
// Returns an error if the signature is missing or invalid.
func VerifySignature(secret string, header string, body []byte) error {
	if secret == "" {
		return nil
	}
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return fmt.Errorf("missing or malformed X-Hub-Signature-256 header")
	}
	gotHex := strings.TrimPrefix(header, prefix)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	wantHex := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(gotHex), []byte(wantHex)) {
		return fmt.Errorf("webhook signature mismatch")
	}
	return nil
}

// ReadAndVerify reads the request body and verifies the GitHub HMAC signature.
// Returns the raw body bytes on success. The caller must not call r.Body.Read again.
func ReadAndVerify(r *http.Request, secret string) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("read webhook body: %w", err)
	}
	sig := r.Header.Get("X-Hub-Signature-256")
	if err := VerifySignature(secret, sig, body); err != nil {
		return nil, err
	}
	return body, nil
}

// ParsePushEvent parses raw push event JSON into a PushEvent.
func ParsePushEvent(data []byte) (*PushEvent, error) {
	var ev PushEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, fmt.Errorf("parse push event: %w", err)
	}
	return &ev, nil
}

// BranchFromRef strips the "refs/heads/" prefix from a git ref.
func BranchFromRef(ref string) string {
	return strings.TrimPrefix(ref, "refs/heads/")
}

// ClassifyBranch returns the BranchKind and parsed identifiers for the given branch name.
func ClassifyBranch(branch, baseBranch string) BranchInfo {
	if branch == "" {
		return BranchInfo{Kind: BranchIgnored}
	}
	if branch == baseBranch {
		return BranchInfo{Kind: BranchBase, Branch: branch}
	}
	if m := taskBranchRe.FindStringSubmatch(branch); m != nil {
		return BranchInfo{Kind: BranchTask, Branch: branch, FeatureID: m[1], TaskID: m[2]}
	}
	if m := featureBranchRe.FindStringSubmatch(branch); m != nil {
		return BranchInfo{Kind: BranchFeature, Branch: branch, FeatureID: m[1]}
	}
	return BranchInfo{Kind: BranchIgnored, Branch: branch}
}

// TouchedFeatureIDs extracts unique feature IDs from docs/features/<feature-id>/ paths
// across all commits in the push event.
func TouchedFeatureIDs(ev *PushEvent) []string {
	seen := make(map[string]struct{})
	var ids []string
	for _, c := range ev.Commits {
		for _, paths := range [][]string{c.Added, c.Modified, c.Removed} {
			for _, p := range paths {
				m := featurePathRe.FindStringSubmatch(p)
				if m == nil {
					continue
				}
				fid := m[1]
				if _, dup := seen[fid]; !dup {
					seen[fid] = struct{}{}
					ids = append(ids, fid)
				}
			}
		}
	}
	return ids
}
