// Package github implements the GitHubWorkspaceAdapter interface.
// It uses the Git Trees API for feature discovery and the Contents API for
// individual file fetches — a single request discovers all feature paths,
// and only the relevant files are read. No archive download or zip extraction
// is required, and the same approach works for both full reconciliation and
// targeted sync.
package github

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

// Adapter implements domain.GitHubWorkspaceAdapter.
type Adapter struct {
	token string
}

// New creates a new Adapter. If token is empty, the GITHUB_TOKEN environment
// variable is used as a fallback.
func New(token string) *Adapter {
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	return &Adapter{token: token}
}

// Ensure Adapter satisfies the interface at compile time.
var _ domain.GitHubWorkspaceAdapter = (*Adapter)(nil)

// ImportWorkspace performs a full reconciliation import of the given repository.
func (a *Adapter) ImportWorkspace(ctx context.Context, input domain.ImportInput) (*domain.WorkspaceSnapshot, error) {
	if err := validateInput(input); err != nil {
		return nil, *err
	}

	token := input.Token
	if token == "" {
		token = a.token
	}
	c := newClient(token)

	owner, repo, err := parseRepoURL(input.RepoURL)
	if err != nil {
		return nil, *err
	}

	ref := input.DefaultBranch
	if ref == "" {
		ref = "main"
	}

	return a.fetchSnapshot(ctx, c, owner, repo, ref, "")
}

// SyncWorkspace re-fetches the repository at the given ref and returns an updated snapshot.
func (a *Adapter) SyncWorkspace(ctx context.Context, workspaceID, repoURL, ref string) (*domain.WorkspaceSnapshot, error) {
	if repoURL == "" {
		return nil, domain.NewValidationError(domain.ErrValidationMissingInput, "repoURL is required")
	}
	if ref == "" {
		ref = "main"
	}

	owner, repo, err := parseRepoURL(repoURL)
	if err != nil {
		return nil, *err
	}

	c := newClient(a.token)
	snap, fetchErr := a.fetchSnapshot(ctx, c, owner, repo, ref, workspaceID)
	if fetchErr != nil {
		return nil, fetchErr
	}
	return snap, nil
}

// fetchSnapshot fetches the full workspace snapshot from GitHub.
func (a *Adapter) fetchSnapshot(ctx context.Context, c *client, owner, repo, ref, workspaceID string) (*domain.WorkspaceSnapshot, error) {
	fetchedAt := time.Now().UTC()

	// Resolve the commit SHA for this ref.
	commitSHA, err := c.getCommitSHA(ctx, owner, repo, ref)
	if err != nil {
		return nil, mapFetchError(err, owner, repo)
	}

	// Fetch the full file tree in one request (Git Trees API, recursive=1).
	// Pass commitSHA directly so getTree doesn't need to re-resolve the ref.
	tree, err := c.getTree(ctx, owner, repo, commitSHA)
	if err != nil {
		return nil, mapFetchError(err, owner, repo)
	}

	// Build a path-set for quick existence checks.
	pathSet := make(map[string]struct{}, len(tree.Tree))
	for _, e := range tree.Tree {
		if e.Type == "blob" {
			pathSet[e.Path] = struct{}{}
		}
	}

	// Fetch and parse workspace.yaml (required).
	wsYAMLPath := "workspace.yaml"
	wsData, err := c.getFileContent(ctx, owner, repo, wsYAMLPath, ref)
	if err != nil {
		return nil, mapFetchError(err, owner, repo)
	}
	if wsData == nil {
		return nil, domain.SourceError{
			Code:      domain.ErrGitHubNotFound,
			Message:   fmt.Sprintf("required file not found: %s", wsYAMLPath),
			Source:    domain.ErrorSourceGitHub,
			Retryable: false,
			Path:      wsYAMLPath,
		}
	}
	wsCfg, parseErr := parseWorkspaceYAML(wsData, wsYAMLPath)
	if parseErr != nil {
		return nil, *parseErr
	}

	// Discover feature directories from the tree.
	featureIDs := discoverFeatureIDs(tree.Tree)

	// Map repos from workspace.yaml.
	repos := make([]domain.RepoEntry, 0, len(wsCfg.Repos))
	for _, r := range wsCfg.Repos {
		repos = append(repos, domain.RepoEntry{
			RepoID:     r.ID,
			BaseBranch: r.BaseBranch,
		})
	}

	name := wsCfg.Name
	if name == "" {
		name = owner + "/" + repo
	}
	slug := slugify(name)
	if workspaceID == "" {
		workspaceID = slug
	}

	var sourceErrors []domain.SourceError
	features := make([]domain.FeatureSnapshot, 0, len(featureIDs))

	for _, featureID := range featureIDs {
		feat, errs := a.fetchFeature(ctx, c, owner, repo, ref, featureID, pathSet)
		sourceErrors = append(sourceErrors, errs...)
		if feat != nil {
			features = append(features, *feat)
		}
	}

	return &domain.WorkspaceSnapshot{
		WorkspaceID:      workspaceID,
		Name:             name,
		Slug:             slug,
		RepoURL:          "https://github.com/" + owner + "/" + repo,
		ManagementRepoID: wsCfg.ManagementRepo,
		CommitSHA:        commitSHA,
		FetchedAt:        fetchedAt,
		Features:         features,
		Repos:            repos,
		SourceErrors:     sourceErrors,
	}, nil
}

