package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog/log"

	"github.com/tiendv89/workspace-github-adapter/pkg/pgutil"
	"github.com/tiendv89/workspace-github-adapter/pkg/queue"
	"github.com/tiendv89/workspace-github-adapter/pkg/urlutil"

	"github.com/tiendv89/workspace-github-adapter/internal/database"
	"github.com/tiendv89/workspace-github-adapter/internal/domain"
	"github.com/tiendv89/workspace-github-adapter/internal/webhook"
)

// commitComparer is the optional capability used for incremental (commit-diff)
// syncs. The production GitHub adapter implements it; test mocks that don't are
// transparently handled by falling back to a full reconciliation.
type commitComparer interface {
	HeadCommit(ctx context.Context, repoURL, ref string) (string, error)
	CompareChangedPaths(ctx context.Context, repoURL, base, head string) (changed, removed []string, complete bool, err error)
}

// HandleWorkspaceSync processes workspace:sync jobs.
func (h *Handler) HandleWorkspaceSync(ctx context.Context, t *asynq.Task) error {
	var payload queue.WorkspaceSyncPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}
	if payload.RepoURL == "" {
		return fmt.Errorf("repo_url is required: %w", asynq.SkipRetry)
	}
	if strings.TrimSpace(payload.WorkspaceID) == "" {
		return fmt.Errorf("workspace_id is required for workspace sync: %w", asynq.SkipRetry)
	}
	if err := h.ensureWorkspaceExists(ctx, payload.WorkspaceID); err != nil {
		return err
	}
	if payload.DefaultBranch == "" {
		payload.DefaultBranch = "main"
	}
	ref := payload.Ref
	if ref == "" {
		ref = payload.DefaultBranch
	}
	trigger := payload.Trigger
	if trigger == "" {
		trigger = "redis_worker"
	}
	mode := payload.Mode
	if mode == "" {
		mode = "full"
	}

	// Targeted sync: fetch and upsert a single feature only.
	if mode == "targeted" && payload.FeatureID != "" {
		return h.handleTargetedSync(ctx, payload, trigger, ref)
	}

	log.Info().Str("workspace_id", payload.WorkspaceID).Str("repo_url", payload.RepoURL).Str("ref", ref).Msg("sync started")

	// Try an incremental, commit-diff sync first: diff the last-synced commit
	// against HEAD and only re-fetch changed features (or no-op when nothing
	// changed). Falls back to a full reconciliation for anything it can't safely
	// handle incrementally (no baseline, deletions/renames, workspace.yaml
	// changes, a too-large diff, or any fetch error).
	if handled, err := h.tryIncrementalSync(ctx, payload, trigger, ref); err != nil {
		return err
	} else if handled {
		return nil
	}

	// Full reconciliation: clear pending task-sync jobs first, then sync everything.

	// Delete all pending task-sync jobs before full reconciliation starts.
	// The full read supersedes all queued partial updates.
	inspector := h.openPendingTaskInspector()
	defer func() {
		if err := inspector.Close(); err != nil {
			log.Warn().Err(err).Msg("close asynq inspector")
		}
	}()
	if _, err := clearPendingTaskSyncJobsForWorkspace(inspector, payload.WorkspaceID); err != nil {
		err = fmt.Errorf("clear pending task-sync jobs for workspace %s: %w", payload.WorkspaceID, err)
		h.recordFailedRun(ctx, payload, trigger, mode, ref, err)
		return err
	}

	log.Info().Str("workspace_id", payload.WorkspaceID).Str("ref", ref).Msg("sync [1/3] fetching workspace from GitHub")
	snap, err := h.GitHub.ImportWorkspace(ctx, domain.ImportInput{
		RepoURL:       payload.RepoURL,
		DefaultBranch: ref,
	})
	if err != nil {
		h.recordFailedRun(ctx, payload, trigger, mode, ref, err)
		return err
	}
	if err := firstSnapshotSourceError(snap); err != nil {
		h.recordFailedRun(ctx, payload, trigger, mode, ref, err)
		return err
	}
	snap.WorkspaceID = payload.WorkspaceID
	snap.RepoURL = payload.RepoURL
	if strings.TrimSpace(payload.Name) != "" {
		snap.Name = payload.Name
		snap.Slug = urlutil.Slugify(payload.Name)
	}

	taskTotal := 0
	for i := range snap.Features {
		taskTotal += len(snap.Features[i].Tasks)
	}
	log.Info().
		Str("workspace_id", payload.WorkspaceID).
		Str("commit_sha", snap.CommitSHA).
		Int("features", len(snap.Features)).
		Int("tasks", taskTotal).
		Msg("sync [2/3] fetched — saving snapshot to DB")

	if err := h.DB.SaveSnapshot(ctx, payload.WorkspaceID, snap); err != nil {
		h.recordFailedRun(ctx, payload, trigger, mode, ref, err)
		return err
	}
	if err := h.upsertGitHubSource(ctx, payload.WorkspaceID, payload.RepoURL, payload.DefaultBranch); err != nil {
		h.recordFailedRun(ctx, payload, trigger, mode, ref, err)
		return err
	}
	if err := h.recordSuccessfulRun(ctx, payload, trigger, mode, ref, snap.CommitSHA); err != nil {
		return err
	}

	log.Info().
		Str("workspace_id", payload.WorkspaceID).
		Str("commit_sha", snap.CommitSHA).
		Int("features", len(snap.Features)).
		Int("tasks", taskTotal).
		Msg("sync [3/3] finished")
	return nil
}

