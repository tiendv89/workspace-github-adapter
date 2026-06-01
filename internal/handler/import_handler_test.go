package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	pgutil2 "github.com/tiendv89/workspace-github-adapter/pkg/pgutil"

	"github.com/tiendv89/workspace-github-adapter/internal/database"
	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

const (
	validOrgID  = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	validOrgID2 = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	zeroUUID    = "00000000-0000-0000-0000-000000000000"
	validWsID   = "cccccccc-cccc-cccc-cccc-cccccccccccc"
)

// importSuccessDB records writes and succeeds without a real database.
type importSuccessDB struct {
	writeQueries      int
	upsertOrgID       pgtype.UUID
	upsertWorkspaceID pgtype.UUID
}

func (db *importSuccessDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (db *importSuccessDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, nil
}

func (db *importSuccessDB) QueryRow(_ context.Context, query string, args ...interface{}) pgx.Row {
	switch {
	case strings.Contains(query, "FROM workspace_github_sources"):
		// No existing import.
		return errRow{err: pgx.ErrNoRows}
	case strings.Contains(query, "INSERT INTO workspaces"):
		db.writeQueries++
		// Capture organisation_id (arg[1]) and workspace id (arg[0]).
		if len(args) > 0 {
			if uid, ok := args[0].(pgtype.UUID); ok {
				db.upsertWorkspaceID = uid
			}
		}
		if len(args) > 1 {
			if uid, ok := args[1].(pgtype.UUID); ok {
				db.upsertOrgID = uid
			}
		}
		return &importSuccessRow{workspaceID: db.upsertWorkspaceID, orgID: db.upsertOrgID}
	case strings.Contains(query, "INSERT INTO workspace_github_sources"):
		db.writeQueries++
		return &importSourceRow{}
	case strings.Contains(query, "INSERT INTO workspace_sync_runs"):
		db.writeQueries++
		return &importSyncRunRow{}
	case strings.Contains(query, "UPDATE workspace_sync_runs"):
		return &importSyncRunRow{}
	default:
		return errRow{err: pgx.ErrNoRows}
	}
}

type importSuccessRow struct {
	workspaceID pgtype.UUID
	orgID       pgtype.UUID
}

func (r *importSuccessRow) Scan(dest ...any) error {
	if len(dest) != 9 {
		return nil
	}
	if d, ok := dest[0].(*pgtype.UUID); ok {
		*d = r.workspaceID
	}
	if d, ok := dest[1].(*pgtype.UUID); ok {
		*d = r.orgID
	}
	if d, ok := dest[2].(*string); ok {
		*d = "test-workspace"
	}
	if d, ok := dest[3].(*string); ok {
		*d = "Test Workspace"
	}
	if d, ok := dest[4].(*string); ok {
		*d = "management-repo"
	}
	return nil
}

type importSourceRow struct{}

func (r *importSourceRow) Scan(dest ...any) error { return nil }

type importSyncRunRow struct{}

func (r *importSyncRunRow) Scan(dest ...any) error {
	syncRunUUID := pgtype.UUID{}
	if len(dest) > 0 {
		if d, ok := dest[0].(*pgtype.UUID); ok {
			*d = syncRunUUID
		}
	}
	wsUUID := pgtype.UUID{}
	if len(dest) > 1 {
		if d, ok := dest[1].(*pgtype.UUID); ok {
			*d = wsUUID
		}
	}
	for i := 2; i < len(dest); i++ {
		switch d := dest[i].(type) {
		case *string:
			*d = ""
		case **string:
			// leave nil
		case *pgtype.Timestamptz:
			*d = pgtype.Timestamptz{}
		case *pgtype.UUID:
			*d = pgtype.UUID{}
		case *[]byte:
			*d = []byte("[]")
		}
	}
	return nil
}

// conflictPreserveOrgDB tests ON CONFLICT: first insert succeeds with validOrgID,
// second insert returns the existing row unchanged (still validOrgID).
type conflictPreserveOrgDB struct {
	insertCount int
	storedOrgID pgtype.UUID
}

func (db *conflictPreserveOrgDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (db *conflictPreserveOrgDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, nil
}

func (db *conflictPreserveOrgDB) QueryRow(_ context.Context, query string, args ...interface{}) pgx.Row {
	switch {
	case strings.Contains(query, "FROM workspace_github_sources"):
		if db.insertCount == 0 {
			return errRow{err: pgx.ErrNoRows}
		}
		// Second call: return existing source so handler returns "exists".
		wsUID, _ := pgutil2.PgUUID(validWsID)
		src := database.WorkspaceGitHubSource{
			WorkspaceID: wsUID,
			RepoURL:     "https://github.com/acme/workspace",
			RepoOwner:   "acme",
			RepoName:    "workspace",
		}
		return webhookSourceRow{src: src}
	case strings.Contains(query, "FROM workspaces") && !strings.Contains(query, "workspace_github_sources"):
		wsUID, _ := pgutil2.PgUUID(validWsID)
		ws := database.Workspace{
			ID:               wsUID,
			OrganizationID:   db.storedOrgID,
			Slug:             "workspace",
			Name:             "Workspace",
			ManagementRepoID: "management-repo",
		}
		return &importSuccessRow{workspaceID: ws.ID, orgID: ws.OrganizationID}
	case strings.Contains(query, "INSERT INTO workspaces"):
		db.insertCount++
		// Capture first org; on conflict, return same stored orgID (simulates Option 2B).
		if len(args) > 1 {
			if uid, ok := args[1].(pgtype.UUID); ok && db.insertCount == 1 {
				db.storedOrgID = uid
			}
		}
		wsUID, _ := pgutil2.PgUUID(validWsID)
		return &importSuccessRow{workspaceID: wsUID, orgID: db.storedOrgID}
	case strings.Contains(query, "INSERT INTO workspace_github_sources"):
		return &importSourceRow{}
	case strings.Contains(query, "INSERT INTO workspace_sync_runs"):
		return &importSyncRunRow{}
	case strings.Contains(query, "UPDATE workspace_sync_runs"):
		return &importSyncRunRow{}
	default:
		return errRow{err: pgx.ErrNoRows}
	}
}

func makeImportRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/internal/workspaces/import",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestImportWorkspaceHandler_MissingOrganizationID(t *testing.T) {
	h := &ServiceHandler{
		GitHub: &recordingGitHubAdapter{},
		Queue:  &recordingEnqueuer{},
	}
	req := makeImportRequest(t, `{"repo_url":"https://github.com/acme/workspace","default_branch":"main"}`)
	rec := httptest.NewRecorder()
	testRouter(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing organization_id, got %d body=%s", rec.Code, rec.Body.String())
	}
	var apiErr domain.APIError
	if err := json.NewDecoder(rec.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if apiErr.Code != domain.ErrValidationMissingInput {
		t.Fatalf("expected %s, got %s", domain.ErrValidationMissingInput, apiErr.Code)
	}
}

func TestImportWorkspaceHandler_EmptyOrganizationID(t *testing.T) {
	h := &ServiceHandler{
		GitHub: &recordingGitHubAdapter{},
		Queue:  &recordingEnqueuer{},
	}
	req := makeImportRequest(t, `{"repo_url":"https://github.com/acme/workspace","organization_id":""}`)
	rec := httptest.NewRecorder()
	testRouter(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty organization_id, got %d body=%s", rec.Code, rec.Body.String())
	}
	var apiErr domain.APIError
	if err := json.NewDecoder(rec.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if apiErr.Code != domain.ErrValidationMissingInput {
		t.Fatalf("expected %s, got %s", domain.ErrValidationMissingInput, apiErr.Code)
	}
}

func TestImportWorkspaceHandler_MalformedOrganizationID(t *testing.T) {
	h := &ServiceHandler{
		GitHub: &recordingGitHubAdapter{},
		Queue:  &recordingEnqueuer{},
	}
	req := makeImportRequest(t, `{"repo_url":"https://github.com/acme/workspace","organization_id":"not-a-uuid"}`)
	rec := httptest.NewRecorder()
	testRouter(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed organization_id, got %d body=%s", rec.Code, rec.Body.String())
	}
	var apiErr domain.APIError
	if err := json.NewDecoder(rec.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if apiErr.Code != domain.ErrValidationInvalidInput {
		t.Fatalf("expected %s, got %s", domain.ErrValidationInvalidInput, apiErr.Code)
	}
}

func TestImportWorkspaceHandler_ZeroOrganizationID(t *testing.T) {
	h := &ServiceHandler{
		GitHub: &recordingGitHubAdapter{},
		Queue:  &recordingEnqueuer{},
	}
	req := makeImportRequest(t, `{"repo_url":"https://github.com/acme/workspace","organization_id":"`+zeroUUID+`"}`)
	rec := httptest.NewRecorder()
	testRouter(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for zero UUID organization_id, got %d body=%s", rec.Code, rec.Body.String())
	}
	var apiErr domain.APIError
	if err := json.NewDecoder(rec.Body).Decode(&apiErr); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if apiErr.Code != domain.ErrValidationInvalidInput {
		t.Fatalf("expected %s, got %s", domain.ErrValidationInvalidInput, apiErr.Code)
	}
}

// TestImportWorkspaceHandler_ValidationRejectsBeforeDBWrite verifies that validation
// errors (missing, empty, malformed, zero) do not trigger any DB writes or queue enqueues.
func TestImportWorkspaceHandler_ValidationRejectsBeforeDBWrite(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"missing", `{"repo_url":"https://github.com/acme/workspace"}`},
		{"empty", `{"repo_url":"https://github.com/acme/workspace","organization_id":""}`},
		{"malformed", `{"repo_url":"https://github.com/acme/workspace","organization_id":"bad"}`},
		{"zero", `{"repo_url":"https://github.com/acme/workspace","organization_id":"` + zeroUUID + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &importNoRowsDB{}
			enqueuer := &recordingEnqueuer{}
			github := &recordingGitHubAdapter{}
			h := &ServiceHandler{
				Q:      database.New(store),
				GitHub: github,
				Queue:  enqueuer,
			}
			req := makeImportRequest(t, tc.body)
			rec := httptest.NewRecorder()
			testRouter(h).ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
			}
			if store.writeQueries != 0 {
				t.Fatalf("expected no DB writes, got %d", store.writeQueries)
			}
			if len(enqueuer.workspaceSyncs) != 0 {
				t.Fatalf("expected no queue enqueues, got %d", len(enqueuer.workspaceSyncs))
			}
			if github.metadataCalls != 0 {
				t.Fatalf("expected no GitHub calls, got %d", github.metadataCalls)
			}
		})
	}
}

