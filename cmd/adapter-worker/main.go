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

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
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
		log.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("ping db: %v", err)
	}

	h := &handler{
		db:     dbadapter.New(pool),
		q:      database.New(pool),
		github: ghadapter.New(cfg.GitHubToken),
		token:  cfg.GitHubToken,
	}

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: 5,
		Queues: map[string]int{
			"default": 1,
		},
	})

	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeWorkspaceSync, h.handleWorkspaceSync)

	log.Println("adapter-worker listening for Redis queue tasks")
	if err := srv.Run(mux); err != nil {
		log.Fatalf("worker stopped: %v", err)
	}
}

type handler struct {
	db     domain.DbWorkspaceAdapter
	q      *database.Queries
	github domain.GitHubWorkspaceAdapter
	token  string
}

func (h *handler) handleWorkspaceSync(ctx context.Context, t *asynq.Task) error {
	var payload queue.WorkspaceSyncPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}
	if payload.RepoURL == "" {
		return fmt.Errorf("repo_url is required")
	}
	if payload.WorkspaceID == "" {
		payload.WorkspaceID = uuid.NewString()
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

	log.Printf("sync started workspace_id=%s repo_url=%s ref=%s", payload.WorkspaceID, payload.RepoURL, ref)

	snap, err := h.github.ImportWorkspace(ctx, domain.ImportInput{
		RepoURL:       payload.RepoURL,
		DefaultBranch: ref,
		Token:         h.token,
	})
	if err != nil {
		h.recordFailedRun(ctx, payload, trigger, mode, err)
		return err
	}
	snap.WorkspaceID = payload.WorkspaceID
	snap.RepoURL = payload.RepoURL
	if strings.TrimSpace(payload.Name) != "" {
		snap.Name = payload.Name
		snap.Slug = slugify(payload.Name)
	}

	if err := h.db.SaveSnapshot(ctx, payload.WorkspaceID, snap); err != nil {
		h.recordFailedRun(ctx, payload, trigger, mode, err)
		return err
	}
	if err := h.upsertGitHubSource(ctx, payload.WorkspaceID, payload.RepoURL, payload.DefaultBranch); err != nil {
		h.recordFailedRun(ctx, payload, trigger, mode, err)
		return err
	}
	if err := h.recordSuccessfulRun(ctx, payload, trigger, mode, ref, snap.CommitSHA); err != nil {
		return err
	}

	log.Printf("sync finished workspace_id=%s commit_sha=%s", payload.WorkspaceID, snap.CommitSHA)
	return nil
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

func (h *handler) recordSuccessfulRun(ctx context.Context, payload queue.WorkspaceSyncPayload, trigger, mode, branch, commitSHA string) error {
	commitPtr := commitSHA
	runID, err := h.ensureSyncRun(ctx, payload, trigger, mode, branch, &commitPtr)
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

func (h *handler) recordFailedRun(ctx context.Context, payload queue.WorkspaceSyncPayload, trigger, mode string, syncErr error) {
	code := "WORKER_SYNC_FAILED"
	message := syncErr.Error()
	var sourceErr domain.SourceError
	if errors.As(syncErr, &sourceErr) {
		code = string(sourceErr.Code)
		message = sourceErr.Message
	}
	runID, err := h.ensureSyncRun(ctx, payload, trigger, mode, payload.DefaultBranch, nil)
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

func (h *handler) ensureSyncRun(ctx context.Context, payload queue.WorkspaceSyncPayload, trigger, mode, branch string, commitSHA *string) (pgtype.UUID, error) {
	if payload.SyncRunID != "" {
		return pgUUID(payload.SyncRunID)
	}
	uid, err := pgUUID(payload.WorkspaceID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	branchPtr := branch
	row, err := h.q.InsertSyncRun(ctx, database.InsertSyncRunParams{
		WorkspaceID:  uid,
		Trigger:      trigger,
		Branch:       &branchPtr,
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
