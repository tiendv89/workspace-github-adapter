package worker

import (
	"context"
	"time"

	"github.com/hibiken/asynq"
	"github.com/rs/zerolog/log"
)

// CleanupInspector is the subset of asynq.Inspector used by the queue cleanup job.
type CleanupInspector interface {
	Queues() ([]string, error)
	ListArchivedTasks(qname string, opts ...asynq.ListOption) ([]*asynq.TaskInfo, error)
	DeleteTask(qname string, id string) error
	Close() error
}

func (h *Handler) openCleanupInspector() CleanupInspector {
	if h.NewCleanupInspector != nil {
		return h.NewCleanupInspector()
	}
	return asynq.NewInspector(h.RedisOpt)
}

// RunQueueCleanup periodically deletes archived (permanently-failed) tasks
// older than retention. Sync/import jobs enqueue with a fixed asynq.TaskID per
// workspace, so an archived task with that ID makes every later enqueue fail
// with ErrTaskIDConflict — surfaced to the caller as 202 "already_queued" and
// leaving the workspace stuck unable to sync. Clearing stale archived tasks
// frees those IDs so syncs can run again.
//
// Blocks until ctx is cancelled; runs once immediately, then every interval.
func (h *Handler) RunQueueCleanup(ctx context.Context, interval, retention time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	if retention < 0 {
		retention = 0
	}
	log.Info().Dur("interval", interval).Dur("archived_retention", retention).Msg("queue cleanup started")
	h.cleanupArchivedOnce(0)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("queue cleanup stopped")
			return
		case <-ticker.C:
			h.cleanupArchivedOnce(retention)
		}
	}
}

// cleanupArchivedOnce deletes archived tasks last-failed before now-retention
// across every queue. IDs are collected before deletion to avoid paging skew.
func (h *Handler) cleanupArchivedOnce(retention time.Duration) {
	insp := h.openCleanupInspector()
	defer func() { _ = insp.Close() }()

	queues, err := insp.Queues()
	if err != nil {
		log.Warn().Err(err).Msg("queue cleanup: list queues failed")
		return
	}

	cutoff := time.Now().Add(-retention)
	const pageSize = 100
	total := 0

	for _, q := range queues {
		var stale []string
		for page := 1; ; page++ {
			tasks, err := insp.ListArchivedTasks(q, asynq.Page(page), asynq.PageSize(pageSize))
			if err != nil {
				log.Warn().Err(err).Str("queue", q).Msg("queue cleanup: list archived failed")
				break
			}
			if len(tasks) == 0 {
				break
			}
			for _, t := range tasks {
				// Keep tasks that failed recently (within the retention window)
				// so a transient failure isn't wiped mid-investigation.
				if t.LastFailedAt.IsZero() || t.LastFailedAt.Before(cutoff) {
					stale = append(stale, t.ID)
				}
			}
			if len(tasks) < pageSize {
				break
			}
		}
		for _, id := range stale {
			if err := insp.DeleteTask(q, id); err != nil {
				log.Debug().Err(err).Str("queue", q).Str("task_id", id).Msg("queue cleanup: delete failed")
				continue
			}
			total++
		}
	}

	if total > 0 {
		log.Info().Int("deleted", total).Msg("queue cleanup: removed stale archived tasks")
	}
}
