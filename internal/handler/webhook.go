package handler

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/rs/zerolog/log"

	"github.com/tiendv89/workspace-github-adapter/pkg/httputil"
	pgutil2 "github.com/tiendv89/workspace-github-adapter/pkg/pgutil"
	"github.com/tiendv89/workspace-github-adapter/pkg/queue"
	"github.com/tiendv89/workspace-github-adapter/pkg/urlutil"

	"github.com/tiendv89/workspace-github-adapter/internal/database"
	"github.com/tiendv89/workspace-github-adapter/internal/webhook"
)

// workspaceWebhookInfo holds the minimal workspace data needed by webhook routing.
type workspaceWebhookInfo struct {
	workspaceID   string
	repoURL       string
	defaultBranch string
	branchPattern string
}

// WebhookHandler processes GitHub push event webhooks.
// It verifies the HMAC signature, parses the push payload, and routes based on branch:
//   - base branch → enqueue targeted sync for touched features
//   - feature branch → enqueue targeted workspace:sync for that feature
//   - task branch → enqueue task:sync with dedup
//   - other → 200 OK, ignored
func (h *ServiceHandler) WebhookHandler(c *gin.Context) {
	body, err := webhook.ReadAndVerify(c.Request, h.WebhookSecret)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "signature verification failed: " + err.Error()})
		return
	}

	eventType := c.GetHeader("X-GitHub-Event")
	if eventType != "push" {
		httputil.WriteOK(c, http.StatusOK, map[string]string{"status": "ignored", "event": eventType})
		return
	}

	ev, err := webhook.ParsePushEvent(body)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid push event payload"})
		return
	}

	branch := webhook.BranchFromRef(ev.Ref)

	repoURL := ev.Repository.CloneURL
	if repoURL == "" {
		repoURL = ev.Repository.HTMLURL
	}
	wsInfo, dbErr := h.findWorkspaceByRepoURL(c.Request.Context(), repoURL)
	if dbErr != nil {
		log.Info().Str("repo_url", repoURL).Msg("webhook: repo not registered")
		httputil.WriteOK(c, http.StatusOK, map[string]string{"status": "ignored", "reason": "repo not registered"})
		return
	}

	info := webhook.ClassifyBranch(branch, wsInfo.defaultBranch, wsInfo.branchPattern)
	switch info.Kind {
	case webhook.BranchIgnored:
		httputil.WriteOK(c, http.StatusOK, map[string]string{"status": "ignored", "branch": branch})

	case webhook.BranchBase:
		payloads := basePushTargetedSyncPayloads(wsInfo, branch, ev)
		if len(payloads) == 0 {
			httputil.WriteOK(c, http.StatusOK, map[string]string{
				"status":      "ignored",
				"branch_kind": "base",
				"reason":      "no feature artifact paths",
			})
			return
		}
		if err := h.enqueueWorkspaceSyncs(payloads); err != nil {
			log.Error().Err(err).Str("workspace_id", wsInfo.workspaceID).Str("branch", branch).Msg("webhook: enqueue base targeted sync failed")
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "enqueue targeted sync failed"})
			return
		}
		httputil.WriteOK(c, http.StatusOK, map[string]string{
			"status":      "queued",
			"branch_kind": "base",
			"mode":        "targeted",
		})

	case webhook.BranchFeature:
		if err := h.enqueueTargetedSync(wsInfo, info.FeatureID, branch, "webhook_feature"); err != nil {
			log.Error().Err(err).Str("workspace_id", wsInfo.workspaceID).Str("feature_id", info.FeatureID).Msg("webhook: enqueue feature targeted sync failed")
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "enqueue targeted sync failed"})
			return
		}
		httputil.WriteOK(c, http.StatusOK, map[string]string{
			"status":      "queued",
			"branch_kind": "feature",
			"feature_id":  info.FeatureID,
		})

	case webhook.BranchTask:
		if err := h.enqueueTaskSync(wsInfo.workspaceID, info.FeatureID, info.TaskID); err != nil {
			log.Error().Err(err).Str("workspace_id", wsInfo.workspaceID).Str("feature_id", info.FeatureID).Str("task_id", info.TaskID).Msg("webhook: enqueue task sync failed")
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "enqueue task sync failed"})
			return
		}
		httputil.WriteOK(c, http.StatusOK, map[string]string{
			"status":      "queued",
			"branch_kind": "task",
			"feature_id":  info.FeatureID,
			"task_id":     info.TaskID,
		})
	}
}

