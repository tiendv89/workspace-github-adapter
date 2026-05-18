// Package db implements DbWorkspaceAdapter backed by PostgreSQL + pgx.
package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tiendv89/workspace-github-adapter/internal/database"
	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

var _ domain.DbWorkspaceAdapter = (*Adapter)(nil)

// Adapter implements domain.DbWorkspaceAdapter.
type Adapter struct {
	pool *pgxpool.Pool
	q    *database.Queries
}

// New creates a new Adapter from an existing pgxpool.Pool.
func New(pool *pgxpool.Pool) *Adapter {
	return &Adapter{pool: pool, q: database.New(pool)}
}

// Connect creates a pgxpool.Pool from the given DATABASE_URL and returns a new Adapter.
func Connect(ctx context.Context, databaseURL string) (*Adapter, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.New: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return New(pool), nil
}

// Close releases the connection pool.
func (a *Adapter) Close() { a.pool.Close() }

// ListWorkspaces returns summary rows for all saved workspaces.
func (a *Adapter) ListWorkspaces(ctx context.Context) ([]domain.WorkspaceSummary, error) {
	rows, err := a.q.ListWorkspaces(ctx)
	if err != nil {
		return nil, dbErr("list workspaces", err)
	}

	// Batch-load the latest sync run per workspace to avoid N+1 queries.
	allRuns, _ := a.q.ListLatestSyncRunsPerWorkspace(ctx) //nolint:errcheck
	runMap := make(map[string]database.WorkspaceSyncRun, len(allRuns))
	for _, run := range allRuns {
		runMap[uuidStr(run.WorkspaceID)] = run
	}

	out := make([]domain.WorkspaceSummary, 0, len(rows))
	for _, r := range rows {
		run, ok := runMap[uuidStr(r.ID)]
		var ss domain.SourceState
		if ok {
			ss = syncRunToSourceState(&run, nil)
		} else {
			ss = syncRunToSourceState(nil, nil)
		}

		out = append(out, domain.WorkspaceSummary{
			ID:          uuidStr(r.ID),
			Name:        r.Name,
			Slug:        r.Slug,
			RepoURL:     githubRepoURL(ctx, a.q, r.ID),
			SourceState: ss,
			UpdatedAt:   r.UpdatedAt.Time,
		})
	}
	return out, nil
}

// GetWorkspace returns the full workspace detail.
func (a *Adapter) GetWorkspace(ctx context.Context, workspaceID string) (*domain.WorkspaceDetail, error) {
	uid, err := parseUUID(workspaceID)
	if err != nil {
		return nil, err
	}

	ws, err := a.q.GetWorkspace(ctx, uid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.NewDatabaseError(domain.ErrDatabaseNotFound, "workspace not found: "+workspaceID)
		}
		return nil, dbErr("get workspace", err)
	}

	features, err := a.q.ListWorkspaceFeatures(ctx, uid)
	if err != nil {
		return nil, dbErr("list features", err)
	}

	tasks, err := a.q.ListWorkspaceTasks(ctx, uid)
	if err != nil {
		return nil, dbErr("list tasks", err)
	}

	latestRun, _ := a.q.GetLatestSyncRun(ctx, uid) //nolint:errcheck
	ss := syncRunToSourceState(&latestRun, nil)

	featureSummaries := make([]domain.FeatureSummary, 0, len(features))
	for _, f := range features {
		featureSummaries = append(featureSummaries, rowToFeatureSummary(f, tasks))
	}

	taskSummaries := make([]domain.TaskSummary, 0, len(tasks))
	for _, t := range tasks {
		taskSummaries = append(taskSummaries, rowToTaskSummary(t))
	}

	return &domain.WorkspaceDetail{
		WorkspaceSummary: domain.WorkspaceSummary{
			ID:          workspaceID,
			Name:        ws.Name,
			Slug:        ws.Slug,
			RepoURL:     githubRepoURL(ctx, a.q, uid),
			SourceState: ss,
			UpdatedAt:   ws.UpdatedAt.Time,
		},
		Features: featureSummaries,
		Tasks:    taskSummaries,
	}, nil
}

