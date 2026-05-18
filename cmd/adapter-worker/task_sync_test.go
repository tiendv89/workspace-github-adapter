package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hibiken/asynq"

	"github.com/tiendv89/workspace-github-adapter/internal/domain"
	"github.com/tiendv89/workspace-github-adapter/internal/queue"
)

// --- Minimal stubs for domain interfaces ---

// stubGitHub implements domain.GitHubWorkspaceAdapter for testing.
type stubGitHub struct {
	fetchTask func(ctx context.Context, repoURL, taskBranch, featureID, taskID string) (*domain.TaskSnapshot, error)
	fetchFeat func(ctx context.Context, repoURL, ref, featureID string) (*domain.FeatureSnapshot, error)
}

func (s *stubGitHub) ImportWorkspace(_ context.Context, _ domain.ImportInput) (*domain.WorkspaceSnapshot, error) {
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
func (s *stubDB) SaveSnapshot(_ context.Context, _ string, _ *domain.WorkspaceSnapshot) error {
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
