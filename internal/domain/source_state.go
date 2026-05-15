package domain

import "time"

// SyncStatus reflects the outcome of a workspace sync run.
type SyncStatus string

const (
	SyncStatusRunning SyncStatus = "running"
	SyncStatusSuccess SyncStatus = "success"
	SyncStatusPartial SyncStatus = "partial"
	SyncStatusFailed  SyncStatus = "failed"
	SyncStatusSkipped SyncStatus = "skipped"
)

// SyncRun is a record of a single sync attempt stored in workspace_sync_runs.
type SyncRun struct {
	ID          string
	WorkspaceID string
	Trigger     string
	Mode        string
	Status      SyncStatus
	CommitSHA   string
	StartedAt   time.Time
	FinishedAt  *time.Time
	ErrorCode   string
	ErrorMsg    string
}

// StaleThreshold is the duration after which a successful sync is considered stale.
// Configurable at runtime; the default is 30 minutes.
const DefaultStaleThreshold = 30 * time.Minute

// DeriveSourceState computes a SourceState from the latest SyncRun.
// When latestRun is nil, the workspace has never been synced — SourceState is stale.
func DeriveSourceState(latestRun *SyncRun, staleThreshold time.Duration) SourceState {
	if latestRun == nil {
		return SourceState{Stale: true}
	}

	switch latestRun.Status {
	case SyncStatusFailed:
		return SourceState{
			Stale:        true,
			LastSyncedAt: latestRun.FinishedAt,
			ErrorCode:    latestRun.ErrorCode,
			ErrorMessage: latestRun.ErrorMsg,
		}
	case SyncStatusSuccess, SyncStatusPartial:
		if latestRun.FinishedAt == nil {
			return SourceState{Stale: true}
		}
		age := time.Since(*latestRun.FinishedAt)
		return SourceState{
			Stale:        age > staleThreshold,
			LastSyncedAt: latestRun.FinishedAt,
		}
	case SyncStatusRunning:
		// A sync is in progress — report the previous sync state as current.
		// Caller can detect this by checking ErrorCode == "" and Stale == false
		// with a non-nil LastSyncedAt near now.
		return SourceState{
			Stale:        false,
			LastSyncedAt: &latestRun.StartedAt,
		}
	default:
		return SourceState{Stale: true}
	}
}

// FreshState returns a non-stale SourceState with the given sync time.
func FreshState(syncedAt time.Time) SourceState {
	t := syncedAt
	return SourceState{
		Stale:        false,
		LastSyncedAt: &t,
	}
}

// StaleState returns a stale SourceState with an optional last-synced time.
func StaleState(syncedAt *time.Time) SourceState {
	return SourceState{
		Stale:        true,
		LastSyncedAt: syncedAt,
	}
}

// UnavailableState returns a stale SourceState with an error indicating the data is unavailable.
func UnavailableState(code, message string) SourceState {
	return SourceState{
		Stale:        true,
		ErrorCode:    code,
		ErrorMessage: message,
	}
}

// FailedState returns a stale SourceState reflecting a failed sync attempt.
func FailedState(syncedAt *time.Time, code, message string) SourceState {
	return SourceState{
		Stale:        true,
		LastSyncedAt: syncedAt,
		ErrorCode:    code,
		ErrorMessage: message,
	}
}
