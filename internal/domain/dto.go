package domain

import "time"

// SourceState describes the freshness and error state of a workspace's data.
type SourceState struct {
	Stale        bool       `json:"stale"`
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
	ErrorCode    string     `json:"error_code,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
}

// PullRequestRef is a reference to a pull request associated with a task.
type PullRequestRef struct {
	Label  string `json:"label"`
	URL    string `json:"url"`
	Status string `json:"status"`
	Repo   string `json:"repo"`
}

// ActivityEvent is a single normalized activity record from feature history or task logs.
type ActivityEvent struct {
	Action     string    `json:"action"`
	Scope      string    `json:"scope"`
	Actor      string    `json:"actor"`
	OccurredAt time.Time `json:"occurred_at"`
	Note       string    `json:"note,omitempty"`
	FeatureID  string    `json:"feature_id,omitempty"`
	TaskID     string    `json:"task_id,omitempty"`
}

// ActivityScope filters activity queries by scope.
type ActivityScope struct {
	FeatureID string
	TaskID    string
}

// WorkspaceSummary is the list-view representation of a workspace.
type WorkspaceSummary struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Slug        string      `json:"slug"`
	RepoURL     string      `json:"repo_url"`
	SourceState SourceState `json:"source_state"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// WorkspaceDetail is the full workspace view including features and tasks.
type WorkspaceDetail struct {
	WorkspaceSummary
	Features []FeatureSummary `json:"features"`
	Tasks    []TaskSummary    `json:"tasks"`
}

// FeatureSummary is the list-view representation of a feature.
type FeatureSummary struct {
	ID           string     `json:"id"`
	FeatureID    string     `json:"feature_id"`
	FeatureName  string     `json:"feature_name"`
	Title        string     `json:"title"`
	Status       string     `json:"status"`
	CurrentStage string     `json:"current_stage,omitempty"`
	UpdatedAt    time.Time  `json:"updated_at"`
	TaskCounts   TaskCounts `json:"task_counts"`
}

// TaskCounts summarises task status distribution within a feature.
type TaskCounts struct {
	Total      int `json:"total"`
	Done       int `json:"done"`
	InProgress int `json:"in_progress"`
	Blocked    int `json:"blocked"`
	Ready      int `json:"ready"`
	Todo       int `json:"todo"`
}

// DocumentLink is a GitHub web URL for a feature document.
type DocumentLink struct {
	DocumentType string `json:"document_type"`
	SourcePath   string `json:"source_path"`
	URL          string `json:"url"`
}

// FeatureDetail is the full feature view including documents, tasks, and activity.
type FeatureDetail struct {
	FeatureSummary
	WorkspaceID string          `json:"workspace_id"`
	Documents   []DocumentLink  `json:"documents"`
	Tasks       []TaskSummary   `json:"tasks"`
	Activity    []ActivityEvent `json:"activity"`
	SourceState SourceState     `json:"source_state"`
}

// TaskSummary is the list-view representation of a task.
type TaskSummary struct {
	ID            string `json:"id"`
	TaskID        string `json:"task_id"`
	TaskName      string `json:"task_name"`
	FeatureID     string `json:"feature_id"`
	FeatureName   string `json:"feature_name"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	Repo          string `json:"repo,omitempty"`
	Branch        string `json:"branch,omitempty"`
	NextAction    string `json:"next_action,omitempty"`
	IsBlocked     bool   `json:"is_blocked"`
	BlockedReason string `json:"blocked_reason,omitempty"`
}

// ExecutionContext holds execution metadata for a task.
type ExecutionContext struct {
	ActorType     string `json:"actor_type"`
	LastUpdatedBy string `json:"last_updated_by,omitempty"`
	LastUpdatedAt string `json:"last_updated_at,omitempty"`
}

// TaskDetail is the full task view including dependencies, PR refs, and activity.
type TaskDetail struct {
	TaskSummary
	WorkspaceID    string           `json:"workspace_id"`
	DependsOn      []string         `json:"depends_on"`
	Execution      ExecutionContext `json:"execution"`
	PRRefs         []PullRequestRef `json:"pr_refs,omitempty"`
	BlockedContext *BlockedContext  `json:"blocked_context,omitempty"`
	Activity       []ActivityEvent  `json:"activity"`
}

// BlockedContext holds context about why a task is blocked and how to resume.
type BlockedContext struct {
	WIPBranch  string `json:"wip_branch,omitempty"`
	WIPSha     string `json:"wip_sha,omitempty"`
	PushedAt   string `json:"pushed_at,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

// WorkspaceSnapshot is the parsed in-memory representation of a GitHub workspace
// returned by GitHubWorkspaceAdapter. It is the input to the DB persistence layer.
type WorkspaceSnapshot struct {
	WorkspaceID      string
	Name             string
	Slug             string
	RepoURL          string
	ManagementRepoID string
	CommitSHA        string
	FetchedAt        time.Time
	Features         []FeatureSnapshot
	Repos            []RepoEntry
	SourceErrors     []SourceError
}

// RepoEntry maps to workspace.yaml repos[].
type RepoEntry struct {
	RepoID     string `json:"repo_id"`
	BaseBranch string `json:"base_branch,omitempty"`
}

// FeatureSnapshot is the parsed representation of a single feature directory.
type FeatureSnapshot struct {
	FeatureID    string
	Title        string
	Status       string
	CurrentStage string
	NextAction   string
	Stages       map[string]interface{}
	SourcePath   string
	SourceHash   string
	Documents    []DocumentSnapshot
	Tasks        []TaskSnapshot
	Activity     []ActivityEvent
}

// DocumentSnapshot is the parsed representation of a feature document.
type DocumentSnapshot struct {
	DocumentType string
	SourcePath   string
	URL          string
}

// TaskSnapshot is the parsed representation of a task YAML file.
type TaskSnapshot struct {
	TaskID        string
	FeatureID     string
	Title         string
	Status        string
	Repo          string
	Branch        string
	DependsOn     []string
	BlockedReason string
	Execution     map[string]interface{}
	PR            map[string]interface{}
	WorkspacePR   map[string]interface{}
	SourcePath    string
	SourceHash    string
	Activity      []ActivityEvent
}
