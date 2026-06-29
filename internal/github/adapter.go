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
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

// featureFetchConcurrency bounds how many features are fetched from GitHub in
// parallel during a full import. Each feature is several Contents API calls, so
// this is the main lever on full-sync latency for large workspaces; kept modest
// to avoid GitHub secondary rate limits.
const featureFetchConcurrency = 8

// Adapter implements domain.GitHubWorkspaceAdapter.
type Adapter struct {
	token           string            // retained for backward compatibility (first token)
	tokens          []string          // split from comma-separated token string
	tokenCache      map[string]string // owner → resolved token (lazy, populated by tokenFor)
	mu              sync.RWMutex      // protects tokenCache
	probeHTTPClient *http.Client      // optional, used by tokenFor probe (for testing)
}

// New creates a new Adapter. token is split on "," to build the token list.
// Falls back to GITHUB_TOKEN env var when the list is empty.
func New(token string) *Adapter {
	tokens := splitTokens(token)
	if len(tokens) == 0 {
		if t := os.Getenv("GITHUB_TOKEN"); t != "" {
			tokens = []string{t}
		}
	}
	var first string
	if len(tokens) > 0 {
		first = tokens[0]
	}
	return &Adapter{
		token:      first,
		tokens:     tokens,
		tokenCache: make(map[string]string),
	}
}

// splitTokens splits a comma-separated token string into a slice of non-empty,
// whitespace-trimmed tokens.
func splitTokens(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// Ensure Adapter satisfies the interface at compile time.
var _ domain.GitHubWorkspaceAdapter = (*Adapter)(nil)

const workspaceYAMLPath = "workspace.yaml"

// tokenFor resolves a token for the given GitHub owner. On cache hit it returns
// immediately. On cache miss it probes each token in order by making a
// lightweight GET request to /repos/{owner}/{repo}. The first token that does
// not return 401/403 is cached for the owner and returned. If all tokens fail,
// returns a SourceError with ErrGitHubUnauthorized.
func (a *Adapter) tokenFor(ctx context.Context, owner, repo string) (string, error) {
	// Fast path: cache hit with read lock.
	a.mu.RLock()
	if t, ok := a.tokenCache[owner]; ok {
		a.mu.RUnlock()
		return t, nil
	}
	a.mu.RUnlock()

	// Cache miss — probe tokens in order under write lock.
	a.mu.Lock()
	defer a.mu.Unlock()

	// Double-check after acquiring write lock.
	if t, ok := a.tokenCache[owner]; ok {
		return t, nil
	}

	for _, t := range a.tokens {
		if t == "" {
			continue
		}
		valid, err := a.probeToken(ctx, t, owner, repo)
		if err != nil {
			return "", err
		}
		if valid {
			a.tokenCache[owner] = t
			return t, nil
		}
	}

	return "", domain.SourceError{
		Code:      domain.ErrGitHubUnauthorized,
		Message:   fmt.Sprintf("no valid GitHub token found for owner %q", owner),
		Source:    domain.ErrorSourceGitHub,
		Retryable: false,
	}
}

// probeToken makes a lightweight GET to /repos/{owner}/{repo} to check whether
// the given token can successfully authenticate for the given owner.
// Returns true if the token is valid (any response that is not 401/403).
func (a *Adapter) probeToken(ctx context.Context, token, owner, repo string) (bool, error) {
	u := fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, nil // transient error, try next token
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	hc := a.probeHTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return false, nil // network error, try next token
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// Detect rate limiting first — it's a real error, not a token validity signal.
	if resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		return false, domain.NewGitHubRateLimitError("")
	}

	// 401/403 means the token does not work for this owner.
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return false, nil // try next token
	}

	// Any other status (2xx, 404, etc.) means the token is valid for this owner.
	return true, nil
}

type repoTarget struct {
	client *client
	owner  string
	repo   string
	ref    string
}

func (a *Adapter) repoTarget(ctx context.Context, input domain.ImportInput) (*repoTarget, error) {
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
		var tokErr error
		token, tokErr = a.tokenFor(ctx, owner, repo)
		if tokErr != nil {
			return nil, tokErr
		}
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
	target, err := a.repoTarget(ctx, input)
	if err != nil {
		return nil, err
	}
	return a.fetchSnapshot(ctx, target.client, target.owner, target.repo, target.ref, "")
}

// FetchWorkspaceMetadata validates the repository and reads only workspace.yaml.
func (a *Adapter) FetchWorkspaceMetadata(ctx context.Context, input domain.ImportInput) (*domain.WorkspaceSnapshot, error) {
	target, err := a.repoTarget(ctx, input)
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
	wsCfg, parseErr := parseWorkspaceYAML(wsData)
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
	token, tokErr := a.tokenFor(ctx, owner, repo)
	if tokErr != nil {
		return nil, tokErr
	}
	c := newClient(token)

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
	if snap == nil {
		// The feature itself is unobtainable (no docs / not found).
		if len(errs) > 0 {
			return nil, errs[0]
		}
		return nil, domain.NewGitHubNotFoundError("https://github.com/" + owner + "/" + repo)
	}
	// The feature loaded; any per-task/doc errors are non-fatal — log them as
	// skipped and return the feature with whatever parsed cleanly.
	for _, e := range errs {
		log.Warn().Str("repo", owner+"/"+repo).Str("feature_id", featureID).Str("path", e.Path).Str("error", e.Message).Msg("fetch feature: skipping artifact with import error")
	}
	return snap, nil
}

