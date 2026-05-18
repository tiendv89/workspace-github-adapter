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
)

type serviceHandler struct {
	db     domain.DbWorkspaceAdapter
	q      *database.Queries
	github domain.GitHubWorkspaceAdapter
	token  string
	queue  *asynq.Client
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
		db:     dbadapter.New(pool),
		q:      database.New(pool),
		github: ghadapter.New(cfg.GitHubToken),
		token:  cfg.GitHubToken,
		queue:  client,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/internal/workspaces/import", h.importWorkspaceHandler)
	mux.HandleFunc("/internal/workspaces/", h.internalWorkspaceHandler)

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

	workspaceID := uuid.NewString()
	name := strings.TrimSpace(req.Name)
	if name == "" {
		owner, repo, err := parseGitHubRepo(req.RepoURL)
		if err != nil {
			writeAnyError(w, err)
			return
		}
		name = owner + "/" + repo
	}
	slug := slugify(name)
	if slug == "" {
		slug = workspaceID
	}

	workspaceID, err := h.createImportPlaceholder(r.Context(), workspaceID, name, slug, req.RepoURL, req.DefaultBranch)
	if err != nil {
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
	info, err := h.queue.Enqueue(task)
	if err != nil {
		if failErr := h.markRunFailed(r.Context(), run.ID, "ENQUEUE_FAILED", err.Error()); failErr != nil {
			log.Printf("mark import enqueue failed run failed workspace_id=%s run_id=%s: %v", workspaceID, syncRunID, failErr)
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
	info, err := h.queue.Enqueue(task)
	if err != nil {
		writeAnyError(w, fmt.Errorf("enqueue task: %w", err))
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{
		"task_id": info.ID,
		"queue":   info.Queue,
		"type":    info.Type,
	})
}

func (h *serviceHandler) createImportPlaceholder(ctx context.Context, workspaceID, name, slug, repoURL, defaultBranch string) (string, error) {
	uid, err := pgUUID(workspaceID)
	if err != nil {
		return "", err
	}
	ws, err := h.q.UpsertWorkspace(ctx, database.UpsertWorkspaceParams{
		ID:               uid,
		Slug:             slug,
		Name:             name,
		ManagementRepoID: "management-repo",
	})
	if err != nil {
		return "", fmt.Errorf("upsert import placeholder workspace: %w", err)
	}
	actualWorkspaceID := uuidString(ws.ID)
	if err := h.upsertGitHubSource(ctx, actualWorkspaceID, repoURL, defaultBranch); err != nil {
		return "", err
	}
	return actualWorkspaceID, nil
}

func (h *serviceHandler) upsertGitHubSource(ctx context.Context, workspaceID, repoURL, defaultBranch string) error {
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