// fetchFeature fetches and parses all artifacts for a single feature.
func (a *Adapter) fetchFeature(ctx context.Context, c *client, owner, repo, ref, featureID string, pathSet map[string]struct{}) (*domain.FeatureSnapshot, []domain.SourceError) {
	var errs []domain.SourceError

	docPaths := featureDocPaths(featureID)
	statusPath := docPaths["status_yaml"]

	// status.yaml is required — if missing, emit a source error.
	statusData, err := c.getFileContent(ctx, owner, repo, statusPath, ref)
	if err != nil {
		errs = append(errs, asSourceError(err, statusPath))
		return nil, errs
	}
	if statusData == nil {
		errs = append(errs, domain.SourceError{
			Code:      domain.ErrGitHubNotFound,
			Message:   fmt.Sprintf("required file not found: %s", statusPath),
			Source:    domain.ErrorSourceGitHub,
			Retryable: false,
			Path:      statusPath,
		})
		return nil, errs
	}

	status, parseErr := parseFeatureStatusYAML(statusData, statusPath)
	if parseErr != nil {
		errs = append(errs, *parseErr)
		return nil, errs
	}

	// Build document links — only status.yaml has required content; the rest are optional.
	docs := []domain.DocumentSnapshot{}
	for docType, docPath := range docPaths {
		_, exists := pathSet[docPath]
		if exists || docType == "status_yaml" {
			docs = append(docs, domain.DocumentSnapshot{
				DocumentType: docType,
				SourcePath:   docPath,
				URL:          buildWebURL(owner, repo, ref, docPath),
			})
		}
	}

	// Fetch all task YAMLs for this feature.
	tasksDir := "docs/features/" + featureID + "/tasks/"
	var taskSnapshots []domain.TaskSnapshot
	for p := range pathSet {
		if !strings.HasPrefix(p, tasksDir) {
			continue
		}
		taskID := taskFileBase(p)
		if taskID == "" {
			continue
		}
		taskData, err := c.getFileContent(ctx, owner, repo, p, ref)
		if err != nil {
			errs = append(errs, asSourceError(err, p))
			continue
		}
		if taskData == nil {
			continue
		}
		taskParsed, parseErr := parseTaskYAML(taskData, p)
		if parseErr != nil {
			errs = append(errs, *parseErr)
			continue
		}

		taskSnap := mapTaskSnapshot(taskParsed, featureID, taskID, p, taskData)
		taskSnapshots = append(taskSnapshots, taskSnap)
	}

	// Map feature history to activity events.
	activity := mapActivityLog(status.History, "feature", featureID, "")

	return &domain.FeatureSnapshot{
		FeatureID:    featureID,
		Title:        status.Title,
		Status:       status.Status,
		CurrentStage: status.Stage,
		NextAction:   status.NextAction,
		SourcePath:   statusPath,
		SourceHash:   hashContent(statusData),
		Documents:    docs,
		Tasks:        taskSnapshots,
		Activity:     activity,
	}, errs
}

