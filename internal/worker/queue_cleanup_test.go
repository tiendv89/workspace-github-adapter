package worker

import (
	"testing"
	"time"

	"github.com/hibiken/asynq"
)

type fakeCleanupInspector struct {
	archived map[string][]*asynq.TaskInfo
	deleted  []string
	closed   bool
}

func (f *fakeCleanupInspector) Queues() ([]string, error) {
	qs := make([]string, 0, len(f.archived))
	for q := range f.archived {
		qs = append(qs, q)
	}
	return qs, nil
}

func (f *fakeCleanupInspector) ListArchivedTasks(qname string, opts ...asynq.ListOption) ([]*asynq.TaskInfo, error) {
	// Single-page fake (callers page until a short page); return all then empty.
	return f.archived[qname], nil
}

func (f *fakeCleanupInspector) DeleteTask(qname string, id string) error {
	f.deleted = append(f.deleted, id)
	// Drop it so a re-list wouldn't return it again.
	tasks := f.archived[qname][:0]
	for _, t := range f.archived[qname] {
		if t.ID != id {
			tasks = append(tasks, t)
		}
	}
	f.archived[qname] = tasks
	return nil
}

func (f *fakeCleanupInspector) DeleteAllArchivedTasks(qname string) (int, error) {
	n := len(f.archived[qname])
	f.archived[qname] = nil
	return n, nil
}

func (f *fakeCleanupInspector) DeleteAllRetryTasks(qname string) (int, error)     { return 0, nil }
func (f *fakeCleanupInspector) DeleteAllScheduledTasks(qname string) (int, error) { return 0, nil }
func (f *fakeCleanupInspector) DeleteAllPendingTasks(qname string) (int, error)   { return 0, nil }

func (f *fakeCleanupInspector) Close() error { f.closed = true; return nil }

func TestCleanupArchivedOnce_DeletesOnlyStale(t *testing.T) {
	now := time.Now()
	fake := &fakeCleanupInspector{
		archived: map[string][]*asynq.TaskInfo{
			"task-sync": {
				{ID: "old-1", LastFailedAt: now.Add(-3 * time.Hour)},      // stale → delete
				{ID: "recent-1", LastFailedAt: now.Add(-5 * time.Minute)}, // within retention → keep
				{ID: "zero", LastFailedAt: time.Time{}},                   // unknown → treated as stale
			},
		},
	}
	h := &Handler{NewCleanupInspector: func() CleanupInspector { return fake }}

	h.cleanupArchivedOnce(time.Hour)

	got := map[string]bool{}
	for _, id := range fake.deleted {
		got[id] = true
	}
	if !got["old-1"] {
		t.Errorf("expected stale task old-1 deleted, deleted=%v", fake.deleted)
	}
	if !got["zero"] {
		t.Errorf("expected zero-LastFailedAt task deleted, deleted=%v", fake.deleted)
	}
	if got["recent-1"] {
		t.Errorf("recent task recent-1 should be kept, deleted=%v", fake.deleted)
	}
	if !fake.closed {
		t.Error("inspector should be closed")
	}
}

func TestCleanupArchivedOnce_ZeroRetentionDeletesAll(t *testing.T) {
	now := time.Now()
	fake := &fakeCleanupInspector{
		archived: map[string][]*asynq.TaskInfo{
			"default": {
				{ID: "a", LastFailedAt: now.Add(-1 * time.Minute)},
				{ID: "b", LastFailedAt: now.Add(-1 * time.Second)},
			},
		},
	}
	h := &Handler{NewCleanupInspector: func() CleanupInspector { return fake }}

	h.cleanupArchivedOnce(0)

	if len(fake.deleted) != 2 {
		t.Errorf("expected all archived deleted with 0 retention, got %v", fake.deleted)
	}
}
