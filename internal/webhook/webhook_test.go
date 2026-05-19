package webhook_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/tiendv89/workspace-github-adapter/internal/webhook"
)

func TestVerifySignature_Valid(t *testing.T) {
	secret := "mysecret"
	body := []byte(`{"ref":"refs/heads/main"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if err := webhook.VerifySignature(secret, sig, body); err != nil {
		t.Fatalf("expected valid signature to pass, got: %v", err)
	}
}

func TestVerifySignature_Invalid(t *testing.T) {
	secret := "mysecret"
	body := []byte(`{"ref":"refs/heads/main"}`)
	badSig := "sha256=0000000000000000000000000000000000000000000000000000000000000000"

	if err := webhook.VerifySignature(secret, badSig, body); err == nil {
		t.Fatal("expected invalid signature to fail, got nil")
	}
}

func TestVerifySignature_MissingHeader(t *testing.T) {
	err := webhook.VerifySignature("secret", "", []byte("body"))
	if err == nil {
		t.Fatal("expected missing header to fail")
	}
}

func TestVerifySignature_EmptySecret(t *testing.T) {
	// Empty secret disables verification.
	if err := webhook.VerifySignature("", "anything", []byte("body")); err != nil {
		t.Fatalf("empty secret should skip verification, got: %v", err)
	}
}

func TestClassifyBranch_Base(t *testing.T) {
	info := webhook.ClassifyBranch("main", "main")
	if info.Kind != webhook.BranchBase {
		t.Errorf("expected BranchBase, got %v", info.Kind)
	}
}

func TestClassifyBranch_Feature(t *testing.T) {
	info := webhook.ClassifyBranch("feature/workspace-data-backend", "main")
	if info.Kind != webhook.BranchFeature {
		t.Errorf("expected BranchFeature, got %v", info.Kind)
	}
	if info.FeatureID != "workspace-data-backend" {
		t.Errorf("expected FeatureID=workspace-data-backend, got %q", info.FeatureID)
	}
}

func TestClassifyBranch_Task(t *testing.T) {
	info := webhook.ClassifyBranch("feature/workspace-data-backend-T7", "main")
	if info.Kind != webhook.BranchTask {
		t.Errorf("expected BranchTask, got %v", info.Kind)
	}
	if info.FeatureID != "workspace-data-backend" {
		t.Errorf("expected FeatureID=workspace-data-backend, got %q", info.FeatureID)
	}
	if info.TaskID != "T7" {
		t.Errorf("expected TaskID=T7, got %q", info.TaskID)
	}
}

func TestClassifyBranch_TaskWithDatedFeatureID(t *testing.T) {
	info := webhook.ClassifyBranch("feature/test-webhook-19-05-T1", "main")
	if info.Kind != webhook.BranchTask {
		t.Errorf("expected BranchTask, got %v", info.Kind)
	}
	if info.FeatureID != "test-webhook-19-05" {
		t.Errorf("expected FeatureID=test-webhook-19-05, got %q", info.FeatureID)
	}
	if info.TaskID != "T1" {
		t.Errorf("expected TaskID=T1, got %q", info.TaskID)
	}
}

func TestClassifyBranch_Ignored(t *testing.T) {
	cases := []string{"hotfix/something", "dependabot/npm/foo", "renovate/bar", ""}
	for _, branch := range cases {
		info := webhook.ClassifyBranch(branch, "main")
		if info.Kind != webhook.BranchIgnored {
			t.Errorf("branch %q: expected BranchIgnored, got %v", branch, info.Kind)
		}
	}
}

func TestBranchFromRef(t *testing.T) {
	cases := []struct {
		ref  string
		want string
	}{
		{"refs/heads/main", "main"},
		{"refs/heads/feature/workspace-data-backend-T7", "feature/workspace-data-backend-T7"},
		{"main", "main"},
	}
	for _, tc := range cases {
		got := webhook.BranchFromRef(tc.ref)
		if got != tc.want {
			t.Errorf("BranchFromRef(%q) = %q, want %q", tc.ref, got, tc.want)
		}
	}
}

func TestTouchedFeatureIDs(t *testing.T) {
	ev := &webhook.PushEvent{
		Commits: []webhook.Commit{
			{
				Modified: []string{
					"docs/features/workspace-data-backend/tasks/T1.yaml",
					"docs/features/workspace-data-backend/tasks.md",
				},
				Added: []string{
					"docs/features/new-feature/status.yaml",
				},
			},
			{
				Modified: []string{
					"docs/features/workspace-data-backend/status.yaml",
					"workflow/something.go",
				},
			},
		},
	}
	ids := webhook.TouchedFeatureIDs(ev)
	if len(ids) != 2 {
		t.Fatalf("expected 2 unique feature IDs, got %d: %v", len(ids), ids)
	}
	seen := make(map[string]bool)
	for _, id := range ids {
		seen[id] = true
	}
	if !seen["workspace-data-backend"] {
		t.Error("expected workspace-data-backend in touched feature IDs")
	}
	if !seen["new-feature"] {
		t.Error("expected new-feature in touched feature IDs")
	}
}

func TestTouchedFeatureIDs_NoPaths(t *testing.T) {
	ev := &webhook.PushEvent{
		Commits: []webhook.Commit{
			{Modified: []string{"workflow/something.go", "README.md"}},
		},
	}
	ids := webhook.TouchedFeatureIDs(ev)
	if len(ids) != 0 {
		t.Errorf("expected 0 feature IDs, got %v", ids)
	}
}

func TestParsePushEvent(t *testing.T) {
	raw := []byte(`{
		"ref": "refs/heads/feature/my-feature-T3",
		"after": "abc123",
		"repository": {"full_name": "owner/repo", "html_url": "https://github.com/owner/repo"},
		"commits": [{"added": ["docs/features/my-feature/tasks/T3.yaml"], "modified": [], "removed": []}]
	}`)
	ev, err := webhook.ParsePushEvent(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Ref != "refs/heads/feature/my-feature-T3" {
		t.Errorf("unexpected Ref: %q", ev.Ref)
	}
	if ev.After != "abc123" {
		t.Errorf("unexpected After: %q", ev.After)
	}
	if ev.Repository.FullName != "owner/repo" {
		t.Errorf("unexpected FullName: %q", ev.Repository.FullName)
	}
	if len(ev.Commits) != 1 || len(ev.Commits[0].Added) != 1 {
		t.Errorf("unexpected commits: %+v", ev.Commits)
	}
}
