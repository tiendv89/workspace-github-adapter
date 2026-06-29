package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"

	"github.com/tiendv89/workspace-github-adapter/pkg/pgutil"
	"github.com/tiendv89/workspace-github-adapter/pkg/queue"

	"github.com/tiendv89/workspace-github-adapter/internal/database"
	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

// HandleTaskSync processes task:sync jobs from the task-sync queue.
// It derives the source branch from workspace branch_pattern at drain time so
// duplicate webhook events always fetch the latest task branch HEAD.
func (h *Handler) HandleTaskSync(ctx context.Context, t *asynq.Task) error {
	var payload queue.TaskSyncPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal task sync payload: %w", err)
	}
	if payload.WorkspaceID == "" || payload.FeatureID == "" || payload.TaskID == "" {
		return fmt.Errorf("task sync payload missing required fields: %+v", payload)
	}

	log.Info().Str("workspace_id", payload.WorkspaceID).Str("feature_id", payload.FeatureID).Str("task_id", payload.TaskID).Msg("task sync started")

	// Look up workspace to get repo_url and branch_pattern.
	uid, err := pgutil.PgUUID(payload.WorkspaceID)
	if err != nil {
		return err
	}
	ws, err := h.Q.GetWorkspace(ctx, uid)
	if err != nil {
		return fmt.Errorf("get workspace for task sync: %w", err)
	}
	src, err := h.Q.GetGitHubSource(ctx, uid)
	if err != nil {
		return fmt.Errorf("get github source for task sync: %w", err)
	}

	// Derive the task branch from branch_pattern.
	branchPattern := "feature/{feature_id}-{work_id}"
	if ws.BranchPattern != nil && *ws.BranchPattern != "" {
		branchPattern = *ws.BranchPattern
	}
	taskBranch := TaskSyncBranch(payload, branchPattern)

	taskSnap, err := h.GitHub.FetchTask(ctx, src.RepoUrl, taskBranch, payload.FeatureID, payload.TaskID)
	if err != nil {
		h.recordTaskSyncFailed(ctx, payload, taskBranch, err)
		return fmt.Errorf("fetch task %s/%s on branch %s: %w", payload.FeatureID, payload.TaskID, taskBranch, err)
	}

	if err := h.DB.SaveTaskSnapshot(ctx, payload.WorkspaceID, *taskSnap); err != nil {
		h.recordTaskSyncFailed(ctx, payload, taskBranch, err)
		return fmt.Errorf("save task snapshot %s/%s: %w", payload.FeatureID, payload.TaskID, err)
	}
	if err := h.recordTaskSyncSuccess(ctx, payload, taskBranch); err != nil {
		return err
	}

	log.Info().Str("workspace_id", payload.WorkspaceID).Str("feature_id", payload.FeatureID).Str("task_id", payload.TaskID).Msg("task sync finished")
	return nil
}

// TaskSyncBranch derives the task branch from the sync payload and the workspace branch pattern.
func TaskSyncBranch(payload queue.TaskSyncPayload, pattern string) string {
	return DeriveBranch(pattern, payload.FeatureID, payload.TaskID)
}

// DeriveBranch substitutes feature_id and task_id into a branch pattern.
// Pattern format: "feature/{feature_id}-{work_id}"
func DeriveBranch(pattern, featureID, taskID string) string {
	branch := pattern
	branch = strings.ReplaceAll(branch, "{feature_id}", featureID)
	branch = strings.ReplaceAll(branch, "{work_id}", taskID)
	return branch
}

func (h *Handler) recordTaskSyncSuccess(ctx context.Context, payload queue.TaskSyncPayload, branch string) error {
	runID, err := h.ensureTaskSyncRun(ctx, payload, branch, true)
	if err != nil {
		return err
	}
	_, err = h.Q.UpdateSyncRunSuccess(ctx, database.UpdateSyncRunSuccessParams{
		ID: runID,
	})
	if err != nil {
		return fmt.Errorf("update task sync run success: %w", err)
	}
	return nil
}

func (h *Handler) recordTaskSyncFailed(ctx context.Context, payload queue.TaskSyncPayload, branch string, syncErr error) {
	code := "WORKER_TASK_SYNC_FAILED"
	message := syncErr.Error()
	var sourceErr domain.SourceError
	if errors.As(syncErr, &sourceErr) {
		code = string(sourceErr.Code)
		message = sourceErr.Message
	}
	runID, err := h.ensureTaskSyncRun(ctx, payload, branch, false)
	if err != nil {
		log.Error().Err(err).Str("workspace_id", payload.WorkspaceID).Str("feature_id", payload.FeatureID).Str("task_id", payload.TaskID).AnErr("original_error", syncErr).Msg("ensure failed task sync run failed")
		return
	}
	if _, err := h.Q.UpdateSyncRunFailed(ctx, database.UpdateSyncRunFailedParams{
		ID:           runID,
		ErrorCode:    &code,
		ErrorMessage: &message,
	}); err != nil {
		log.Error().Err(err).Str("workspace_id", payload.WorkspaceID).Str("feature_id", payload.FeatureID).Str("task_id", payload.TaskID).AnErr("original_error", syncErr).Msg("update failed task sync run failed")
	}
}

func (h *Handler) ensureTaskSyncRun(ctx context.Context, payload queue.TaskSyncPayload, branch string, requireRefs bool) (pgtype.UUID, error) {
	uid, err := pgutil.PgUUID(payload.WorkspaceID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	featureUUID, taskUUID, err := h.syncRunReferenceIDs(ctx, uid, payload.FeatureID, payload.TaskID)
	if err != nil {
		if requireRefs {
			return pgtype.UUID{}, err
		}
		log.Warn().Err(err).Str("workspace_id", payload.WorkspaceID).Str("feature_id", payload.FeatureID).Str("task_id", payload.TaskID).Msg("could not resolve task sync run refs")
		featureUUID = pgtype.UUID{}
		taskUUID = pgtype.UUID{}
	}
	branchPtr := branch
	row, err := h.Q.InsertSyncRun(ctx, database.InsertSyncRunParams{
		WorkspaceID:  uid,
		Trigger:      "webhook_task",
		Branch:       &branchPtr,
		FeatureID:    featureUUID,
		TaskID:       taskUUID,
		Mode:         "task",
		Status:       "running",
		ChangedPaths: []byte("[]"),
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("insert task sync run: %w", err)
	}
	return row.ID, nil
}
