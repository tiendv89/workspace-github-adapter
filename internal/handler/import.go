package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"

	"github.com/tiendv89/workspace-github-adapter/pkg/httputil"
	pgutil2 "github.com/tiendv89/workspace-github-adapter/pkg/pgutil"
	"github.com/tiendv89/workspace-github-adapter/pkg/queue"
	"github.com/tiendv89/workspace-github-adapter/pkg/urlutil"

	"github.com/tiendv89/workspace-github-adapter/internal/database"
	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

type importWorkspaceRequest struct {
	RepoURL       string `json:"repo_url"`
	DefaultBranch string `json:"default_branch,omitempty"`
	Name          string `json:"name,omitempty"`
}

type importWorkspaceResponse struct {
	Status        string `json:"status,omitempty"`
	WorkspaceID   string `json:"workspace_id"`
	Name          string `json:"name,omitempty"`
	Slug          string `json:"slug,omitempty"`
	RepoURL       string `json:"repo_url"`
	DefaultBranch string `json:"default_branch"`
}

// ImportWorkspaceHandler handles POST /internal/workspaces/import.
func (h *ServiceHandler) ImportWorkspaceHandler(c *gin.Context) {
	var req importWorkspaceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteSourceError(c, domain.NewValidationError(domain.ErrValidationMissingInput, "invalid JSON body: "+err.Error()))
		return
	}
	if strings.TrimSpace(req.RepoURL) == "" {
		httputil.WriteSourceError(c, domain.NewValidationError(domain.ErrValidationMissingInput, "repo_url is required"))
		return
	}
	if req.DefaultBranch == "" {
		req.DefaultBranch = "main"
	}

	owner, repo, err := urlutil.ParseGitHubRepo(req.RepoURL)
	if err != nil {
		httputil.WriteAnyError(c, err)
		return
	}
	existing, found, err := h.findExistingImport(c.Request.Context(), owner, repo)
	if err != nil {
		httputil.WriteAnyError(c, err)
		return
	}
	if found {
		writeExistingImport(c, existing, req.RepoURL, req.DefaultBranch)
		return
	}

	snap, err := h.GitHub.FetchWorkspaceMetadata(c.Request.Context(), domain.ImportInput{
		RepoURL:       req.RepoURL,
		DefaultBranch: req.DefaultBranch,
	})
	if err != nil {
		httputil.WriteAnyError(c, err)
		return
	}

	workspaceID := uuid.NewString()
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = snap.Name
	}
	if name == "" {
		name = owner + "/" + repo
	}
	slug := urlutil.Slugify(name)
	if slug == "" {
		slug = workspaceID
	}

	workspaceID, err = h.createImportPlaceholder(c.Request.Context(), workspaceID, name, slug, req.RepoURL, req.DefaultBranch, snap.ManagementRepoID)
	if err != nil {
		if !pgutil2.IsUniqueViolation(err) {
			httputil.WriteAnyError(c, err)
			return
		}
		if existing, found, findErr := h.findExistingImport(c.Request.Context(), owner, repo); findErr != nil {
			httputil.WriteAnyError(c, findErr)
			return
		} else if found {
			writeExistingImport(c, existing, req.RepoURL, req.DefaultBranch)
			return
		}
		if pgutil2.IsUniqueConstraintViolation(err, "workspaces_slug_unique") {
			httputil.WriteSourceError(c, domain.NewDatabaseConflictError(fmt.Sprintf("workspace slug %q already exists for another GitHub repository", slug)))
			return
		}
		httputil.WriteAnyError(c, err)
		return
	}

	run, err := h.insertRunningRun(c.Request.Context(), workspaceID, "api_import", "full", req.DefaultBranch)
	if err != nil {
		httputil.WriteAnyError(c, err)
		return
	}
	syncRunID := pgutil2.UUIDString(run.ID)

	payload := queue.WorkspaceSyncPayload{
		WorkspaceID:   workspaceID,
		RepoURL:       req.RepoURL,
		DefaultBranch: req.DefaultBranch,
		Trigger:       "api_import",
		Mode:          "full",
		Name:          name,
		SyncRunID:     syncRunID,
	}
	task, err := queue.NewWorkspaceSyncTask(payload)
	if err != nil {
		httputil.WriteAnyError(c, err)
		return
	}
	info, err := h.Queue.Enqueue(task, asynq.TaskID(WorkspaceSyncTaskID(payload)))
	if err != nil {
		if failErr := h.markRunFailed(c.Request.Context(), run.ID, "ENQUEUE_FAILED", err.Error()); failErr != nil {
			log.Error().Err(failErr).Str("workspace_id", workspaceID).Str("run_id", syncRunID).Msg("mark import enqueue failed run failed")
		}
		if pgutil2.IsDedupeError(err) {
			httputil.WriteOK(c, http.StatusAccepted, map[string]string{
				"status":       "already_queued",
				"workspace_id": workspaceID,
				"sync_run_id":  syncRunID,
				"type":         queue.TypeWorkspaceSync,
			})
			return
		}
		httputil.WriteAnyError(c, fmt.Errorf("enqueue task: %w", err))
		return
	}

	httputil.WriteOK(c, http.StatusAccepted, map[string]string{
		"workspace_id":   workspaceID,
		"name":           name,
		"slug":           slug,
		"repo_url":       req.RepoURL,
		"default_branch": req.DefaultBranch,
		"sync_run_id":    syncRunID,
		"task_id":        info.ID,
		"queue":          info.Queue,
		"type":           info.Type,
	})
}