func clearPendingTaskSyncJobsForWorkspace(inspector PendingTaskInspector, workspaceID string) (int, error) {
	const pageSize = 100
	deleted := 0
	page := 1
	for {
		tasks, err := inspector.ListPendingTasks(queue.QueueTaskSync, asynq.Page(page), asynq.PageSize(pageSize))
		if errors.Is(err, asynq.ErrQueueNotFound) {
			return deleted, nil
		}
		if err != nil {
			return deleted, fmt.Errorf("list pending task-sync jobs: %w", err)
		}
		if len(tasks) == 0 {
			return deleted, nil
		}

		deletedFromPage := false
		for _, info := range tasks {
			if info.Type != queue.TypeTaskSync {
				continue
			}
			var payload queue.TaskSyncPayload
			if err := json.Unmarshal(info.Payload, &payload); err != nil {
				continue
			}
			if payload.WorkspaceID != workspaceID {
				continue
			}
			if err := inspector.DeleteTask(queue.QueueTaskSync, info.ID); err != nil {
				return deleted, fmt.Errorf("delete pending task-sync job %s: %w", info.ID, err)
			}
			deleted++
			deletedFromPage = true
		}

		if deletedFromPage {
			page = 1
			continue
		}
		if len(tasks) < pageSize {
			return deleted, nil
		}
		page++
	}
}

// handleTargetedSync fetches and upserts a single feature's artifacts.
func (h *Handler) handleTargetedSync(ctx context.Context, payload queue.WorkspaceSyncPayload, trigger, ref string) error {
	log.Info().Str("workspace_id", payload.WorkspaceID).Str("feature_id", payload.FeatureID).Str("ref", ref).Msg("targeted sync started")

	snap, err := h.GitHub.FetchFeature(ctx, payload.RepoURL, ref, payload.FeatureID)
	if err != nil {
		h.recordFailedRun(ctx, payload, trigger, "targeted", ref, err)
		return err
	}
	log.Info().Str("workspace_id", payload.WorkspaceID).Str("feature_id", payload.FeatureID).Int("tasks", len(snap.Tasks)).Msg("targeted sync: fetched feature — saving")

	err = h.DB.SaveFeatureSnapshot(ctx, payload.WorkspaceID, *snap)
	if err != nil {
		h.recordFailedRun(ctx, payload, trigger, "targeted", ref, err)
		return err
	}

	runUID, err := h.ensureSyncRun(ctx, payload, trigger, "targeted", ref, nil, true)
	if err != nil {
		return err
	}
	_, err = h.Q.UpdateSyncRunSuccess(ctx, database.UpdateSyncRunSuccessParams{
		ID: runUID,
	})
	if err != nil {
		log.Warn().Err(err).Str("workspace_id", payload.WorkspaceID).Msg("update targeted sync run success")
	}

	log.Info().Str("workspace_id", payload.WorkspaceID).Str("feature_id", payload.FeatureID).Msg("targeted sync finished")
	return nil
}

