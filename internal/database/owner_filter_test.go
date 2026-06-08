package database

import (
	"strings"
	"testing"
)

// TestDeleteFeatureOwnerFilter verifies that bulk-delete queries for workspace_features
// are scoped to owner IS NULL rows — preventing deletion of go-owned features.
func TestDeleteFeatureOwnerFilter(t *testing.T) {
	const expected = "owner IS NULL OR owner = ''"
	if !strings.Contains(deleteWorkspaceFeaturesNotIn, expected) {
		t.Errorf("deleteWorkspaceFeaturesNotIn must contain %q to protect go-owned rows;\ngot:\n%s",
			expected, deleteWorkspaceFeaturesNotIn)
	}
}

// TestDeleteTaskOwnerFilter verifies that bulk-delete queries for workspace_tasks
// are scoped to owner IS NULL rows — preventing deletion of go-owned tasks.
func TestDeleteTaskOwnerFilter(t *testing.T) {
	const expected = "owner IS NULL OR owner = ''"
	for name, q := range map[string]string{
		"deleteFeatureTasksNotIn": deleteFeatureTasksNotIn,
		"deleteAllFeatureTasks":   deleteAllFeatureTasks,
	} {
		if !strings.Contains(q, expected) {
			t.Errorf("%s must contain %q to protect go-owned rows;\ngot:\n%s",
				name, expected, q)
		}
	}
}

// TestUpsertFeatureOwnerCoalesce verifies that the feature upsert preserves an
// existing non-null owner value (e.g. 'go') using COALESCE.
func TestUpsertFeatureOwnerCoalesce(t *testing.T) {
	const expected = "COALESCE(workspace_features.owner, EXCLUDED.owner)"
	if !strings.Contains(upsertWorkspaceFeature, expected) {
		t.Errorf("upsertWorkspaceFeature must contain %q to preserve go-owned owner;\ngot:\n%s",
			expected, upsertWorkspaceFeature)
	}
}

// TestUpsertTaskOwnerCoalesce verifies that the task upsert preserves an
// existing non-null owner value (e.g. 'go') using COALESCE.
func TestUpsertTaskOwnerCoalesce(t *testing.T) {
	const expected = "COALESCE(workspace_tasks.owner, EXCLUDED.owner)"
	if !strings.Contains(upsertWorkspaceTask, expected) {
		t.Errorf("upsertWorkspaceTask must contain %q to preserve go-owned owner;\ngot:\n%s",
			expected, upsertWorkspaceTask)
	}
}
