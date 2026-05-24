package handler

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hibiken/asynq"
	"github.com/rs/zerolog/log"

	"github.com/tiendv89/workspace-github-adapter/internal/database"
	"github.com/tiendv89/workspace-github-adapter/internal/httputil"
	"github.com/tiendv89/workspace-github-adapter/internal/pgutil"
	"github.com/tiendv89/workspace-github-adapter/internal/queue"
	"github.com/tiendv89/workspace-github-adapter/internal/urlutil"
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
func (h *ServiceHandler) WebhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := webhook.ReadAndVerify(r, h.WebhookSecret)
	if err != nil {
		http.Error(w, "signature verification failed: "+err.Error(), http.StatusUnauthorized)
		return
	}

	// Only handle push events; ignore other event types gracefully.
	eventType := r.Header.Get("X-GitHub-Event")
	if eventType != "push" {
		httputil.WriteOK(w, http.StatusOK, map[string]string{"status": "ignored", "event": eventType})
		return
	}

	ev, err := webhook.ParsePushEvent(body)
	if err != nil {
		http.Error(w, "invalid push event payload", http.StatusBadRequest)
		return
	}

	branch := webhook.BranchFromRef(ev.Ref)

	// Look up the workspace by repo URL to find workspaceID, defaultBranch, and branchPattern.
	repoURL := ev.Repository.CloneURL
	if repoURL == "" {
		repoURL = ev.Repository.HTMLURL
	}
	wsInfo, dbErr := h.findWorkspaceByRepoURL(r.Context(), repoURL)
	if dbErr != nil {
		// Unknown repo — not an error from our side, just ignore.
		log.Info().Str("repo_url", repoURL).Msg("webhook: repo not registered")
		httputil.WriteOK(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "repo not registered"})
		return
	}

	info := webhook.ClassifyBranch(branch, wsInfo.defaultBranch, wsInfo.branchPattern)
	switch info.Kind {
	case webhook.BranchIgnored:
		httputil.WriteOK(w, http.StatusOK, map[string]string{"status": "ignored", "branch": branch})
		return

	case webhook.BranchBase:
		payloads := basePushTargetedSyncPayloads(wsInfo, branch, ev)
		if len(payloads) == 0 {
			httputil.WriteOK(w, http.StatusOK, map[string]string{
				"status":      "ignored",
				"branch_kind": "base",
				"reason":      "no feature artifact paths",
			})
			return
		}
		if err := h.enqueueWorkspaceSyncs(payloads); err != nil {
			log.Error().Err(err).Str("workspace_id", wsInfo.workspaceID).Str("branch", branch).Msg("webhook: enqueue base targeted sync failed")
			http.Error(w, "enqueue targeted sync failed", http.StatusServiceUnavailable)
			return
		}
		httputil.WriteOK(w, http.StatusOK, map[string]string{
			"status":      "queued",
			"branch_kind": "base",
			"mode":        "targeted",
		})

	case webhook.BranchFeature:
		if err := h.enqueueTargetedSync(wsInfo, info.FeatureID, branch, "webhook_feature"); err != nil {
			log.Error().Err(err).Str("workspace_id", wsInfo.workspaceID).Str("feature_id", info.FeatureID).Msg("webhook: enqueue feature targeted sync failed")
			http.Error(w, "enqueue targeted sync failed", http.StatusServiceUnavailable)
			return
		}
		httputil.WriteOK(w, http.StatusOK, map[string]string{
			"status":      "queued",
			"branch_kind": "feature",
			"feature_id":  info.FeatureID,
		})

	case webhook.BranchTask:
		if err := h.enqueueTaskSync(wsInfo.workspaceID, info.FeatureID, info.TaskID); err != nil {
			log.Error().Err(err).Str("workspace_id", wsInfo.workspaceID).Str("feature_id", info.FeatureID).Str("task_id", info.TaskID).Msg("webhook: enqueue task sync failed")
			http.Error(w, "enqueue task sync failed", http.StatusServiceUnavailable)
			return
		}
		httputil.WriteOK(w, http.StatusOK, map[string]string{
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
		workspaceID:   pgutil.UUIDString(src.WorkspaceID),
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
		if pgutil.IsDedupeError(err) {
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
		// ErrTaskIDConflict means duplicate — already queued, this is expected with Unique(24h).
		if pgutil.IsDedupeError(err) {
			log.Info().Str("workspace_id", workspaceID).Str("feature_id", featureID).Str("task_id", taskID).Msg("task:sync already queued (dedup)")
			return nil
		}
		return fmt.Errorf("enqueue task:sync: %w", err)
	}
	log.Info().Str("id", info.ID).Str("workspace_id", workspaceID).Str("feature_id", featureID).Str("task_id", taskID).Msg("task:sync enqueued")
	return nil
}
