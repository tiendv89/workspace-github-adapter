package domain

import "context"

// ImportInput is the input for the initial import of a workspace from GitHub.
type ImportInput struct {
	RepoURL       string
	DefaultBranch string
	Token         string // optional; falls back to GITHUB_TOKEN env var
}

// GitHubWorkspaceAdapter fetches and parses a GitHub management repository.
// It never reads from or writes to the database.
type GitHubWorkspaceAdapter interface {
	// ImportWorkspace performs a full reconciliation fetch of the given repository.
	// Returns a complete WorkspaceSnapshot or an error if the repo is inaccessible.
	ImportWorkspace(ctx context.Context, input ImportInput) (*WorkspaceSnapshot, error)

	// FetchWorkspaceMetadata validates the repository and reads only workspace.yaml.
	// It is used by HTTP import preflight so full reconciliation can run asynchronously.
	FetchWorkspaceMetadata(ctx context.Context, input ImportInput) (*WorkspaceSnapshot, error)

	// SyncWorkspace re-fetches the repository at the given ref (branch or SHA)
	// and returns an updated WorkspaceSnapshot.
	SyncWorkspace(ctx context.Context, workspaceID, repoURL, ref string) (*WorkspaceSnapshot, error)

	// FetchFeature fetches and parses all artifacts for a single feature.
	// Used for targeted sync triggered by webhook events on feature branches or base-branch pushes.
	FetchFeature(ctx context.Context, repoURL, ref, featureID string) (*FeatureSnapshot, error)

	// FetchTask fetches and parses a single task YAML from the given task branch.
	// Used by the task:sync worker to read current task state at execution time.
	FetchTask(ctx context.Context, repoURL, taskBranch, featureID, taskID string) (*TaskSnapshot, error)
}

// DbWorkspaceAdapter reads and writes workspace data in PostgreSQL.
// It never calls GitHub.
type DbWorkspaceAdapter interface {
	// ListWorkspaces returns summary rows for all saved workspaces.
	ListWorkspaces(ctx context.Context) ([]WorkspaceSummary, error)

	// GetWorkspace returns the full workspace detail for the given workspace ID.
	GetWorkspace(ctx context.Context, workspaceID string) (*WorkspaceDetail, error)

	// GetFeature returns the full feature detail for the given workspace and feature IDs.
	GetFeature(ctx context.Context, workspaceID, featureID string) (*FeatureDetail, error)

	// GetTask returns the full task detail for the given workspace, feature, and task IDs.
	GetTask(ctx context.Context, workspaceID, featureID, taskID string) (*TaskDetail, error)

	// ListFeatureTasks returns task summaries for all tasks in the given feature.
	ListFeatureTasks(ctx context.Context, workspaceID, featureID string) ([]TaskSummary, error)

	// ListActivity returns activity events filtered by the given scope.
	ListActivity(ctx context.Context, workspaceID string, scope ActivityScope) ([]ActivityEvent, error)

	// SaveSnapshot upserts all core tables from the given snapshot inside a single transaction.
	SaveSnapshot(ctx context.Context, workspaceID string, snapshot *WorkspaceSnapshot) error

	// SaveFeatureSnapshot upserts a single feature's rows (features, documents, tasks, activity)
	// inside a single transaction. Used for targeted sync from webhook events.
	SaveFeatureSnapshot(ctx context.Context, workspaceID string, snap FeatureSnapshot) error

	// SaveTaskSnapshot upserts a single task's rows (workspace_tasks, activity events)
	// inside a single transaction. Used by the task:sync queue worker.
	SaveTaskSnapshot(ctx context.Context, workspaceID string, snap TaskSnapshot) error

	// GetActiveSnapshot returns the latest WorkspaceSnapshot for the given workspace.
	// Returns nil, nil when no snapshot has been saved yet.
	GetActiveSnapshot(ctx context.Context, workspaceID string) (*WorkspaceSnapshot, error)

	// GetLatestSyncRun returns the most recent sync run record for staleness derivation.
	// Returns nil, nil when no sync has been recorded.
	GetLatestSyncRun(ctx context.Context, workspaceID string) (*SyncRun, error)
}
