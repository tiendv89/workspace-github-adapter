package domain_test

import (
	"testing"
	"time"

	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

func TestWorkspaceSummary_SourceState(t *testing.T) {
	syncedAt := time.Now()
	ws := domain.WorkspaceSummary{
		ID:          "ws-1",
		Name:        "My Workspace",
		Slug:        "my-workspace",
		RepoURL:     "https://github.com/acme/mgmt",
		SourceState: domain.FreshState(syncedAt),
	}
	if ws.SourceState.Stale {
		t.Error("expected non-stale workspace summary")
	}
}

func TestFeatureSummary_TaskCounts(t *testing.T) {
	f := domain.FeatureSummary{
		FeatureID: "executor-self-briefing",
		Title:     "Executor Self-Briefing",
		Status:    "in_implementation",
		TaskCounts: domain.TaskCounts{
			Total:      5,
			Done:       2,
			InProgress: 1,
			Blocked:    1,
			Ready:      1,
		},
	}
	if f.TaskCounts.Total != 5 {
		t.Errorf("expected total=5, got %d", f.TaskCounts.Total)
	}
	completed := f.TaskCounts.Done + f.TaskCounts.InProgress + f.TaskCounts.Blocked + f.TaskCounts.Ready + f.TaskCounts.Todo
	if completed != f.TaskCounts.Total {
		t.Errorf("task counts do not sum to total: %+v", f.TaskCounts)
	}
}

func TestTaskSummary_BlockedState(t *testing.T) {
	ts := domain.TaskSummary{
		TaskID:        "T1",
		FeatureID:     "executor-self-briefing",
		Title:         "Go backend foundation",
		Status:        "blocked",
		IsBlocked:     true,
		BlockedReason: "missing dependency",
	}
	if !ts.IsBlocked {
		t.Error("expected IsBlocked=true")
	}
	if ts.BlockedReason == "" {
		t.Error("expected blocked reason to be set")
	}
}

func TestPullRequestRef_Fields(t *testing.T) {
	pr := domain.PullRequestRef{
		Label:  "feat(T1): add DTOs",
		URL:    "https://github.com/acme/repo/pull/1",
		Status: "open",
		Repo:   "workspace-github-adapter",
	}
	if pr.URL == "" {
		t.Error("expected non-empty PR URL")
	}
}

func TestActivityEvent_Fields(t *testing.T) {
	ev := domain.ActivityEvent{
		Action:     "started",
		Scope:      "task",
		Actor:      "agent@example.com",
		OccurredAt: time.Now(),
		Note:       "executor work phase begun.",
		FeatureID:  "executor-self-briefing",
		TaskID:     "T1",
	}
	if ev.Action == "" {
		t.Error("expected non-empty action")
	}
	if ev.OccurredAt.IsZero() {
		t.Error("expected non-zero occurred_at")
	}
}

func TestWorkspaceSnapshot_EmptyFeatures(t *testing.T) {
	snap := domain.WorkspaceSnapshot{
		WorkspaceID: "ws-1",
		Name:        "Test Workspace",
		Slug:        "test-workspace",
		RepoURL:     "https://github.com/acme/mgmt",
		CommitSHA:   "abc123",
		FetchedAt:   time.Now(),
		Features:    []domain.FeatureSnapshot{},
		Repos:       []domain.RepoEntry{},
	}
	if len(snap.Features) != 0 {
		t.Errorf("expected 0 features, got %d", len(snap.Features))
	}
	if snap.CommitSHA == "" {
		t.Error("expected non-empty CommitSHA")
	}
}

func TestWorkspaceSnapshot_FeatureWithTasks(t *testing.T) {
	snap := domain.WorkspaceSnapshot{
		WorkspaceID: "ws-1",
		Features: []domain.FeatureSnapshot{
			{
				FeatureID:    "my-feature",
				Title:        "My Feature",
				Status:       "in_implementation",
				CurrentStage: "in_implementation",
				SourcePath:   "docs/features/my-feature/status.yaml",
				Documents: []domain.DocumentSnapshot{
					{DocumentType: "product_spec", SourcePath: "docs/features/my-feature/product-spec.md"},
					{DocumentType: "technical_design", SourcePath: "docs/features/my-feature/technical-design.md"},
				},
				Tasks: []domain.TaskSnapshot{
					{
						TaskID:    "T1",
						FeatureID: "my-feature",
						Title:     "First task",
						Status:    "done",
						DependsOn: []string{},
					},
				},
			},
		},
	}
	if len(snap.Features[0].Documents) != 2 {
		t.Errorf("expected 2 documents, got %d", len(snap.Features[0].Documents))
	}
	if len(snap.Features[0].Tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(snap.Features[0].Tasks))
	}
}

func TestBlockedContext_Fields(t *testing.T) {
	bc := domain.BlockedContext{
		WIPBranch:  "feature/workspace-data-backend-T1",
		WIPSha:     "deadbeef",
		PushedAt:   "2026-05-15T18:00:00+0000",
		Suggestion: "Check Go version and re-run go build",
	}
	if bc.WIPBranch == "" {
		t.Error("expected WIPBranch to be set")
	}
	if bc.Suggestion == "" {
		t.Error("expected Suggestion to be set")
	}
}