// GetFeature returns the full feature detail.
func (a *Adapter) GetFeature(ctx context.Context, workspaceID, featureID string) (*domain.FeatureDetail, error) {
	uid, err := parseUUID(workspaceID)
	if err != nil {
		return nil, err
	}

	f, err := a.q.GetWorkspaceFeature(ctx, database.GetWorkspaceFeatureParams{
		WorkspaceID: uid,
		FeatureID:   featureID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.NewDatabaseError(domain.ErrDatabaseNotFound, "feature not found: "+featureID)
		}
		return nil, dbErr("get feature", err)
	}

	docs, err := a.q.ListFeatureDocuments(ctx, database.ListFeatureDocumentsParams{
		WorkspaceID: uid,
		FeatureID:   featureID,
	})
	if err != nil {
		return nil, dbErr("list feature documents", err)
	}

	tasks, err := a.q.ListFeatureTasks(ctx, database.ListFeatureTasksParams{
		WorkspaceID: uid,
		FeatureID:   featureID,
	})
	if err != nil {
		return nil, dbErr("list feature tasks", err)
	}

	actEvents, err := a.q.ListFeatureActivityEvents(ctx, database.ListFeatureActivityEventsParams{
		WorkspaceID: uid,
		FeatureID:   featureID,
	})
	if err != nil {
		return nil, dbErr("list feature activity", err)
	}

	latestRun, _ := a.q.GetLatestSyncRun(ctx, uid) //nolint:errcheck
	ss := syncRunToSourceState(&latestRun, nil)

	summary := rowToFeatureSummary(f, tasks)

	docLinks := make([]domain.DocumentLink, 0, len(docs))
	for _, d := range docs {
		dl := domain.DocumentLink{
			DocumentType: d.DocumentType,
			SourcePath:   d.SourcePath,
		}
		if d.URL != nil {
			dl.URL = *d.URL
		}
		docLinks = append(docLinks, dl)
	}

	taskSummaries := make([]domain.TaskSummary, 0, len(tasks))
	for _, t := range tasks {
		taskSummaries = append(taskSummaries, rowToTaskSummary(t))
	}

	activity := make([]domain.ActivityEvent, 0, len(actEvents))
	for _, e := range actEvents {
		activity = append(activity, rowToActivityEvent(e))
	}

	return &domain.FeatureDetail{
		FeatureSummary: summary,
		WorkspaceID:    workspaceID,
		Documents:      docLinks,
		Tasks:          taskSummaries,
		Activity:       activity,
		SourceState:    ss,
	}, nil
}

// GetTask returns the full task detail.
func (a *Adapter) GetTask(ctx context.Context, workspaceID, featureID, taskID string) (*domain.TaskDetail, error) {
	uid, err := parseUUID(workspaceID)
	if err != nil {
		return nil, err
	}

	t, err := a.q.GetWorkspaceTask(ctx, database.GetWorkspaceTaskParams{
		WorkspaceID: uid,
		FeatureID:   featureID,
		TaskID:      taskID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.NewDatabaseError(domain.ErrDatabaseNotFound, "task not found: "+taskID)
		}
		return nil, dbErr("get task", err)
	}

	actEvents, _ := a.q.ListTaskActivityEvents(ctx, database.ListTaskActivityEventsParams{ //nolint:errcheck
		WorkspaceID: uid,
		FeatureID:   featureID,
		TaskID:      taskID,
	})

	activity := make([]domain.ActivityEvent, 0, len(actEvents))
	for _, e := range actEvents {
		activity = append(activity, rowToActivityEvent(e))
	}

	return &domain.TaskDetail{
		TaskSummary: rowToTaskSummary(t),
		WorkspaceID: workspaceID,
		DependsOn:   unmarshalStringSlice(t.DependsOn),
		Execution:   unmarshalExecution(t.Execution),
		Activity:    activity,
	}, nil
}

