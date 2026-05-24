package db_test

import (
	"context"
	"encoding/json"
	"errors"
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
	fake := &branchPatternDB{t: t, workspaceID: workspaceID}

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

type branchPatternDB struct {
	t             *testing.T
	workspaceID   pgtype.UUID
	updateCalls   int
	branchPattern *string
}

func (f *branchPatternDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (f *branchPatternDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("not implemented")
}

func (f *branchPatternDB) QueryRow(_ context.Context, query string, args ...interface{}) pgx.Row {
	if !strings.Contains(query, "UPDATE workspaces") {
		f.t.Fatalf("unexpected query: %s", query)
	}
	f.updateCalls++
	f.branchPattern, _ = args[4].(*string)
	return workspaceUpdateRow{
		workspaceID:   f.workspaceID,
		branchPattern: f.branchPattern,
	}
}

type workspaceUpdateRow struct {
	workspaceID   pgtype.UUID
	branchPattern *string
}

func (r workspaceUpdateRow) Scan(dest ...any) error {
	if len(dest) != 7 {
		return errors.New("unexpected workspace scan destination count")
	}
	*(dest[0].(*pgtype.UUID)) = r.workspaceID               //nolint:errcheck
	*(dest[1].(*string)) = "workspace"                      //nolint:errcheck
	*(dest[2].(*string)) = "Workspace"                      //nolint:errcheck
	*(dest[3].(*string)) = "management-repo"                //nolint:errcheck
	*(dest[4].(**string)) = r.branchPattern                 //nolint:errcheck
	*(dest[5].(*pgtype.Timestamptz)) = pgtype.Timestamptz{} //nolint:errcheck
	*(dest[6].(*pgtype.Timestamptz)) = pgtype.Timestamptz{} //nolint:errcheck
	return nil
}
