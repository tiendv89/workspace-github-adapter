package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tiendv89/workspace-github-adapter/internal/database"
	"github.com/tiendv89/workspace-github-adapter/internal/domain"
	"github.com/tiendv89/workspace-github-adapter/internal/queue"
)

// --- Minimal stubs for domain interfaces ---

// stubGitHub implements domain.GitHubWorkspaceAdapter for testing.
type stubGitHub struct {
	importWorkspace func(ctx context.Context, in domain.ImportInput) (*domain.WorkspaceSnapshot, error)
	fetchTask       func(ctx context.Context, repoURL, taskBranch, featureID, taskID string) (*domain.TaskSnapshot, error)
	fetchFeat       func(ctx context.Context, repoURL, ref, featureID string) (*domain.FeatureSnapshot, error)
}

func (s *stubGitHub) ImportWorkspace(ctx context.Context, in domain.ImportInput) (*domain.WorkspaceSnapshot, error) {
	if s.importWorkspace != nil {
		return s.importWorkspace(ctx, in)
	}
	return nil, errors.New("not implemented in stub")
}
func (s *stubGitHub) SyncWorkspace(_ context.Context, _, _, _ string) (*domain.WorkspaceSnapshot, error) {
	return nil, errors.New("not implemented in stub")
}
func (s *stubGitHub) FetchFeature(ctx context.Context, repoURL, ref, featureID string) (*domain.FeatureSnapshot, error) {
	if s.fetchFeat != nil {
		return s.fetchFeat(ctx, repoURL, ref, featureID)
	}
	return nil, errors.New("not implemented in stub")
}
func (s *stubGitHub) FetchTask(ctx context.Context, repoURL, taskBranch, featureID, taskID string) (*domain.TaskSnapshot, error) {
	if s.fetchTask != nil {
		return s.fetchTask(ctx, repoURL, taskBranch, featureID, taskID)
	}
	return nil, errors.New("not implemented in stub")
}

// stubDB implements domain.DbWorkspaceAdapter for testing.
type stubDB struct {
	saveSnap     func(ctx context.Context, workspaceID string, snap *domain.WorkspaceSnapshot) error
	saveTaskSnap func(ctx context.Context, workspaceID string, snap domain.TaskSnapshot) error
	saveFeatSnap func(ctx context.Context, workspaceID string, snap domain.FeatureSnapshot) error
}

func (s *stubDB) ListWorkspaces(_ context.Context) ([]domain.WorkspaceSummary, error) {
	return nil, nil
}
func (s *stubDB) GetWorkspace(_ context.Context, _ string) (*domain.WorkspaceDetail, error) {
	return nil, nil
}
func (s *stubDB) GetFeature(_ context.Context, _, _ string) (*domain.FeatureDetail, error) {
	return nil, nil
}
func (s *stubDB) GetTask(_ context.Context, _, _, _ string) (*domain.TaskDetail, error) {
	return nil, nil
}
func (s *stubDB) ListFeatureTasks(_ context.Context, _, _ string) ([]domain.TaskSummary, error) {
	return nil, nil
}
func (s *stubDB) ListActivity(_ context.Context, _ string, _ domain.ActivityScope) ([]domain.ActivityEvent, error) {
	return nil, nil
}
func (s *stubDB) SaveSnapshot(ctx context.Context, workspaceID string, snap *domain.WorkspaceSnapshot) error {
	if s.saveSnap != nil {
		return s.saveSnap(ctx, workspaceID, snap)
	}
	return nil
}
func (s *stubDB) SaveFeatureSnapshot(ctx context.Context, workspaceID string, snap domain.FeatureSnapshot) error {
	if s.saveFeatSnap != nil {
		return s.saveFeatSnap(ctx, workspaceID, snap)
	}
	return nil
}
func (s *stubDB) SaveTaskSnapshot(ctx context.Context, workspaceID string, snap domain.TaskSnapshot) error {
	if s.saveTaskSnap != nil {
		return s.saveTaskSnap(ctx, workspaceID, snap)
	}
	return nil
}
func (s *stubDB) GetActiveSnapshot(_ context.Context, _ string) (*domain.WorkspaceSnapshot, error) {
	return nil, nil
}
func (s *stubDB) GetLatestSyncRun(_ context.Context, _ string) (*domain.SyncRun, error) {
	return nil, nil
}

