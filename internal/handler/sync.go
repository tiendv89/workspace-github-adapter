package handler

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"github.com/tiendv89/workspace-github-adapter/pkg/httputil"
	pgutil2 "github.com/tiendv89/workspace-github-adapter/pkg/pgutil"
	"github.com/tiendv89/workspace-github-adapter/pkg/queue"

	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

// SyncWorkspaceHandler handles POST /internal/workspaces/:id/sync.
func (h *ServiceHandler) SyncWorkspaceHandler(c *gin.Context) {
	workspaceID := c.Param("id")
	if workspaceID == "" {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	uid, err := pgutil2.PgUUID(workspaceID)
	if err != nil {
		httputil.WriteAnyError(c, err)
		return
	}
	src, err := h.Q.GetGitHubSource(c.Request.Context(), uid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httputil.WriteSourceError(c, domain.NewDatabaseError(domain.ErrDatabaseNotFound, "github source not found for workspace"))
			return
		}
		httputil.WriteAnyError(c, err)
		return
	}

	defaultBranch := "main"
	if src.DefaultBranch != nil && *src.DefaultBranch != "" {
		defaultBranch = *src.DefaultBranch
	}
	payload := queue.WorkspaceSyncPayload{
		WorkspaceID:   workspaceID,
		RepoURL:       src.RepoUrl,
		DefaultBranch: defaultBranch,
		Trigger:       "api_sync",
		Mode:          "full",
	}
	task, err := queue.NewWorkspaceSyncTask(payload)
	if err != nil {
		httputil.WriteAnyError(c, err)
		return
	}
	info, err := h.Queue.Enqueue(task, asynq.TaskID(WorkspaceSyncTaskID(payload)))
	if err != nil {
		if pgutil2.IsDedupeError(err) {
			httputil.WriteOK(c, http.StatusAccepted, map[string]string{
				"status": "already_queued",
				"type":   queue.TypeWorkspaceSync,
			})
			return
		}
		httputil.WriteAnyError(c, fmt.Errorf("enqueue task: %w", err))
		return
	}

	httputil.WriteOK(c, http.StatusAccepted, map[string]string{
		"task_id": info.ID,
		"queue":   info.Queue,
		"type":    info.Type,
	})
}