func basePushTargetedSyncPayloads(ws *workspaceWebhookInfo, branch string, ev *webhook.PushEvent) []queue.WorkspaceSyncPayload {
	featureIDs := webhook.TouchedFeatureIDs(ev)
	payloads := make([]queue.WorkspaceSyncPayload, 0, len(featureIDs))
	for _, featureID := range featureIDs {
		payloads = append(payloads, queue.WorkspaceSyncPayload{
			WorkspaceID:   ws.workspaceID,
			RepoURL:       ws.repoURL,
			DefaultBranch: ws.defaultBranch,
			Ref:           branch,
			Trigger:       "webhook_base",
			Mode:          "targeted",
			FeatureID:     featureID,
		})
	}
	return payloads
}

// findWorkspaceByRepoURL queries the DB for a workspace matching the given repo URL.
func (h *ServiceHandler) findWorkspaceByRepoURL(ctx context.Context, repoURL string) (*workspaceWebhookInfo, error) {
	owner, repo, err := urlutil.ParseGitHubRepo(repoURL)
	if err != nil {
		return nil, err
	}
	src, dbError := h.Q.GetGitHubSourceByRepo(ctx, database.GetGitHubSourceByRepoParams{
		RepoOwner: owner,
		RepoName:  repo,
	})
	if dbError != nil {
		return nil, dbError
	}
	defaultBranch := "main"
	if src.DefaultBranch != nil && *src.DefaultBranch != "" {
		defaultBranch = *src.DefaultBranch
	}
	ws, err := h.Q.GetWorkspace(ctx, src.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("get webhook workspace: %w", err)
	}
	branchPattern := ""
	if ws.BranchPattern != nil {
		branchPattern = *ws.BranchPattern
	}
	return &workspaceWebhookInfo{
		workspaceID:   pgutil2.UUIDString(src.WorkspaceID),
		repoURL:       src.RepoURL,
		defaultBranch: defaultBranch,
		branchPattern: branchPattern,
	}, nil
}

// enqueueTargetedSync enqueues a workspace:sync task with mode=targeted for a single feature.
func (h *ServiceHandler) enqueueTargetedSync(ws *workspaceWebhookInfo, featureID, branch, trigger string) error {
	payload := queue.WorkspaceSyncPayload{
		WorkspaceID:   ws.workspaceID,
		RepoURL:       ws.repoURL,
		DefaultBranch: ws.defaultBranch,
		Ref:           branch,
		Trigger:       trigger,
		Mode:          "targeted",
		FeatureID:     featureID,
	}
	return h.enqueueWorkspaceSync(payload)
}

func (h *ServiceHandler) enqueueWorkspaceSyncs(payloads []queue.WorkspaceSyncPayload) error {
	for _, payload := range payloads {
		if err := h.enqueueWorkspaceSync(payload); err != nil {
			return err
		}
	}
	return nil
}

func (h *ServiceHandler) enqueueWorkspaceSync(payload queue.WorkspaceSyncPayload) error {
	task, err := queue.NewWorkspaceSyncTask(payload)
	if err != nil {
		return fmt.Errorf("build workspace sync task: %w", err)
	}
	if _, err := h.Queue.Enqueue(task, asynq.TaskID(WorkspaceSyncTaskID(payload))); err != nil {
		if pgutil2.IsDedupeError(err) {
			return nil
		}
		return fmt.Errorf("enqueue workspace sync: %w", err)
	}
	return nil
}

// enqueueTaskSync enqueues a task:sync job with deduplication.
func (h *ServiceHandler) enqueueTaskSync(workspaceID, featureID, taskID string) error {
	payload := queue.TaskSyncPayload{
		WorkspaceID: workspaceID,
		FeatureID:   featureID,
		TaskID:      taskID,
	}
	task, err := queue.NewTaskSyncTask(payload)
	if err != nil {
		return fmt.Errorf("build task:sync task: %w", err)
	}
	info, err := h.Queue.Enqueue(task)
	if err != nil {
		if pgutil2.IsDedupeError(err) {
			log.Info().Str("workspace_id", workspaceID).Str("feature_id", featureID).Str("task_id", taskID).Msg("task:sync already queued (dedup)")
			return nil
		}
		return fmt.Errorf("enqueue task:sync: %w", err)
	}
	log.Info().Str("id", info.ID).Str("workspace_id", workspaceID).Str("feature_id", featureID).Str("task_id", taskID).Msg("task:sync enqueued")
	return nil
}
