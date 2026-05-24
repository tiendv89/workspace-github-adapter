package handler

import (
	"github.com/tiendv89/workspace-github-adapter/pkg/queue"
	"github.com/tiendv89/workspace-github-adapter/pkg/urlutil"
)

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