// ListFeatureTasks returns task summaries for all tasks in the given feature.
func (a *Adapter) ListFeatureTasks(ctx context.Context, workspaceID, featureID string) ([]domain.TaskSummary, error) {
	uid, err := parseUUID(workspaceID)
	if err != nil {
		return nil, err
	}

	tasks, err := a.q.ListFeatureTasks(ctx, database.ListFeatureTasksParams{
		WorkspaceID: uid,
		FeatureID:   featureID,
	})
	if err != nil {
		return nil, dbErr("list feature tasks", err)
	}

	out := make([]domain.TaskSummary, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, rowToTaskSummary(t))
	}
	return out, nil
}

// ListActivity returns activity events filtered by the given scope.
func (a *Adapter) ListActivity(ctx context.Context, workspaceID string, scope domain.ActivityScope) ([]domain.ActivityEvent, error) {
	uid, err := parseUUID(workspaceID)
	if err != nil {
		return nil, err
	}

	var rows []database.WorkspaceActivityEvent
	var queryErr error

	switch {
	case scope.FeatureID != "" && scope.TaskID != "":
		rows, queryErr = a.q.ListTaskActivityEvents(ctx, database.ListTaskActivityEventsParams{
			WorkspaceID: uid,
			FeatureID:   scope.FeatureID,
			TaskID:      scope.TaskID,
		})
	case scope.FeatureID != "":
		rows, queryErr = a.q.ListFeatureActivityEvents(ctx, database.ListFeatureActivityEventsParams{
			WorkspaceID: uid,
			FeatureID:   scope.FeatureID,
		})
	default:
		rows, queryErr = a.q.ListActivityEvents(ctx, uid)
	}

	if queryErr != nil {
		return nil, dbErr("list activity", queryErr)
	}

	out := make([]domain.ActivityEvent, 0, len(rows))
	for _, r := range rows {
		out = append(out, rowToActivityEvent(r))
	}
	return out, nil
}

