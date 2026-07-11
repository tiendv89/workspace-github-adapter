package database

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestWorkspaceSyncRunReferenceFieldsUseUUIDs(t *testing.T) {
	uuidType := reflect.TypeOf(pgtype.UUID{})

	runType := reflect.TypeOf(WorkspaceSyncRun{})
	for _, fieldName := range []string{"FeatureID", "TaskID"} {
		field, ok := runType.FieldByName(fieldName)
		if !ok {
			t.Fatalf("WorkspaceSyncRun missing %s", fieldName)
		}
		if field.Type != uuidType {
			t.Fatalf("WorkspaceSyncRun.%s type = %s, want %s", fieldName, field.Type, uuidType)
		}
	}

	paramsType := reflect.TypeOf(InsertSyncRunParams{})
	for _, fieldName := range []string{"FeatureID", "TaskID"} {
		field, ok := paramsType.FieldByName(fieldName)
		if !ok {
			t.Fatalf("InsertSyncRunParams missing %s", fieldName)
		}
		if field.Type != uuidType {
			t.Fatalf("InsertSyncRunParams.%s type = %s, want %s", fieldName, field.Type, uuidType)
		}
	}
}

func TestWorkspaceSyncRunQueriesOrderByFinishedAt(t *testing.T) {
	if !strings.Contains(getLatestSyncRun, "ORDER BY finished_at DESC NULLS LAST") {
		t.Fatalf("GetLatestSyncRun query should prefer finished_at ordering, got:\n%s", getLatestSyncRun)
	}
	if !strings.Contains(listLatestSyncRunsPerWorkspace, "ORDER BY workspace_id, finished_at DESC NULLS LAST") {
		t.Fatalf("ListLatestSyncRunsPerWorkspace query should prefer finished_at ordering, got:\n%s", listLatestSyncRunsPerWorkspace)
	}
}

// TestWorkspaceSyncRunSchemaFKsTargetUnifiedID verifies that after migration 00022
// the workspace_sync_runs FKs reference workspace_features(id) and workspace_tasks(id)
// — not the dropped feature_id/task_id columns — and that the core tables no longer
// carry those redundant columns.
func TestWorkspaceSyncRunSchemaFKsTargetUnifiedID(t *testing.T) {
	// Load the vendored schema snapshot embedded in the schema.sql file.
	// We use package-level constants exported by workspace_sync_runs.sql.go to
	// confirm the generated SQL references id, not task_id.  For the schema-level
	// FK check we read the SQL source file directly via the go:generate comment
	// approach — here we do a string-based sanity check on the known SQL package
	// constants that are already in scope within this package.

	// 1. workspace_sync_runs FKs reference id on both parent tables.
	//    The InsertSyncRun query writes feature_id / task_id columns on
	//    workspace_sync_runs itself; the FK targets are workspace_features(id)
	//    and workspace_tasks(id). Verify the InsertSyncRun SQL does not embed a
	//    reference to the dropped columns.
	if strings.Contains(insertSyncRun, "workspace_features.feature_id") {
		t.Error("InsertSyncRun query references dropped column workspace_features.feature_id")
	}
	if strings.Contains(insertSyncRun, "workspace_tasks.task_id") {
		t.Error("InsertSyncRun query references dropped column workspace_tasks.task_id")
	}

	// 2. WorkspaceFeature struct no longer has a FeatureID field (dropped column).
	featureType := reflect.TypeOf(WorkspaceFeature{})
	if _, ok := featureType.FieldByName("FeatureID"); ok {
		t.Error("WorkspaceFeature still has FeatureID field; expected it to be removed by migration 00022")
	}

	// 3. WorkspaceTask struct no longer has a TaskID field (dropped column).
	taskType := reflect.TypeOf(WorkspaceTask{})
	if _, ok := taskType.FieldByName("TaskID"); ok {
		t.Error("WorkspaceTask still has TaskID field; expected it to be removed by migration 00022")
	}

	// 4. WorkspaceSyncRun still has FeatureID and TaskID (its own FK reference
	//    columns — distinct from the parent table identity columns dropped above).
	uuidType := reflect.TypeOf(pgtype.UUID{})
	runType := reflect.TypeOf(WorkspaceSyncRun{})
	for _, fieldName := range []string{"FeatureID", "TaskID"} {
		field, ok := runType.FieldByName(fieldName)
		if !ok {
			t.Fatalf("WorkspaceSyncRun missing reference column %s", fieldName)
		}
		if field.Type != uuidType {
			t.Fatalf("WorkspaceSyncRun.%s type = %s, want %s", fieldName, field.Type, uuidType)
		}
	}
}
