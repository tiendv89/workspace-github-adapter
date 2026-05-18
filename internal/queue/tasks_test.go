package queue_test

import (
	"encoding/json"
	"testing"

	"github.com/tiendv89/workspace-github-adapter/internal/queue"
)

func TestNewTaskSyncTask_Serialization(t *testing.T) {
	payload := queue.TaskSyncPayload{
		WorkspaceID: "ws-123",
		FeatureID:   "workspace-data-backend",
		TaskID:      "T7",
	}
	task, err := queue.NewTaskSyncTask(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if task.Type() != queue.TypeTaskSync {
		t.Errorf("expected type %q, got %q", queue.TypeTaskSync, task.Type())
	}

	var got queue.TaskSyncPayload
	if err := json.Unmarshal(task.Payload(), &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got.WorkspaceID != payload.WorkspaceID {
		t.Errorf("WorkspaceID: got %q, want %q", got.WorkspaceID, payload.WorkspaceID)
	}
	if got.FeatureID != payload.FeatureID {
		t.Errorf("FeatureID: got %q, want %q", got.FeatureID, payload.FeatureID)
	}
	if got.TaskID != payload.TaskID {
		t.Errorf("TaskID: got %q, want %q", got.TaskID, payload.TaskID)
	}
}

func TestRedisOpt_Default(t *testing.T) {
	opt, err := queue.RedisOpt("")
	if err != nil {
		t.Fatalf("unexpected error for empty REDIS_URL: %v", err)
	}
	if opt == nil {
		t.Fatal("expected non-nil RedisConnOpt")
	}
}

func TestRedisOpt_InvalidURL(t *testing.T) {
	_, err := queue.RedisOpt("not-a-valid-redis-url://bad")
	if err == nil {
		t.Fatal("expected error for invalid Redis URL")
	}
}
