package handler

import (
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tiendv89/workspace-github-adapter/internal/database"
	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

// TaskEnqueuer is the minimal interface required to enqueue Asynq tasks.
type TaskEnqueuer interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// ServiceHandler holds shared dependencies for all HTTP handlers.
type ServiceHandler struct {
	DB            domain.DbWorkspaceAdapter
	Q             *database.Queries
	Pool          *pgxpool.Pool
	GitHub        domain.GitHubWorkspaceAdapter
	Token         string
	Queue         TaskEnqueuer
	WebhookSecret string
}
