package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

// adapterWithTransportAndFetch wraps adapterWithTransport and adds FetchFeature/FetchTask support.
type adapterWithTransportAndFetch struct {
	*Adapter
	transport http.RoundTripper
}

func newFetchAdapter(t *testing.T, transport http.RoundTripper) *adapterWithTransportAndFetch {
	t.Helper()
	return &adapterWithTransportAndFetch{
		Adapter:   &Adapter{token: "test-token"},
		transport: transport,
	}
}

func (a *adapterWithTransportAndFetch) clientWith() *client {
	c := newClient(a.token)
	c.http = &http.Client{Transport: a.transport}
	return c
}

func (a *adapterWithTransportAndFetch) FetchFeature(ctx context.Context, repoURL, ref, featureID string) (*domain.FeatureSnapshot, error) {
	owner, repo, se := parseRepoURL(repoURL)
	if se != nil {
		return nil, *se
	}
	c := a.clientWith()

	commitSHA, err := c.getCommitSHA(ctx, owner, repo, ref)
	if err != nil {
		return nil, mapFetchError(err, owner, repo)
	}
	tree, err := c.getTree(ctx, owner, repo, commitSHA)
	if err != nil {
		return nil, mapFetchError(err, owner, repo)
	}
	pathSet := make(map[string]struct{}, len(tree.Tree))
	for _, e := range tree.Tree {
		if e.Type == "blob" {
			pathSet[e.Path] = struct{}{}
		}
	}
	snap, errs := a.fetchFeature(ctx, c, owner, repo, ref, featureID, pathSet)
	if len(errs) > 0 && snap == nil {
		return nil, errs[0]
	}
	if snap == nil {
		return nil, domain.NewGitHubNotFoundError("https://github.com/" + owner + "/" + repo)
	}
	return snap, nil
}

func (a *adapterWithTransportAndFetch) FetchTask(ctx context.Context, repoURL, taskBranch, featureID, taskID string) (*domain.TaskSnapshot, error) {
	owner, repo, se := parseRepoURL(repoURL)
	if se != nil {
		return nil, *se
	}
	c := a.clientWith()

	taskPath := "docs/features/" + featureID + "/tasks/" + taskID + ".yaml"
	data, err := c.getFileContent(ctx, owner, repo, taskPath, taskBranch)
	if err != nil {
		return nil, err
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

// TestFetchFeature_Success verifies that FetchFeature fetches and parses a single feature.
func TestFetchFeature_Success(t *testing.T) {
	statusData := fixture(t, "status_alpha.yaml")
	taskData := fixture(t, "task_T1.yaml")

	treePaths := []string{
		"workspace.yaml",
		"docs/features/alpha-feature/status.yaml",
		"docs/features/alpha-feature/tasks/T1.yaml",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/commits/main"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(commitJSON("sha-fetch")))
		case strings.Contains(path, "/git/trees/"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(treeJSON(treePaths)))
		case strings.Contains(path, "/contents/docs/features/alpha-feature/status.yaml"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(contentJSON(statusData)))
		case strings.Contains(path, "/contents/docs/features/alpha-feature/tasks/T1.yaml"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(contentJSON(taskData)))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}
	}))
	defer srv.Close()

	adapter := newFetchAdapter(t, proxyTransport(srv.URL))
	snap, err := adapter.FetchFeature(context.Background(), "https://github.com/owner/repo", "main", "alpha-feature")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.FeatureID != "alpha-feature" {
		t.Errorf("expected FeatureID=alpha-feature, got %q", snap.FeatureID)
	}
	if snap.Status != "in_implementation" {
		t.Errorf("expected status in_implementation, got %q", snap.Status)
	}
	if len(snap.Tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(snap.Tasks))
	}
	if snap.Tasks[0].TaskID != "T1" {
		t.Errorf("expected task T1, got %q", snap.Tasks[0].TaskID)
	}
}

// TestFetchFeature_MissingStatus verifies that a missing status.yaml returns an error.
func TestFetchFeature_MissingStatus(t *testing.T) {
	treePaths := []string{"workspace.yaml"} // no feature files

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/commits/main"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(commitJSON("sha-missing")))
		case strings.Contains(path, "/git/trees/"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(treeJSON(treePaths)))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}
	}))
	defer srv.Close()

	adapter := newFetchAdapter(t, proxyTransport(srv.URL))
	snap, err := adapter.FetchFeature(context.Background(), "https://github.com/owner/repo", "main", "alpha-feature")
	if err == nil {
		t.Fatalf("expected error for missing status.yaml, got snap: %+v", snap)
	}
}

// TestFetchTask_Success verifies that FetchTask fetches and parses a single task YAML.
func TestFetchTask_Success(t *testing.T) {
	taskData := fixture(t, "task_T1.yaml")
	taskPath := "docs/features/alpha-feature/tasks/T1.yaml"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		if strings.Contains(path, "/contents/"+taskPath) {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(contentJSON(taskData)))
		} else {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}
	}))
	defer srv.Close()

	adapter := newFetchAdapter(t, proxyTransport(srv.URL))
	snap, err := adapter.FetchTask(context.Background(),
		"https://github.com/owner/repo",
		"feature/alpha-feature-T1",
		"alpha-feature",
		"T1",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.TaskID != "T1" {
		t.Errorf("expected TaskID=T1, got %q", snap.TaskID)
	}
	if snap.Status != "done" {
		t.Errorf("expected status=done, got %q", snap.Status)
	}
	if snap.FeatureID != "alpha-feature" {
		t.Errorf("expected FeatureID=alpha-feature, got %q", snap.FeatureID)
	}
}

// TestFetchTask_NotFound verifies that a missing task YAML returns a not-found error.
func TestFetchTask_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()

	adapter := newFetchAdapter(t, proxyTransport(srv.URL))
	_, err := adapter.FetchTask(context.Background(),
		"https://github.com/owner/repo",
		"feature/alpha-feature-T99",
		"alpha-feature",
		"T99",
	)
	if err == nil {
		t.Fatal("expected error for missing task, got nil")
	}
	se, ok := err.(domain.SourceError)
	if !ok {
		t.Fatalf("expected SourceError, got %T: %v", err, err)
	}
	if se.Code != domain.ErrGitHubNotFound {
		t.Errorf("expected ErrGitHubNotFound, got %s", se.Code)
	}
}

// TestFetchTask_InvalidYAML verifies that an invalid task YAML returns a parse error.
func TestFetchTask_InvalidYAML(t *testing.T) {
	invalidData := fixture(t, "invalid.yaml")
	taskPath := "docs/features/alpha-feature/tasks/T1.yaml"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/contents/"+taskPath) {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(contentJSON(invalidData)))
		} else {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}
	}))
	defer srv.Close()

	adapter := newFetchAdapter(t, proxyTransport(srv.URL))
	_, err := adapter.FetchTask(context.Background(),
		"https://github.com/owner/repo",
		"feature/alpha-feature-T1",
		"alpha-feature",
		"T1",
	)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	se, ok := err.(domain.SourceError)
	if !ok {
		t.Fatalf("expected SourceError, got %T: %v", err, err)
	}
	if se.Code != domain.ErrParserInvalidYAML {
		t.Errorf("expected ErrParserInvalidYAML, got %s", se.Code)
	}
}
