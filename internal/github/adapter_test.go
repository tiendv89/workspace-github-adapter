package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

// fixture reads a file from the testdata directory.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return data
}

// contentJSON returns a JSON-encoded GitHub Contents API response for the given bytes.
func contentJSON(data []byte) string {
	encoded := base64.StdEncoding.EncodeToString(data)
	resp := map[string]interface{}{
		"encoding": "base64",
		"content":  encoded,
		"sha":      "abc123",
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// treeJSON returns a JSON-encoded GitHub Git Trees API response.
func treeJSON(paths []string) string {
	entries := make([]map[string]interface{}, 0, len(paths))
	for _, p := range paths {
		entries = append(entries, map[string]interface{}{
			"path": p,
			"type": "blob",
			"sha":  "abc" + p,
		})
	}
	resp := map[string]interface{}{
		"sha":       "treesha123",
		"tree":      entries,
		"truncated": false,
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// commitJSON returns a minimal GitHub commit response.
func commitJSON(sha string) string {
	resp := map[string]string{"sha": sha}
	b, _ := json.Marshal(resp)
	return string(b)
}

// testServer builds an httptest.Server with route-based dispatch.
// routes maps URL path prefixes to response bodies (JSON strings).
// Unmatched paths return 404.
type routeFunc func(r *http.Request) (int, string)

func newTestServer(routes routeFunc) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status, body := routes(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

// --- Client tests ---

func TestClientDoUnauthorized(t *testing.T) {
	srv := newTestServer(func(r *http.Request) (int, string) {
		return http.StatusUnauthorized, `{"message":"Bad credentials"}`
	})
	defer srv.Close()

	c := newClient("bad-token")
	c.http.Transport = proxyTransport(srv.URL)

	_, err := c.do(context.Background(), srv.URL+"/repos/owner/repo/commits/main")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	se, ok := err.(domain.SourceError)
	if !ok {
		t.Fatalf("expected SourceError, got %T: %v", err, err)
	}
	if se.Code != domain.ErrGitHubUnauthorized {
		t.Errorf("expected ErrGitHubUnauthorized, got %s", se.Code)
	}
}

func TestClientDoRateLimit(t *testing.T) {
	srv := newTestServer(func(r *http.Request) (int, string) {
		return http.StatusForbidden, `{"message":"rate limit exceeded"}`
	})
	defer srv.Close()

	// Override the server to set the rate limit header.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "9999999999")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"rate limit exceeded"}`))
	}))
	defer srv2.Close()

	c := newClient("token")
	_, err := c.do(context.Background(), srv2.URL+"/something")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	se, ok := err.(domain.SourceError)
	if !ok {
		t.Fatalf("expected SourceError, got %T", err)
	}
	if se.Code != domain.ErrGitHubRateLimit {
		t.Errorf("expected ErrGitHubRateLimit, got %s", se.Code)
	}
	if !se.Retryable {
		t.Error("rate limit error should be retryable")
	}
}

func TestClientDoNotFound(t *testing.T) {
	srv := newTestServer(func(r *http.Request) (int, string) {
		return http.StatusNotFound, `{"message":"Not Found"}`
	})
	defer srv.Close()

	c := newClient("")
	_, err := c.do(context.Background(), srv.URL+"/repos/x/y/contents/missing.yaml")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	se, ok := err.(domain.SourceError)
	if !ok {
		t.Fatalf("expected SourceError, got %T", err)
	}
	if se.Code != domain.ErrGitHubNotFound {
		t.Errorf("expected ErrGitHubNotFound, got %s", se.Code)
	}
}

func TestClientDoServerError(t *testing.T) {
	srv := newTestServer(func(r *http.Request) (int, string) {
		return http.StatusInternalServerError, `{"message":"Internal Server Error"}`
	})
	defer srv.Close()

	c := newClient("")
	_, err := c.do(context.Background(), srv.URL+"/something")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	se, ok := err.(domain.SourceError)
	if !ok {
		t.Fatalf("expected SourceError, got %T", err)
	}
	if se.Code != domain.ErrGitHubServerError {
		t.Errorf("expected ErrGitHubServerError, got %s", se.Code)
	}
	if !se.Retryable {
		t.Error("server error should be retryable")
	}
}

func TestClientDoCancelled(t *testing.T) {
	srv := newTestServer(func(r *http.Request) (int, string) {
		return http.StatusOK, `{}`
	})
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	c := newClient("")
	_, err := c.do(ctx, srv.URL+"/something")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// --- Parser tests ---

func TestParseWorkspaceYAML(t *testing.T) {
	data := fixture(t, "workspace.yaml")
	ws, se := parseWorkspaceYAML(data, "workspace.yaml")
	if se != nil {
		t.Fatalf("unexpected error: %v", se)
	}
	if ws.Name != "Test Workspace" {
		t.Errorf("expected name 'Test Workspace', got %q", ws.Name)
	}
	if len(ws.Repos) != 2 {
		t.Errorf("expected 2 repos, got %d", len(ws.Repos))
	}
}

func TestParseWorkspaceYAMLInvalid(t *testing.T) {
	data := fixture(t, "invalid.yaml")
	_, se := parseWorkspaceYAML(data, "workspace.yaml")
	if se == nil {
		t.Fatal("expected parse error, got nil")
	}
	if se.Code != domain.ErrParserInvalidYAML {
		t.Errorf("expected ErrParserInvalidYAML, got %s", se.Code)
	}
}

func TestParseFeatureStatusYAML(t *testing.T) {
	data := fixture(t, "status_alpha.yaml")
	fs, se := parseFeatureStatusYAML(data, "docs/features/alpha/status.yaml")
	if se != nil {
		t.Fatalf("unexpected error: %v", se)
	}
	if fs.Title != "Alpha Feature" {
		t.Errorf("expected title 'Alpha Feature', got %q", fs.Title)
	}
	if fs.featureStatus() != "in_implementation" {
		t.Errorf("expected status 'in_implementation', got %q", fs.featureStatus())
	}
	if fs.currentStage() != "in_implementation" {
		t.Errorf("expected current stage 'in_implementation', got %q", fs.currentStage())
	}
	if len(fs.History) != 2 {
		t.Errorf("expected 2 history entries, got %d", len(fs.History))
	}
}

func TestParseFeatureStatusYAMLWorkspaceSchema(t *testing.T) {
	data := fixture(t, "status_workspace_schema.yaml")
	fs, se := parseFeatureStatusYAML(data, "docs/features/runtime-portable-architecture/status.yaml")
	if se != nil {
		t.Fatalf("unexpected error: %v", se)
	}
	if got := fs.featureID(); got != "runtime-portable-architecture" {
		t.Errorf("featureID: got %q", got)
	}
	if got := fs.featureStatus(); got != "ready_for_implementation" {
		t.Errorf("featureStatus: got %q", got)
	}
	if got := fs.currentStage(); got != "implementation" {
		t.Errorf("currentStage: got %q", got)
	}
	if fs.Stages["product_spec"] == nil {
		t.Errorf("expected product_spec stage to be parsed")
	}
}

func TestParseTaskYAML(t *testing.T) {
	data := fixture(t, "task_T1.yaml")
	task, se := parseTaskYAML(data, "docs/features/alpha/tasks/T1.yaml")
	if se != nil {
		t.Fatalf("unexpected error: %v", se)
	}
	if task.ID != "T1" {
		t.Errorf("expected task ID 'T1', got %q", task.ID)
	}
	if task.Status != "done" {
		t.Errorf("expected status 'done', got %q", task.Status)
	}
	if len(task.Log) != 2 {
		t.Errorf("expected 2 log entries, got %d", len(task.Log))
	}
}

func TestParseTaskYAMLNullBlockedReason(t *testing.T) {
	data := fixture(t, "task_T1.yaml")
	task, se := parseTaskYAML(data, "docs/features/alpha/tasks/T1.yaml")
	if se != nil {
		t.Fatalf("unexpected error: %v", se)
	}
	if reason := blockedReasonString(task.BlockedReason); reason != "" {
		t.Errorf("expected empty blocked reason, got %q", reason)
	}
}

// --- URL tests ---

func TestBuildWebURL(t *testing.T) {
	u := buildWebURL("owner", "repo", "main", "docs/features/x/product-spec.md")
	expected := "https://github.com/owner/repo/blob/main/docs/features/x/product-spec.md"
	if u != expected {
		t.Errorf("expected %q, got %q", expected, u)
	}
}

func TestBuildWebURLLeadingSlash(t *testing.T) {
	u := buildWebURL("owner", "repo", "main", "/docs/features/x/status.yaml")
	if strings.Contains(u, "//docs") {
		t.Errorf("URL should not have double slash: %s", u)
	}
}

// --- Adapter URL parsing tests ---

func TestParseRepoURLHTTPS(t *testing.T) {
	cases := []struct {
		url   string
		owner string
		repo  string
	}{
		{"https://github.com/tiendv89/workspace-github-adapter", "tiendv89", "workspace-github-adapter"},
		{"https://github.com/tiendv89/workspace-github-adapter.git", "tiendv89", "workspace-github-adapter"},
		{"https://github.com/org/repo/", "org", "repo"},
	}
	for _, tc := range cases {
		owner, repo, se := parseRepoURL(tc.url)
		if se != nil {
			t.Errorf("parseRepoURL(%q): unexpected error: %v", tc.url, se)
			continue
		}
		if owner != tc.owner || repo != tc.repo {
			t.Errorf("parseRepoURL(%q) = (%q, %q), want (%q, %q)", tc.url, owner, repo, tc.owner, tc.repo)
		}
	}
}

func TestParseRepoURLSSH(t *testing.T) {
	owner, repo, se := parseRepoURL("git@github.com:tiendv89/workspace-github-adapter.git")
	if se != nil {
		t.Fatalf("unexpected error: %v", se)
	}
	if owner != "tiendv89" || repo != "workspace-github-adapter" {
		t.Errorf("got (%q, %q), want (tiendv89, workspace-github-adapter)", owner, repo)
	}
}

func TestParseRepoURLInvalid(t *testing.T) {
	cases := []string{
		"",
		"not-a-url",
		"https://gitlab.com/owner/repo",
		"https://github.com/onlyone",
	}
	for _, tc := range cases {
		_, _, se := parseRepoURL(tc)
		if se == nil {
			t.Errorf("parseRepoURL(%q): expected error, got nil", tc)
		}
	}
}

// --- Adapter integration tests (using httptest server) ---

// fullWorkspaceServer returns a test server that simulates a full successful GitHub workspace fetch.
func fullWorkspaceServer(t *testing.T) *httptest.Server {
	t.Helper()
	wsData := fixture(t, "workspace.yaml")
	statusData := fixture(t, "status_alpha.yaml")
	taskData := fixture(t, "task_T1.yaml")

	treePaths := []string{
		"workspace.yaml",
		"docs/features/alpha-feature/status.yaml",
		"docs/features/alpha-feature/product-spec.md",
		"docs/features/alpha-feature/technical-design.md",
		"docs/features/alpha-feature/tasks.md",
		"docs/features/alpha-feature/tasks/T1.yaml",
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path

		switch {
		case strings.HasSuffix(path, "/commits/main"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(commitJSON("abc123sha")))

		case strings.Contains(path, "/git/trees/"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(treeJSON(treePaths)))

		case strings.Contains(path, "/contents/workspace.yaml"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(contentJSON(wsData)))

		case strings.Contains(path, "/contents/docs/features/alpha-feature/status.yaml"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(contentJSON(statusData)))

		case strings.Contains(path, "/contents/docs/features/alpha-feature/tasks/T1.yaml"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(contentJSON(taskData)))

		case strings.Contains(path, "/contents/docs/features/alpha-feature/"):
			// Optional docs — return 404 to simulate missing files.
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))

		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}
	}))
}