// mapTaskSnapshot converts a parsed taskYAML to a domain.TaskSnapshot.
func mapTaskSnapshot(t *taskYAML, featureID, taskID, sourcePath string, rawData []byte) domain.TaskSnapshot {
	var activity []domain.ActivityEvent
	if len(t.Log) > 0 {
		activity = mapActivityLog(t.Log, "task", featureID, taskID)
	}

	return domain.TaskSnapshot{
		TaskID:        taskID,
		FeatureID:     featureID,
		Title:         t.Title,
		Status:        t.Status,
		Repo:          t.Repo,
		Branch:        t.Branch,
		DependsOn:     t.DependsOn,
		BlockedReason: blockedReasonString(t.BlockedReason),
		Execution:     t.Execution,
		PR:            t.PR,
		WorkspacePR:   t.WorkspacePR,
		SourcePath:    sourcePath,
		SourceHash:    hashContent(rawData),
		Activity:      activity,
	}
}

// discoverFeatureIDs scans the tree entries for docs/features/*/status.yaml paths
// and returns the unique feature IDs found.
func discoverFeatureIDs(entries []treeEntry) []string {
	seen := make(map[string]struct{})
	var ids []string
	for _, e := range entries {
		if e.Type != "blob" {
			continue
		}
		featureID, ok := extractFeatureID(e.Path)
		if !ok {
			continue
		}
		if _, dup := seen[featureID]; dup {
			continue
		}
		seen[featureID] = struct{}{}
		ids = append(ids, featureID)
	}
	return ids
}

// validateInput checks ImportInput for required fields.
func validateInput(input domain.ImportInput) *domain.SourceError {
	if input.RepoURL == "" {
		se := domain.NewValidationError(domain.ErrValidationMissingInput, "RepoURL is required")
		return &se
	}
	return nil
}

// parseRepoURL extracts the owner and repo name from a GitHub URL.
// Accepts https://github.com/owner/repo and git@github.com:owner/repo.git forms.
func parseRepoURL(rawURL string) (owner, repo string, se *domain.SourceError) {
	rawURL = strings.TrimSpace(rawURL)

	// SSH form: git@github.com:owner/repo.git
	if strings.HasPrefix(rawURL, "git@github.com:") {
		rest := strings.TrimPrefix(rawURL, "git@github.com:")
		rest = strings.TrimSuffix(rest, ".git")
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			se = ptr(domain.NewValidationError(domain.ErrValidationInvalidURL,
				fmt.Sprintf("invalid GitHub SSH URL: %s", rawURL)))
			return
		}
		return parts[0], parts[1], nil
	}

	// HTTPS form.
	u, err := url.Parse(rawURL)
	if err != nil || u.Host != "github.com" {
		se = ptr(domain.NewValidationError(domain.ErrValidationInvalidURL,
			fmt.Sprintf("invalid GitHub URL: %s", rawURL)))
		return
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		se = ptr(domain.NewValidationError(domain.ErrValidationInvalidURL,
			fmt.Sprintf("GitHub URL must include owner and repo: %s", rawURL)))
		return
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git"), nil
}

func ptr(se domain.SourceError) *domain.SourceError { return &se }

// mapFetchError converts a repo-level fetch error to a SourceError, adding repo context.
func mapFetchError(err error, owner, repo string) error {
	if se, ok := err.(domain.SourceError); ok {
		if se.Code == domain.ErrGitHubNotFound {
			return domain.NewGitHubNotFoundError("https://github.com/" + owner + "/" + repo)
		}
	}
	return err
}

// asSourceError coerces any error to a domain.SourceError, adding path context.
func asSourceError(err error, path string) domain.SourceError {
	if se, ok := err.(domain.SourceError); ok {
		se.Path = path
		return se
	}
	return domain.SourceError{
		Code:      domain.ErrAdapterInternal,
		Message:   err.Error(),
		Source:    domain.ErrorSourceAdapter,
		Retryable: false,
		Path:      path,
	}
}