// HeadCommit resolves the current commit SHA at ref — a single lightweight API
// call used to decide whether an incremental sync is even needed.
func (a *Adapter) HeadCommit(ctx context.Context, repoURL, ref string) (string, error) {
	owner, repo, perr := parseRepoURL(repoURL)
	if perr != nil {
		return "", *perr
	}
	token, tokErr := a.tokenFor(ctx, owner, repo)
	if tokErr != nil {
		return "", tokErr
	}
	sha, err := newClient(token).getCommitSHA(ctx, owner, repo, ref)
	if err != nil {
		return "", mapFetchError(err, owner, repo)
	}
	return sha, nil
}

// CompareChangedPaths returns the repo-relative paths changed (added/modified/
// renamed) and removed between base and head. complete=false means the diff
// couldn't be fully determined (too large) and the caller should do a full
// reconciliation instead.
func (a *Adapter) CompareChangedPaths(ctx context.Context, repoURL, base, head string) (changed, removed []string, complete bool, err error) {
	owner, repo, perr := parseRepoURL(repoURL)
	if perr != nil {
		return nil, nil, false, *perr
	}
	token, tokErr := a.tokenFor(ctx, owner, repo)
	if tokErr != nil {
		return nil, nil, false, tokErr
	}
	changed, removed, complete, cerr := newClient(token).compareCommits(ctx, owner, repo, base, head)
	if cerr != nil {
		return nil, nil, false, mapFetchError(cerr, owner, repo)
	}
	return changed, removed, complete, nil
}

// FetchTask fetches and parses a single task YAML from the given task branch.
func (a *Adapter) FetchTask(ctx context.Context, repoURL, taskBranch, featureID, taskID string) (*domain.TaskSnapshot, error) {
	owner, repo, err := parseRepoURL(repoURL)
	if err != nil {
		return nil, *err
	}
	token, tokErr := a.tokenFor(ctx, owner, repo)
	if tokErr != nil {
		return nil, tokErr
	}
	c := newClient(token)

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

	token, tokErr := a.tokenFor(ctx, owner, repo)
	if tokErr != nil {
		return nil, tokErr
	}

	c := newClient(token)
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
	wsCfg, parseErr := parseWorkspaceYAML(wsData)
	if parseErr != nil {
		return nil, *parseErr
	}

	policy, policyErr := parseModelPolicy(wsCfg.ModelPolicy)
	if policyErr != nil {
		return nil, *policyErr
	}

	// Discover feature directories from the tree.
	featureIDs := discoverFeatureIDs(tree.Tree)

	// Map repos from workspace.yaml.
	repos := make([]domain.RepoEntry, 0, len(wsCfg.Repos))
	for _, r := range wsCfg.Repos {
		repos = append(repos, domain.RepoEntry{
			RepoID:     r.ID,
			BaseBranch: r.BaseBranch,
			RepoURL:    r.GitHub,
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

	// Fetch features concurrently — each feature is several GitHub round trips,
	// so a sequential loop is the dominant cost for large workspaces (>50
	// features took minutes). Bounded concurrency keeps it fast without
	// tripping GitHub's secondary rate limits. Results are written by index to
	// preserve order; only the progress counter is shared (atomic).
	type featResult struct {
		feat *domain.FeatureSnapshot
		errs []domain.SourceError
	}
	results := make([]featResult, len(featureIDs))
	total := len(featureIDs)

	log.Info().Str("repo", owner+"/"+repo).Str("ref", ref).Int("features", total).Int("concurrency", featureFetchConcurrency).Msg("import: discovered features, fetching")

	sem := make(chan struct{}, featureFetchConcurrency)
	var wg sync.WaitGroup
	var done int64
	for i, featureID := range featureIDs {
		wg.Add(1)
		go func(i int, featureID string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			feat, errs := a.fetchFeature(ctx, c, owner, repo, ref, featureID, pathSet)
			results[i] = featResult{feat: feat, errs: errs}

			n := atomic.AddInt64(&done, 1)
			if feat != nil {
				log.Info().Str("repo", owner+"/"+repo).Str("feature_id", featureID).
					Int64("progress", n).Int("total", total).Int("tasks", len(feat.Tasks)).
					Msg("import: fetched feature")
			} else {
				log.Warn().Str("repo", owner+"/"+repo).Str("feature_id", featureID).
					Int64("progress", n).Int("total", total).
					Msg("import: feature skipped (no snapshot)")
			}
		}(i, featureID)
	}
	wg.Wait()

	var sourceErrors []domain.SourceError
	features := make([]domain.FeatureSnapshot, 0, len(featureIDs))
	for _, r := range results {
		sourceErrors = append(sourceErrors, r.errs...)
		if r.feat != nil {
			features = append(features, *r.feat)
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
		ModelPolicy:      policy,
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
	featureOwner := ""
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
		featureOwner = status.Owner
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
		Owner:        featureOwner,
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
