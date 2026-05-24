package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	dbadapter "github.com/tiendv89/workspace-github-adapter/pkg/adapter/db"
	ghadapter "github.com/tiendv89/workspace-github-adapter/pkg/github"
	"github.com/tiendv89/workspace-github-adapter/pkg/queue"

	"github.com/tiendv89/workspace-github-adapter/configs"
	"github.com/tiendv89/workspace-github-adapter/internal/database"
	"github.com/tiendv89/workspace-github-adapter/internal/worker"
)

func main() {
	var cfgPath string

	root := &cobra.Command{Use: "adapter-worker"}
	root.PersistentFlags().StringVarP(&cfgPath, "config", "c", "configs/config.yaml", "path to config YAML")

	root.AddCommand(workCmd(&cfgPath))
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func workCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "work",
		Short: "Start the adapter Asynq worker",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWork(*cfgPath)
		},
	}
}

func runWork(cfgPath string) error {
	cfg, err := configs.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	level, err := zerolog.ParseLevel(cfg.Log.Level)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)

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

	h := &worker.Handler{
		DB:       dbadapter.New(pool),
		Q:        database.New(pool),
		GitHub:   ghadapter.New(cfg.GitHub.Token),
		Token:    cfg.GitHub.Token,
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

	log.Info().Msg("adapter-worker listening for Redis queue tasks")
	if err := srv.Run(mux); err != nil {
		return fmt.Errorf("worker stopped: %w", err)
	}
	return nil
}
