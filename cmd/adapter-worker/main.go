package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbadapter "github.com/tiendv89/workspace-github-adapter/internal/adapter/db"
	"github.com/tiendv89/workspace-github-adapter/internal/config"
	"github.com/tiendv89/workspace-github-adapter/internal/database"
	"github.com/tiendv89/workspace-github-adapter/internal/domain"
	ghadapter "github.com/tiendv89/workspace-github-adapter/internal/github"
	"github.com/tiendv89/workspace-github-adapter/internal/queue"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	redisOpt, err := queue.RedisOpt(cfg.RedisURL)
	if err != nil {
		log.Fatalf("redis: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("pgxpool.New: %v", err) //nolint:gocritic
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("ping db: %v", err)
	}

	h := &handler{
		db:       dbadapter.New(pool),
		q:        database.New(pool),
		github:   ghadapter.New(cfg.GitHubToken),
		token:    cfg.GitHubToken,
		redisOpt: redisOpt,
	}

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: 5,
		Queues: map[string]int{
			queue.QueueDefault:  1,
			queue.QueueTaskSync: 3,
		},
	})

	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeWorkspaceSync, h.handleWorkspaceSync)
	mux.HandleFunc(queue.TypeTaskSync, h.handleTaskSync)

	log.Println("adapter-worker listening for Redis queue tasks")
	if err := srv.Run(mux); err != nil {
		log.Fatalf("worker stopped: %v", err)
	}
}

type handler struct {
	db                      domain.DbWorkspaceAdapter
	q                       *database.Queries
	github                  domain.GitHubWorkspaceAdapter
	token                   string
	redisOpt                asynq.RedisConnOpt
	newPendingTaskInspector func() pendingTaskInspector
}

type pendingTaskInspector interface {
	ListPendingTasks(queue string, opts ...asynq.ListOption) ([]*asynq.TaskInfo, error)
	DeleteTask(queue string, id string) error
	Close() error
}

