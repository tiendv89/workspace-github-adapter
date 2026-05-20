package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
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
	"github.com/tiendv89/workspace-github-adapter/internal/webhook"
)

type serviceHandler struct {
	db            domain.DbWorkspaceAdapter
	q             *database.Queries
	pool          *pgxpool.Pool
	github        domain.GitHubWorkspaceAdapter
	token         string
	queue         taskEnqueuer
	webhookSecret string
}

type taskEnqueuer interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

type importWorkspaceRequest struct {
	RepoURL       string `json:"repo_url"`
	DefaultBranch string `json:"default_branch,omitempty"`
	Name          string `json:"name,omitempty"`
}

type importWorkspaceResponse struct {
	WorkspaceID   string `json:"workspace_id"`
	Name          string `json:"name,omitempty"`
	Slug          string `json:"slug,omitempty"`
	RepoURL       string `json:"repo_url"`
	DefaultBranch string `json:"default_branch"`
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if cfg.WebhookSecret == "" {
		log.Fatal("GITHUB_WEBHOOK_SECRET is required for adapter-service webhooks")
	}

	redisOpt, err := queue.RedisOpt(cfg.RedisURL)
	if err != nil {
		log.Fatalf("redis: %v", err)
	}
	client := asynq.NewClient(redisOpt)
	defer client.Close()

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

	h := &serviceHandler{
		db:            dbadapter.New(pool),
		q:             database.New(pool),
		pool:          pool,
		github:        ghadapter.New(cfg.GitHubToken),
		token:         cfg.GitHubToken,
		queue:         client,
		webhookSecret: cfg.WebhookSecret,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/internal/workspaces/import", h.importWorkspaceHandler)
	mux.HandleFunc("/internal/workspaces/", h.internalWorkspaceHandler)
	mux.HandleFunc("/webhook", h.webhookHandler)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      mux,
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("adapter-service listening on :%d", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-done
	log.Println("adapter-service: shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

func (h *serviceHandler) importWorkspaceHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	defer r.Body.Close()
	var req importWorkspaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSourceError(w, domain.NewValidationError(domain.ErrValidationMissingInput, "invalid JSON body: "+err.Error()))
		return
	}
	if strings.TrimSpace(req.RepoURL) == "" {
		writeSourceError(w, domain.NewValidationError(domain.ErrValidationMissingInput, "repo_url is required"))
		return
	}
	if req.DefaultBranch == "" {
		req.DefaultBranch = "main"
	}

	owner, repo, err := parseGitHubRepo(req.RepoURL)
	if err != nil {
		writeAnyError(w, err)
		return
	}
	if existing, found, err := h.findExistingImport(r.Context(), owner, repo); err != nil {
		writeAnyError(w, err)
		return
	} else if found {
		writeJSON(w, http.StatusOK, map[string]string{
			"status":         "exists",
			"workspace_id":   uuidString(existing.ID),
			"name":           existing.Name,
			"slug":           existing.Slug,
			"repo_url":       req.RepoURL,
			"default_branch": req.DefaultBranch,
		})
		return
	}

	// Validate the GitHub source before creating any DB placeholder so missing
	// repos or invalid workspace repos do not leave orphaned workspace rows.
	if _, err := h.github.ImportWorkspace(r.Context(), domain.ImportInput{
		RepoURL:       req.RepoURL,
		DefaultBranch: req.DefaultBranch,
		Token:         h.token,
	}); err != nil {
		writeAnyError(w, err)
		return
	}

	workspaceID := uuid.NewString()
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = owner + "/" + repo
	}
	slug := slugify(name)
	if slug == "" {
		slug = workspaceID
	}

	workspaceID, err = h.createImportPlaceholder(r.Context(), workspaceID, name, slug, req.RepoURL, req.DefaultBranch)
	if err != nil {
		if existing, found, findErr := h.findExistingImport(r.Context(), owner, repo); findErr != nil {
			writeAnyError(w, err)
			return
		} else if found {
			writeJSON(w, http.StatusOK, map[string]string{
				"status":         "exists",
				"workspace_id":   uuidString(existing.ID),
				"name":           existing.Name,
				"slug":           existing.Slug,
				"repo_url":       req.RepoURL,
				"default_branch": req.DefaultBranch,
			})
			return
		}
		writeAnyError(w, err)
		return
	}
	run, err := h.insertRunningRun(r.Context(), workspaceID, "api_import", "full", req.DefaultBranch)
	if err != nil {
		writeAnyError(w, err)
		return
	}
	syncRunID := uuidString(run.ID)

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
		writeAnyError(w, err)
		return
	}
	info, err := h.queue.Enqueue(task, asynq.TaskID(workspaceSyncTaskID(payload)))
	if err != nil {
		if failErr := h.markRunFailed(r.Context(), run.ID, "ENQUEUE_FAILED", err.Error()); failErr != nil {
			log.Printf("mark import enqueue failed run failed workspace_id=%s run_id=%s: %v", workspaceID, syncRunID, failErr)
		}
		if isDedupeError(err) {
			writeJSON(w, http.StatusAccepted, map[string]string{
				"status":       "already_queued",
				"workspace_id": workspaceID,
				"sync_run_id":  syncRunID,
				"type":         queue.TypeWorkspaceSync,
			})
			return
		}
		writeAnyError(w, fmt.Errorf("enqueue task: %w", err))
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{
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

func (h *serviceHandler) internalWorkspaceHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	workspaceID, ok := workspaceIDFromSyncPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	uid, err := pgUUID(workspaceID)
	if err != nil {
		writeAnyError(w, err)
		return
	}
	src, err := h.q.GetGitHubSource(r.Context(), uid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeSourceError(w, domain.NewDatabaseError(domain.ErrDatabaseNotFound, "github source not found for workspace"))
			return
		}
		writeAnyError(w, err)
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
		writeAnyError(w, err)
		return
	}
	info, err := h.queue.Enqueue(task, asynq.TaskID(workspaceSyncTaskID(payload)))
	if err != nil {
		if isDedupeError(err) {
			writeJSON(w, http.StatusAccepted, map[string]string{
				"status": "already_queued",
				"type":   queue.TypeWorkspaceSync,
			})
			return
		}
		writeAnyError(w, fmt.Errorf("enqueue task: %w", err))
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{
		"task_id": info.ID,
		"queue":   info.Queue,
		"type":    info.Type,
	})
}

// webhookHandler processes GitHub push event webhooks.
// It verifies the HMAC signature, parses the push payload, and routes based on branch:
//   - base branch → enqueue targeted sync for touched features
//   - feature branch → enqueue targeted workspace:sync for that feature
//   - task branch → enqueue task:sync with dedup
//   - other → 200 OK, ignored
func (h *serviceHandler) webhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := webhook.ReadAndVerify(r, h.webhookSecret)
	if err != nil {
		http.Error(w, "signature verification failed: "+err.Error(), http.StatusUnauthorized)
		return
	}

	// Only handle push events; ignore other event types gracefully.
	eventType := r.Header.Get("X-GitHub-Event")
	if eventType != "push" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "event": eventType})
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
		log.Printf("webhook: repo not registered: %s", repoURL)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "repo not registered"})
		return
	}

	info := webhook.ClassifyBranch(branch, wsInfo.defaultBranch, wsInfo.branchPattern)
	switch info.Kind {
	case webhook.BranchIgnored:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "branch": branch})
		return

	case webhook.BranchBase:
		payloads := basePushTargetedSyncPayloads(wsInfo, branch, ev)
		if len(payloads) == 0 {
			writeJSON(w, http.StatusOK, map[string]string{
				"status":      "ignored",
				"branch_kind": "base",
				"reason":      "no feature artifact paths",
			})
			return
		}
		if err := h.enqueueWorkspaceSyncs(payloads); err != nil {
			log.Printf("webhook: enqueue base targeted sync failed workspace=%s branch=%s: %v",
				wsInfo.workspaceID, branch, err)
			http.Error(w, "enqueue targeted sync failed", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"status":      "queued",
			"branch_kind": "base",
			"mode":        "targeted",
		})

	case webhook.BranchFeature:
		if err := h.enqueueTargetedSync(wsInfo, info.FeatureID, branch, "webhook_feature"); err != nil {
			log.Printf("webhook: enqueue feature targeted sync failed workspace=%s feature=%s: %v",
				wsInfo.workspaceID, info.FeatureID, err)
			http.Error(w, "enqueue targeted sync failed", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"status":      "queued",
			"branch_kind": "feature",
			"feature_id":  info.FeatureID,
		})

	case webhook.BranchTask:
		if err := h.enqueueTaskSync(wsInfo.workspaceID, info.FeatureID, info.TaskID); err != nil {
			log.Printf("webhook: enqueue task sync failed workspace=%s feature=%s task=%s: %v",
				wsInfo.workspaceID, info.FeatureID, info.TaskID, err)
			http.Error(w, "enqueue task sync failed", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
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

// workspaceWebhookInfo holds the minimal workspace data needed by webhook routing.
type workspaceWebhookInfo struct {
	workspaceID   string
	repoURL       string
	defaultBranch string
	branchPattern string
}

// findWorkspaceByRepoURL queries the DB for a workspace matching the given repo URL.
func (h *serviceHandler) findWorkspaceByRepoURL(ctx context.Context, repoURL string) (*workspaceWebhookInfo, error) {
	owner, repo, err := parseGitHubRepo(repoURL)
	if err != nil {
		return nil, err
	}
	src, dbError := h.q.GetGitHubSourceByRepo(ctx, database.GetGitHubSourceByRepoParams{
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
	ws, err := h.q.GetWorkspace(ctx, src.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("get webhook workspace: %w", err)
	}
	branchPattern := ""
	if ws.BranchPattern != nil {
		branchPattern = *ws.BranchPattern
	}
	return &workspaceWebhookInfo{
		workspaceID:   uuidString(src.WorkspaceID),
		repoURL:       src.RepoURL,
		defaultBranch: defaultBranch,
		branchPattern: branchPattern,
	}, nil
}

// enqueueTargetedSync enqueues a workspace:sync task with mode=targeted for a single feature.
func (h *serviceHandler) enqueueTargetedSync(ws *workspaceWebhookInfo, featureID, branch, trigger string) error {
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

func (h *serviceHandler) enqueueWorkspaceSyncs(payloads []queue.WorkspaceSyncPayload) error {
	for _, payload := range payloads {
		if err := h.enqueueWorkspaceSync(payload); err != nil {
			return err
		}
	}
	return nil
}

func (h *serviceHandler) enqueueWorkspaceSync(payload queue.WorkspaceSyncPayload) error {
	task, err := queue.NewWorkspaceSyncTask(payload)
	if err != nil {
		return fmt.Errorf("build workspace sync task: %w", err)
	}
	if _, err := h.queue.Enqueue(task, asynq.TaskID(workspaceSyncTaskID(payload))); err != nil {
		if isDedupeError(err) {
			return nil
		}
		return fmt.Errorf("enqueue workspace sync: %w", err)
	}
	return nil
}

// enqueueTaskSync enqueues a task:sync job with deduplication.
func (h *serviceHandler) enqueueTaskSync(workspaceID, featureID, taskID string) error {
	payload := queue.TaskSyncPayload{
		WorkspaceID: workspaceID,
		FeatureID:   featureID,
		TaskID:      taskID,
	}
	task, err := queue.NewTaskSyncTask(payload)
	if err != nil {
		return fmt.Errorf("build task:sync task: %w", err)
	}
	info, err := h.queue.Enqueue(task)
	if err != nil {
		// ErrTaskIDConflict means duplicate — already queued, this is expected with Unique(24h).
		if isDedupeError(err) {
			log.Printf("task:sync already queued (dedup) workspace=%s feature=%s task=%s", workspaceID, featureID, taskID)
			return nil
		}
		return fmt.Errorf("enqueue task:sync: %w", err)
	}
	log.Printf("task:sync enqueued id=%s workspace=%s feature=%s task=%s", info.ID, workspaceID, featureID, taskID)
	return nil
}

// isDedupeError returns true when the asynq error indicates a duplicate task (Unique constraint).
func isDedupeError(err error) bool {
	return errors.Is(err, asynq.ErrDuplicateTask) || errors.Is(err, asynq.ErrTaskIDConflict) ||
		(err != nil && strings.Contains(err.Error(), "task already exists"))
}

func (h *serviceHandler) findExistingImport(ctx context.Context, owner, repo string) (database.Workspace, bool, error) {
	src, err := h.q.GetGitHubSourceByRepo(ctx, database.GetGitHubSourceByRepoParams{
		RepoOwner: owner,
		RepoName:  repo,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return database.Workspace{}, false, nil
		}
		return database.Workspace{}, false, fmt.Errorf("get github source by repo: %w", err)
	}
	ws, err := h.q.GetWorkspace(ctx, src.WorkspaceID)
	if err != nil {
		return database.Workspace{}, false, fmt.Errorf("get existing imported workspace: %w", err)
	}
	return ws, true, nil
}

func (h *serviceHandler) createImportPlaceholder(ctx context.Context, workspaceID, name, slug, repoURL, defaultBranch string) (string, error) {
	uid, err := pgUUID(workspaceID)
	if err != nil {
		return "", err
	}
	if h.pool == nil {
		return h.createImportPlaceholderWithQueries(ctx, h.q, uid, name, slug, repoURL, defaultBranch)
	}

	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", fmt.Errorf("begin import placeholder transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	actualWorkspaceID, err := h.createImportPlaceholderWithQueries(ctx, h.q.WithTx(tx), uid, name, slug, repoURL, defaultBranch)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit import placeholder transaction: %w", err)
	}
	return actualWorkspaceID, nil
}

func (h *serviceHandler) createImportPlaceholderWithQueries(ctx context.Context, q *database.Queries, uid pgtype.UUID, name, slug, repoURL, defaultBranch string) (string, error) {
	ws, err := q.UpsertWorkspaceByID(ctx, database.UpsertWorkspaceByIDParams{
		ID:               uid,
		Slug:             slug,
		Name:             name,
		ManagementRepoID: "management-repo",
	})
	if err != nil {
		return "", fmt.Errorf("upsert import placeholder workspace: %w", err)
	}
	actualWorkspaceID := uuidString(ws.ID)
	if err := h.upsertGitHubSourceWithQueries(ctx, q, actualWorkspaceID, repoURL, defaultBranch); err != nil {
		return "", err
	}
	return actualWorkspaceID, nil
}

func (h *serviceHandler) upsertGitHubSource(ctx context.Context, workspaceID, repoURL, defaultBranch string) error {
	return h.upsertGitHubSourceWithQueries(ctx, h.q, workspaceID, repoURL, defaultBranch)
}

func (h *serviceHandler) upsertGitHubSourceWithQueries(ctx context.Context, q *database.Queries, workspaceID, repoURL, defaultBranch string) error {
	uid, err := pgUUID(workspaceID)
	if err != nil {
		return err
	}
	owner, repo, err := parseGitHubRepo(repoURL)
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

func (h *serviceHandler) insertRunningRun(ctx context.Context, workspaceID, trigger, mode, branch string) (database.WorkspaceSyncRun, error) {
	uid, err := pgUUID(workspaceID)
	if err != nil {
		return database.WorkspaceSyncRun{}, err
	}
	branchPtr := branch
	row, err := h.q.InsertSyncRun(ctx, database.InsertSyncRunParams{
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

func (h *serviceHandler) markRunFailed(ctx context.Context, runID pgtype.UUID, code, message string) error {
	_, err := h.q.UpdateSyncRunFailed(ctx, database.UpdateSyncRunFailedParams{
		ID:           runID,
		ErrorCode:    &code,
		ErrorMessage: &message,
	})
	if err != nil {
		return fmt.Errorf("update sync run failed: %w", err)
	}
	return nil
}

func workspaceSyncTaskID(payload queue.WorkspaceSyncPayload) string {
	ref := payload.Ref
	if ref == "" {
		ref = payload.DefaultBranch
	}
	mode := payload.Mode
	if mode == "" {
		mode = "full"
	}
	raw := payload.WorkspaceID + "-" + payload.RepoURL + "-" + ref + "-" + mode + "-" + payload.FeatureID
	id := slugify(raw)
	if id == "" {
		id = payload.WorkspaceID
	}
	return "workspace-sync-" + id
}

func uuidString(uid pgtype.UUID) string {
	return uuid.UUID(uid.Bytes).String()
}

func slugify(name string) string {
	lower := strings.ToLower(name)
	re := regexp.MustCompile(`[^a-z0-9]+`)
	slug := re.ReplaceAllString(lower, "-")
	return strings.Trim(slug, "-")
}

func workspaceIDFromSyncPath(path string) (string, bool) {
	const prefix = "/internal/workspaces/"
	const suffix = "/sync"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	workspaceID := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	workspaceID = strings.Trim(workspaceID, "/")
	return workspaceID, workspaceID != ""
}

func pgUUID(raw string) (pgtype.UUID, error) {
	var uid pgtype.UUID
	if err := uid.Scan(raw); err != nil {
		return pgtype.UUID{}, domain.NewValidationError(domain.ErrValidationMissingInput, "invalid workspace_id: "+raw)
	}
	return uid, nil
}

func parseGitHubRepo(raw string) (string, string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", "", domain.NewValidationError(domain.ErrValidationInvalidURL, "invalid GitHub repo URL: "+raw)
	}
	if !strings.EqualFold(u.Host, "github.com") {
		return "", "", domain.NewValidationError(domain.ErrValidationInvalidURL, "unsupported GitHub host: "+u.Host)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", domain.NewValidationError(domain.ErrValidationInvalidURL, "invalid GitHub repo URL path: "+raw)
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git"), nil
}

func writeAnyError(w http.ResponseWriter, err error) {
	var se domain.SourceError
	if errors.As(err, &se) {
		writeSourceError(w, se)
		return
	}
	writeSourceError(w, domain.NewDatabaseError(domain.ErrDatabaseQuery, err.Error()))
}

func writeSourceError(w http.ResponseWriter, se domain.SourceError) {
	status := http.StatusInternalServerError
	switch se.Source {
	case domain.ErrorSourceValidation:
		status = http.StatusBadRequest
	case domain.ErrorSourceGitHub:
		switch se.Code {
		case domain.ErrGitHubNotFound:
			status = http.StatusNotFound
		case domain.ErrGitHubUnauthorized:
			status = http.StatusUnauthorized
		case domain.ErrGitHubRateLimit:
			status = http.StatusTooManyRequests
		default:
			status = http.StatusBadGateway
		}
	case domain.ErrorSourceDatabase:
		if se.Code == domain.ErrDatabaseNotFound {
			status = http.StatusNotFound
		}
	}
	writeJSON(w, status, domain.FromSourceError(se, nil))
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
