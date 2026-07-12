package worker

import "github.com/hibiken/asynq"

// Handler holds shared dependencies for worker task handlers.
type Handler struct {
	RedisOpt            asynq.RedisConnOpt
	NewCleanupInspector func() CleanupInspector
}
