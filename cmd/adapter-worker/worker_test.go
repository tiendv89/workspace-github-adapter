package main

import (
	"testing"

	"github.com/tiendv89/workspace-github-adapter/internal/queue"
)

func TestDeriveBranch(t *testing.T) {
	cases := []struct {
		pattern   string
		featureID string
		taskID    string
		want      string
	}{
		{"feature/{feature_id}-{work_id}", "workspace-data-backend", "T7", "feature/workspace-data-backend-T7"},
		{"feature/{feature_id}-{work_id}", "executor-self-briefing", "T1", "feature/executor-self-briefing-T1"},
		{"feature/{feature_id}-{work_id}", "my-feature", "T12", "feature/my-feature-T12"},
	}
	for _, tc := range cases {
		got := deriveBranch(tc.pattern, tc.featureID, tc.taskID)
		if got != tc.want {
			t.Errorf("deriveBranch(%q, %q, %q) = %q, want %q", tc.pattern, tc.featureID, tc.taskID, got, tc.want)
		}
	}
}

func TestTaskSyncBranch_PrefersWebhookBranch(t *testing.T) {
	payload := queue.TaskSyncPayload{
		FeatureID: "test-webhook-19-05",
		TaskID:    "T1",
		Branch:    "feature/test-webhook-19-05-T1",
	}

	got := taskSyncBranch(payload, "feature/{feature_id}")
	if got != "feature/test-webhook-19-05-T1" {
		t.Errorf("taskSyncBranch() = %q, want webhook task branch", got)
	}
}