// proxyTransport returns an http.RoundTripper that rewrites requests to the given base URL.
func proxyTransport(baseURL string) http.RoundTripper {
	return &rewriteTransport{base: baseURL}
}

type rewriteTransport struct {
	base string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Replace the host with the test server, keep the path+query.
	req2 := req.Clone(req.Context())
	req2.URL.Scheme = "http"
	base := strings.TrimSuffix(t.base, "/")
	// Extract path from base to use as server address.
	hostPort := strings.TrimPrefix(base, "http://")
	req2.URL.Host = hostPort
	return http.DefaultTransport.RoundTrip(req2)
}

func TestImportWorkspaceSuccess(t *testing.T) {
	srv := fullWorkspaceServer(t)
	defer srv.Close()

	adapter := &Adapter{token: "test-token"}
	adapter2 := &adapterWithTransport{Adapter: adapter, transport: proxyTransport(srv.URL)}

	snap, err := adapter2.ImportWorkspace(context.Background(), domain.ImportInput{
		RepoURL:       "https://github.com/owner/repo",
		DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap == nil {
		t.Fatal("expected snapshot, got nil")
	}
	if snap.CommitSHA != "abc123sha" {
		t.Errorf("expected CommitSHA 'abc123sha', got %q", snap.CommitSHA)
	}
	if len(snap.Features) != 1 {
		t.Fatalf("expected 1 feature, got %d", len(snap.Features))
	}
	feat := snap.Features[0]
	if feat.FeatureID != "alpha-feature" {
		t.Errorf("expected featureID 'alpha-feature', got %q", feat.FeatureID)
	}
	if feat.Status != "in_implementation" {
		t.Errorf("expected feature status 'in_implementation', got %q", feat.Status)
	}
	if len(feat.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(feat.Tasks))
	}
	task := feat.Tasks[0]
	if task.TaskID != "T1" {
		t.Errorf("expected task ID 'T1', got %q", task.TaskID)
	}
	if task.Status != "done" {
		t.Errorf("expected task status 'done', got %q", task.Status)
	}
	// Documents: status_yaml should be present when the file exists; optional docs may not be present.
	var hasStatusDoc bool
	for _, doc := range feat.Documents {
		if doc.DocumentType == "status_yaml" {
			hasStatusDoc = true
			if doc.URL == "" {
				t.Error("status_yaml document should have a non-empty URL")
			}
		}
	}
	if !hasStatusDoc {
		t.Error("expected status_yaml document link")
	}
	// Repos from workspace.yaml.
	if len(snap.Repos) != 2 {
		t.Errorf("expected 2 repos, got %d", len(snap.Repos))
	}
	if snap.BranchPattern != "feature/{feature_id}-{work_id}" {
		t.Errorf("expected branch pattern from workspace.yaml, got %q", snap.BranchPattern)
	}
}

func TestImportWorkspaceMissingInput(t *testing.T) {
	adapter := New("")
	_, err := adapter.ImportWorkspace(context.Background(), domain.ImportInput{})
	if err == nil {
		t.Fatal("expected error for missing RepoURL")
	}
	se, ok := err.(domain.SourceError)
	if !ok {
		t.Fatalf("expected SourceError, got %T", err)
	}
	if se.Code != domain.ErrValidationMissingInput {
		t.Errorf("expected ErrValidationMissingInput, got %s", se.Code)
	}
}

func TestImportWorkspaceInaccessibleRepo(t *testing.T) {
	srv := newTestServer(func(r *http.Request) (int, string) {
		return http.StatusNotFound, `{"message":"Not Found"}`
	})
	defer srv.Close()

	adapter := &adapterWithTransport{
		Adapter:   &Adapter{token: ""},
		transport: proxyTransport(srv.URL),
	}
	_, err := adapter.ImportWorkspace(context.Background(), domain.ImportInput{
		RepoURL:       "https://github.com/owner/private-repo",
		DefaultBranch: "main",
	})
	if err == nil {
		t.Fatal("expected error for inaccessible repo")
	}
	se, ok := err.(domain.SourceError)
	if !ok {
		t.Fatalf("expected SourceError, got %T", err)
	}
	if se.Code != domain.ErrGitHubNotFound {
		t.Errorf("expected ErrGitHubNotFound, got %s", se.Code)
	}
}

func TestImportWorkspaceRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "9999999999")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"rate limit exceeded"}`))
	}))
	defer srv.Close()

	adapter := &adapterWithTransport{
		Adapter:   &Adapter{token: ""},
		transport: proxyTransport(srv.URL),
	}
	_, err := adapter.ImportWorkspace(context.Background(), domain.ImportInput{
		RepoURL:       "https://github.com/owner/repo",
		DefaultBranch: "main",
	})
	if err == nil {
		t.Fatal("expected error for rate limit")
	}
	se, ok := err.(domain.SourceError)
	if !ok {
		t.Fatalf("expected SourceError, got %T", err)
	}
	if se.Code != domain.ErrGitHubRateLimit {
		t.Errorf("expected ErrGitHubRateLimit, got %s", se.Code)
	}
	if !se.Retryable {
		t.Error("rate limit error should be retryable")
	}
}

func TestImportWorkspaceInvalidFeatureYAML(t *testing.T) {
	wsData := fixture(t, "workspace.yaml")
	invalidYAML := fixture(t, "invalid.yaml")

	treePaths := []string{
		"workspace.yaml",
		"docs/features/bad-feature/status.yaml",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/commits/main"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(commitJSON("sha123")))
		case strings.Contains(path, "/git/trees/"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(treeJSON(treePaths)))
		case strings.Contains(path, "/contents/workspace.yaml"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(contentJSON(wsData)))
		case strings.Contains(path, "/contents/docs/features/bad-feature/status.yaml"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(contentJSON(invalidYAML)))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}
	}))
	defer srv.Close()

	adapter := &adapterWithTransport{
		Adapter:   &Adapter{token: ""},
		transport: proxyTransport(srv.URL),
	}
	snap, err := adapter.ImportWorkspace(context.Background(), domain.ImportInput{
		RepoURL:       "https://github.com/owner/repo",
		DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("unexpected top-level error (expected SourceErrors in snapshot): %v", err)
	}
	// Invalid YAML should produce a source error in the snapshot, not a top-level error.
	if len(snap.SourceErrors) == 0 {
		t.Error("expected source errors in snapshot for invalid YAML feature, got none")
	}
	var hasParseErr bool
	for _, se := range snap.SourceErrors {
		if se.Code == domain.ErrParserInvalidYAML {
			hasParseErr = true
		}
	}
	if !hasParseErr {
		t.Errorf("expected ErrParserInvalidYAML in source errors, got: %v", snap.SourceErrors)
	}
}

func TestImportWorkspaceMissingOptionalFiles(t *testing.T) {
	wsData := fixture(t, "workspace.yaml")
	statusData := fixture(t, "status_alpha.yaml")

	// Only status.yaml present — no product-spec, technical-design, or tasks.
	treePaths := []string{
		"workspace.yaml",
		"docs/features/alpha-feature/status.yaml",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/commits/main"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(commitJSON("sha456")))
		case strings.Contains(path, "/git/trees/"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(treeJSON(treePaths)))
		case strings.Contains(path, "/contents/workspace.yaml"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(contentJSON(wsData)))
		case strings.Contains(path, "/contents/docs/features/alpha-feature/status.yaml"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(contentJSON(statusData)))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}
	}))
	defer srv.Close()

	adapter := &adapterWithTransport{
		Adapter:   &Adapter{token: ""},
		transport: proxyTransport(srv.URL),
	}
	snap, err := adapter.ImportWorkspace(context.Background(), domain.ImportInput{
		RepoURL:       "https://github.com/owner/repo",
		DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(snap.Features) != 1 {
		t.Fatalf("expected 1 feature, got %d", len(snap.Features))
	}
	feat := snap.Features[0]
	// No source errors expected — missing optional files are not errors.
	if len(snap.SourceErrors) != 0 {
		t.Errorf("expected no source errors for missing optional files, got: %v", snap.SourceErrors)
	}
	// status_yaml document should still be present.
	var hasStatusDoc bool
	for _, doc := range feat.Documents {
		if doc.DocumentType == "status_yaml" {
			hasStatusDoc = true
		}
	}
	if !hasStatusDoc {
		t.Error("expected status_yaml document link even when other docs are missing")
	}
	// Tasks list should be empty.
	if len(feat.Tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(feat.Tasks))
	}
}

func TestImportWorkspaceNetworkError(t *testing.T) {
	// Simulate a network failure with a transport that always errors.
	adapter := &adapterWithTransport{
		Adapter:   &Adapter{token: ""},
		transport: &errorTransport{},
	}
	_, err := adapter.ImportWorkspace(context.Background(), domain.ImportInput{
		RepoURL:       "https://github.com/owner/repo",
		DefaultBranch: "main",
	})
	if err == nil {
		t.Fatal("expected network error, got nil")
	}
	// Should be mapped to a SourceError — either network or adapter.
	se, ok := err.(domain.SourceError)
	if !ok {
		t.Fatalf("expected SourceError, got %T: %v", err, err)
	}
	if !se.Retryable {
		t.Errorf("network error should be retryable, got: %+v", se)
	}
}

func TestSyncWorkspace(t *testing.T) {
	srv := fullWorkspaceServer(t)
	defer srv.Close()

	adapter := &adapterWithTransport{
		Adapter:   &Adapter{token: "test-token"},
		transport: proxyTransport(srv.URL),
	}
	snap, err := adapter.SyncWorkspace(context.Background(), "ws-1", "https://github.com/owner/repo", "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap == nil {
		t.Fatal("expected snapshot, got nil")
	}
	if snap.CommitSHA != "abc123sha" {
		t.Errorf("expected CommitSHA 'abc123sha', got %q", snap.CommitSHA)
	}
}

func TestSyncWorkspaceMissingRepoURL(t *testing.T) {
	adapter := New("")
	_, err := adapter.SyncWorkspace(context.Background(), "ws-1", "", "main")
	if err == nil {
		t.Fatal("expected error for missing repoURL")
	}
}

// TestImportWorkspaceSingleCommitSHAFetch verifies that the commits endpoint is called
// exactly once per ImportWorkspace call (not duplicated by getTree).
func TestImportWorkspaceSingleCommitSHAFetch(t *testing.T) {
	wsData := fixture(t, "workspace.yaml")
	statusData := fixture(t, "status_alpha.yaml")
	taskData := fixture(t, "task_T1.yaml")

	treePaths := []string{
		"workspace.yaml",
		"docs/features/alpha-feature/status.yaml",
		"docs/features/alpha-feature/tasks/T1.yaml",
	}

	commitCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/commits/main"):
			commitCalls++
			w.WriteHeader(200)
			_, _ = w.Write([]byte(commitJSON("onlyonce123")))
		case strings.Contains(path, "/git/trees/"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(treeJSON(treePaths)))
		case strings.Contains(path, "/contents/workspace.yaml"):
			w.WriteHeader(200)
			_, _ = w.Write([]byte(contentJSON(wsData)))
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

	adapter := &adapterWithTransport{
		Adapter:   &Adapter{token: "test-token"},
		transport: proxyTransport(srv.URL),
	}
	snap, err := adapter.ImportWorkspace(context.Background(), domain.ImportInput{
		RepoURL:       "https://github.com/owner/repo",
		DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.CommitSHA != "onlyonce123" {
		t.Errorf("expected CommitSHA 'onlyonce123', got %q", snap.CommitSHA)
	}
	if commitCalls != 1 {
		t.Errorf("expected commits endpoint to be called exactly once, got %d calls", commitCalls)
	}
}

// errorTransport is an http.RoundTripper that always returns a network error.
type errorTransport struct{}

func (e *errorTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("simulated network failure")
}

// --- Helper types for test transport injection ---

// adapterWithTransport wraps Adapter and injects a custom http.Transport.
type adapterWithTransport struct {
	*Adapter
	transport http.RoundTripper
}

func (a *adapterWithTransport) ImportWorkspace(ctx context.Context, input domain.ImportInput) (*domain.WorkspaceSnapshot, error) {
	if err := validateInput(input); err != nil {
		return nil, *err
	}
	token := input.Token
	if token == "" {
		token = a.token
	}
	c := newClient(token)
	c.http = &http.Client{Transport: a.transport}

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

func (a *adapterWithTransport) SyncWorkspace(ctx context.Context, workspaceID, repoURL, ref string) (*domain.WorkspaceSnapshot, error) {
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
	c.http = &http.Client{Transport: a.transport}
	return a.fetchSnapshot(ctx, c, owner, repo, ref, workspaceID)
}

// --- Feature discovery tests ---

func TestDiscoverFeatureIDs(t *testing.T) {
	entries := []treeEntry{
		{Path: "docs/features/alpha-feature/status.yaml", Type: "blob"},
		{Path: "docs/features/beta-feature/status.yaml", Type: "blob"},
		{Path: "docs/features/beta-feature/status.yaml", Type: "blob"},
		{Path: "docs/features/alpha-feature/product-spec.md", Type: "blob"},
		{Path: "docs/features/gamma-feature/product-spec.md", Type: "blob"},
		{Path: "docs/features/delta-feature/technical-design.md", Type: "blob"},
		{Path: "workspace.yaml", Type: "blob"},
		{Path: "docs/features/epsilon-tree", Type: "tree"}, // tree, not blob — ignored
	}
	ids := discoverFeatureIDs(entries)
	if len(ids) != 4 {
		t.Errorf("expected 4 feature IDs, got %d: %v", len(ids), ids)
	}
}

func TestExtractFeatureID(t *testing.T) {
	cases := []struct {
		path   string
		want   string
		wantOK bool
	}{
		{"docs/features/alpha-feature/status.yaml", "alpha-feature", true},
		{"docs/features/beta/status.yaml", "beta", true},
		{"docs/features/alpha-feature/product-spec.md", "alpha-feature", true},
		{"docs/features/alpha-feature/technical-design.md", "alpha-feature", true},
		{"workspace.yaml", "", false},
		{"docs/features/alpha/tasks/T1.yaml", "", false},
	}
	for _, tc := range cases {
		got, ok := extractFeatureID(tc.path)
		if ok != tc.wantOK {
			t.Errorf("extractFeatureID(%q): ok=%v want %v", tc.path, ok, tc.wantOK)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("extractFeatureID(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestTaskFileBase(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"docs/features/x/tasks/T1.yaml", "T1"},
		{"docs/features/x/tasks/T23.yaml", "T23"},
		{"docs/features/x/tasks.md", ""},
		{"docs/features/x/tasks/notes.yaml", ""},
	}
	for _, tc := range cases {
		got := taskFileBase(tc.path)
		if got != tc.want {
			t.Errorf("taskFileBase(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// --- Timestamp parsing tests ---

func TestParseTimestamp(t *testing.T) {
	cases := []struct {
		input   string
		wantNil bool // whether we expect zero time
	}{
		{"2026-05-15T14:06:12+0700", false},
		{"2026-05-15T18:44:37.415Z", false},
		{"2026-01-01T10:00:00+0000", false},
		{"2026-01-05T12:00:00+0000", false},
		{"", true},
		{"not-a-timestamp", true},
	}
	for _, tc := range cases {
		got := parseTimestamp(tc.input)
		isZero := got.IsZero()
		if isZero != tc.wantNil {
			t.Errorf("parseTimestamp(%q): isZero=%v, wantNil=%v", tc.input, isZero, tc.wantNil)
		}
	}
}

func TestActivityEventOccurredAt(t *testing.T) {
	log := []activityYAML{
		{Action: "created", By: "dev@example.com", At: "2026-01-01T10:00:00+0000", Note: "Created"},
	}
	events := mapActivityLog(log, "feature", "alpha-feature", "")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].OccurredAt.IsZero() {
		t.Error("expected non-zero OccurredAt for a valid timestamp")
	}
	if events[0].Actor != "dev@example.com" {
		t.Errorf("expected actor 'dev@example.com', got %q", events[0].Actor)
	}
}

func TestImportWorkspaceActivityTimestamps(t *testing.T) {
	srv := fullWorkspaceServer(t)
	defer srv.Close()

	adapter := &adapterWithTransport{
		Adapter:   &Adapter{token: "test-token"},
		transport: proxyTransport(srv.URL),
	}
	snap, err := adapter.ImportWorkspace(context.Background(), domain.ImportInput{
		RepoURL:       "https://github.com/owner/repo",
		DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(snap.Features) == 0 {
		t.Fatal("no features in snapshot")
	}
	feat := snap.Features[0]
	// status_alpha.yaml has 2 history entries with valid timestamps.
	if len(feat.Activity) != 2 {
		t.Fatalf("expected 2 activity events, got %d", len(feat.Activity))
	}
	for i, ev := range feat.Activity {
		if ev.OccurredAt.IsZero() {
			t.Errorf("activity[%d]: OccurredAt should not be zero for valid timestamp", i)
		}
	}
	// Task T1 has 2 log entries with valid timestamps.
	if len(feat.Tasks) == 0 {
		t.Fatal("no tasks in feature")
	}
	task := feat.Tasks[0]
	if len(task.Activity) != 2 {
		t.Fatalf("expected 2 task activity events, got %d", len(task.Activity))
	}
	for i, ev := range task.Activity {
		if ev.OccurredAt.IsZero() {
			t.Errorf("task activity[%d]: OccurredAt should not be zero for valid timestamp", i)
		}
	}
}
