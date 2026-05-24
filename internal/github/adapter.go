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

const workspaceYAMLPath = "workspace.yaml"

type repoTarget struct {
	client *client
	owner  string
	repo   string
	ref    string
}

func (a *Adapter) repoTarget(input domain.ImportInput) (*repoTarget, error) {
	if err := validateInput(input); err != nil {
		return nil, *err
	}

	owner, repo, err := parseRepoURL(input.RepoURL)
	if err != nil {
		return nil, *err
	}

	ref := input.DefaultBranch
	if ref == "" {
		ref = "main"
	}

	token := input.Token
	if token == "" {
		token = a.token
	}

	return &repoTarget{
		client: newClient(token),
		owner:  owner,
		repo:   repo,
		ref:    ref,
	}, nil
}

// ImportWorkspace performs a full reconciliation import of the given repository.
func (a *Adapter) ImportWorkspace(ctx context.Context, input domain.ImportInput) (*domain.WorkspaceSnapshot, error) {
	target, err := a.repoTarget(input)
	if err != nil {
		return nil, err
	}
	return a.fetchSnapshot(ctx, target.client, target.owner, target.repo, target.ref, "")
}

// FetchWorkspaceMetadata validates the repository and reads only workspace.yaml.
func (a *Adapter) FetchWorkspaceMetadata(ctx context.Context, input domain.ImportInput) (*domain.WorkspaceSnapshot, error) {
	target, err := a.repoTarget(input)
	if err != nil {
		return nil, err
	}

	wsData, err := target.client.getFileContent(ctx, target.owner, target.repo, workspaceYAMLPath, target.ref)
	if err != nil {
		return nil, mapFetchError(err, target.owner, target.repo)
	}
	if wsData == nil {
		return nil, domain.SourceError{
			Code:      domain.ErrGitHubNotFound,
			Message:   fmt.Sprintf("required file not found: %s", workspaceYAMLPath),
			Source:    domain.ErrorSourceGitHub,
			Retryable: false,
			Path:      workspaceYAMLPath,
		}
	}
	wsCfg, parseErr := parseWorkspaceYAML(wsData, workspaceYAMLPath)
	if parseErr != nil {
		return nil, *parseErr
	}

	name := wsCfg.Name
	if name == "" {
		name = target.owner + "/" + target.repo
	}
	slug := slugify(name)

	return &domain.WorkspaceSnapshot{
		WorkspaceID:      slug,
		Name:             name,
		Slug:             slug,
		RepoURL:          "https://github.com/" + target.owner + "/" + target.repo,
		ManagementRepoID: wsCfg.ManagementRepo,
		BranchPattern:    wsCfg.Git.BranchPattern,
		SlackChannelID:   wsCfg.Notifications.Slack.ChannelID,
		FetchedAt:        time.Now().UTC(),
	}, nil
}

// FetchFeature fetches and parses all artifacts for a single feature from the given ref.
// It builds a path set for the feature's tasks directory to discover all task YAMLs.
func (a *Adapter) FetchFeature(ctx context.Context, repoURL, ref, featureID string) (*domain.FeatureSnapshot, error) {
	owner, repo, err := parseRepoURL(repoURL)
	if err != nil {
		return nil, *err
	}
	c := newClient(a.token)

	// Resolve commit SHA.
	commitSHA, ferr := c.getCommitSHA(ctx, owner, repo, ref)
	if ferr != nil {
		return nil, mapFetchError(ferr, owner, repo)
	}

	// Get the full tree to discover task YAMLs for this feature.
	tree, ferr := c.getTree(ctx, owner, repo, commitSHA)
	if ferr != nil {
		return nil, mapFetchError(ferr, owner, repo)
	}

	pathSet := make(map[string]struct{}, len(tree.Tree))
	for _, e := range tree.Tree {
		if e.Type == "blob" {
			pathSet[e.Path] = struct{}{}
		}
	}

	snap, errs := a.fetchFeature(ctx, c, owner, repo, ref, featureID, pathSet)
	if len(errs) > 0 {
		return nil, errs[0]
	}
	if snap == nil {
		return nil, domain.NewGitHubNotFoundError("https://github.com/" + owner + "/" + repo)
	}
	return snap, nil
}