func (h *handler) handleWorkspaceSync(ctx context.Context, t *asynq.Task) error {
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

	// Full reconciliation: clear pending task-sync jobs first, then sync everything.
	log.Printf("sync started workspace_id=%s repo_url=%s ref=%s", payload.WorkspaceID, payload.RepoURL, ref)

	// Delete all pending task-sync jobs before full reconciliation starts.
	// The full read supersedes all queued partial updates.
	inspector := h.openPendingTaskInspector()
	defer func() {
		if err := inspector.Close(); err != nil {
			log.Printf("warn: close asynq inspector: %v", err)
		}
	}()
	if _, err := clearPendingTaskSyncJobsForWorkspace(inspector, payload.WorkspaceID); err != nil {
		err = fmt.Errorf("clear pending task-sync jobs for workspace %s: %w", payload.WorkspaceID, err)
		h.recordFailedRun(ctx, payload, trigger, mode, ref, err)
		return err
	}

	snap, err := h.github.ImportWorkspace(ctx, domain.ImportInput{
		RepoURL:       payload.RepoURL,
		DefaultBranch: ref,
		Token:         h.token,
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
		snap.Slug = slugify(payload.Name)
	}

	if err := h.db.SaveSnapshot(ctx, payload.WorkspaceID, snap); err != nil {
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

	log.Printf("sync finished workspace_id=%s commit_sha=%s", payload.WorkspaceID, snap.CommitSHA)
	return nil
}

func (h *handler) openPendingTaskInspector() pendingTaskInspector {
	if h.newPendingTaskInspector != nil {
		return h.newPendingTaskInspector()
	}
	return asynq.NewInspector(h.redisOpt)
}

func clearPendingTaskSyncJobsForWorkspace(inspector pendingTaskInspector, workspaceID string) (int, error) {
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
func (h *handler) handleTargetedSync(ctx context.Context, payload queue.WorkspaceSyncPayload, trigger, ref string) error {
	log.Printf("targeted sync started workspace_id=%s feature_id=%s ref=%s",
		payload.WorkspaceID, payload.FeatureID, ref)

	snap, err := h.github.FetchFeature(ctx, payload.RepoURL, ref, payload.FeatureID)
	if err != nil {
		h.recordFailedRun(ctx, payload, trigger, "targeted", ref, err)
		return err
	}

	if err := h.db.SaveFeatureSnapshot(ctx, payload.WorkspaceID, *snap); err != nil {
		h.recordFailedRun(ctx, payload, trigger, "targeted", ref, err)
		return err
	}

	runUID, err := h.ensureSyncRun(ctx, payload, trigger, "targeted", ref, nil, true)
	if err != nil {
		return err
	}
	_, err = h.q.UpdateSyncRunSuccess(ctx, database.UpdateSyncRunSuccessParams{
		ID: runUID,
	})
	if err != nil {
		log.Printf("warn: update targeted sync run success workspace_id=%s: %v", payload.WorkspaceID, err)
	}

	log.Printf("targeted sync finished workspace_id=%s feature_id=%s", payload.WorkspaceID, payload.FeatureID)
	return nil
}

// handleTaskSync processes task:sync jobs from the task-sync queue.
// It derives the source branch from workspace branch_pattern at drain time so
// duplicate webhook events always fetch the latest task branch HEAD.
func (h *handler) handleTaskSync(ctx context.Context, t *asynq.Task) error {
	var payload queue.TaskSyncPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal task sync payload: %w", err)
	}
	if payload.WorkspaceID == "" || payload.FeatureID == "" || payload.TaskID == "" {
		return fmt.Errorf("task sync payload missing required fields: %+v", payload)
	}

	log.Printf("task sync started workspace_id=%s feature_id=%s task_id=%s",
		payload.WorkspaceID, payload.FeatureID, payload.TaskID)

	// Look up workspace to get repo_url and branch_pattern.
	uid, err := pgUUID(payload.WorkspaceID)
	if err != nil {
		return err
	}
	ws, err := h.q.GetWorkspace(ctx, uid)
	if err != nil {
		return fmt.Errorf("get workspace for task sync: %w", err)
	}
	src, err := h.q.GetGitHubSource(ctx, uid)
	if err != nil {
		return fmt.Errorf("get github source for task sync: %w", err)
	}

	// Derive the task branch from branch_pattern.
	branchPattern := "feature/{feature_id}-{work_id}"
	if ws.BranchPattern != nil && *ws.BranchPattern != "" {
		branchPattern = *ws.BranchPattern
	}
	taskBranch := taskSyncBranch(payload, branchPattern)

	taskSnap, err := h.github.FetchTask(ctx, src.RepoURL, taskBranch, payload.FeatureID, payload.TaskID)
	if err != nil {
		h.recordTaskSyncFailed(ctx, payload, taskBranch, err)
		return fmt.Errorf("fetch task %s/%s on branch %s: %w", payload.FeatureID, payload.TaskID, taskBranch, err)
	}

	if err := h.db.SaveTaskSnapshot(ctx, payload.WorkspaceID, *taskSnap); err != nil {
		h.recordTaskSyncFailed(ctx, payload, taskBranch, err)
		return fmt.Errorf("save task snapshot %s/%s: %w", payload.FeatureID, payload.TaskID, err)
	}
	if err := h.recordTaskSyncSuccess(ctx, payload, taskBranch); err != nil {
		return err
	}

	log.Printf("task sync finished workspace_id=%s feature_id=%s task_id=%s",
		payload.WorkspaceID, payload.FeatureID, payload.TaskID)
	return nil
}

func taskSyncBranch(payload queue.TaskSyncPayload, pattern string) string {
	return deriveBranch(pattern, payload.FeatureID, payload.TaskID)
}

// deriveBranch substitutes feature_id and task_id into a branch pattern.
// Pattern format: "feature/{feature_id}-{work_id}"
func deriveBranch(pattern, featureID, taskID string) string {
	branch := pattern
	branch = strings.ReplaceAll(branch, "{feature_id}", featureID)
	branch = strings.ReplaceAll(branch, "{work_id}", taskID)
	return branch
}

func (h *handler) ensureWorkspaceExists(ctx context.Context, workspaceID string) error {
	uid, err := pgUUID(workspaceID)
	if err != nil {
		return fmt.Errorf("%w: %w", err, asynq.SkipRetry)
	}
	if h.q == nil {
		return nil
	}
	if _, err := h.q.GetWorkspace(ctx, uid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("workspace not found: %s: %w", workspaceID, asynq.SkipRetry)
		}
		return fmt.Errorf("get workspace before sync: %w", err)
	}
	return nil
}

func (h *handler) recordTaskSyncSuccess(ctx context.Context, payload queue.TaskSyncPayload, branch string) error {
	runID, err := h.ensureTaskSyncRun(ctx, payload, branch, true)
	if err != nil {
		return err
	}
	_, err = h.q.UpdateSyncRunSuccess(ctx, database.UpdateSyncRunSuccessParams{
		ID: runID,
	})
	if err != nil {
		return fmt.Errorf("update task sync run success: %w", err)
	}
	return nil
}

func (h *handler) recordTaskSyncFailed(ctx context.Context, payload queue.TaskSyncPayload, branch string, syncErr error) {
	code := "WORKER_TASK_SYNC_FAILED"
	message := syncErr.Error()
	var sourceErr domain.SourceError
	if errors.As(syncErr, &sourceErr) {
		code = string(sourceErr.Code)
		message = sourceErr.Message
	}
	runID, err := h.ensureTaskSyncRun(ctx, payload, branch, false)
	if err != nil {
		log.Printf("ensure failed task sync run failed workspace_id=%s feature_id=%s task_id=%s error=%v original_error=%v",
			payload.WorkspaceID, payload.FeatureID, payload.TaskID, err, syncErr)
		return
	}
	if _, err := h.q.UpdateSyncRunFailed(ctx, database.UpdateSyncRunFailedParams{
		ID:           runID,
		ErrorCode:    &code,
		ErrorMessage: &message,
	}); err != nil {
		log.Printf("update failed task sync run failed workspace_id=%s feature_id=%s task_id=%s error=%v original_error=%v",
			payload.WorkspaceID, payload.FeatureID, payload.TaskID, err, syncErr)
	}
}

func (h *handler) ensureTaskSyncRun(ctx context.Context, payload queue.TaskSyncPayload, branch string, requireRefs bool) (pgtype.UUID, error) {
	uid, err := pgUUID(payload.WorkspaceID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	featureUUID, taskUUID, err := h.syncRunReferenceIDs(ctx, uid, payload.FeatureID, payload.TaskID)
	if err != nil {
		if requireRefs {
			return pgtype.UUID{}, err
		}
		log.Printf("warn: could not resolve task sync run refs workspace_id=%s feature_id=%s task_id=%s: %v",
			payload.WorkspaceID, payload.FeatureID, payload.TaskID, err)
		featureUUID = pgtype.UUID{}
		taskUUID = pgtype.UUID{}
	}
	branchPtr := branch
	row, err := h.q.InsertSyncRun(ctx, database.InsertSyncRunParams{
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

func (h *handler) syncRunReferenceIDs(ctx context.Context, workspaceID pgtype.UUID, featureName, taskName string) (pgtype.UUID, pgtype.UUID, error) {
	var featureUUID pgtype.UUID
	var taskUUID pgtype.UUID
	if strings.TrimSpace(featureName) == "" {
		return featureUUID, taskUUID, nil
	}
	feature, err := h.q.GetWorkspaceFeatureByName(ctx, database.GetWorkspaceFeatureByNameParams{
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
	task, err := h.q.GetWorkspaceTaskByName(ctx, database.GetWorkspaceTaskByNameParams{
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

func (h *handler) upsertGitHubSource(ctx context.Context, workspaceID, repoURL, defaultBranch string) error {
	uid, err := pgUUID(workspaceID)
	if err != nil {
		return err
	}
	owner, repo, err := parseGitHubRepo(repoURL)
	if err != nil {
		return err
	}
	_, err = h.q.UpsertGitHubSource(ctx, database.UpsertGitHubSourceParams{
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

func (h *handler) recordSuccessfulRun(ctx context.Context, payload queue.WorkspaceSyncPayload, trigger, mode, branch, commitSHA string) error {
	commitPtr := commitSHA
	runID, err := h.ensureSyncRun(ctx, payload, trigger, mode, branch, &commitPtr, true)
	if err != nil {
		return err
	}
	_, err = h.q.UpdateSyncRunSuccess(ctx, database.UpdateSyncRunSuccessParams{
		ID:        runID,
		CommitSha: &commitPtr,
	})
	if err != nil {
		return fmt.Errorf("update sync run success: %w", err)
	}
	return nil
}

func (h *handler) recordFailedRun(ctx context.Context, payload queue.WorkspaceSyncPayload, trigger, mode, branch string, syncErr error) {
	code := "WORKER_SYNC_FAILED"
	message := syncErr.Error()
	var sourceErr domain.SourceError
	if errors.As(syncErr, &sourceErr) {
		code = string(sourceErr.Code)
		message = sourceErr.Message
	}
	runID, err := h.ensureSyncRun(ctx, payload, trigger, mode, branch, nil, false)
	if err != nil {
		log.Printf("ensure failed sync run failed workspace_id=%s error=%v original_error=%v", payload.WorkspaceID, err, syncErr)
		return
	}
	if _, err := h.q.UpdateSyncRunFailed(ctx, database.UpdateSyncRunFailedParams{
		ID:           runID,
		ErrorCode:    &code,
		ErrorMessage: &message,
	}); err != nil {
		log.Printf("update failed sync run failed workspace_id=%s error=%v original_error=%v", payload.WorkspaceID, err, syncErr)
	}
}

func (h *handler) ensureSyncRun(ctx context.Context, payload queue.WorkspaceSyncPayload, trigger, mode, branch string, commitSHA *string, requireRefs bool) (pgtype.UUID, error) {
	if payload.SyncRunID != "" {
		return pgUUID(payload.SyncRunID)
	}
	uid, err := pgUUID(payload.WorkspaceID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	featureUUID, _, err := h.syncRunReferenceIDs(ctx, uid, payload.FeatureID, "")
	if err != nil {
		if requireRefs {
			return pgtype.UUID{}, err
		}
		log.Printf("warn: could not resolve sync run feature ref workspace_id=%s feature_id=%s: %v",
			payload.WorkspaceID, payload.FeatureID, err)
		featureUUID = pgtype.UUID{}
	}
	branchPtr := branch
	row, err := h.q.InsertSyncRun(ctx, database.InsertSyncRunParams{
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

func slugify(name string) string {
	lower := strings.ToLower(name)
	re := regexp.MustCompile(`[^a-z0-9]+`)
	slug := re.ReplaceAllString(lower, "-")
	return strings.Trim(slug, "-")
}

func pgUUID(raw string) (pgtype.UUID, error) {
	var uid pgtype.UUID
	if err := uid.Scan(raw); err != nil {
		return pgtype.UUID{}, fmt.Errorf("invalid workspace_id %q: %w", raw, err)
	}
	return uid, nil
}

func parseGitHubRepo(raw string) (string, string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", "", fmt.Errorf("invalid GitHub repo URL: %s", raw)
	}
	if !strings.EqualFold(u.Host, "github.com") {
		return "", "", fmt.Errorf("unsupported GitHub host: %s", u.Host)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid GitHub repo URL path: %s", raw)
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git"), nil
}
