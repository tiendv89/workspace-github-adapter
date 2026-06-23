package queue

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

const (
	TypeWorkspaceSync = "workspace:sync"
	TypeTaskSync      = "task:sync"

	QueueDefault  = "default"
	QueueTaskSync = "task-sync"
)

// WorkspaceSyncPayload is the payload consumed by worker.
type WorkspaceSyncPayload struct {
	WorkspaceID   string `json:"workspace_id"`
	RepoURL       string `json:"repo_url"`
	Ref           string `json:"ref,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
	Trigger       string `json:"trigger,omitempty"`
	// Mode is "full" (default) or "targeted". When "targeted", FeatureID must be set.
	Mode      string `json:"mode,omitempty"`
	FeatureID string `json:"feature_id,omitempty"`
	Name      string `json:"name,omitempty"`
	SyncRunID string `json:"sync_run_id,omitempty"`
}

// TaskSyncPayload is the payload for a task:sync job enqueued on task-branch webhook events.
// Keep this payload limited to the source task identity because asynq.Unique
// derives its dedupe key from the full task payload.
type TaskSyncPayload struct {
	WorkspaceID string `json:"workspace_id"`
	FeatureID   string `json:"feature_id"`
	TaskID      string `json:"task_id"`
}

// NewWorkspaceSyncTask creates an asynq task for syncing/importing a workspace from GitHub.
// Timeout bounds a single run (so a slow/stuck fetch can't hang a worker slot
// indefinitely — the handler ctx is cancelled at the deadline), and MaxRetry
// caps retries so a persistently-failing sync doesn't re-run ~25 times and
// flood the logs with repeated "sync started".
func NewWorkspaceSyncTask(payload WorkspaceSyncPayload) (*asynq.Task, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal workspace sync payload: %w", err)
	}
	return asynq.NewTask(TypeWorkspaceSync, b,
		asynq.Timeout(10*time.Minute),
		asynq.MaxRetry(3),
	), nil
}

// NewTaskSyncTask creates an asynq task:sync job for a task-branch webhook event.
// It uses Unique(24h) for one pending job per workspace/feature/task.
func NewTaskSyncTask(payload TaskSyncPayload) (*asynq.Task, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal task sync payload: %w", err)
	}
	return asynq.NewTask(TypeTaskSync, b,
		asynq.Queue(QueueTaskSync),
		asynq.Unique(24*time.Hour),
		asynq.MaxRetry(3),
	), nil
}

// RedisOpt returns an asynq Redis connection option for the given host:port address.
// If addr is empty, localhost:6379 is used for local development.
func RedisOpt(addr string) asynq.RedisConnOpt {
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	return asynq.RedisClientOpt{Addr: addr}
}