// FetchTask fetches and parses a single task YAML from the given task branch.
func (a *Adapter) FetchTask(ctx context.Context, repoURL, taskBranch, featureID, taskID string) (*domain.TaskSnapshot, error) {
	owner, repo, err := parseRepoURL(repoURL)
	if err != nil {
		return nil, *err
	}
	c := newClient(a.token)

	taskPath := "docs/features/" + featureID + "/tasks/" + taskID + ".yaml"
	data, ferr := c.getFileContent(ctx, owner, repo, taskPath, taskBranch)
	if ferr != nil {
		return nil, ferr
	}
	if data == nil {
		return nil, domain.SourceError{
			Code:      domain.ErrGitHubNotFound,
			Message:   "task file not found: " + taskPath,
			Source:    domain.ErrorSourceGitHub,
			Retryable: false,
			Path:      taskPath,
		}
	}

	parsed, parseErr := parseTaskYAML(data, taskPath)
	if parseErr != nil {
		return nil, *parseErr
	}

	snap := mapTaskSnapshot(parsed, featureID, taskID, taskPath, data)
	return &snap, nil
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
		BranchPattern:    wsCfg.Git.BranchPattern,
		SlackChannelID:   wsCfg.Notifications.Slack.ChannelID,
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

	// A webhook/PR may introduce or update only one feature artifact (for example,
	// product-spec.md or technical-design.md). Treat status.yaml as optional for
	// discovery and targeted sync; parse it when present, otherwise build a minimal
	// feature snapshot from the available document path(s).
	var status *featureStatusYAML
	var statusData []byte
	if _, exists := pathSet[statusPath]; exists {
		var err error
		statusData, err = c.getFileContent(ctx, owner, repo, statusPath, ref)
		if err != nil {
			errs = append(errs, asSourceError(err, statusPath))
			return nil, errs
		}
		if statusData != nil {
			var parseErr *domain.SourceError
			status, parseErr = parseFeatureStatusYAML(statusData, statusPath)
			if parseErr != nil {
				errs = append(errs, *parseErr)
				return nil, errs
			}
		}
	}

	// Build document links for whichever recognized docs exist in the tree.
	docs := []domain.DocumentSnapshot{}
	primarySourcePath := ""
	for docType, docPath := range docPaths {
		if _, exists := pathSet[docPath]; !exists {
			continue
		}
		docs = append(docs, domain.DocumentSnapshot{
			DocumentType: docType,
			SourcePath:   docPath,
			URL:          buildWebURL(owner, repo, ref, docPath),
		})
		if primarySourcePath == "" || docType == "status_yaml" {
			primarySourcePath = docPath
		}
	}
	if len(docs) == 0 {
		errs = append(errs, domain.SourceError{
			Code:      domain.ErrGitHubNotFound,
			Message:   fmt.Sprintf("no recognized feature documents found for %s", featureID),
			Source:    domain.ErrorSourceGitHub,
			Retryable: false,
			Path:      "docs/features/" + featureID,
		})
		return nil, errs
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
			errs = append(errs, domain.SourceError{
				Code:      domain.ErrGitHubNotFound,
				Message:   "task file not found: " + p,
				Source:    domain.ErrorSourceGitHub,
				Retryable: false,
				Path:      p,
			})
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

	featureIDValue := featureID
	title := featureID
	featureStatus := ""
	currentStage := ""
	nextAction := ""
	stages := map[string]interface{}{}
	var activity []domain.ActivityEvent
	sourceHash := ""
	if status != nil {
		featureIDValue = firstNonEmpty(status.featureID(), featureID)
		title = firstNonEmpty(status.Title, featureIDValue)
		featureStatus = status.featureStatus()
		currentStage = status.currentStage()
		nextAction = status.NextAction
		if status.Stages != nil {
			stages = status.Stages
		}
		activity = mapActivityLog(status.History, "feature", featureIDValue, "")
		sourceHash = hashContent(statusData)
	}

	return &domain.FeatureSnapshot{
		FeatureID:    featureIDValue,
		Title:        title,
		Status:       featureStatus,
		CurrentStage: currentStage,
		NextAction:   nextAction,
		Stages:       stages,
		SourcePath:   primarySourcePath,
		SourceHash:   sourceHash,
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

// discoverFeatureIDs scans the tree entries for recognized docs/features/* artifacts
// (status.yaml, product-spec.md, technical-design.md) and returns the unique feature IDs found.
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