// SaveSnapshot upserts all core tables from the given snapshot inside a single transaction.
func (a *Adapter) SaveSnapshot(ctx context.Context, workspaceID string, snapshot *domain.WorkspaceSnapshot) error {
	uid, err := parseUUID(workspaceID)
	if err != nil {
		return err
	}

	tx, err := a.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := upsertSnapshot(ctx, a.q.WithTx(tx), uid, snapshot); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// GetActiveSnapshot returns the latest WorkspaceSnapshot reconstructed from core tables.
func (a *Adapter) GetActiveSnapshot(ctx context.Context, workspaceID string) (*domain.WorkspaceSnapshot, error) {
	uid, err := parseUUID(workspaceID)
	if err != nil {
		return nil, err
	}

	ws, err := a.q.GetWorkspace(ctx, uid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, dbErr("get workspace", err)
	}

	features, err := a.q.ListWorkspaceFeatures(ctx, uid)
	if err != nil {
		return nil, dbErr("list features for snapshot", err)
	}

	repos, err := a.q.ListWorkspaceRepos(ctx, uid)
	if err != nil {
		return nil, dbErr("list repos for snapshot", err)
	}

	src, _ := a.q.GetGitHubSource(ctx, uid) //nolint:errcheck

	// Pre-fetch all docs and tasks in two queries to avoid N+1 per feature.
	allDocs, err := a.q.ListWorkspaceFeatureDocuments(ctx, uid)
	if err != nil {
		return nil, dbErr("list docs for snapshot", err)
	}
	allTasks, err := a.q.ListWorkspaceTasks(ctx, uid)
	if err != nil {
		return nil, dbErr("list tasks for snapshot", err)
	}

	// Group by feature_name (the legacy slug) for O(1) lookup inside buildFeatureSnapshotFromBatch.
	docsByFeature := make(map[string][]database.WorkspaceFeatureDocument, len(features))
	for _, d := range allDocs {
		docsByFeature[d.FeatureName] = append(docsByFeature[d.FeatureName], d)
	}
	tasksByFeature := make(map[string][]database.WorkspaceTask, len(features))
	for _, t := range allTasks {
		tasksByFeature[t.FeatureName] = append(tasksByFeature[t.FeatureName], t)
	}

	featureSnapshots := make([]domain.FeatureSnapshot, 0, len(features))
	for _, f := range features {
		fs := buildFeatureSnapshotFromBatch(f, docsByFeature[f.FeatureID], tasksByFeature[f.FeatureID])
		featureSnapshots = append(featureSnapshots, fs)
	}

	repoEntries := make([]domain.RepoEntry, 0, len(repos))
	for _, r := range repos {
		re := domain.RepoEntry{RepoID: r.RepoID}
		if r.BaseBranch != nil {
			re.BaseBranch = *r.BaseBranch
		}
		repoEntries = append(repoEntries, re)
	}

	repoURL := ""
	if src.RepoURL != "" {
		repoURL = src.RepoURL
	}

	return &domain.WorkspaceSnapshot{
		WorkspaceID:      workspaceID,
		Name:             ws.Name,
		Slug:             ws.Slug,
		RepoURL:          repoURL,
		ManagementRepoID: ws.ManagementRepoID,
		Features:         featureSnapshots,
		Repos:            repoEntries,
	}, nil
}

// GetLatestSyncRun returns the most recent sync run for staleness derivation.
func (a *Adapter) GetLatestSyncRun(ctx context.Context, workspaceID string) (*domain.SyncRun, error) {
	uid, err := parseUUID(workspaceID)
	if err != nil {
		return nil, err
	}

	row, err := a.q.GetLatestSyncRun(ctx, uid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, dbErr("get latest sync run", err)
	}
	return syncRunRowToDomain(row), nil
}

// upsertSnapshot is the transactional core of SaveSnapshot.
func upsertSnapshot(ctx context.Context, q *database.Queries, uid pgtype.UUID, snap *domain.WorkspaceSnapshot) error {
	mgmtRepoID := snap.ManagementRepoID
	if mgmtRepoID == "" {
		mgmtRepoID = "management-repo"
	}
	// Upsert workspace record (slug is the natural key).
	_, err := q.UpsertWorkspace(ctx, database.UpsertWorkspaceParams{
		ID:               uid,
		Slug:             snap.Slug,
		Name:             snap.Name,
		ManagementRepoID: mgmtRepoID,
	})
	if err != nil {
		return fmt.Errorf("upsert workspace: %w", err)
	}

	// Upsert repos.
	repoIDs := make([]string, 0, len(snap.Repos))
	for _, r := range snap.Repos {
		bp := ptrStr(r.BaseBranch)
		_, err := q.UpsertWorkspaceRepo(ctx, database.UpsertWorkspaceRepoParams{
			WorkspaceID: uid,
			RepoID:      r.RepoID,
			BaseBranch:  bp,
		})
		if err != nil {
			return fmt.Errorf("upsert repo %s: %w", r.RepoID, err)
		}
		repoIDs = append(repoIDs, r.RepoID)
	}
	if err := q.DeleteWorkspaceReposNotIn(ctx, database.DeleteWorkspaceReposNotInParams{
		WorkspaceID: uid,
		RepoIds:     repoIDs,
	}); err != nil {
		return fmt.Errorf("delete stale repos: %w", err)
	}

	// Upsert features, docs, tasks, activity.
	featureIDs := make([]string, 0, len(snap.Features))
	for _, f := range snap.Features {
		if err := upsertFeatureSnapshot(ctx, q, uid, f); err != nil {
			return err
		}
		featureIDs = append(featureIDs, f.FeatureID)
	}
	if err := q.DeleteWorkspaceFeaturesNotIn(ctx, database.DeleteWorkspaceFeaturesNotInParams{
		WorkspaceID: uid,
		FeatureIds:  featureIDs,
	}); err != nil {
		return fmt.Errorf("delete stale features: %w", err)
	}

	return nil
}

func upsertFeatureSnapshot(ctx context.Context, q *database.Queries, uid pgtype.UUID, f domain.FeatureSnapshot) error {
	stages, _ := json.Marshal(nil) //nolint:errcheck
	featureRow, err := q.UpsertWorkspaceFeature(ctx, database.UpsertWorkspaceFeatureParams{
		WorkspaceID:   uid,
		FeatureID:     f.FeatureID,
		Title:         f.Title,
		FeatureStatus: ptrStr(f.Status),
		CurrentStage:  ptrStr(f.CurrentStage),
		NextAction:    ptrStr(f.NextAction),
		Stages:        stages,
		SourcePath:    f.SourcePath,
		SourceHash:    ptrStr(f.SourceHash),
	})
	if err != nil {
		return fmt.Errorf("upsert feature %s: %w", f.FeatureID, err)
	}

	// Upsert documents.
	docTypes := make([]string, 0, len(f.Documents))
	for _, d := range f.Documents {
		_, err := q.UpsertFeatureDocument(ctx, database.UpsertFeatureDocumentParams{
			WorkspaceID:  uid,
			FeatureID:    featureRow.ID,
			FeatureName:  f.FeatureID,
			DocumentType: d.DocumentType,
			SourcePath:   d.SourcePath,
			URL:          ptrStr(d.URL),
		})
		if err != nil {
			return fmt.Errorf("upsert document %s/%s: %w", f.FeatureID, d.DocumentType, err)
		}
		docTypes = append(docTypes, d.DocumentType)
	}
	if err := q.DeleteFeatureDocumentsNotIn(ctx, database.DeleteFeatureDocumentsNotInParams{
		WorkspaceID:   uid,
		FeatureID:     uuidStr(featureRow.ID),
		DocumentTypes: docTypes,
	}); err != nil {
		return fmt.Errorf("delete stale documents for %s: %w", f.FeatureID, err)
	}

	// Upsert tasks.
	taskIDs := make([]string, 0, len(f.Tasks))
	for _, t := range f.Tasks {
		if err := upsertTaskSnapshot(ctx, q, uid, featureRow.ID, f.FeatureID, t); err != nil {
			return err
		}
		taskIDs = append(taskIDs, t.TaskID)
	}
	if err := q.DeleteFeatureTasksNotIn(ctx, database.DeleteFeatureTasksNotInParams{
		WorkspaceID: uid,
		FeatureID:   uuidStr(featureRow.ID),
		TaskIds:     taskIDs,
	}); err != nil {
		return fmt.Errorf("delete stale tasks for %s: %w", f.FeatureID, err)
	}

	// Upsert feature-level activity (task_id = nil).
	if err := upsertFeatureActivity(ctx, q, uid, featureRow.ID, f); err != nil {
		return err
	}

	return nil
}

func upsertTaskSnapshot(ctx context.Context, q *database.Queries, uid pgtype.UUID, featureUUID pgtype.UUID, featureName string, t domain.TaskSnapshot) error {
	dependsOn, _ := json.Marshal(t.DependsOn) //nolint:errcheck
	execution, _ := json.Marshal(t.Execution) //nolint:errcheck
	pr, _ := json.Marshal(t.PR)               //nolint:errcheck
	wsPr, _ := json.Marshal(t.WorkspacePR)    //nolint:errcheck

	taskRow, err := q.UpsertWorkspaceTask(ctx, database.UpsertWorkspaceTaskParams{
		WorkspaceID:   uid,
		FeatureID:     featureUUID,
		FeatureName:   featureName,
		TaskID:        t.TaskID,
		Title:         t.Title,
		Repo:          ptrStr(t.Repo),
		Status:        ptrStr(t.Status),
		DependsOn:     dependsOn,
		BlockedReason: ptrStr(t.BlockedReason),
		Branch:        ptrStr(t.Branch),
		Execution:     execution,
		Pr:            pr,
		WorkspacePr:   wsPr,
		SourcePath:    t.SourcePath,
		SourceHash:    ptrStr(t.SourceHash),
	})
	if err != nil {
		return fmt.Errorf("upsert task %s/%s: %w", featureName, t.TaskID, err)
	}

	// Upsert task-level activity from the task log.
	if err := upsertTaskActivity(ctx, q, uid, featureUUID, featureName, taskRow.ID, t); err != nil {
		return err
	}

	return nil
}

// upsertFeatureActivity normalizes feature-level activity events (scope_type=feature, task_id=NULL).
// Uses the partial unique index on (workspace_id, feature_id, sequence) WHERE task_id IS NULL.
func upsertFeatureActivity(ctx context.Context, q *database.Queries, uid pgtype.UUID, featureUUID pgtype.UUID, f domain.FeatureSnapshot) error {
	for i, evt := range f.Activity {
		raw, _ := json.Marshal(evt) //nolint:errcheck
		_, err := q.UpsertFeatureActivityEvent(ctx, database.UpsertFeatureActivityEventParams{
			WorkspaceID: uid,
			ScopeType:   "feature",
			FeatureID:   featureUUID,
			FeatureName: f.FeatureID,
			Action:      ptrStr(evt.Action),
			Actor:       ptrStr(evt.Actor),
			OccurredAt:  ptrStr(evt.OccurredAt.Format(time.RFC3339)),
			Note:        ptrStr(evt.Note),
			Sequence:    int32(i),
			RawEvent:    raw,
		})
		if err != nil {
			return fmt.Errorf("upsert feature activity %s[%d]: %w", f.FeatureID, i, err)
		}
	}
	return nil
}

// upsertTaskActivity normalizes task log entries (scope_type=task).
// Uses the partial unique index on (workspace_id, feature_id, task_id, sequence) WHERE task_id IS NOT NULL.
func upsertTaskActivity(ctx context.Context, q *database.Queries, uid pgtype.UUID, featureUUID pgtype.UUID, featureName string, taskUUID pgtype.UUID, t domain.TaskSnapshot) error {
	for i, evt := range t.Activity {
		raw, _ := json.Marshal(evt) //nolint:errcheck
		_, err := q.UpsertTaskActivityEvent(ctx, database.UpsertTaskActivityEventParams{
			WorkspaceID: uid,
			ScopeType:   "task",
			FeatureID:   featureUUID,
			FeatureName: featureName,
			TaskID:      taskUUID,
			TaskName:    t.TaskID,
			Action:      ptrStr(evt.Action),
			Actor:       ptrStr(evt.Actor),
			OccurredAt:  ptrStr(evt.OccurredAt.Format(time.RFC3339)),
			Note:        ptrStr(evt.Note),
			Sequence:    int32(i),
			RawEvent:    raw,
		})
		if err != nil {
			return fmt.Errorf("upsert task activity %s/%s[%d]: %w", featureName, t.TaskID, i, err)
		}
	}
	return nil
}

// buildFeatureSnapshotFromBatch reconstructs a FeatureSnapshot from pre-fetched rows,
// avoiding per-feature database queries.
func buildFeatureSnapshotFromBatch(f database.WorkspaceFeature, docs []database.WorkspaceFeatureDocument, tasks []database.WorkspaceTask) domain.FeatureSnapshot {
	docSnaps := make([]domain.DocumentSnapshot, 0, len(docs))
	for _, d := range docs {
		ds := domain.DocumentSnapshot{
			DocumentType: d.DocumentType,
			SourcePath:   d.SourcePath,
		}
		if d.URL != nil {
			ds.URL = *d.URL
		}
		docSnaps = append(docSnaps, ds)
	}

	taskSnaps := make([]domain.TaskSnapshot, 0, len(tasks))
	for _, t := range tasks {
		taskSnaps = append(taskSnaps, rowToTaskSnapshot(t))
	}

	fs := domain.FeatureSnapshot{
		FeatureID:  f.FeatureID,
		Title:      f.Title,
		SourcePath: f.SourcePath,
		Documents:  docSnaps,
		Tasks:      taskSnaps,
	}
	if f.FeatureStatus != nil {
		fs.Status = *f.FeatureStatus
	}
	if f.CurrentStage != nil {
		fs.CurrentStage = *f.CurrentStage
	}
	if f.NextAction != nil {
		fs.NextAction = *f.NextAction
	}
	if f.SourceHash != nil {
		fs.SourceHash = *f.SourceHash
	}
	return fs
}

// rowToTaskSnapshot converts a database row to a domain.TaskSnapshot.
func rowToTaskSnapshot(t database.WorkspaceTask) domain.TaskSnapshot {
	ts := domain.TaskSnapshot{
		TaskID:     t.TaskID,
		FeatureID:  t.FeatureName,
		Title:      t.Title,
		SourcePath: t.SourcePath,
	}
	if t.Repo != nil {
		ts.Repo = *t.Repo
	}
	if t.Status != nil {
		ts.Status = *t.Status
	}
	if t.BlockedReason != nil {
		ts.BlockedReason = *t.BlockedReason
	}
	if t.Branch != nil {
		ts.Branch = *t.Branch
	}
	if t.SourceHash != nil {
		ts.SourceHash = *t.SourceHash
	}
	ts.DependsOn = unmarshalStringSlice(t.DependsOn)
	if len(t.Execution) > 0 {
		var m map[string]interface{}
		_ = json.Unmarshal(t.Execution, &m)
		ts.Execution = m
	}
	if len(t.Pr) > 0 {
		var m map[string]interface{}
		_ = json.Unmarshal(t.Pr, &m)
		ts.PR = m
	}
	if len(t.WorkspacePr) > 0 {
		var m map[string]interface{}
		_ = json.Unmarshal(t.WorkspacePr, &m)
		ts.WorkspacePR = m
	}
	return ts
}

// rowToFeatureSummary converts feature row + task list to a domain.FeatureSummary.
func rowToFeatureSummary(f database.WorkspaceFeature, tasks []database.WorkspaceTask) domain.FeatureSummary {
	counts := domain.TaskCounts{}
	for _, t := range tasks {
		if t.FeatureName != f.FeatureID {
			continue
		}
		counts.Total++
		if t.Status == nil {
			continue
		}
		switch *t.Status {
		case "done":
			counts.Done++
		case "in_progress":
			counts.InProgress++
		case "blocked":
			counts.Blocked++
		case "ready":
			counts.Ready++
		case "todo":
			counts.Todo++
		}
	}

	fs := domain.FeatureSummary{
		ID:         uuidStr(f.ID),
		FeatureID:  f.FeatureID,
		Title:      f.Title,
		UpdatedAt:  f.UpdatedAt.Time,
		TaskCounts: counts,
	}
	if f.FeatureStatus != nil {
		fs.Status = *f.FeatureStatus
	}
	if f.CurrentStage != nil {
		fs.CurrentStage = *f.CurrentStage
	}
	return fs
}

// rowToTaskSummary converts a workspace_tasks row to domain.TaskSummary.
func rowToTaskSummary(t database.WorkspaceTask) domain.TaskSummary {
	ts := domain.TaskSummary{
		ID:          uuidStr(t.ID),
		TaskID:      t.TaskID,
		FeatureID:   uuidStr(t.FeatureID),
		FeatureName: t.FeatureName,
		Title:       t.Title,
	}
	if t.Status != nil {
		ts.Status = *t.Status
	}
	if t.Repo != nil {
		ts.Repo = *t.Repo
	}
	if t.Branch != nil {
		ts.Branch = *t.Branch
	}
	if t.BlockedReason != nil {
		ts.BlockedReason = *t.BlockedReason
		ts.IsBlocked = true
	}
	return ts
}

// rowToActivityEvent converts a database row to domain.ActivityEvent.
func rowToActivityEvent(r database.WorkspaceActivityEvent) domain.ActivityEvent {
	evt := domain.ActivityEvent{
		Scope: r.ScopeType,
	}
	if r.Action != nil {
		evt.Action = *r.Action
	}
	if r.Actor != nil {
		evt.Actor = *r.Actor
	}
	if r.Note != nil {
		evt.Note = *r.Note
	}
	if r.FeatureID.Valid {
		evt.FeatureID = uuidStr(r.FeatureID)
	}
	if r.TaskID.Valid {
		evt.TaskID = uuidStr(r.TaskID)
	}
	if r.OccurredAt != nil {
		if t, err := time.Parse(time.RFC3339, *r.OccurredAt); err == nil {
			evt.OccurredAt = t
		}
	}
	return evt
}

// syncRunRowToDomain converts a database sync run row to domain.SyncRun.
func syncRunRowToDomain(r database.WorkspaceSyncRun) *domain.SyncRun {
	sr := &domain.SyncRun{
		ID:          uuidStr(r.ID),
		WorkspaceID: uuidStr(r.WorkspaceID),
		Trigger:     r.Trigger,
		Mode:        r.Mode,
		Status:      domain.SyncStatus(r.Status),
		StartedAt:   r.StartedAt.Time,
	}
	if r.CommitSha != nil {
		sr.CommitSHA = *r.CommitSha
	}
	if r.ErrorCode != nil {
		sr.ErrorCode = *r.ErrorCode
	}
	if r.ErrorMessage != nil {
		sr.ErrorMsg = *r.ErrorMessage
	}
	if r.FinishedAt.Valid {
		t := r.FinishedAt.Time
		sr.FinishedAt = &t
	}
	return sr
}

// syncRunToSourceState derives a SourceState from the latest sync run.
func syncRunToSourceState(r *database.WorkspaceSyncRun, threshold *time.Duration) domain.SourceState {
	if r == nil || r.ID == (pgtype.UUID{}) {
		return domain.StaleState(nil)
	}
	thr := domain.DefaultStaleThreshold
	if threshold != nil {
		thr = *threshold
	}
	return domain.DeriveSourceState(syncRunRowToDomain(*r), thr)
}

// githubRepoURL fetches the repo URL from workspace_github_sources (best-effort).
func githubRepoURL(ctx context.Context, q *database.Queries, uid pgtype.UUID) string {
	src, err := q.GetGitHubSource(ctx, uid)
	if err != nil {
		return ""
	}
	return src.RepoURL
}

// parseUUID parses a plain UUID string into pgtype.UUID.
func parseUUID(s string) (pgtype.UUID, error) {
	var uid pgtype.UUID
	if err := uid.Scan(s); err != nil {
		return pgtype.UUID{}, domain.NewValidationError(domain.ErrValidationMissingInput, "invalid workspace id: "+s)
	}
	return uid, nil
}

// uuidStr renders a pgtype.UUID as a lowercase hyphenated string.
func uuidStr(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ptrStr returns nil if s is empty, otherwise returns &s.
func ptrStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// unmarshalStringSlice decodes a JSON array of strings.
func unmarshalStringSlice(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	_ = json.Unmarshal(raw, &out)
	return out
}

// unmarshalExecution decodes execution JSON into domain.ExecutionContext.
func unmarshalExecution(raw json.RawMessage) domain.ExecutionContext {
	if len(raw) == 0 {
		return domain.ExecutionContext{}
	}
	var ec domain.ExecutionContext
	_ = json.Unmarshal(raw, &ec)
	return ec
}

// dbErr wraps a database error in a domain SourceError.
func dbErr(op string, err error) error {
	if strings.Contains(err.Error(), "no rows") {
		return domain.NewDatabaseError(domain.ErrDatabaseNotFound, op+": not found")
	}
	return domain.NewDatabaseError(domain.ErrDatabaseQuery, op+": "+err.Error())
}