func (h *ServiceHandler) findExistingImport(ctx context.Context, owner, repo string) (database.Workspace, bool, error) {
	src, err := h.Q.GetGitHubSourceByRepo(ctx, database.GetGitHubSourceByRepoParams{
		RepoOwner: owner,
		RepoName:  repo,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return database.Workspace{}, false, nil
		}
		return database.Workspace{}, false, fmt.Errorf("get github source by repo: %w", err)
	}
	ws, err := h.Q.GetWorkspace(ctx, src.WorkspaceID)
	if err != nil {
		return database.Workspace{}, false, fmt.Errorf("get existing imported workspace: %w", err)
	}
	return ws, true, nil
}

func (h *ServiceHandler) createImportPlaceholder(ctx context.Context, workspaceID, name, slug, repoURL, defaultBranch, managementRepoID string) (string, error) {
	uid, err := pgutil2.PgUUID(workspaceID)
	if err != nil {
		return "", err
	}
	if h.Pool == nil {
		return h.createImportPlaceholderWithQueries(ctx, h.Q, uid, name, slug, repoURL, defaultBranch, managementRepoID)
	}

	tx, err := h.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", fmt.Errorf("begin import placeholder transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	actualWorkspaceID, err := h.createImportPlaceholderWithQueries(ctx, h.Q.WithTx(tx), uid, name, slug, repoURL, defaultBranch, managementRepoID)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit import placeholder transaction: %w", err)
	}
	return actualWorkspaceID, nil
}

func (h *ServiceHandler) createImportPlaceholderWithQueries(ctx context.Context, q *database.Queries, uid pgtype.UUID, name, slug, repoURL, defaultBranch, managementRepoID string) (string, error) {
	ws, err := q.UpsertWorkspaceByID(ctx, database.UpsertWorkspaceByIDParams{
		ID:               uid,
		Slug:             slug,
		Name:             name,
		ManagementRepoID: managementRepoID,
	})
	if err != nil {
		return "", fmt.Errorf("upsert import placeholder workspace: %w", err)
	}
	actualWorkspaceID := pgutil2.UUIDString(ws.ID)
	if err := h.upsertGitHubSourceWithQueries(ctx, q, actualWorkspaceID, repoURL, defaultBranch); err != nil {
		return "", err
	}
	return actualWorkspaceID, nil
}

func (h *ServiceHandler) upsertGitHubSourceWithQueries(ctx context.Context, q *database.Queries, workspaceID, repoURL, defaultBranch string) error {
	uid, err := pgutil2.PgUUID(workspaceID)
	if err != nil {
		return err
	}
	owner, repo, err := urlutil.ParseGitHubRepo(repoURL)
	if err != nil {
		return err
	}
	_, err = q.UpsertGitHubSource(ctx, database.UpsertGitHubSourceParams{
		WorkspaceID:   uid,
		RepoURL:       repoURL,
		RepoOwner:     owner,
		RepoName:      repo,
		DefaultBranch: &defaultBranch,
	})
	if err != nil {
		return fmt.Errorf("upsert github source: %w", err)
	}
	return nil
}

func (h *ServiceHandler) insertRunningRun(ctx context.Context, workspaceID, trigger, mode, branch string) (database.WorkspaceSyncRun, error) {
	uid, err := pgutil2.PgUUID(workspaceID)
	if err != nil {
		return database.WorkspaceSyncRun{}, err
	}
	branchPtr := branch
	row, err := h.Q.InsertSyncRun(ctx, database.InsertSyncRunParams{
		WorkspaceID:  uid,
		Trigger:      trigger,
		Branch:       &branchPtr,
		Mode:         mode,
		Status:       "running",
		ChangedPaths: []byte("[]"),
	})
	if err != nil {
		return database.WorkspaceSyncRun{}, fmt.Errorf("insert sync run: %w", err)
	}
	return row, nil
}

func (h *ServiceHandler) markRunFailed(ctx context.Context, runID pgtype.UUID, code, message string) error {
	_, err := h.Q.UpdateSyncRunFailed(ctx, database.UpdateSyncRunFailedParams{
		ID:           runID,
		ErrorCode:    &code,
		ErrorMessage: &message,
	})
	if err != nil {
		return fmt.Errorf("update sync run failed: %w", err)
	}
	return nil
}

func writeExistingImport(c *gin.Context, existing database.Workspace, repoURL, defaultBranch string) {
	httputil.WriteOK(c, http.StatusOK, importWorkspaceResponse{
		Status:        "exists",
		WorkspaceID:   pgutil2.UUIDString(existing.ID),
		Name:          existing.Name,
		Slug:          existing.Slug,
		RepoURL:       repoURL,
		DefaultBranch: defaultBranch,
	})
}
