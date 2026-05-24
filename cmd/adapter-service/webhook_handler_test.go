package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tiendv89/workspace-github-adapter/internal/database"
	"github.com/tiendv89/workspace-github-adapter/internal/domain"
	"github.com/tiendv89/workspace-github-adapter/internal/queue"
	"github.com/tiendv89/workspace-github-adapter/internal/webhook"
)

// buildSig computes the HMAC-SHA256 signature for a payload.
func buildSig(secret string, body []byte) string { //nolint:unparam
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// TestWebhookHandler_InvalidSignature verifies that requests with wrong HMAC are rejected.
func TestWebhookHandler_InvalidSignature(t *testing.T) {
	h := &serviceHandler{webhookSecret: "mysecret"}

	body := []byte(`{"ref":"refs/heads/main","repository":{"clone_url":"https://github.com/o/r"},"commits":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", "sha256=badsignature")
	rec := httptest.NewRecorder()

	h.webhookHandler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// TestWebhookHandler_MethodNotAllowed verifies that non-POST requests are rejected.
func TestWebhookHandler_MethodNotAllowed(t *testing.T) {
	h := &serviceHandler{}
	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	rec := httptest.NewRecorder()
	h.webhookHandler(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

// TestWebhookHandler_NonPushEvent verifies that non-push events are ignored with 200.
func TestWebhookHandler_NonPushEvent(t *testing.T) {
	secret := "mysecret"
	h := &serviceHandler{webhookSecret: secret}
	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-Hub-Signature-256", buildSig(secret, body))
	rec := httptest.NewRecorder()
	h.webhookHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != "ignored" {
		t.Errorf("expected status=ignored, got %q", resp["status"])
	}
}

func TestBasePushTargetedSyncPayloads(t *testing.T) {
	ws := &workspaceWebhookInfo{
		workspaceID:   "11111111-1111-1111-1111-111111111111",
		repoURL:       "https://github.com/acme/workspace",
		defaultBranch: "main",
	}
	ev := &webhook.PushEvent{
		Commits: []webhook.Commit{
			{
				Modified: []string{
					"docs/features/workspace-data-backend/status.yaml",
					"docs/features/workspace-data-backend/tasks/T7.yaml",
				},
				Added: []string{
					"docs/features/another-feature/product-spec.md",
				},
			},
		},
	}

	payloads := basePushTargetedSyncPayloads(ws, "main", ev)
	if len(payloads) != 2 {
		t.Fatalf("expected 2 targeted sync payloads, got %d: %+v", len(payloads), payloads)
	}
	for _, payload := range payloads {
		if payload.Mode != "targeted" {
			t.Errorf("expected targeted mode, got %q", payload.Mode)
		}
		if payload.Trigger != "webhook_base" {
			t.Errorf("expected webhook_base trigger, got %q", payload.Trigger)
		}
		if payload.WorkspaceID != ws.workspaceID || payload.RepoURL != ws.repoURL || payload.Ref != "main" {
			t.Errorf("unexpected common payload fields: %+v", payload)
		}
	}
	gotFeatures := map[string]bool{}
	for _, payload := range payloads {
		gotFeatures[payload.FeatureID] = true
	}
	if !gotFeatures["workspace-data-backend"] || !gotFeatures["another-feature"] {
		t.Fatalf("missing targeted feature payloads: %+v", payloads)
	}
}

func TestBasePushTargetedSyncPayloads_NoFeaturePaths(t *testing.T) {
	ws := &workspaceWebhookInfo{
		workspaceID:   "11111111-1111-1111-1111-111111111111",
		repoURL:       "https://github.com/acme/workspace",
		defaultBranch: "main",
	}
	ev := &webhook.PushEvent{
		Commits: []webhook.Commit{{Modified: []string{"README.md"}}},
	}

	payloads := basePushTargetedSyncPayloads(ws, "main", ev)
	if len(payloads) != 0 {
		t.Fatalf("expected no targeted sync payloads, got %+v", payloads)
	}
}

func TestWebhookHandler_BaseBranchEnqueuesTargetedSyncs(t *testing.T) {
	secret := "mysecret"
	enqueuer := &recordingEnqueuer{}
	h := &serviceHandler{
		q:             database.New(&webhookSourceDB{src: testGitHubSource(t)}),
		queue:         enqueuer,
		webhookSecret: secret,
	}
	body := []byte(`{
		"ref":"refs/heads/main",
		"after":"abc123",
		"repository":{"clone_url":"https://github.com/acme/workspace.git"},
		"commits":[{"added":["docs/features/alpha/status.yaml"],"modified":["docs/features/beta/tasks.md"],"removed":["README.md"]}]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", buildSig(secret, body))
	rec := httptest.NewRecorder()

	h.webhookHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(enqueuer.workspaceSyncs) != 2 {
		t.Fatalf("expected 2 workspace sync tasks, got %d: %+v", len(enqueuer.workspaceSyncs), enqueuer.workspaceSyncs)
	}
	for _, payload := range enqueuer.workspaceSyncs {
		if payload.Mode != "targeted" || payload.Trigger != "webhook_base" {
			t.Fatalf("expected base webhook targeted sync payload, got %+v", payload)
		}
	}
}

func TestWebhookHandler_TaskBranchEnqueueFailureReturnsServerError(t *testing.T) {
	secret := "mysecret"
	h := &serviceHandler{
		q:             database.New(&webhookSourceDB{src: testGitHubSource(t)}),
		queue:         &recordingEnqueuer{err: errors.New("redis unavailable")},
		webhookSecret: secret,
	}
	body := []byte(`{
		"ref":"refs/heads/feature/workspace-data-backend-T7",
		"after":"abc123",
		"repository":{"clone_url":"https://github.com/acme/workspace.git"},
		"commits":[{"modified":["docs/features/workspace-data-backend/tasks/T7.yaml"]}]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", buildSig(secret, body))
	rec := httptest.NewRecorder()

	h.webhookHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 so GitHub can retry, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWebhookHandler_TaskBranchUsesWorkspaceBranchPattern(t *testing.T) {
	secret := "mysecret"
	enqueuer := &recordingEnqueuer{}
	h := &serviceHandler{
		q: database.New(&webhookSourceDB{
			src:       testGitHubSource(t),
			workspace: testWorkspace(t, "workspaces/{feature_id}/tasks/{work_id}"),
		}),
		queue:         enqueuer,
		webhookSecret: secret,
	}
	body := []byte(`{
		"ref":"refs/heads/workspaces/workspace-data-backend/tasks/T7",
		"after":"abc123",
		"repository":{"clone_url":"https://github.com/acme/workspace.git"},
		"commits":[{"modified":["docs/features/workspace-data-backend/tasks/T7.yaml"]}]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", buildSig(secret, body))
	rec := httptest.NewRecorder()

	h.webhookHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(enqueuer.taskSyncs) != 1 {
		t.Fatalf("expected 1 task sync, got %d: %+v", len(enqueuer.taskSyncs), enqueuer.taskSyncs)
	}
	got := enqueuer.taskSyncs[0]
	if got.FeatureID != "workspace-data-backend" || got.TaskID != "T7" {
		t.Fatalf("task sync payload = %+v, want feature workspace-data-backend task T7", got)
	}
}

func TestWebhookHandler_FeatureBranchUsesWorkspaceBranchPattern(t *testing.T) {
	secret := "mysecret"
	enqueuer := &recordingEnqueuer{}
	h := &serviceHandler{
		q: database.New(&webhookSourceDB{
			src:       testGitHubSource(t),
			workspace: testWorkspace(t, "workspaces/{feature_id}/tasks/{work_id}"),
		}),
		queue:         enqueuer,
		webhookSecret: secret,
	}
	body := []byte(`{
		"ref":"refs/heads/workspaces/workspace-data-backend",
		"after":"abc123",
		"repository":{"clone_url":"https://github.com/acme/workspace.git"},
		"commits":[{"modified":["docs/features/workspace-data-backend/tasks.md"]}]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", buildSig(secret, body))
	rec := httptest.NewRecorder()

	h.webhookHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(enqueuer.workspaceSyncs) != 1 {
		t.Fatalf("expected 1 workspace sync, got %d: %+v", len(enqueuer.workspaceSyncs), enqueuer.workspaceSyncs)
	}
	got := enqueuer.workspaceSyncs[0]
	if got.FeatureID != "workspace-data-backend" || got.Mode != "targeted" || got.Trigger != "webhook_feature" {
		t.Fatalf("workspace sync payload = %+v, want targeted webhook_feature for workspace-data-backend", got)
	}
}

func TestImportWorkspaceHandler_GitHubNotFoundDoesNotPersistPlaceholder(t *testing.T) {
	github := &recordingGitHubAdapter{
		importErr: domain.NewGitHubNotFoundError("https://github.com/acme/missing"),
	}
	db := &recordingWorkspaceDB{}
	store := &importNoRowsDB{}
	enqueuer := &recordingEnqueuer{}
	h := &serviceHandler{
		db:     db,
		q:      database.New(store),
		github: github,
		queue:  enqueuer,
	}

	req := httptest.NewRequest(http.MethodPost, "/internal/workspaces/import", strings.NewReader(`{
		"repo_url":"https://github.com/acme/missing",
		"default_branch":"main"
	}`))
	rec := httptest.NewRecorder()

	h.importWorkspaceHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing GitHub repo before DB write, got %d body=%s", rec.Code, rec.Body.String())
	}
	if github.metadataCalls != 1 {
		t.Fatalf("expected one GitHub metadata preflight, got %d", github.metadataCalls)
	}
	if github.importCalls != 0 {
		t.Fatalf("expected full GitHub import not to run in HTTP handler, got %d", github.importCalls)
	}
	if store.writeQueries != 0 {
		t.Fatalf("expected no placeholder writes, got %d write queries", store.writeQueries)
	}
	if db.saveSnapshotCalls != 0 {
		t.Fatalf("expected SaveSnapshot not to run, got %d calls", db.saveSnapshotCalls)
	}
	if len(enqueuer.workspaceSyncs) != 0 {
		t.Fatalf("expected no queued workspace syncs, got %+v", enqueuer.workspaceSyncs)
	}
}

func TestImportWorkspaceHandler_DifferentRepoWithExistingSlugReturnsConflict(t *testing.T) {
	github := &recordingGitHubAdapter{
		metadata: &domain.WorkspaceSnapshot{Name: "Project Workspace", Slug: "project-workspace", ManagementRepoID: "repo"},
	}
	store := &importSlugConflictDB{}
	enqueuer := &recordingEnqueuer{}
	h := &serviceHandler{
		q:      database.New(store),
		github: github,
		queue:  enqueuer,
	}

	req := httptest.NewRequest(http.MethodPost, "/internal/workspaces/import", strings.NewReader(`{
		"repo_url":"https://github.com/Kadamato/test_workspace.git",
		"default_branch":"main",
		"name":"Project Workspace"
	}`))
	rec := httptest.NewRecorder()

	h.importWorkspaceHandler(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate slug on different repo, got %d body=%s", rec.Code, rec.Body.String())
	}
	var apiErr domain.APIError
	if err := json.NewDecoder(rec.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if apiErr.Code != domain.ErrDatabaseConflict {
		t.Fatalf("expected error code %s, got %s", domain.ErrDatabaseConflict, apiErr.Code)
	}
	if apiErr.Retryable {
		t.Fatal("expected duplicate slug conflict to be non-retryable")
	}
	if len(enqueuer.workspaceSyncs) != 0 {
		t.Fatalf("expected no queued workspace syncs, got %+v", enqueuer.workspaceSyncs)
	}
}

// TestIsDedupeError verifies dedupe error detection.
func TestIsDedupeError_Match(t *testing.T) {
	err := &fakeError{"task already exists"}
	if !isDedupeError(err) {
		t.Error("expected dedup error to match")
	}
}

func TestIsDedupeError_NoMatch(t *testing.T) {
	err := &fakeError{"some other error"}
	if isDedupeError(err) {
		t.Error("expected non-dedup error to not match")
	}
}

func TestIsDedupeError_Nil(t *testing.T) {
	if isDedupeError(nil) {
		t.Error("expected nil error to not match")
	}
}

type fakeError struct{ msg string }

func (e *fakeError) Error() string { return e.msg }

type recordingGitHubAdapter struct {
	metadataCalls int
	importCalls   int
	importErr     error
	metadata      *domain.WorkspaceSnapshot
}

func (g *recordingGitHubAdapter) FetchWorkspaceMetadata(_ context.Context, _ domain.ImportInput) (*domain.WorkspaceSnapshot, error) {
	g.metadataCalls++
	if g.importErr != nil {
		return nil, g.importErr
	}
	if g.metadata != nil {
		return g.metadata, nil
	}
	return &domain.WorkspaceSnapshot{Name: "Test Workspace", Slug: "test-workspace", ManagementRepoID: "repo"}, nil
}

func (g *recordingGitHubAdapter) ImportWorkspace(_ context.Context, _ domain.ImportInput) (*domain.WorkspaceSnapshot, error) {
	g.importCalls++
	if g.importErr != nil {
		return nil, g.importErr
	}
	return &domain.WorkspaceSnapshot{}, nil
}

func (g *recordingGitHubAdapter) SyncWorkspace(context.Context, string, string, string) (*domain.WorkspaceSnapshot, error) {
	return nil, errors.New("not implemented")
}

func (g *recordingGitHubAdapter) FetchFeature(context.Context, string, string, string) (*domain.FeatureSnapshot, error) {
	return nil, errors.New("not implemented")
}

func (g *recordingGitHubAdapter) FetchTask(context.Context, string, string, string, string) (*domain.TaskSnapshot, error) {
	return nil, errors.New("not implemented")
}

type recordingWorkspaceDB struct {
	saveSnapshotCalls int
}

func (db *recordingWorkspaceDB) ListWorkspaces(context.Context) ([]domain.WorkspaceSummary, error) {
	return nil, errors.New("not implemented")
}

func (db *recordingWorkspaceDB) GetWorkspace(context.Context, string) (*domain.WorkspaceDetail, error) {
	return nil, errors.New("not implemented")
}

func (db *recordingWorkspaceDB) GetFeature(context.Context, string, string) (*domain.FeatureDetail, error) {
	return nil, errors.New("not implemented")
}

func (db *recordingWorkspaceDB) GetTask(context.Context, string, string, string) (*domain.TaskDetail, error) {
	return nil, errors.New("not implemented")
}

func (db *recordingWorkspaceDB) ListFeatureTasks(context.Context, string, string) ([]domain.TaskSummary, error) {
	return nil, errors.New("not implemented")
}

func (db *recordingWorkspaceDB) ListActivity(context.Context, string, domain.ActivityScope) ([]domain.ActivityEvent, error) {
	return nil, errors.New("not implemented")
}

func (db *recordingWorkspaceDB) SaveSnapshot(context.Context, string, *domain.WorkspaceSnapshot) error {
	db.saveSnapshotCalls++
	return nil
}

func (db *recordingWorkspaceDB) SaveFeatureSnapshot(context.Context, string, domain.FeatureSnapshot) error {
	return errors.New("not implemented")
}

func (db *recordingWorkspaceDB) SaveTaskSnapshot(context.Context, string, domain.TaskSnapshot) error {
	return errors.New("not implemented")
}

func (db *recordingWorkspaceDB) GetActiveSnapshot(context.Context, string) (*domain.WorkspaceSnapshot, error) {
	return nil, errors.New("not implemented")
}

func (db *recordingWorkspaceDB) GetLatestSyncRun(context.Context, string) (*domain.SyncRun, error) {
	return nil, errors.New("not implemented")
}

type importNoRowsDB struct {
	writeQueries int
}

func (db *importNoRowsDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (db *importNoRowsDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("not implemented")
}

func (db *importNoRowsDB) QueryRow(_ context.Context, query string, _ ...interface{}) pgx.Row {
	if strings.Contains(query, "INSERT INTO workspaces") ||
		strings.Contains(query, "INSERT INTO workspace_github_sources") ||
		strings.Contains(query, "INSERT INTO workspace_sync_runs") {
		db.writeQueries++
	}
	return errRow{err: pgx.ErrNoRows}
}

type errRow struct {
	err error
}

func (r errRow) Scan(...any) error {
	return r.err
}

type importSlugConflictDB struct{}

func (db *importSlugConflictDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (db *importSlugConflictDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("not implemented")
}

func (db *importSlugConflictDB) QueryRow(_ context.Context, query string, _ ...interface{}) pgx.Row {
	switch {
	case strings.Contains(query, "FROM workspace_github_sources"):
		return errRow{err: pgx.ErrNoRows}
	case strings.Contains(query, "INSERT INTO workspaces"):
		return errRow{err: &pgconn.PgError{Code: "23505", ConstraintName: "workspaces_slug_unique"}}
	default:
		return errRow{err: errors.New("unexpected query")}
	}
}

type recordingEnqueuer struct {
	err            error
	workspaceSyncs []queue.WorkspaceSyncPayload
	taskSyncs      []queue.TaskSyncPayload
}

func (e *recordingEnqueuer) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	if e.err != nil {
		return nil, e.err
	}
	switch task.Type() {
	case queue.TypeWorkspaceSync:
		var payload queue.WorkspaceSyncPayload
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return nil, err
		}
		e.workspaceSyncs = append(e.workspaceSyncs, payload)
	case queue.TypeTaskSync:
		var payload queue.TaskSyncPayload
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return nil, err
		}
		e.taskSyncs = append(e.taskSyncs, payload)
	}
	return &asynq.TaskInfo{ID: "task-id", Queue: queue.QueueDefault, Type: task.Type()}, nil
}

type webhookSourceDB struct {
	src       database.WorkspaceGitHubSource
	workspace database.Workspace
}

func (db *webhookSourceDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("not implemented")
}

func (db *webhookSourceDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("not implemented")
}

func (db *webhookSourceDB) QueryRow(_ context.Context, query string, _ ...interface{}) pgx.Row {
	if strings.Contains(query, "FROM workspaces") {
		ws := db.workspace
		if !ws.ID.Valid {
			ws = testWorkspaceFromSource(db.src, "")
		}
		return webhookWorkspaceRow{workspace: ws}
	}
	return webhookSourceRow{src: db.src}
}

type webhookSourceRow struct {
	src database.WorkspaceGitHubSource
}

func (r webhookSourceRow) Scan(dest ...any) error {
	values := []any{
		r.src.ID,
		r.src.WorkspaceID,
		r.src.RepoURL,
		r.src.RepoOwner,
		r.src.RepoName,
		r.src.DefaultBranch,
		r.src.CreatedAt,
		r.src.UpdatedAt,
	}
	if len(dest) != len(values) {
		return fmt.Errorf("expected %d scan destinations, got %d", len(values), len(dest))
	}
	for i := range dest {
		switch d := dest[i].(type) {
		case *pgtype.UUID:
			*d = values[i].(pgtype.UUID) //nolint:errcheck
		case *string:
			*d = values[i].(string) //nolint:errcheck
		case **string:
			*d = values[i].(*string) //nolint:errcheck
		case *pgtype.Timestamptz:
			*d = values[i].(pgtype.Timestamptz) //nolint:errcheck
		default:
			return fmt.Errorf("unsupported scan destination %T", dest[i])
		}
	}
	return nil
}

type webhookWorkspaceRow struct {
	workspace database.Workspace
}

func (r webhookWorkspaceRow) Scan(dest ...any) error {
	values := []any{
		r.workspace.ID,
		r.workspace.Slug,
		r.workspace.Name,
		r.workspace.ManagementRepoID,
		r.workspace.BranchPattern,
		r.workspace.CreatedAt,
		r.workspace.UpdatedAt,
	}
	if len(dest) != len(values) {
		return fmt.Errorf("expected %d scan destinations, got %d", len(values), len(dest))
	}
	for i := range dest {
		switch d := dest[i].(type) {
		case *pgtype.UUID:
			*d = values[i].(pgtype.UUID) //nolint:errcheck
		case *string:
			*d = values[i].(string) //nolint:errcheck
		case **string:
			*d = values[i].(*string) //nolint:errcheck
		case *pgtype.Timestamptz:
			*d = values[i].(pgtype.Timestamptz) //nolint:errcheck
		default:
			return fmt.Errorf("unsupported scan destination %T", dest[i])
		}
	}
	return nil
}

func testGitHubSource(t *testing.T) database.WorkspaceGitHubSource {
	t.Helper()
	workspaceID := mustPGUUID(t, "11111111-1111-1111-1111-111111111111")
	sourceID := mustPGUUID(t, "22222222-2222-2222-2222-222222222222")
	defaultBranch := "main"
	return database.WorkspaceGitHubSource{
		ID:            sourceID,
		WorkspaceID:   workspaceID,
		RepoURL:       "https://github.com/acme/workspace",
		RepoOwner:     "acme",
		RepoName:      "workspace",
		DefaultBranch: &defaultBranch,
	}
}

func testWorkspace(t *testing.T, branchPattern string) database.Workspace {
	t.Helper()
	workspaceID := mustPGUUID(t, "11111111-1111-1111-1111-111111111111")
	return testWorkspaceFromID(workspaceID, branchPattern)
}

func testWorkspaceFromSource(src database.WorkspaceGitHubSource, branchPattern string) database.Workspace {
	return testWorkspaceFromID(src.WorkspaceID, branchPattern)
}

func testWorkspaceFromID(workspaceID pgtype.UUID, branchPattern string) database.Workspace {
	var branchPatternPtr *string
	if branchPattern != "" {
		branchPatternPtr = &branchPattern
	}
	return database.Workspace{
		ID:               workspaceID,
		Slug:             "workspace",
		Name:             "Workspace",
		ManagementRepoID: "management-repo",
		BranchPattern:    branchPatternPtr,
	}
}

func mustPGUUID(t *testing.T, raw string) pgtype.UUID {
	t.Helper()
	var uid pgtype.UUID
	if err := uid.Scan(raw); err != nil {
		t.Fatalf("scan uuid %s: %v", raw, err)
	}
	return uid
}