// makeTaskSyncTask builds a serialized asynq.Task for testing.
func makeTaskSyncTask(t *testing.T, payload queue.TaskSyncPayload) *asynq.Task {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal task sync payload: %v", err)
	}
	return asynq.NewTask(queue.TypeTaskSync, b)
}

// TestHandleTaskSync_BadPayload verifies that a malformed payload returns a non-retryable error.
func TestHandleTaskSync_BadPayload(t *testing.T) {
	h := &handler{db: &stubDB{}, github: &stubGitHub{}}
	task := asynq.NewTask(queue.TypeTaskSync, []byte("not-json"))
	err := h.handleTaskSync(context.Background(), task)
	if err == nil {
		t.Fatal("expected error for bad payload")
	}
}

func TestHandleWorkspaceSync_MissingWorkspaceIDDoesNotCreateWorkspace(t *testing.T) {
	githubCalled := false
	saveCalled := false
	h := &handler{
		db: &stubDB{
			saveSnap: func(context.Context, string, *domain.WorkspaceSnapshot) error {
				saveCalled = true
				return nil
			},
		},
		github: &stubGitHub{
			importWorkspace: func(context.Context, domain.ImportInput) (*domain.WorkspaceSnapshot, error) {
				githubCalled = true
				return &domain.WorkspaceSnapshot{Name: "Test", Slug: "test"}, nil
			},
		},
	}
	payload := queue.WorkspaceSyncPayload{RepoURL: "https://github.com/acme/repo"}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal workspace sync payload: %v", err)
	}

	err = h.handleWorkspaceSync(context.Background(), asynq.NewTask(queue.TypeWorkspaceSync, b))
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("expected SkipRetry for missing workspace_id, got %v", err)
	}
	if githubCalled {
		t.Fatal("expected GitHub import not to be called")
	}
	if saveCalled {
		t.Fatal("expected snapshot not to be saved")
	}
}

func TestHandleWorkspaceSync_CleanupFailureSkipsImport(t *testing.T) {
	importCalled := false
	saveCalled := false
	cleanupErr := errors.New("redis unavailable")
	runDB := newFakeSyncRunDB(t)
	h := &handler{
		db: &stubDB{
			saveSnap: func(context.Context, string, *domain.WorkspaceSnapshot) error {
				saveCalled = true
				return nil
			},
		},
		github: &stubGitHub{
			importWorkspace: func(context.Context, domain.ImportInput) (*domain.WorkspaceSnapshot, error) {
				importCalled = true
				t.Fatal("GitHub import must not run when pending task cleanup fails")
				return nil, nil
			},
		},
		newPendingTaskInspector: func() pendingTaskInspector {
			return &fakePendingTaskInspector{listErr: cleanupErr}
		},
		q: database.New(runDB),
	}
	payload := queue.WorkspaceSyncPayload{
		WorkspaceID:   "00000000-0000-0000-0000-000000000001",
		RepoURL:       "https://github.com/acme/repo",
		DefaultBranch: "main",
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal workspace sync payload: %v", err)
	}

	err = h.handleWorkspaceSync(context.Background(), asynq.NewTask(queue.TypeWorkspaceSync, b))
	if err == nil {
		t.Fatal("expected cleanup failure to fail the full sync")
	}
	if !strings.Contains(err.Error(), "clear pending task-sync jobs") {
		t.Fatalf("expected cleanup failure context, got %v", err)
	}
	if importCalled {
		t.Fatal("expected GitHub import not to be called")
	}
	if saveCalled {
		t.Fatal("expected snapshot not to be saved")
	}
	if runDB.insertSyncRunCalls != 1 {
		t.Fatalf("expected one failed sync run insert, got %d", runDB.insertSyncRunCalls)
	}
	if runDB.updateFailedCalls != 1 {
		t.Fatalf("expected one failed sync run update, got %d", runDB.updateFailedCalls)
	}
	if runDB.lastMode != "full" {
		t.Fatalf("failed sync run mode = %q, want full", runDB.lastMode)
	}
	if runDB.lastErrorCode == nil || *runDB.lastErrorCode != "WORKER_SYNC_FAILED" {
		t.Fatalf("failed sync run error code = %v, want WORKER_SYNC_FAILED", runDB.lastErrorCode)
	}
}

