package domain_test

import (
	"testing"
	"time"

	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

func TestDeriveSourceState_NilRun_IsStale(t *testing.T) {
	state := domain.DeriveSourceState(nil, domain.DefaultStaleThreshold)
	if !state.Stale {
		t.Error("expected Stale=true when no sync run exists")
	}
	if state.LastSyncedAt != nil {
		t.Error("expected nil LastSyncedAt")
	}
}

func TestDeriveSourceState_FailedRun_IsStale(t *testing.T) {
	finishedAt := time.Now().Add(-5 * time.Minute)
	run := &domain.SyncRun{
		Status:     domain.SyncStatusFailed,
		ErrorCode:  string(domain.ErrGitHubRateLimit),
		ErrorMsg:   "rate limited",
		FinishedAt: &finishedAt,
	}
	state := domain.DeriveSourceState(run, domain.DefaultStaleThreshold)
	if !state.Stale {
		t.Error("expected Stale=true for failed run")
	}
	if state.ErrorCode != string(domain.ErrGitHubRateLimit) {
		t.Errorf("expected error code %s, got %s", domain.ErrGitHubRateLimit, state.ErrorCode)
	}
	if state.LastSyncedAt == nil {
		t.Error("expected LastSyncedAt to be set")
	}
}

func TestDeriveSourceState_SuccessRecent_NotStale(t *testing.T) {
	finishedAt := time.Now().Add(-5 * time.Minute)
	run := &domain.SyncRun{
		Status:     domain.SyncStatusSuccess,
		FinishedAt: &finishedAt,
	}
	state := domain.DeriveSourceState(run, domain.DefaultStaleThreshold)
	if state.Stale {
		t.Error("expected Stale=false for recent successful run")
	}
}

func TestDeriveSourceState_SuccessOld_IsStale(t *testing.T) {
	finishedAt := time.Now().Add(-60 * time.Minute)
	run := &domain.SyncRun{
		Status:     domain.SyncStatusSuccess,
		FinishedAt: &finishedAt,
	}
	state := domain.DeriveSourceState(run, domain.DefaultStaleThreshold)
	if !state.Stale {
		t.Error("expected Stale=true for old successful run")
	}
}

func TestDeriveSourceState_PartialRecent_NotStale(t *testing.T) {
	finishedAt := time.Now().Add(-1 * time.Minute)
	run := &domain.SyncRun{
		Status:     domain.SyncStatusPartial,
		FinishedAt: &finishedAt,
	}
	state := domain.DeriveSourceState(run, domain.DefaultStaleThreshold)
	if state.Stale {
		t.Error("expected Stale=false for recent partial run")
	}
}

func TestDeriveSourceState_RunningStatus(t *testing.T) {
	startedAt := time.Now().Add(-2 * time.Minute)
	run := &domain.SyncRun{
		Status:    domain.SyncStatusRunning,
		StartedAt: startedAt,
	}
	state := domain.DeriveSourceState(run, domain.DefaultStaleThreshold)
	if state.Stale {
		t.Error("expected Stale=false while sync is running")
	}
	if state.LastSyncedAt == nil {
		t.Error("expected LastSyncedAt to be set to StartedAt for running sync")
	}
}

func TestDeriveSourceState_SuccessNilFinishedAt_IsStale(t *testing.T) {
	run := &domain.SyncRun{
		Status:     domain.SyncStatusSuccess,
		FinishedAt: nil,
	}
	state := domain.DeriveSourceState(run, domain.DefaultStaleThreshold)
	if !state.Stale {
		t.Error("expected Stale=true when FinishedAt is nil")
	}
}

func TestDeriveSourceState_CustomThreshold(t *testing.T) {
	finishedAt := time.Now().Add(-10 * time.Minute)
	run := &domain.SyncRun{
		Status:     domain.SyncStatusSuccess,
		FinishedAt: &finishedAt,
	}
	// With a 5-minute threshold the 10-minute-old sync should be stale.
	state := domain.DeriveSourceState(run, 5*time.Minute)
	if !state.Stale {
		t.Error("expected Stale=true with 5-minute threshold and 10-minute-old sync")
	}
	// With a 60-minute threshold it should not be stale.
	state = domain.DeriveSourceState(run, 60*time.Minute)
	if state.Stale {
		t.Error("expected Stale=false with 60-minute threshold and 10-minute-old sync")
	}
}

func TestFreshState(t *testing.T) {
	now := time.Now()
	state := domain.FreshState(now)
	if state.Stale {
		t.Error("expected Stale=false")
	}
	if state.LastSyncedAt == nil || !state.LastSyncedAt.Equal(now) {
		t.Errorf("expected LastSyncedAt=%v, got %v", now, state.LastSyncedAt)
	}
}

func TestStaleState_WithTime(t *testing.T) {
	now := time.Now()
	state := domain.StaleState(&now)
	if !state.Stale {
		t.Error("expected Stale=true")
	}
	if state.LastSyncedAt == nil {
		t.Error("expected LastSyncedAt to be set")
	}
}

func TestStaleState_NilTime(t *testing.T) {
	state := domain.StaleState(nil)
	if !state.Stale {
		t.Error("expected Stale=true")
	}
	if state.LastSyncedAt != nil {
		t.Error("expected nil LastSyncedAt")
	}
}

func TestUnavailableState(t *testing.T) {
	state := domain.UnavailableState(string(domain.ErrGitHubNotFound), "repo not found")
	if !state.Stale {
		t.Error("expected Stale=true")
	}
	if state.ErrorCode != string(domain.ErrGitHubNotFound) {
		t.Errorf("expected error code %s, got %s", domain.ErrGitHubNotFound, state.ErrorCode)
	}
}

func TestFailedState(t *testing.T) {
	syncedAt := time.Now().Add(-20 * time.Minute)
	state := domain.FailedState(&syncedAt, string(domain.ErrDatabaseConnection), "connection refused")
	if !state.Stale {
		t.Error("expected Stale=true")
	}
	if state.LastSyncedAt == nil {
		t.Error("expected LastSyncedAt to be set")
	}
	if state.ErrorCode != string(domain.ErrDatabaseConnection) {
		t.Errorf("expected error code, got: %s", state.ErrorCode)
	}
}
