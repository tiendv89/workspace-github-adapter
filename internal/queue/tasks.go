package queue

import (
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
)

const TypeWorkspaceSync = "workspace:sync"

// WorkspaceSyncPayload is the payload consumed by adapter-worker.
type WorkspaceSyncPayload struct {
	WorkspaceID   string `json:"workspace_id"`
	RepoURL       string `json:"repo_url"`
	Ref           string `json:"ref,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
	Trigger       string `json:"trigger,omitempty"`
	Mode          string `json:"mode,omitempty"`
	Name          string `json:"name,omitempty"`
	SyncRunID     string `json:"sync_run_id,omitempty"`
}

// NewWorkspaceSyncTask creates an asynq task for syncing/importing a workspace from GitHub.
func NewWorkspaceSyncTask(payload WorkspaceSyncPayload) (*asynq.Task, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal workspace sync payload: %w", err)
	}
	return asynq.NewTask(TypeWorkspaceSync, b), nil
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