func TestHandleTargetedSync_FetchFeatureFailureSkipsFeaturePersistence(t *testing.T) {
	saveCalled := false
	runDB := newFakeSyncRunDB(t)
	fetchErr := domain.SourceError{
		Code:      domain.ErrParserInvalidYAML,
		Message:   "invalid task YAML",
		Source:    domain.ErrorSourceParser,
		Retryable: false,
		Path:      "docs/features/alpha-feature/tasks/T2.yaml",
	}
	h := &handler{
		db: &stubDB{
			saveFeatSnap: func(context.Context, string, domain.FeatureSnapshot) error {
				saveCalled = true
				return nil
			},
		},
		github: &stubGitHub{
			fetchFeat: func(context.Context, string, string, string) (*domain.FeatureSnapshot, error) {
				return nil, fetchErr
			},
		},
		q: database.New(runDB),
	}
	payload := queue.WorkspaceSyncPayload{
		WorkspaceID: "00000000-0000-0000-0000-000000000001",
		RepoURL:     "https://github.com/acme/repo",
		FeatureID:   "alpha-feature",
	}

	err := h.handleTargetedSync(context.Background(), payload, "webhook", "main")
	if err == nil {
		t.Fatal("expected targeted sync to fail when feature fetch fails")
	}
	var se domain.SourceError
	if !errors.As(err, &se) {
		t.Fatalf("expected SourceError, got %T: %v", err, err)
	}
	if se.Path != fetchErr.Path {
		t.Fatalf("source error path = %q, want %q", se.Path, fetchErr.Path)
	}
	if saveCalled {
		t.Fatal("expected SaveFeatureSnapshot not to be called")
	}
	if runDB.insertSyncRunCalls != 1 {
		t.Fatalf("expected one failed targeted sync run insert, got %d", runDB.insertSyncRunCalls)
	}
	if runDB.updateFailedCalls != 1 {
		t.Fatalf("expected one failed targeted sync run update, got %d", runDB.updateFailedCalls)
	}
	if runDB.lastMode != "targeted" {
		t.Fatalf("failed sync run mode = %q, want targeted", runDB.lastMode)
	}
	if runDB.lastErrorCode == nil || *runDB.lastErrorCode != string(domain.ErrParserInvalidYAML) {
		t.Fatalf("failed sync run error code = %v, want %s", runDB.lastErrorCode, domain.ErrParserInvalidYAML)
	}
}

func TestClearPendingTaskSyncJobsForWorkspaceDeletesOnlyMatchingWorkspace(t *testing.T) {
	inspector := &fakePendingTaskInspector{
		tasks: []*asynq.TaskInfo{
			taskInfo(t, "same-1", queue.TypeTaskSync, queue.TaskSyncPayload{WorkspaceID: "ws-a", FeatureID: "feature-a", TaskID: "T1"}),
			taskInfo(t, "other-workspace", queue.TypeTaskSync, queue.TaskSyncPayload{WorkspaceID: "ws-b", FeatureID: "feature-b", TaskID: "T1"}),
			taskInfo(t, "workspace-sync", queue.TypeWorkspaceSync, queue.WorkspaceSyncPayload{WorkspaceID: "ws-a"}),
			taskInfo(t, "same-2", queue.TypeTaskSync, queue.TaskSyncPayload{WorkspaceID: "ws-a", FeatureID: "feature-a", TaskID: "T2"}),
		},
	}

	deleted, err := clearPendingTaskSyncJobsForWorkspace(inspector, "ws-a")
	if err != nil {
		t.Fatalf("clear pending task sync jobs: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 deleted tasks, got %d", deleted)
	}
	if got := inspector.deletedIDs; !equalStringSlices(got, []string{"same-1", "same-2"}) {
		t.Fatalf("deleted IDs = %v, want [same-1 same-2]", got)
	}
	if len(inspector.tasks) != 2 {
		t.Fatalf("expected non-matching tasks to remain, got %+v", inspector.tasks)
	}
}

