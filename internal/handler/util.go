package handler

import (
	"strings"

	"github.com/tiendv89/workspace-github-adapter/pkg/queue"
	"github.com/tiendv89/workspace-github-adapter/pkg/urlutil"
)

// WorkspaceIDFromSyncPath extracts the workspace ID from paths like /internal/workspaces/{id}/sync.
func WorkspaceIDFromSyncPath(path string) (string, bool) {
	const prefix = "/internal/workspaces/"
	const suffix = "/sync"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	workspaceID := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	workspaceID = strings.Trim(workspaceID, "/")
	return workspaceID, workspaceID != ""
}

// WorkspaceSyncTaskID builds the dedup task ID for a workspace sync payload.
func WorkspaceSyncTaskID(payload queue.WorkspaceSyncPayload) string {
	ref := payload.Ref
	if ref == "" {
		ref = payload.DefaultBranch
	}
	mode := payload.Mode
	if mode == "" {
		mode = "full"
	}
	raw := payload.WorkspaceID + "-" + payload.RepoURL + "-" + ref + "-" + mode + "-" + payload.FeatureID
	id := urlutil.Slugify(raw)
	if id == "" {
		id = payload.WorkspaceID
	}
	return "workspace-sync-" + id
}
