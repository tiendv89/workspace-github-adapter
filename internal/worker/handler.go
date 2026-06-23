package worker

import (
	"github.com/hibiken/asynq"

	"github.com/tiendv89/workspace-github-adapter/internal/database"
	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

// PendingTaskInspector is the interface used to inspect and delete pending Asynq tasks.
type PendingTaskInspector interface {
	ListPendingTasks(queue string, opts ...asynq.ListOption) ([]*asynq.TaskInfo, error)
	DeleteTask(queue string, id string) error
	Close() error
}

// Handler holds shared dependencies for all worker task handlers.
type Handler struct {
	DB                      domain.DbWorkspaceAdapter
	Q                       *database.Queries
	GitHub                  domain.GitHubWorkspaceAdapter
	RedisOpt                asynq.RedisConnOpt
	NewPendingTaskInspector func() PendingTaskInspector
	NewCleanupInspector     func() CleanupInspector
}

func (h *Handler) openPendingTaskInspector() PendingTaskInspector {
	if h.NewPendingTaskInspector != nil {
		return h.NewPendingTaskInspector()
	}
	return asynq.NewInspector(h.RedisOpt)
}