// TestImportWorkspaceHandler_HappyPath verifies that a valid import request writes
// the workspace row with the correct organization_id.
func TestImportWorkspaceHandler_HappyPath(t *testing.T) {
	wsUID, _ := pgutil2.PgUUID(validWsID)
	store := &importSuccessDB{upsertWorkspaceID: wsUID}
	enqueuer := &recordingEnqueuer{}
	github := &recordingGitHubAdapter{
		metadata: &domain.WorkspaceSnapshot{Name: "Workspace", Slug: "workspace", ManagementRepoID: "repo"},
	}
	h := &ServiceHandler{
		Q:      database.New(store),
		GitHub: github,
		Queue:  enqueuer,
	}

	req := makeImportRequest(t, `{
		"repo_url":"https://github.com/acme/workspace",
		"default_branch":"main",
		"organization_id":"`+validOrgID+`"
	}`)
	rec := httptest.NewRecorder()
	testRouter(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	if store.writeQueries == 0 {
		t.Fatal("expected at least one DB write")
	}

	expectedOrgUID, _ := pgutil2.PgUUID(validOrgID)
	if store.upsertOrgID != expectedOrgUID {
		t.Fatalf("upserted org_id = %v, want %s", store.upsertOrgID, validOrgID)
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["organization_id"] != validOrgID {
		t.Fatalf("response organization_id = %q, want %q", resp["organization_id"], validOrgID)
	}
}

// TestImportWorkspaceHandler_OnConflictPreservesOrganizationID verifies that
// re-importing the same workspace with a different organization_id leaves the
// original organization_id unchanged (Option 2B semantics).
func TestImportWorkspaceHandler_OnConflictPreservesOrganizationID(t *testing.T) {
	conflictDB := &conflictPreserveOrgDB{}
	enqueuer := &recordingEnqueuer{}
	github := &recordingGitHubAdapter{
		metadata: &domain.WorkspaceSnapshot{Name: "Workspace", Slug: "workspace", ManagementRepoID: "repo"},
	}
	h := &ServiceHandler{
		Q:      database.New(conflictDB),
		GitHub: github,
		Queue:  enqueuer,
	}

	// First import: org validOrgID.
	req1 := makeImportRequest(t, `{
		"repo_url":"https://github.com/acme/workspace",
		"default_branch":"main",
		"organization_id":"`+validOrgID+`"
	}`)
	rec1 := httptest.NewRecorder()
	testRouter(h).ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusAccepted {
		t.Fatalf("first import: expected 202, got %d body=%s", rec1.Code, rec1.Body.String())
	}

	// Confirm stored orgID is the first org.
	expectedOrgUID, _ := pgutil2.PgUUID(validOrgID)
	if conflictDB.storedOrgID != expectedOrgUID {
		t.Fatalf("after first import storedOrgID = %v, want %s", conflictDB.storedOrgID, validOrgID)
	}

	// Second import: different org (validOrgID2). The DB returns the original org.
	req2 := makeImportRequest(t, `{
		"repo_url":"https://github.com/acme/workspace",
		"default_branch":"main",
		"organization_id":"`+validOrgID2+`"
	}`)
	rec2 := httptest.NewRecorder()

	// Reset GitHub call count for the second request.
	github2 := &recordingGitHubAdapter{
		metadata: &domain.WorkspaceSnapshot{Name: "Workspace", Slug: "workspace", ManagementRepoID: "repo"},
	}
	h.GitHub = github2
	testRouter(h).ServeHTTP(rec2, req2)

	// Second import of the same repo finds the existing record and returns 200 "exists".
	if rec2.Code != http.StatusOK {
		t.Fatalf("second import: expected 200 (exists), got %d body=%s", rec2.Code, rec2.Body.String())
	}

	// storedOrgID must still be validOrgID (unchanged).
	if conflictDB.storedOrgID != expectedOrgUID {
		t.Fatalf("after second import storedOrgID = %v, want %s (must not change)", conflictDB.storedOrgID, validOrgID)
	}
}
