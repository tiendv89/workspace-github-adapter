package handler

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/tiendv89/workspace-github-adapter/pkg/httputil"
	pgutil2 "github.com/tiendv89/workspace-github-adapter/pkg/pgutil"
	"github.com/tiendv89/workspace-github-adapter/pkg/queue"

	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

// InternalWorkspaceHandler handles POST /internal/workspaces/{id}/sync.
func (h *ServiceHandler) InternalWorkspaceHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	workspaceID, ok := WorkspaceIDFromSyncPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	uid, err := pgutil2.PgUUID(workspaceID)
	if err != nil {
		httputil.WriteAnyError(w, err)
		return
	}
	src, err := h.Q.GetGitHubSource(r.Context(), uid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httputil.WriteSourceError(w, domain.NewDatabaseError(domain.ErrDatabaseNotFound, "github source not found for workspace"))
			return
		}
		httputil.WriteAnyError(w, err)
		return
	}

	defaultBranch := "main"
	if src.DefaultBranch != nil && *src.DefaultBranch != "" {
		defaultBranch = *src.DefaultBranch
	}
	payload := queue.WorkspaceSyncPayload{
		WorkspaceID:   workspaceID,
		RepoURL:       src.RepoURL,
		DefaultBranch: defaultBranch,
		Trigger:       "api_sync",
		Mode:          "full",
	}
	task, err := queue.NewWorkspaceSyncTask(payload)
	if err != nil {
		httputil.WriteAnyError(w, err)
		return
	}
	info, err := h.Queue.Enqueue(task, asynq.TaskID(WorkspaceSyncTaskID(payload)))
	if err != nil {
		if pgutil2.IsDedupeError(err) {
			httputil.WriteOK(w, http.StatusAccepted, map[string]string{
				"status": "already_queued",
				"type":   queue.TypeWorkspaceSync,
			})
			return
		}
		httputil.WriteAnyError(w, fmt.Errorf("enqueue task: %w", err))
		return
	}

	httputil.WriteOK(w, http.StatusAccepted, map[string]string{
		"task_id": info.ID,
		"queue":   info.Queue,
		"type":    info.Type,
	})
}