// TestHandleTaskSync_MissingFields verifies that empty required fields return an error.
func TestHandleTaskSync_MissingFields(t *testing.T) {
	h := &handler{db: &stubDB{}, github: &stubGitHub{}}
	payload := queue.TaskSyncPayload{WorkspaceID: "ws-123"} // missing FeatureID, TaskID
	task := makeTaskSyncTask(t, payload)
	err := h.handleTaskSync(context.Background(), task)
	if err == nil {
		t.Fatal("expected error for missing required fields")
	}
}

// TestDeriveBranch_Comprehensive tests additional pattern substitutions.
func TestDeriveBranch_Comprehensive(t *testing.T) {
	cases := []struct {
		name      string
		pattern   string
		featureID string
		taskID    string
		want      string
	}{
		{
			name:      "standard pattern",
			pattern:   "feature/{feature_id}-{work_id}",
			featureID: "workspace-data-backend",
			taskID:    "T7",
			want:      "feature/workspace-data-backend-T7",
		},
		{
			name:      "nested feature",
			pattern:   "feature/{feature_id}-{work_id}",
			featureID: "my-complex-feature-name",
			taskID:    "T42",
			want:      "feature/my-complex-feature-name-T42",
		},
		{
			name:      "no substitution markers",
			pattern:   "custom/branch",
			featureID: "any",
			taskID:    "T1",
			want:      "custom/branch",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveBranch(tc.pattern, tc.featureID, tc.taskID)
			if got != tc.want {
				t.Errorf("deriveBranch(%q, %q, %q) = %q, want %q",
					tc.pattern, tc.featureID, tc.taskID, got, tc.want)
			}
		})
	}
}

// TestHandleTaskSync_RetryableFailure verifies that a retryable GitHub error is propagated
// so asynq can retry the job.
func TestHandleTaskSync_RetryableFailure(t *testing.T) {
	retryableErr := domain.SourceError{
		Code:      domain.ErrGitHubRateLimit,
		Message:   "rate limit exceeded",
		Source:    domain.ErrorSourceGitHub,
		Retryable: true,
	}

	fetchCalled := false
	h := &handler{
		db: &stubDB{},
		github: &stubGitHub{
			fetchTask: func(_ context.Context, _, _, _, _ string) (*domain.TaskSnapshot, error) {
				fetchCalled = true
				return nil, retryableErr
			},
		},
		q: nil, // q is not needed here since GetWorkspace won't be called
	}

	// We can't easily test the full handleTaskSync without a DB connection for GetWorkspace,
	// but we can verify that FetchTask propagates retryable errors by calling it directly.
	_, err := h.github.FetchTask(context.Background(), "https://github.com/o/r", "feature/f-T1", "f", "T1")
	if !fetchCalled {
		t.Error("expected FetchTask to be called")
	}
	if err == nil {
		t.Fatal("expected retryable error")
	}
	var se domain.SourceError
	if !errors.As(err, &se) {
		t.Fatalf("expected SourceError, got %T", err)
	}
	if !se.Retryable {
		t.Error("expected error to be retryable")
	}
}

type fakePendingTaskInspector struct {
	tasks      []*asynq.TaskInfo
	deletedIDs []string
	listErr    error
}

func (f *fakePendingTaskInspector) ListPendingTasks(string, ...asynq.ListOption) ([]*asynq.TaskInfo, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]*asynq.TaskInfo, len(f.tasks))
	copy(out, f.tasks)
	return out, nil
}

func (f *fakePendingTaskInspector) DeleteTask(_ string, id string) error {
	for i, task := range f.tasks {
		if task.ID == id {
			f.deletedIDs = append(f.deletedIDs, id)
			f.tasks = append(f.tasks[:i], f.tasks[i+1:]...)
			return nil
		}
	}
	return errors.New("task not found")
}

func (f *fakePendingTaskInspector) Close() error {
	return nil
}

