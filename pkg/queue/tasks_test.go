package queue_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/tiendv89/workspace-github-adapter/pkg/queue"
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

func TestTaskSyncPayload_DedupeKeyFieldsOnly(t *testing.T) {
	payloadType := reflect.TypeOf(queue.TaskSyncPayload{})
	for _, fieldName := range []string{"Branch", "CommitSHA"} {
		if _, ok := payloadType.FieldByName(fieldName); ok {
			t.Fatalf("TaskSyncPayload must not include %s because asynq.Unique keys include the full payload", fieldName)
		}
	}
}

func TestRedisOpt_Default(t *testing.T) {
	opt := queue.RedisOpt("")
	if opt == nil {
		t.Fatal("expected non-nil RedisConnOpt")
	}
}

func TestRedisOpt_WithAddr(t *testing.T) {
	opt := queue.RedisOpt("redis-host:6380")
	if opt == nil {
		t.Fatal("expected non-nil RedisConnOpt")
	}
}
