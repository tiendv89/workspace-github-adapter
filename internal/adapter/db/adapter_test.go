package db_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tiendv89/workspace-github-adapter/internal/adapter/db"
	"github.com/tiendv89/workspace-github-adapter/internal/database"
	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

// ---------------------------------------------------------------------------
// Unit tests — no real database required.
// All tests in this file exercise helper functions and domain logic only.
// Integration tests that require a live PostgreSQL are in adapter_integration_test.go
// and are gated by the `integration` build tag.
// ---------------------------------------------------------------------------

// TestParseUUID_Valid verifies that a well-formed UUID string is accepted.
func TestParseUUID_Valid(t *testing.T) {
	const raw = "550e8400-e29b-41d4-a716-446655440000"
	uid, err := db.ExportedParseUUID(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := db.ExportedUUIDStr(uid)
	if got != raw {
		t.Errorf("got %q, want %q", got, raw)
	}
}

// TestParseUUID_Invalid verifies that a malformed UUID returns an error.
func TestParseUUID_Invalid(t *testing.T) {
	_, err := db.ExportedParseUUID("not-a-uuid")
	if err == nil {
		t.Fatal("expected error for invalid UUID, got nil")
	}
	var se domain.SourceError
	if !errors.As(err, &se) {
		t.Fatalf("expected domain.SourceError, got %T: %v", err, err)
	}
	if se.Code != domain.ErrValidationMissingInput {
		t.Errorf("expected code %q, got %q", domain.ErrValidationMissingInput, se.Code)
	}
}

// TestPtrStr verifies pointer-or-nil semantics.
func TestPtrStr(t *testing.T) {
	if got := db.ExportedPtrStr(""); got != nil {
		t.Errorf("empty string should return nil, got %v", got)
	}
	s := "hello"
	if got := db.ExportedPtrStr(s); got == nil || *got != s {
		t.Errorf("non-empty string should return pointer to %q", s)
	}
}

// TestUnmarshalStringSlice verifies JSON array decoding.
func TestUnmarshalStringSlice(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"empty", `[]`, nil},
		{"null", `null`, nil},
		{"values", `["T1","T2"]`, []string{"T1", "T2"}},
		{"empty bytes", ``, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := db.ExportedUnmarshalStringSlice(json.RawMessage(tc.raw))
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d] got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestRowToTaskSummary verifies the conversion from a database row to a TaskSummary.
func TestRowToTaskSummary(t *testing.T) {
	status := "in_progress"
	repo := "workspace-github-adapter"
	branch := "feature/test"
	blocked := "waiting for dep"

	row := database.WorkspaceTask{
		TaskName:      "T1",
		FeatureName:   "my-feature",
		Title:         "Test Task",
		Status:        &status,
		Repo:          &repo,
		Branch:        &branch,
		BlockedReason: &blocked,
		DependsOn:     json.RawMessage(`["T0"]`),
	}
	_ = row.ID.Scan("550e8400-e29b-41d4-a716-446655440010")
	_ = row.TaskID.Scan("550e8400-e29b-41d4-a716-446655440011")
	_ = row.FeatureID.Scan("550e8400-e29b-41d4-a716-446655440020")

	got := db.ExportedRowToTaskSummary(row)

	if got.TaskID != "550e8400-e29b-41d4-a716-446655440011" {
		t.Errorf("TaskID: got %q", got.TaskID)
	}
	if got.TaskName != "T1" {
		t.Errorf("TaskName: got %q", got.TaskName)
	}
	if got.Status != "in_progress" {
		t.Errorf("Status: got %q", got.Status)
	}
	if !got.IsBlocked {
		t.Error("IsBlocked should be true when BlockedReason is set")
	}
	if got.BlockedReason != blocked {
		t.Errorf("BlockedReason: got %q", got.BlockedReason)
	}
}

// TestRowToTaskSummary_NoBlocked verifies non-blocked task.
func TestRowToTaskSummary_NoBlocked(t *testing.T) {
	status := "ready"
	row := database.WorkspaceTask{
		TaskName:    "T2",
		FeatureName: "feat",
		Title:       "Ready Task",
		Status:      &status,
		DependsOn:   json.RawMessage(`[]`),
	}
	got := db.ExportedRowToTaskSummary(row)
	if got.IsBlocked {
		t.Error("IsBlocked should be false when BlockedReason is nil")
	}
}

// TestRowToActivityEvent verifies activity event conversion.
func TestRowToActivityEvent(t *testing.T) {
	ts := "2026-05-15T10:00:00Z"
	action := "claimed"
	actor := "norepy@tiendv.dev"
	note := "executor work phase begun"
	fid := "550e8400-e29b-41d4-a716-446655440020"
	tid := "550e8400-e29b-41d4-a716-446655440030"

	row := database.WorkspaceActivityEvent{
		ScopeType:  "task",
		Action:     &action,
		Actor:      &actor,
		OccurredAt: &ts,
		Note:       &note,
		Sequence:   0,
		RawEvent:   json.RawMessage(`{}`),
	}
	_ = row.FeatureID.Scan(fid)
	_ = row.TaskID.Scan(tid)

	got := db.ExportedRowToActivityEvent(row)

	if got.Action != action {
		t.Errorf("Action: got %q, want %q", got.Action, action)
	}
	if got.Actor != actor {
		t.Errorf("Actor: got %q, want %q", got.Actor, actor)
	}
	if got.FeatureID != fid {
		t.Errorf("FeatureID: got %q, want %q", got.FeatureID, fid)
	}
	if got.TaskID != tid {
		t.Errorf("TaskID: got %q, want %q", got.TaskID, tid)
	}
	expectedTime, _ := time.Parse(time.RFC3339, ts)
	if !got.OccurredAt.Equal(expectedTime) {
		t.Errorf("OccurredAt: got %v, want %v", got.OccurredAt, expectedTime)
	}
}

// TestSyncRunToSourceState verifies staleness derivation from sync run rows.
func TestSyncRunToSourceState(t *testing.T) {
	t.Run("nil row returns stale", func(t *testing.T) {
		ss := db.ExportedSyncRunToSourceState(nil, nil)
		if !ss.Stale {
			t.Error("expected stale=true when run is nil")
		}
	})

	t.Run("success run with recent time returns not stale", func(t *testing.T) {
		now := pgtype.Timestamptz{}
		_ = now.Scan(time.Now())
		finished := pgtype.Timestamptz{}
		_ = finished.Scan(time.Now())

		status := "success"
		row := &database.WorkspaceSyncRun{
			Status:     status,
			StartedAt:  now,
			FinishedAt: finished,
		}
		_ = row.ID.Scan("550e8400-e29b-41d4-a716-446655440000")
		_ = row.WorkspaceID.Scan("550e8400-e29b-41d4-a716-446655440001")

		ss := db.ExportedSyncRunToSourceState(row, nil)
		if ss.Stale {
			t.Error("expected stale=false for a recent success run")
		}
	})

	t.Run("failed run returns stale", func(t *testing.T) {
		now := pgtype.Timestamptz{}
		_ = now.Scan(time.Now())
		finished := pgtype.Timestamptz{}
		_ = finished.Scan(time.Now())

		errCode := "GITHUB_RATE_LIMIT"
		row := &database.WorkspaceSyncRun{
			Status:     "failed",
			StartedAt:  now,
			FinishedAt: finished,
			ErrorCode:  &errCode,
		}
		_ = row.ID.Scan("550e8400-e29b-41d4-a716-446655440000")
		_ = row.WorkspaceID.Scan("550e8400-e29b-41d4-a716-446655440001")

		ss := db.ExportedSyncRunToSourceState(row, nil)
		if !ss.Stale {
			t.Error("expected stale=true for a failed run")
		}
		if ss.ErrorCode != errCode {
			t.Errorf("ErrorCode: got %q, want %q", ss.ErrorCode, errCode)
		}
	})
}

// TestDeriveSourceStateThreshold verifies that old successful syncs are marked stale.
func TestDeriveSourceStateThreshold(t *testing.T) {
	now := pgtype.Timestamptz{}
	_ = now.Scan(time.Now())
	finished := pgtype.Timestamptz{}
	_ = finished.Scan(time.Now().Add(-2 * time.Hour)) // 2 hours ago

	row := &database.WorkspaceSyncRun{
		Status:     "success",
		StartedAt:  now,
		FinishedAt: finished,
	}
	_ = row.ID.Scan("550e8400-e29b-41d4-a716-446655440000")
	_ = row.WorkspaceID.Scan("550e8400-e29b-41d4-a716-446655440001")

	threshold := 30 * time.Minute
	ss := db.ExportedSyncRunToSourceState(row, &threshold)
	if !ss.Stale {
		t.Error("expected stale=true for run older than threshold")
	}
}

// TestRowToFeatureSummary verifies feature summary task count aggregation.
func TestRowToFeatureSummary(t *testing.T) {
	feat := database.WorkspaceFeature{
		FeatureName: "feat-1",
		Title:       "Feature One",
		UpdatedAt:   pgtype.Timestamptz{Valid: true, Time: time.Now()},
	}
	_ = feat.FeatureID.Scan("550e8400-e29b-41d4-a716-446655440021")
	inProgress := "in_progress"
	done := "done"
	blocked := "blocked"

	tasks := []database.WorkspaceTask{
		{FeatureName: "feat-1", Status: &inProgress},
		{FeatureName: "feat-1", Status: &done},
		{FeatureName: "feat-1", Status: &blocked},
		{FeatureName: "other-feat", Status: &done}, // should be excluded
	}

	got := db.ExportedRowToFeatureSummary(feat, tasks)

	if got.TaskCounts.Total != 3 {
		t.Errorf("Total: got %d, want 3", got.TaskCounts.Total)
	}
	if got.TaskCounts.InProgress != 1 {
		t.Errorf("InProgress: got %d, want 1", got.TaskCounts.InProgress)
	}
	if got.TaskCounts.Done != 1 {
		t.Errorf("Done: got %d, want 1", got.TaskCounts.Done)
	}
	if got.TaskCounts.Blocked != 1 {
		t.Errorf("Blocked: got %d, want 1", got.TaskCounts.Blocked)
	}
}

func TestUpsertSnapshotPersistsBranchPattern(t *testing.T) {
	workspaceID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440000")
	branchPattern := "workspaces/{feature_id}/tasks/{work_id}"
	fake := &workspaceUpdateDB{t: t, workspaceID: workspaceID}

	err := db.ExportedUpsertSnapshot(context.Background(), database.New(fake), workspaceID, &domain.WorkspaceSnapshot{
		Name:          "Workspace",
		Slug:          "workspace",
		BranchPattern: branchPattern,
	})
	if err != nil {
		t.Fatalf("upsert snapshot: %v", err)
	}
	if fake.updateCalls != 1 {
		t.Fatalf("expected one workspace update call, got %d", fake.updateCalls)
	}
	if fake.branchPattern == nil {
		t.Fatal("expected branch_pattern to be persisted, got nil")
	}
	if *fake.branchPattern != branchPattern {
		t.Fatalf("branch_pattern = %q, want %q", *fake.branchPattern, branchPattern)
	}
}

func TestUpsertSnapshotPersistsSlackChannelID(t *testing.T) {
	workspaceID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440000")
	channelID := "C0123456789"
	fake := &workspaceUpdateDB{t: t, workspaceID: workspaceID}

	err := db.ExportedUpsertSnapshot(context.Background(), database.New(fake), workspaceID, &domain.WorkspaceSnapshot{
		Name:           "Workspace",
		Slug:           "workspace",
		SlackChannelID: channelID,
	})
	if err != nil {
		t.Fatalf("upsert snapshot: %v", err)
	}
	if fake.slackChannelID == nil {
		t.Fatal("expected slack_channel_id to be persisted, got nil")
	}
	if *fake.slackChannelID != channelID {
		t.Fatalf("slack_channel_id = %q, want %q", *fake.slackChannelID, channelID)
	}
}

func TestUpsertSnapshotEmptySlackChannelID(t *testing.T) {
	workspaceID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440000")
	fake := &workspaceUpdateDB{t: t, workspaceID: workspaceID}

	err := db.ExportedUpsertSnapshot(context.Background(), database.New(fake), workspaceID, &domain.WorkspaceSnapshot{
		Name: "Workspace",
		Slug: "workspace",
	})
	if err != nil {
		t.Fatalf("upsert snapshot: %v", err)
	}
	if fake.slackChannelID != nil {
		t.Fatalf("expected slack_channel_id to be nil for empty string, got %q", *fake.slackChannelID)
	}
}

type workspaceUpdateDB struct {
	t              *testing.T
	workspaceID    pgtype.UUID
	updateCalls    int
	branchPattern  *string
	slackChannelID *string
}

func (f *workspaceUpdateDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (f *workspaceUpdateDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("not implemented")
}

func (f *workspaceUpdateDB) QueryRow(_ context.Context, query string, args ...interface{}) pgx.Row {
	if !strings.Contains(query, "UPDATE workspaces") {
		f.t.Fatalf("unexpected query: %s", query)
	}
	f.updateCalls++
	if len(args) > 4 {
		f.branchPattern, _ = args[4].(*string)
	}
	if len(args) > 5 {
		f.slackChannelID, _ = args[5].(*string)
	}
	return workspaceUpdateRow{
		workspaceID:    f.workspaceID,
		branchPattern:  f.branchPattern,
		slackChannelID: f.slackChannelID,
	}
}

type workspaceUpdateRow struct {
	workspaceID    pgtype.UUID
	branchPattern  *string
	slackChannelID *string
}

func (r workspaceUpdateRow) Scan(dest ...any) error {
	if len(dest) != 9 {
		return fmt.Errorf("unexpected workspace scan destination count: got %d, want 9", len(dest))
	}
	d0, ok0 := dest[0].(*pgtype.UUID)
	d1, ok1 := dest[1].(*string)
	d2, ok2 := dest[2].(*string)
	d3, ok3 := dest[3].(*string)
	d4, ok4 := dest[4].(**string)
	d5, ok5 := dest[5].(*pgtype.Timestamptz)
	d6, ok6 := dest[6].(*pgtype.Timestamptz)
	d7, ok7 := dest[7].(**string)
	d8, ok8 := dest[8].(*pgtype.UUID)
	if !ok0 || !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !ok6 || !ok7 || !ok8 {
		return fmt.Errorf("workspaceUpdateRow.Scan: unexpected destination types")
	}
	*d0 = r.workspaceID
	*d1 = "workspace"
	*d2 = "Workspace"
	*d3 = "management-repo"
	*d4 = r.branchPattern
	*d5 = pgtype.Timestamptz{}
	*d6 = pgtype.Timestamptz{}
	*d7 = r.slackChannelID
	*d8 = pgtype.UUID{}
	return nil
}

// ---------------------------------------------------------------------------
// Owner-protection tests — verify that go-owned rows are never deleted or
// overwritten by the YAML→DB sync adapter.
// ---------------------------------------------------------------------------

// sqlCapturingDB records every SQL string passed through Exec and QueryRow.
type sqlCapturingDB struct {
	execQueries     []string
	queryRowQueries []string
	featureID       pgtype.UUID
}

func (c *sqlCapturingDB) Exec(_ context.Context, sql string, _ ...interface{}) (pgconn.CommandTag, error) {
	c.execQueries = append(c.execQueries, sql)
	return pgconn.CommandTag{}, nil
}

func (c *sqlCapturingDB) Query(_ context.Context, _ string, _ ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("not implemented")
}

func (c *sqlCapturingDB) QueryRow(_ context.Context, sql string, _ ...interface{}) pgx.Row {
	c.queryRowQueries = append(c.queryRowQueries, sql)
	return &capturedRow{sql: sql, featureID: c.featureID}
}

// capturedRow fakes RETURNING rows for any QueryRow call.
// It detects the query type by its SQL content and fills dest accordingly.
type capturedRow struct {
	sql       string
	featureID pgtype.UUID
}

func (r *capturedRow) Scan(dest ...any) error {
	switch {
	case strings.Contains(r.sql, "INSERT INTO workspace_features"):
		// UpsertWorkspaceFeature: id, workspace_id, feature_id, feature_name,
		// title, feature_status, current_stage, next_action, stages, source_path,
		// source_hash, created_at, updated_at (13 fields).
		if len(dest) < 3 {
			return fmt.Errorf("capturedRow(feature): expected ≥3 dest, got %d", len(dest))
		}
		if d, ok := dest[0].(*pgtype.UUID); ok {
			*d = r.featureID
		}
		if d, ok := dest[1].(*pgtype.UUID); ok {
			*d = r.featureID
		}
		if d, ok := dest[2].(*pgtype.UUID); ok {
			*d = r.featureID
		}
		if len(dest) > 3 {
			if d, ok := dest[3].(*string); ok {
				*d = "test-feature"
			}
		}
		if len(dest) > 4 {
			if d, ok := dest[4].(*string); ok {
				*d = "Test Feature"
			}
		}
	case strings.Contains(r.sql, "UPDATE workspaces"):
		// UpdateWorkspaceByID: 9 fields.
		if len(dest) != 9 {
			return fmt.Errorf("capturedRow(workspace): expected 9 dest, got %d", len(dest))
		}
		if d, ok := dest[0].(*pgtype.UUID); ok {
			*d = r.featureID // reuse as workspace UUID
		}
		if d, ok := dest[1].(*pgtype.UUID); ok {
			*d = pgtype.UUID{}
		}
		if d, ok := dest[2].(*string); ok {
			*d = "ws"
		}
		if d, ok := dest[3].(*string); ok {
			*d = "ws"
		}
		if d, ok := dest[4].(*string); ok {
			*d = "management-repo"
		}
	case strings.Contains(r.sql, "INSERT INTO workspace_tasks"):
		// UpsertWorkspaceTask: 19 fields.
		if len(dest) < 3 {
			return fmt.Errorf("capturedRow(task): expected ≥3 dest, got %d", len(dest))
		}
		if d, ok := dest[0].(*pgtype.UUID); ok {
			*d = r.featureID
		}
		if d, ok := dest[1].(*pgtype.UUID); ok {
			*d = r.featureID
		}
		if d, ok := dest[2].(*pgtype.UUID); ok {
			*d = r.featureID
		}
		if len(dest) > 3 {
			if d, ok := dest[3].(*string); ok {
				*d = "test-feature"
			}
		}
		if len(dest) > 4 {
			if d, ok := dest[4].(*pgtype.UUID); ok {
				*d = r.featureID
			}
		}
		if len(dest) > 5 {
			if d, ok := dest[5].(*string); ok {
				*d = "T1"
			}
		}
		if len(dest) > 6 {
			if d, ok := dest[6].(*string); ok {
				*d = "Task"
			}
		}
	case strings.Contains(r.sql, "workspace_repos"):
		// UpsertWorkspaceRepo: just needs a UUID scan.
		if len(dest) > 0 {
			if d, ok := dest[0].(*pgtype.UUID); ok {
				*d = r.featureID
			}
		}
	}
	return nil
}

// TestOwnerFilter_DeleteFeaturesSQL verifies the SQL sent to the database for
// DeleteWorkspaceFeaturesNotIn includes the owner IS NULL filter.
func TestOwnerFilter_DeleteFeaturesSQL(t *testing.T) {
	featureID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440020")
	fake := &sqlCapturingDB{featureID: featureID}
	q := database.New(fake)

	err := db.ExportedUpsertSnapshot(context.Background(), q, db.UUIDFromString("550e8400-e29b-41d4-a716-446655440000"), &domain.WorkspaceSnapshot{
		Name: "ws",
		Slug: "ws",
		Features: []domain.FeatureSnapshot{
			{
				FeatureID:  "test-feature",
				Title:      "Test Feature",
				SourcePath: "docs/features/test-feature/status.yaml",
			},
		},
	})
	if err != nil {
		t.Fatalf("upsertSnapshot: %v", err)
	}

	const ownerFilter = "owner IS NULL OR owner = ''"
	var found bool
	for _, sql := range fake.execQueries {
		if strings.Contains(sql, "DELETE FROM workspace_features") {
			if !strings.Contains(sql, ownerFilter) {
				t.Errorf("DeleteWorkspaceFeaturesNotIn SQL missing owner filter %q;\ngot: %s", ownerFilter, sql)
			}
			found = true
		}
	}
	if !found {
		t.Error("no DELETE FROM workspace_features SQL captured — check test setup")
	}
}

// TestOwnerFilter_DeleteTasksSQL verifies the SQL for DeleteFeatureTasksNotIn
// includes the owner IS NULL filter.
func TestOwnerFilter_DeleteTasksSQL(t *testing.T) {
	featureID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440020")
	fake := &sqlCapturingDB{featureID: featureID}
	q := database.New(fake)

	workspaceID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440000")
	err := db.ExportedUpsertFeatureSnapshot(context.Background(), q, workspaceID, domain.FeatureSnapshot{
		FeatureID:  "test-feature",
		Title:      "Test Feature",
		SourcePath: "docs/features/test-feature/status.yaml",
	})
	if err != nil {
		t.Fatalf("upsertFeatureSnapshot: %v", err)
	}

	const ownerFilter = "owner IS NULL OR owner = ''"
	var found bool
	for _, sql := range fake.execQueries {
		if strings.Contains(sql, "DELETE FROM workspace_tasks") {
			if !strings.Contains(sql, ownerFilter) {
				t.Errorf("DeleteFeatureTasksNotIn SQL missing owner filter %q;\ngot: %s", ownerFilter, sql)
			}
			found = true
		}
	}
	if !found {
		t.Error("no DELETE FROM workspace_tasks SQL captured — check test setup")
	}
}

// statefulOwnerDB extends sqlCapturingDB to maintain an in-memory feature-owner
// map. When Exec receives a DELETE FROM workspace_features, it applies the same
// owner IS NULL filter the real database would, letting tests assert that
// go-owned rows survive a sync cycle without a live database.
type statefulOwnerDB struct {
	sqlCapturingDB
	// featureOwners maps feature_name → owner ("" = NULL / legacy, "go" = go-owned).
	featureOwners map[string]string
}

func (s *statefulOwnerDB) Exec(_ context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	s.execQueries = append(s.execQueries, sql)
	if strings.Contains(sql, "DELETE FROM workspace_features") && len(args) >= 2 {
		keepNames, _ := args[1].([]string)
		keepSet := make(map[string]bool, len(keepNames))
		for _, n := range keepNames {
			keepSet[n] = true
		}
		for name, owner := range s.featureOwners {
			if !keepSet[name] && owner == "" {
				delete(s.featureOwners, name)
			}
		}
	}
	return pgconn.CommandTag{}, nil
}

// TestGoOwnedFeatureSurvivesSync seeds a go-owned feature and a stale legacy
// feature in the mock "database", then runs a full sync cycle that omits both.
// Asserts:
//   - go-owned-feature survives — owner='go' is excluded by the AND (owner IS NULL
//     OR owner = ”) filter in DeleteWorkspaceFeaturesNotIn.
//   - stale-legacy is purged — owner IS NULL makes it eligible for deletion.
func TestGoOwnedFeatureSurvivesSync(t *testing.T) {
	featureID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440020")
	mock := &statefulOwnerDB{
		sqlCapturingDB: sqlCapturingDB{featureID: featureID},
		featureOwners: map[string]string{
			"go-owned-feature": "go", // seeded by Go orchestrator — must survive
			"stale-legacy":     "",   // legacy row absent from YAML — must be purged
		},
	}
	q := database.New(mock)
	workspaceID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440000")

	// Sync a workspace with no features — both seeded rows are absent from the
	// YAML snapshot, so the delete runs with an empty keep-list.
	err := db.ExportedUpsertSnapshot(context.Background(), q, workspaceID, &domain.WorkspaceSnapshot{
		Name: "ws",
		Slug: "ws",
	})
	if err != nil {
		t.Fatalf("upsertSnapshot: %v", err)
	}

	if _, exists := mock.featureOwners["go-owned-feature"]; !exists {
		t.Error("go-owned-feature was deleted — owner IS NULL filter did not protect go-owned rows")
	}
	if _, exists := mock.featureOwners["stale-legacy"]; exists {
		t.Error("stale-legacy (NULL owner) should have been purged but was retained")
	}
}

// TestOwnerFilter_UpsertFeatureSQL verifies the upsert SQL uses COALESCE to
// preserve existing non-null owner values (e.g. 'go').
func TestOwnerFilter_UpsertFeatureSQL(t *testing.T) {
	featureID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440020")
	fake := &sqlCapturingDB{featureID: featureID}
	q := database.New(fake)

	workspaceID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440000")
	err := db.ExportedUpsertFeatureSnapshot(context.Background(), q, workspaceID, domain.FeatureSnapshot{
		FeatureID:  "test-feature",
		Title:      "Test Feature",
		SourcePath: "docs/features/test-feature/status.yaml",
	})
	if err != nil {
		t.Fatalf("upsertFeatureSnapshot: %v", err)
	}

	const coalesceExpr = "COALESCE(workspace_features.owner, EXCLUDED.owner)"
	var found bool
	for _, sql := range fake.queryRowQueries {
		if strings.Contains(sql, "INSERT INTO workspace_features") {
			if !strings.Contains(sql, coalesceExpr) {
				t.Errorf("UpsertWorkspaceFeature SQL missing owner COALESCE %q;\ngot: %s", coalesceExpr, sql)
			}
			found = true
		}
	}
	if !found {
		t.Error("no INSERT INTO workspace_features SQL captured — check test setup")
	}
}

// ---------------------------------------------------------------------------
// Owner-scope CASE guard tests — T2 (go-orchestrator-autonomy).
//
// These tests verify that the UpsertWorkspaceFeature SQL contains the CASE
// guard that prevents the adapter from clobbering orchestrator-owned
// feature_status/current_stage/next_action values (in_implementation,
// in_handoff) for owner='go' rows unless the incoming status is
// cancelled or done.
// ---------------------------------------------------------------------------

// TestOwnerScope_UpsertFeatureSQL_HasCaseGuard verifies that the generated SQL
// contains the CASE expression that guards owner='go' protected statuses.
func TestOwnerScope_UpsertFeatureSQL_HasCaseGuard(t *testing.T) {
	featureID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440020")
	fake := &sqlCapturingDB{featureID: featureID}
	q := database.New(fake)

	workspaceID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440000")
	err := db.ExportedUpsertFeatureSnapshot(context.Background(), q, workspaceID, domain.FeatureSnapshot{
		FeatureID:  "test-feature",
		Title:      "Test Feature",
		SourcePath: "docs/features/test-feature/status.yaml",
		Status:     "ready_for_implementation",
		Owner:      "go",
	})
	if err != nil {
		t.Fatalf("upsertFeatureSnapshot: %v", err)
	}

	requiredFragments := []string{
		"workspace_features.owner = 'go'",
		"workspace_features.feature_status IN ('in_implementation', 'in_handoff')",
		"EXCLUDED.feature_status NOT IN ('cancelled', 'done')",
		"THEN workspace_features.feature_status",
		"THEN workspace_features.current_stage",
		"THEN workspace_features.next_action",
	}

	var found bool
	for _, sql := range fake.queryRowQueries {
		if !strings.Contains(sql, "INSERT INTO workspace_features") {
			continue
		}
		found = true
		for _, frag := range requiredFragments {
			if !strings.Contains(sql, frag) {
				t.Errorf("UpsertWorkspaceFeature SQL missing required fragment %q;\ngot SQL:\n%s", frag, sql)
			}
		}
	}
	if !found {
		t.Error("no INSERT INTO workspace_features SQL captured — check test setup")
	}
}

// ---------------------------------------------------------------------------
// caseGuardDB — a mock that simulates the CASE guard behavior of
// UpsertWorkspaceFeature without a live database.
//
// When QueryRow is called for the upsert, it applies the same CASE logic as
// the SQL: if owner='go' AND feature_status ∈ {in_implementation, in_handoff}
// AND incoming NOT IN {cancelled, done}, keep the existing values; else sync.
// ---------------------------------------------------------------------------

// caseGuardFeature holds the simulated DB state for one feature row.
type caseGuardFeature struct {
	owner         string
	featureStatus string
	currentStage  string
	nextAction    string
}

// caseGuardDB intercepts the UpsertWorkspaceFeature QueryRow call, applies the
// CASE guard logic in memory, and records what was "written" to the feature row.
type caseGuardDB struct {
	featureID pgtype.UUID
	// existing is the current DB state (nil = row does not exist yet).
	existing *caseGuardFeature
	// written holds the result of the last upsert after the CASE guard ran.
	written *caseGuardFeature
}

func (c *caseGuardDB) Exec(_ context.Context, _ string, _ ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (c *caseGuardDB) Query(_ context.Context, _ string, _ ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("not implemented")
}

func (c *caseGuardDB) QueryRow(_ context.Context, sql string, args ...interface{}) pgx.Row {
	if !strings.Contains(sql, "INSERT INTO workspace_features") {
		return &caseGuardNilRow{}
	}

	// Parameter positions per the query:
	//   $1 workspace_id, $2 feature_name, $3 title,
	//   $4 feature_status, $5 current_stage, $6 next_action,
	//   $7 stages, $8 source_path, $9 source_hash, $10 owner
	incomingStatus := derefArgStr(args, 3)
	incomingStage := derefArgStr(args, 4)
	incomingAction := derefArgStr(args, 5)
	incomingOwner := derefArgStr(args, 9)

	resultStatus := incomingStatus
	resultStage := incomingStage
	resultAction := incomingAction
	resultOwner := incomingOwner

	if c.existing != nil {
		// Simulate COALESCE(workspace_features.owner, EXCLUDED.owner).
		if c.existing.owner != "" {
			resultOwner = c.existing.owner
		}

		// Apply the CASE guard.
		protected := c.existing.owner == "go" &&
			(c.existing.featureStatus == "in_implementation" || c.existing.featureStatus == "in_handoff") &&
			(incomingStatus != "cancelled" && incomingStatus != "done")
		if protected {
			resultStatus = c.existing.featureStatus
			resultStage = c.existing.currentStage
			resultAction = c.existing.nextAction
		}
	}

	result := &caseGuardFeature{
		owner:         resultOwner,
		featureStatus: resultStatus,
		currentStage:  resultStage,
		nextAction:    resultAction,
	}
	c.written = result

	return &caseGuardRow{featureID: c.featureID, feature: result}
}

// derefArgStr extracts args[i] as string (handles *string and string).
func derefArgStr(args []interface{}, i int) string {
	if i >= len(args) {
		return ""
	}
	switch v := args[i].(type) {
	case *string:
		if v == nil {
			return ""
		}
		return *v
	case string:
		return v
	}
	return ""
}

type caseGuardRow struct {
	featureID pgtype.UUID
	feature   *caseGuardFeature
}

func (r *caseGuardRow) Scan(dest ...any) error {
	// UpsertWorkspaceFeature RETURNING: id, workspace_id, title, feature_status,
	// current_stage, next_action, stages, source_path, source_hash, created_at,
	// updated_at, feature_name, feature_id, owner, init_pr_url, init_pr_merged
	// Fill only what tests assert on; leave rest as zero values.
	if len(dest) < 14 {
		return fmt.Errorf("caseGuardRow.Scan: need ≥14 dest, got %d", len(dest))
	}
	if d, ok := dest[0].(*pgtype.UUID); ok {
		*d = r.featureID
	}
	if d, ok := dest[1].(*pgtype.UUID); ok {
		*d = r.featureID
	}
	if d, ok := dest[3].(**string); ok && r.feature != nil {
		s := r.feature.featureStatus
		*d = &s
	}
	if d, ok := dest[4].(**string); ok && r.feature != nil {
		s := r.feature.currentStage
		*d = &s
	}
	if d, ok := dest[5].(**string); ok && r.feature != nil {
		s := r.feature.nextAction
		*d = &s
	}
	if d, ok := dest[12].(*pgtype.UUID); ok {
		*d = r.featureID
	}
	if d, ok := dest[13].(**string); ok && r.feature != nil {
		s := r.feature.owner
		*d = &s
	}
	return nil
}

type caseGuardNilRow struct{}

func (r *caseGuardNilRow) Scan(_ ...any) error { return pgx.ErrNoRows }

// TestOwnerScope_GoFeature_ProtectsInImplementation verifies that syncing a
// go-owned feature that is in_implementation does NOT overwrite feature_status,
// current_stage, or next_action when the incoming value is a design-phase status.
func TestOwnerScope_GoFeature_ProtectsInImplementation(t *testing.T) {
	featureID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440020")
	mock := &caseGuardDB{
		featureID: featureID,
		existing: &caseGuardFeature{
			owner:         "go",
			featureStatus: "in_implementation",
			currentStage:  "handoff",
			nextAction:    "waiting for orchestrator",
		},
	}
	q := database.New(mock)
	workspaceID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440000")

	// Adapter syncs status.yaml which reports ready_for_implementation —
	// orchestrator has already advanced to in_implementation; guard must hold.
	err := db.ExportedUpsertFeatureSnapshot(context.Background(), q, workspaceID, domain.FeatureSnapshot{
		FeatureID:    "go-feature",
		Title:        "Go Feature",
		Status:       "ready_for_implementation",
		CurrentStage: "tasks",
		NextAction:   "run tasks",
		Owner:        "go",
	})
	if err != nil {
		t.Fatalf("upsertFeatureSnapshot: %v", err)
	}

	if mock.written == nil {
		t.Fatal("no upsert was executed")
	}
	if mock.written.featureStatus != "in_implementation" {
		t.Errorf("feature_status: got %q, want %q (guard must hold)", mock.written.featureStatus, "in_implementation")
	}
	if mock.written.currentStage != "handoff" {
		t.Errorf("current_stage: got %q, want %q (guard must hold)", mock.written.currentStage, "handoff")
	}
	if mock.written.nextAction != "waiting for orchestrator" {
		t.Errorf("next_action: got %q, want %q (guard must hold)", mock.written.nextAction, "waiting for orchestrator")
	}
}

// TestOwnerScope_GoFeature_ProtectsInHandoff verifies that syncing a go-owned
// feature that is in_handoff does NOT overwrite the orchestrator-owned fields.
func TestOwnerScope_GoFeature_ProtectsInHandoff(t *testing.T) {
	featureID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440020")
	mock := &caseGuardDB{
		featureID: featureID,
		existing: &caseGuardFeature{
			owner:         "go",
			featureStatus: "in_handoff",
			currentStage:  "handoff",
			nextAction:    "merging handoff PRs",
		},
	}
	q := database.New(mock)
	workspaceID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440000")

	err := db.ExportedUpsertFeatureSnapshot(context.Background(), q, workspaceID, domain.FeatureSnapshot{
		FeatureID:    "go-feature",
		Title:        "Go Feature",
		Status:       "in_implementation",
		CurrentStage: "implementation",
		NextAction:   "run tasks",
		Owner:        "go",
	})
	if err != nil {
		t.Fatalf("upsertFeatureSnapshot: %v", err)
	}

	if mock.written == nil {
		t.Fatal("no upsert was executed")
	}
	if mock.written.featureStatus != "in_handoff" {
		t.Errorf("feature_status: got %q, want %q (guard must hold)", mock.written.featureStatus, "in_handoff")
	}
}

// TestOwnerScope_GoFeature_AllowsCancelled verifies that cancelled overrides
// the guard even when the current status is in_implementation.
func TestOwnerScope_GoFeature_AllowsCancelled(t *testing.T) {
	featureID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440020")
	mock := &caseGuardDB{
		featureID: featureID,
		existing: &caseGuardFeature{
			owner:         "go",
			featureStatus: "in_implementation",
			currentStage:  "handoff",
			nextAction:    "waiting",
		},
	}
	q := database.New(mock)
	workspaceID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440000")

	err := db.ExportedUpsertFeatureSnapshot(context.Background(), q, workspaceID, domain.FeatureSnapshot{
		FeatureID: "go-feature",
		Title:     "Go Feature",
		Status:    "cancelled",
		Owner:     "go",
	})
	if err != nil {
		t.Fatalf("upsertFeatureSnapshot: %v", err)
	}

	if mock.written == nil {
		t.Fatal("no upsert was executed")
	}
	if mock.written.featureStatus != "cancelled" {
		t.Errorf("feature_status: got %q, want %q (cancelled must bypass guard)", mock.written.featureStatus, "cancelled")
	}
}

// TestOwnerScope_GoFeature_AllowsDone verifies that done overrides the guard
// even when the current status is in_handoff.
func TestOwnerScope_GoFeature_AllowsDone(t *testing.T) {
	featureID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440020")
	mock := &caseGuardDB{
		featureID: featureID,
		existing: &caseGuardFeature{
			owner:         "go",
			featureStatus: "in_handoff",
			currentStage:  "handoff",
			nextAction:    "merging",
		},
	}
	q := database.New(mock)
	workspaceID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440000")

	err := db.ExportedUpsertFeatureSnapshot(context.Background(), q, workspaceID, domain.FeatureSnapshot{
		FeatureID: "go-feature",
		Title:     "Go Feature",
		Status:    "done",
		Owner:     "go",
	})
	if err != nil {
		t.Fatalf("upsertFeatureSnapshot: %v", err)
	}

	if mock.written == nil {
		t.Fatal("no upsert was executed")
	}
	if mock.written.featureStatus != "done" {
		t.Errorf("feature_status: got %q, want %q (done must bypass guard)", mock.written.featureStatus, "done")
	}
}

// TestOwnerScope_GoFeature_SyncsDesignPhaseStatuses verifies that design-phase
// statuses (in_design, in_tdd, ready_for_implementation) are always synced when
// the current DB value is also a design-phase status (guard condition not met).
func TestOwnerScope_GoFeature_SyncsDesignPhaseStatuses(t *testing.T) {
	cases := []struct {
		existing string
		incoming string
	}{
		{"in_design", "in_tdd"},
		{"in_tdd", "ready_for_implementation"},
		{"ready_for_implementation", "in_design"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.existing+"->"+tc.incoming, func(t *testing.T) {
			featureID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440020")
			mock := &caseGuardDB{
				featureID: featureID,
				existing: &caseGuardFeature{
					owner:         "go",
					featureStatus: tc.existing,
					currentStage:  "product_spec",
					nextAction:    "approve",
				},
			}
			q := database.New(mock)
			workspaceID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440000")

			err := db.ExportedUpsertFeatureSnapshot(context.Background(), q, workspaceID, domain.FeatureSnapshot{
				FeatureID:    "go-feature",
				Title:        "Go Feature",
				Status:       tc.incoming,
				CurrentStage: "technical_design",
				NextAction:   "run tech-lead",
				Owner:        "go",
			})
			if err != nil {
				t.Fatalf("upsertFeatureSnapshot: %v", err)
			}

			if mock.written == nil {
				t.Fatal("no upsert was executed")
			}
			if mock.written.featureStatus != tc.incoming {
				t.Errorf("feature_status: got %q, want %q (design-phase must sync)", mock.written.featureStatus, tc.incoming)
			}
		})
	}
}

// TestOwnerScope_NonGoFeature_AlwaysSyncs verifies that a non-go-owned feature
// (owner='' or absent) always has its status overwritten regardless of value.
func TestOwnerScope_NonGoFeature_AlwaysSyncs(t *testing.T) {
	cases := []struct {
		owner    string
		existing string
		incoming string
	}{
		{"", "in_implementation", "ready_for_implementation"},
		{"ts", "in_handoff", "in_design"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run("owner="+tc.owner+"_"+tc.existing+"->"+tc.incoming, func(t *testing.T) {
			featureID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440020")
			mock := &caseGuardDB{
				featureID: featureID,
				existing: &caseGuardFeature{
					owner:         tc.owner,
					featureStatus: tc.existing,
					currentStage:  "handoff",
					nextAction:    "merging",
				},
			}
			q := database.New(mock)
			workspaceID := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440000")

			err := db.ExportedUpsertFeatureSnapshot(context.Background(), q, workspaceID, domain.FeatureSnapshot{
				FeatureID: "ts-feature",
				Title:     "TS Feature",
				Status:    tc.incoming,
				Owner:     tc.owner,
			})
			if err != nil {
				t.Fatalf("upsertFeatureSnapshot: %v", err)
			}

			if mock.written == nil {
				t.Fatal("no upsert was executed")
			}
			if mock.written.featureStatus != tc.incoming {
				t.Errorf("feature_status: got %q, want %q (non-go must always sync)", mock.written.featureStatus, tc.incoming)
			}
		})
	}
}
