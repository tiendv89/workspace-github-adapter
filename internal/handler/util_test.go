package handler

import (
	"testing"
)

func TestWorkspaceIDFromSyncPath(t *testing.T) {
	cases := []struct {
		path   string
		wantID string
		wantOK bool
	}{
		{"/internal/workspaces/abc-123/sync", "abc-123", true},
		{"/internal/workspaces/abc-123/", "", false},
		{"/internal/workspaces//sync", "", false},
		{"/other/path", "", false},
		{"/internal/workspaces/11111111-1111-1111-1111-111111111111/sync", "11111111-1111-1111-1111-111111111111", true},
	}
	for _, tc := range cases {
		id, ok := WorkspaceIDFromSyncPath(tc.path)
		if ok != tc.wantOK {
			t.Errorf("WorkspaceIDFromSyncPath(%q): ok=%v, want %v", tc.path, ok, tc.wantOK)
		}
		if id != tc.wantID {
			t.Errorf("WorkspaceIDFromSyncPath(%q): id=%q, want %q", tc.path, id, tc.wantID)
		}
	}
}
