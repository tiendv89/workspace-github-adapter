package db_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tiendv89/workspace-github-adapter/internal/adapter/db"
	"github.com/tiendv89/workspace-github-adapter/internal/database"
	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

// modelPolicyDB is a mock DBTX for syncModelPolicy tests.
// It records exec calls and answers GetModelByModelID queries via modelRows.
type modelPolicyDB struct {
	execQueries []string
	// modelRows maps model_id string → (model UUID, active flag).
	// If the key is absent the query returns pgx.ErrNoRows.
	modelRows map[string]modelPolicyRow
}

type modelPolicyRow struct {
	id     pgtype.UUID
	active bool
}

func (m *modelPolicyDB) Exec(_ context.Context, sql string, _ ...interface{}) (pgconn.CommandTag, error) {
	m.execQueries = append(m.execQueries, sql)
	return pgconn.CommandTag{}, nil
}

func (m *modelPolicyDB) Query(_ context.Context, _ string, _ ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("not implemented")
}

func (m *modelPolicyDB) QueryRow(_ context.Context, sql string, args ...interface{}) pgx.Row {
	if !strings.Contains(sql, "FROM models") {
		// Not a model lookup — return a no-op row.
		return &modelPolicyQueryRow{err: pgx.ErrNoRows}
	}
	slug, _ := args[0].(string)
	row, ok := m.modelRows[slug]
	if !ok {
		return &modelPolicyQueryRow{err: pgx.ErrNoRows}
	}
	return &modelPolicyQueryRow{id: row.id, modelID: slug, active: row.active}
}

type modelPolicyQueryRow struct {
	err     error
	id      pgtype.UUID
	modelID string
	active  bool
}

func (r *modelPolicyQueryRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	// Model columns: id, model_id, display_name, active, created_at, updated_at
	if len(dest) < 4 {
		return errors.New("modelPolicyQueryRow: too few scan destinations")
	}
	if d, ok := dest[0].(*pgtype.UUID); ok {
		*d = r.id
	}
	if d, ok := dest[1].(*string); ok {
		*d = r.modelID
	}
	// display_name is *string — leave nil
	if d, ok := dest[3].(*bool); ok {
		*d = r.active
	}
	return nil
}

// newModelPolicyDB creates a mock with three seed Anthropic models all active.
func newModelPolicyDB() *modelPolicyDB {
	haiku := db.UUIDFromString("aaaaaaaa-0000-0000-0000-000000000001")
	sonnet := db.UUIDFromString("aaaaaaaa-0000-0000-0000-000000000002")
	opus := db.UUIDFromString("aaaaaaaa-0000-0000-0000-000000000003")
	return &modelPolicyDB{
		modelRows: map[string]modelPolicyRow{
			"claude-haiku-4-5-20251001": {id: haiku, active: true},
			"claude-sonnet-4-6":         {id: sonnet, active: true},
			"claude-opus-4-8":           {id: opus, active: true},
		},
	}
}

func allPhasesPolicy(slug string) *domain.ModelPolicySnapshot {
	pp := domain.PhasePolicySnapshot{Allowed: []string{slug}, Default: slug}
	return &domain.ModelPolicySnapshot{
		Phases: map[string]domain.PhasePolicySnapshot{
			"implementation":      pp,
			"self_review":         pp,
			"pr_description":      pp,
			"suggested_next_step": pp,
			"conflict_resolution": pp,
		},
	}
}

