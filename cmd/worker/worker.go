package worker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	dbadapter "github.com/tiendv89/workspace-github-adapter/internal/adapter/db"
	ghadapter "github.com/tiendv89/workspace-github-adapter/internal/github"

	"github.com/tiendv89/workspace-github-adapter/configs"
	"github.com/tiendv89/workspace-github-adapter/internal/database"
	internalworker "github.com/tiendv89/workspace-github-adapter/internal/worker"
	"github.com/tiendv89/workspace-github-adapter/pkg/queue"
)

var Command = &cobra.Command{
	Use:   "worker",
	Short: "Start the adapter Asynq worker",
	RunE:  runWork,
}

func runWork(_ *cobra.Command, _ []string) error {
	cfg := configs.G

	redisOpt := queue.RedisOpt(cfg.Redis.Addr())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DB.DSN())
	if err != nil {
		return fmt.Errorf("pgxpool.New: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}

	ghAdapter := ghadapter.New(cfg.GitHub.Token)
	if cfg.GitHub.Token == "" {
		log.Warn().Msg("github.token is empty — no GitHub credentials configured")
	} else {
		count := 0
		for _, t := range strings.Split(cfg.GitHub.Token, ",") {
			if strings.TrimSpace(t) != "" {
				count++
			}
		}
		log.Info().Int("token_count", count).Msg("github adapter initialised")
	}

	h := &internalworker.Handler{
		DB:       dbadapter.New(pool),
		Q:        database.New(pool),
		GitHub:   ghAdapter,
		RedisOpt: redisOpt,
	}

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: 5,
		Queues: map[string]int{
			queue.QueueDefault:  1,
			queue.QueueTaskSync: 3,
		},
	})

	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeWorkspaceSync, h.HandleWorkspaceSync)
	mux.HandleFunc(queue.TypeTaskSync, h.HandleTaskSync)

	// Periodically clear stale archived (permanently-failed) tasks so their
	// fixed per-workspace TaskIDs stop blocking re-enqueue ("already_queued").
	// Tunables live under `queue:` in config.yaml.
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	defer cleanupCancel()
	go h.RunQueueCleanup(cleanupCtx, cfg.QueueCleanupInterval(), cfg.QueueArchivedRetention())

	log.Info().Msg("worker listening for Redis queue tasks")
	if err := srv.Run(mux); err != nil {
		return fmt.Errorf("worker stopped: %w", err)
	}
	return nil
}
