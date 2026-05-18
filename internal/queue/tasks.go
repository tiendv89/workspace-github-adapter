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

// WorkspaceSyncPayload is the payload consumed by adapter-worker.
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
// The task branch is derived from workspace branch_pattern + FeatureID + TaskID at execution time.
type TaskSyncPayload struct {
	WorkspaceID string `json:"workspace_id"`
	FeatureID   string `json:"feature_id"`
	TaskID      string `json:"task_id"`
}

// NewWorkspaceSyncTask creates an asynq task for syncing/importing a workspace from GitHub.
func NewWorkspaceSyncTask(payload WorkspaceSyncPayload) (*asynq.Task, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal workspace sync payload: %w", err)
	}
	return asynq.NewTask(TypeWorkspaceSync, b), nil
}

// NewTaskSyncTask creates an asynq task:sync job for a task-branch webhook event.
// It uses Unique(24h) for deduplication — only one pending item per (WorkspaceID, FeatureID, TaskID).
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

// RedisOpt parses REDIS_URL into an asynq Redis connection option.
// If redisURL is empty, localhost:6379 is used for local development.
func RedisOpt(redisURL string) (asynq.RedisConnOpt, error) {
	if redisURL == "" {
		return asynq.RedisClientOpt{Addr: "127.0.0.1:6379"}, nil
	}
	opt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}
	return opt, nil
}