// tryIncrementalSync diffs the last-synced commit against HEAD and re-fetches
// only the changed features (or no-ops when nothing changed), avoiding the cost
// of a full reconciliation. Returns handled=true when it fully satisfied the
// sync; handled=false means the caller should fall back to a full sync.
func (h *Handler) tryIncrementalSync(ctx context.Context, payload queue.WorkspaceSyncPayload, trigger, ref string) (bool, error) {
	cc, ok := h.GitHub.(commitComparer)
	if !ok {
		return false, nil
	}
	wsUUID, err := pgutil.PgUUID(payload.WorkspaceID)
	if err != nil {
		return false, nil
	}
	last, err := h.Q.GetLatestSyncRun(ctx, wsUUID)
	if err != nil || last.Status != "success" || last.CommitSha == nil || *last.CommitSha == "" {
		return false, nil // no trustworthy baseline → full sync
	}
	lastCommit := *last.CommitSha

	head, err := cc.HeadCommit(ctx, payload.RepoURL, ref)
	if err != nil {
		return false, nil // can't resolve HEAD → let full sync run/report
	}

	if head == lastCommit {
		log.Info().Str("workspace_id", payload.WorkspaceID).Str("commit_sha", head).Msg("sync incremental: already up to date — skipping fetch")
		if rerr := h.recordSuccessfulRun(ctx, payload, trigger, "incremental", ref, head); rerr != nil {
			return true, rerr
		}
		return true, nil
	}

	changed, removed, complete, err := cc.CompareChangedPaths(ctx, payload.RepoURL, lastCommit, head)
	if err != nil || !complete {
		return false, nil
	}
	// Anything structural (deletions/renames or workspace.yaml) needs a full
	// reconciliation — full sync also prunes features removed from git.
	if webhook.PathsTouchWorkspaceConfig(changed) || webhook.PathsTouchWorkspaceConfig(removed) {
		return false, nil
	}
	if len(webhook.FeatureIDsFromPaths(removed)) > 0 {
		return false, nil
	}

	featureIDs := webhook.FeatureIDsFromPaths(changed)
	log.Info().
		Str("workspace_id", payload.WorkspaceID).
		Str("from", lastCommit).Str("to", head).
		Int("changed_features", len(featureIDs)).
		Msg("sync incremental: re-fetching changed features")

	for i, fid := range featureIDs {
		snap, ferr := h.GitHub.FetchFeature(ctx, payload.RepoURL, ref, fid)
		if ferr != nil {
			return false, nil // fall back to full on any fetch issue
		}
		if serr := h.DB.SaveFeatureSnapshot(ctx, payload.WorkspaceID, *snap); serr != nil {
			return false, nil
		}
		log.Info().Str("workspace_id", payload.WorkspaceID).Str("feature_id", fid).Int("progress", i+1).Int("total", len(featureIDs)).Int("tasks", len(snap.Tasks)).Msg("sync incremental: synced feature")
	}

	if rerr := h.recordSuccessfulRun(ctx, payload, trigger, "incremental", ref, head); rerr != nil {
		return true, rerr
	}
	log.Info().Str("workspace_id", payload.WorkspaceID).Str("commit_sha", head).Int("features", len(featureIDs)).Msg("sync incremental: finished")
	return true, nil
}

func (h *Handler) ensureWorkspaceExists(ctx context.Context, workspaceID string) error {
	uid, err := pgutil.PgUUID(workspaceID)
	if err != nil {
		return fmt.Errorf("%w: %w", err, asynq.SkipRetry)
	}
	if h.Q == nil {
		return nil
	}
	if _, err := h.Q.GetWorkspace(ctx, uid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("workspace not found: %s: %w", workspaceID, asynq.SkipRetry)
		}
		return fmt.Errorf("get workspace before sync: %w", err)
	}
	return nil
}

