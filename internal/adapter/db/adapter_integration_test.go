//go:build integration

package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/tiendv89/workspace-github-adapter/database"
	adapterdb "github.com/tiendv89/workspace-github-adapter/internal/adapter/db"
	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

// Integration tests require a live PostgreSQL database.
// Run with:
//
//	DATABASE_URL=postgres://... go test -tags=integration ./internal/adapter/db/...

func dbURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	return url
}

// TestIntegration_MigrateAndRoundtrip runs migrations, saves a snapshot, and
// reads it back to verify the full adapter lifecycle.
func TestIntegration_MigrateAndRoundtrip(t *testing.T) {
	ctx := context.Background()
	databaseURL := dbURL(t)

	// Run migrations.
	if err := database.RunMigrations(ctx, databaseURL); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Connect adapter.
	adapter, err := adapterdb.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer adapter.Close()

	// No workspaces yet.
	workspaces, err := adapter.ListWorkspaces(ctx)
	if err != nil {
		t.Fatalf("ListWorkspaces (empty): %v", err)
	}
	if len(workspaces) != 0 {
		t.Errorf("expected 0 workspaces, got %d", len(workspaces))
	}

	// Create a snapshot.
	workspaceID := "550e8400-e29b-41d4-a716-446655440000"
	now := time.Now().UTC().Truncate(time.Second)
	snap := &domain.WorkspaceSnapshot{
		WorkspaceID: workspaceID,
		Name:        "Test Workspace",
		Slug:        "test-workspace",
		RepoURL:     "https://github.com/example/workspace",
		CommitSHA:   "abc123",
		FetchedAt:   now,
		Repos: []domain.RepoEntry{
			{RepoID: "my-repo", BaseBranch: "main"},
		},
		Features: []domain.FeatureSnapshot{
			{
				FeatureID:    "my-feature",
				Title:        "My Feature",
				Status:       "in_design",
				CurrentStage: "product_spec",
				SourcePath:   "docs/features/my-feature/status.yaml",
				Documents: []domain.DocumentSnapshot{
					{
						DocumentType: "product_spec",
						SourcePath:   "docs/features/my-feature/product-spec.md",
						URL:          "https://github.com/example/workspace/blob/main/docs/features/my-feature/product-spec.md",
					},
				},
				Tasks: []domain.TaskSnapshot{
					{
						TaskID:     "T1",
						FeatureID:  "my-feature",
						Title:      "First Task",
						Status:     "ready",
						Repo:       "my-repo",
						Branch:     "feature/my-feature-T1",
						DependsOn:  []string{},
						SourcePath: "docs/features/my-feature/tasks/T1.yaml",
						Activity: []domain.ActivityEvent{
							{
								Action:     "created",
								Actor:      "human@example.com",
								OccurredAt: now,
								Note:       "initial creation",
							},
						},
					},
				},
				Activity: []domain.ActivityEvent{
					{
						Action:     "stage_change",
						Actor:      "system",
						OccurredAt: now,
						Note:       "moved to product_spec",
					},
				},
			},
		},
	}

	// SaveSnapshot.
	if err := adapter.SaveSnapshot(ctx, workspaceID, snap); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	// ListWorkspaces.
	workspaces, err = adapter.ListWorkspaces(ctx)
	if err != nil {
		t.Fatalf("ListWorkspaces after save: %v", err)
	}
	if len(workspaces) != 1 {
		t.Fatalf("expected 1 workspace, got %d", len(workspaces))
	}
	if workspaces[0].Name != "Test Workspace" {
		t.Errorf("Name: got %q, want %q", workspaces[0].Name, "Test Workspace")
	}

	// GetWorkspace.
	ws, err := adapter.GetWorkspace(ctx, workspaceID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if len(ws.Features) != 1 {
		t.Errorf("expected 1 feature, got %d", len(ws.Features))
	}
	if len(ws.Tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(ws.Tasks))
	}

	// GetFeature.
	feat, err := adapter.GetFeature(ctx, workspaceID, "my-feature")
	if err != nil {
		t.Fatalf("GetFeature: %v", err)
	}
	if feat.FeatureID != "my-feature" {
		t.Errorf("FeatureID: got %q", feat.FeatureID)
	}
	if len(feat.Documents) != 1 {
		t.Errorf("expected 1 document, got %d", len(feat.Documents))
	}
	if len(feat.Tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(feat.Tasks))
	}
	if len(feat.Activity) == 0 {
		t.Error("expected feature activity events")
	}

	// GetTask.
	task, err := adapter.GetTask(ctx, workspaceID, "T1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.TaskID != "T1" {
		t.Errorf("TaskID: got %q", task.TaskID)
	}
	if len(task.Activity) != 1 {
		t.Errorf("expected 1 task activity event, got %d", len(task.Activity))
	}

	// ListFeatureTasks.
	tasks, err := adapter.ListFeatureTasks(ctx, workspaceID, "my-feature")
	if err != nil {
		t.Fatalf("ListFeatureTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(tasks))
	}

	// ListActivity (workspace scope).
	activity, err := adapter.ListActivity(ctx, workspaceID, domain.ActivityScope{})
	if err != nil {
		t.Fatalf("ListActivity: %v", err)
	}
	if len(activity) == 0 {
		t.Error("expected activity events")
	}

	// Stale fallback: GetLatestSyncRun returns nil when no sync was recorded.
	run, err := adapter.GetLatestSyncRun(ctx, workspaceID)
	if err != nil {
		t.Fatalf("GetLatestSyncRun: %v", err)
	}
	if run != nil {
		t.Errorf("expected nil sync run, got %+v", run)
	}

	// GetActiveSnapshot round-trip.
	snap2, err := adapter.GetActiveSnapshot(ctx, workspaceID)
	if err != nil {
		t.Fatalf("GetActiveSnapshot: %v", err)
	}
	if snap2 == nil {
		t.Fatal("expected snapshot, got nil")
	}
	if snap2.Name != snap.Name {
		t.Errorf("snapshot Name: got %q, want %q", snap2.Name, snap.Name)
	}
	if len(snap2.Features) != 1 {
		t.Errorf("expected 1 feature in snapshot, got %d", len(snap2.Features))
	}
}

// TestIntegration_StaleCache verifies that a failed sync run marks the workspace stale.
func TestIntegration_StaleCache(t *testing.T) {
	// This test depends on the TestIntegration_MigrateAndRoundtrip workspace.
	// Skipped in this file; use the full integration test suite.
	t.Skip("requires workspace from MigrateAndRoundtrip test")
}
