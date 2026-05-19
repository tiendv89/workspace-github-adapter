package database

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrationFilesExist verifies that all expected migration files are embedded.
func TestMigrationFilesExist(t *testing.T) {
	expected := []string{
		"00001_workspaces.sql",
		"00002_workspace_repos.sql",
		"00003_workspace_features.sql",
		"00004_workspace_feature_documents.sql",
		"00005_workspace_tasks.sql",
		"00006_workspace_activity_events.sql",
		"00007_workspace_github_sources.sql",
		"00008_workspace_sync_runs.sql",
		"00009_use_uuid_feature_ids_for_tasks_documents_and_activity_events.sql",
		"00010_feature_and_task_names.sql",
		"00011_workspace_sync_runs_uuid_refs.sql",
	}

	for _, name := range expected {
		path := filepath.Join("migrations", name)
		_, err := MigrationFS.Open(path)
		if err != nil {
			t.Errorf("migration file not found: %s", path)
		}
	}
}

// TestMigrationFilesSyntax does a basic sanity check that each SQL file has
// both +goose Up and +goose Down annotations and is non-empty.
func TestMigrationFilesSyntax(t *testing.T) {
	err := fs.WalkDir(MigrationFS, "migrations", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".sql") {
			return err
		}
		data, err := MigrationFS.ReadFile(path)
		if err != nil {
			t.Errorf("cannot read %s: %v", path, err)
			return nil
		}
		content := string(data)
		if len(content) == 0 {
			t.Errorf("%s is empty", path)
		}
		if !strings.Contains(content, "-- +goose Up") {
			t.Errorf("%s missing '-- +goose Up' annotation", path)
		}
		if !strings.Contains(content, "-- +goose Down") {
			t.Errorf("%s missing '-- +goose Down' annotation", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walkdir: %v", err)
	}
}