func (h *Handler) upsertGitHubSource(ctx context.Context, workspaceID, repoURL, defaultBranch string) error {
	uid, err := pgutil.PgUUID(workspaceID)
	if err != nil {
		return err
	}
	owner, repo, err := urlutil.ParseGitHubRepo(repoURL)
	if err != nil {
		return err
	}
	_, err = h.Q.UpsertGitHubSource(ctx, database.UpsertGitHubSourceParams{
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

func firstSnapshotSourceError(snap *domain.WorkspaceSnapshot) error {
	if snap == nil {
		return domain.NewDatabaseError(domain.ErrAdapterInternal, "workspace import returned nil snapshot")
	}
	if len(snap.SourceErrors) == 0 {
		return nil
	}
	return snap.SourceErrors[0]
}

func (h *Handler) recordSuccessfulRun(ctx context.Context, payload queue.WorkspaceSyncPayload, trigger, mode, branch, commitSHA string) error {
	commitPtr := commitSHA
	runID, err := h.ensureSyncRun(ctx, payload, trigger, mode, branch, &commitPtr, true)
	if err != nil {
		return err
	}
	_, err = h.Q.UpdateSyncRunSuccess(ctx, database.UpdateSyncRunSuccessParams{
		ID:        runID,
		CommitSha: &commitPtr,
	})
	if err != nil {
		return fmt.Errorf("update sync run success: %w", err)
	}
	return nil
}

func (h *Handler) recordFailedRun(ctx context.Context, payload queue.WorkspaceSyncPayload, trigger, mode, branch string, syncErr error) {
	code := "WORKER_SYNC_FAILED"
	message := syncErr.Error()
	var sourceErr domain.SourceError
	if errors.As(syncErr, &sourceErr) {
		code = string(sourceErr.Code)
		message = sourceErr.Message
	}
	runID, err := h.ensureSyncRun(ctx, payload, trigger, mode, branch, nil, false)
	if err != nil {
		log.Error().Err(err).Str("workspace_id", payload.WorkspaceID).AnErr("original_error", syncErr).Msg("ensure failed sync run failed")
		return
	}
	if _, err := h.Q.UpdateSyncRunFailed(ctx, database.UpdateSyncRunFailedParams{
		ID:           runID,
		ErrorCode:    &code,
		ErrorMessage: &message,
	}); err != nil {
		log.Error().Err(err).Str("workspace_id", payload.WorkspaceID).AnErr("original_error", syncErr).Msg("update failed sync run failed")
	}
}

func (h *Handler) ensureSyncRun(ctx context.Context, payload queue.WorkspaceSyncPayload, trigger, mode, branch string, commitSHA *string, requireRefs bool) (pgtype.UUID, error) {
	if payload.SyncRunID != "" {
		return pgutil.PgUUID(payload.SyncRunID)
	}
	uid, err := pgutil.PgUUID(payload.WorkspaceID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	featureUUID, _, err := h.syncRunReferenceIDs(ctx, uid, payload.FeatureID, "")
	if err != nil {
		if requireRefs {
			return pgtype.UUID{}, err
		}
		log.Warn().Err(err).Str("workspace_id", payload.WorkspaceID).Str("feature_id", payload.FeatureID).Msg("could not resolve sync run feature ref")
		featureUUID = pgtype.UUID{}
	}
	branchPtr := branch
	row, err := h.Q.InsertSyncRun(ctx, database.InsertSyncRunParams{
		WorkspaceID:  uid,
		Trigger:      trigger,
		Branch:       &branchPtr,
		FeatureID:    featureUUID,
		Mode:         mode,
		Status:       "running",
		CommitSha:    commitSHA,
		ChangedPaths: []byte("[]"),
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("insert sync run: %w", err)
	}
	return row.ID, nil
}

func (h *Handler) syncRunReferenceIDs(ctx context.Context, workspaceID pgtype.UUID, featureName, taskName string) (pgtype.UUID, pgtype.UUID, error) {
	var featureUUID pgtype.UUID
	var taskUUID pgtype.UUID
	if strings.TrimSpace(featureName) == "" {
		return featureUUID, taskUUID, nil
	}
	feature, err := h.Q.GetWorkspaceFeatureByName(ctx, database.GetWorkspaceFeatureByNameParams{
		WorkspaceID: workspaceID,
		FeatureName: featureName,
	})
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("resolve sync run feature ref %s: %w", featureName, err)
	}
	featureUUID = feature.ID
	if strings.TrimSpace(taskName) == "" {
		return featureUUID, taskUUID, nil
	}
	task, err := h.Q.GetWorkspaceTaskByName(ctx, database.GetWorkspaceTaskByNameParams{
		WorkspaceID: workspaceID,
		FeatureID:   featureUUID,
		TaskName:    taskName,
	})
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("resolve sync run task ref %s/%s: %w", featureName, taskName, err)
	}
	taskUUID = task.ID
	return featureUUID, taskUUID, nil
}