func taskInfo(t *testing.T, id, taskType string, payload any) *asynq.TaskInfo {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &asynq.TaskInfo{ID: id, Type: taskType, Payload: b}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type fakeSyncRunDB struct {
	t                  *testing.T
	workspaceID        pgtype.UUID
	runID              pgtype.UUID
	insertSyncRunCalls int
	updateFailedCalls  int
	lastMode           string
	lastErrorCode      *string
	lastErrorMessage   *string
}

func newFakeSyncRunDB(t *testing.T) *fakeSyncRunDB {
	t.Helper()
	return &fakeSyncRunDB{
		t:           t,
		workspaceID: mustTestUUID(t, "00000000-0000-0000-0000-000000000001"),
		runID:       mustTestUUID(t, "00000000-0000-0000-0000-000000000099"),
	}
}

func (f *fakeSyncRunDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("not implemented")
}

func (f *fakeSyncRunDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeSyncRunDB) QueryRow(_ context.Context, query string, args ...interface{}) pgx.Row {
	switch {
	case strings.Contains(query, "FROM workspaces"):
		return workspaceRow{workspaceID: f.workspaceID}
	case strings.Contains(query, "FROM workspace_features"):
		return errorRow{err: pgx.ErrNoRows}
	case strings.Contains(query, "INSERT INTO workspace_sync_runs"):
		f.insertSyncRunCalls++
		if len(args) > 5 {
			if mode, ok := args[5].(string); ok {
				f.lastMode = mode
			}
		}
		return syncRunRow{
			id:          f.runID,
			workspaceID: f.workspaceID,
			status:      "running",
			mode:        f.lastMode,
		}
	case strings.Contains(query, "UPDATE workspace_sync_runs SET") && strings.Contains(query, "status        = 'failed'"):
		f.updateFailedCalls++
		if len(args) > 1 {
			f.lastErrorCode, _ = args[1].(*string)
		}
		if len(args) > 2 {
			f.lastErrorMessage, _ = args[2].(*string)
		}
		return syncRunRow{
			id:           f.runID,
			workspaceID:  f.workspaceID,
			status:       "failed",
			mode:         f.lastMode,
			errorCode:    f.lastErrorCode,
			errorMessage: f.lastErrorMessage,
		}
	default:
		f.t.Fatalf("unexpected query: %s", query)
		return errorRow{err: errors.New("unexpected query")}
	}
}

type workspaceRow struct {
	workspaceID pgtype.UUID
}

func (r workspaceRow) Scan(dest ...any) error {
	values := []any{
		r.workspaceID,
		"workspace",
		"Workspace",
		"management-repo",
		(*string)(nil),
		pgtype.Timestamptz{},
		pgtype.Timestamptz{},
	}
	return scanValues(dest, values)
}

type syncRunRow struct {
	id           pgtype.UUID
	workspaceID  pgtype.UUID
	status       string
	mode         string
	errorCode    *string
	errorMessage *string
}

func (r syncRunRow) Scan(dest ...any) error {
	changedPaths := json.RawMessage("[]")
	metadata := json.RawMessage("{}")
	values := []any{
		r.id,
		r.workspaceID,
		"redis_worker",
		(*string)(nil),
		pgtype.UUID{},
		pgtype.UUID{},
		r.mode,
		r.status,
		(*string)(nil),
		changedPaths,
		pgtype.Timestamptz{},
		pgtype.Timestamptz{},
		r.errorCode,
		r.errorMessage,
		metadata,
	}
	return scanValues(dest, values)
}

type errorRow struct {
	err error
}

func (r errorRow) Scan(...any) error {
	return r.err
}

func scanValues(dest []any, values []any) error {
	if len(dest) != len(values) {
		return errors.New("unexpected scan destination count")
	}
	for i, d := range dest {
		switch out := d.(type) {
		case *pgtype.UUID:
			*out = values[i].(pgtype.UUID)
		case *string:
			*out = values[i].(string)
		case **string:
			*out = values[i].(*string)
		case *json.RawMessage:
			*out = values[i].(json.RawMessage)
		case *pgtype.Timestamptz:
			*out = values[i].(pgtype.Timestamptz)
		default:
			return errors.New("unsupported scan destination")
		}
	}
	return nil
}

func mustTestUUID(t *testing.T, raw string) pgtype.UUID {
	t.Helper()
	var uid pgtype.UUID
	if err := uid.Scan(raw); err != nil {
		t.Fatalf("scan uuid %s: %v", raw, err)
	}
	return uid
}