func TestSyncModelPolicy_NilPolicyDeletesRows(t *testing.T) {
	mock := newModelPolicyDB()
	q := database.New(mock)
	uid := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440000")

	if err := db.ExportedSyncModelPolicy(context.Background(), q, uid, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var hasDelete bool
	for _, sql := range mock.execQueries {
		if strings.Contains(sql, "DELETE FROM workspace_model_policies") {
			hasDelete = true
		}
		if strings.Contains(sql, "INSERT INTO workspace_model_policies") {
			t.Error("INSERT must not run when policy is nil")
		}
	}
	if !hasDelete {
		t.Error("expected DELETE SQL for nil policy")
	}
}

func TestSyncModelPolicy_ValidPolicyInsertsRows(t *testing.T) {
	mock := newModelPolicyDB()
	q := database.New(mock)
	uid := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440000")

	policy := allPhasesPolicy("claude-sonnet-4-6")
	if err := db.ExportedSyncModelPolicy(context.Background(), q, uid, policy); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var inserts int
	for _, sql := range mock.execQueries {
		if strings.Contains(sql, "INSERT INTO workspace_model_policies") {
			inserts++
		}
	}
	if inserts != 5 {
		t.Errorf("expected 5 INSERT calls (one per phase), got %d", inserts)
	}
}

func TestSyncModelPolicy_MultipleAllowedInsertAll(t *testing.T) {
	mock := newModelPolicyDB()
	q := database.New(mock)
	uid := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440000")

	base := allPhasesPolicy("claude-haiku-4-5-20251001")
	base.Phases["implementation"] = domain.PhasePolicySnapshot{
		Allowed: []string{"claude-haiku-4-5-20251001", "claude-sonnet-4-6", "claude-opus-4-8"},
		Default: "claude-sonnet-4-6",
	}

	if err := db.ExportedSyncModelPolicy(context.Background(), q, uid, base); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var inserts int
	for _, sql := range mock.execQueries {
		if strings.Contains(sql, "INSERT INTO workspace_model_policies") {
			inserts++
		}
	}
	// 4 phases × 1 model + 1 phase × 3 models = 7
	if inserts != 7 {
		t.Errorf("expected 7 INSERT calls, got %d", inserts)
	}
}

func TestSyncModelPolicy_UnknownSlugFails(t *testing.T) {
	mock := newModelPolicyDB()
	q := database.New(mock)
	uid := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440000")

	policy := allPhasesPolicy("claude-unknown-model")
	err := db.ExportedSyncModelPolicy(context.Background(), q, uid, policy)
	if err == nil {
		t.Fatal("expected error for unknown model slug")
	}
	var se domain.SourceError
	if !errors.As(err, &se) {
		t.Fatalf("expected domain.SourceError, got %T: %v", err, err)
	}
	if se.Code != domain.ErrModelUnknown {
		t.Errorf("unexpected code: %s", se.Code)
	}
}

func TestSyncModelPolicy_InactiveModelFails(t *testing.T) {
	mock := newModelPolicyDB()
	mock.modelRows["claude-inactive"] = modelPolicyRow{
		id:     db.UUIDFromString("aaaaaaaa-0000-0000-0000-000000000099"),
		active: false,
	}
	q := database.New(mock)
	uid := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440000")

	policy := allPhasesPolicy("claude-inactive")
	err := db.ExportedSyncModelPolicy(context.Background(), q, uid, policy)
	if err == nil {
		t.Fatal("expected error for inactive model")
	}
	var se domain.SourceError
	if !errors.As(err, &se) {
		t.Fatalf("expected domain.SourceError, got %T: %v", err, err)
	}
	if se.Code != domain.ErrModelInactive {
		t.Errorf("unexpected code: %s", se.Code)
	}
}

func TestSyncModelPolicy_Idempotent(t *testing.T) {
	mock := newModelPolicyDB()
	q := database.New(mock)
	uid := db.UUIDFromString("550e8400-e29b-41d4-a716-446655440000")

	policy := allPhasesPolicy("claude-sonnet-4-6")

	for i := range 2 {
		if err := db.ExportedSyncModelPolicy(context.Background(), q, uid, policy); err != nil {
			t.Fatalf("run %d: unexpected error: %v", i+1, err)
		}
	}

	var deletes, inserts int
	for _, sql := range mock.execQueries {
		if strings.Contains(sql, "DELETE FROM workspace_model_policies") {
			deletes++
		}
		if strings.Contains(sql, "INSERT INTO workspace_model_policies") {
			inserts++
		}
	}
	// Two runs: 2 DELETEs, 10 INSERTs (5 per run)
	if deletes != 2 {
		t.Errorf("expected 2 DELETE calls (one per run), got %d", deletes)
	}
	if inserts != 10 {
		t.Errorf("expected 10 INSERT calls (5 per run × 2), got %d", inserts)
	}
}
