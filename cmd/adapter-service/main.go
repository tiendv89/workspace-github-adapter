package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/tiendv89/workspace-github-adapter/configs"
	dbadapter "github.com/tiendv89/workspace-github-adapter/internal/adapter/db"
	"github.com/tiendv89/workspace-github-adapter/internal/database"
	ghadapter "github.com/tiendv89/workspace-github-adapter/internal/github"
	"github.com/tiendv89/workspace-github-adapter/internal/handler"
	"github.com/tiendv89/workspace-github-adapter/internal/queue"
)

func main() {
	var cfgPath string

	root := &cobra.Command{Use: "adapter-service"}
	root.PersistentFlags().StringVarP(&cfgPath, "config", "c", "configs/config.yaml", "path to config YAML")

	root.AddCommand(serveCmd(&cfgPath))
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func serveCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the adapter HTTP service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(*cfgPath)
		},
	}
}

func runServe(cfgPath string) error {
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

	if cfg.GitHub.WebhookSecret == "" {
		log.Fatal().Msg("GITHUB_WEBHOOK_SECRET is required for adapter-service webhooks")
	}

	redisOpt, err := queue.RedisOpt(cfg.Redis.URL)
	if err != nil {
		return fmt.Errorf("redis: %w", err)
	}
	client := asynq.NewClient(redisOpt)
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.Database.URL)
	if err != nil {
		return fmt.Errorf("pgxpool.New: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}

	h := &handler.ServiceHandler{
		DB:            dbadapter.New(pool),
		Q:             database.New(pool),
		Pool:          pool,
		GitHub:        ghadapter.New(cfg.GitHub.Token),
		Token:         cfg.GitHub.Token,
		Queue:         client,
		WebhookSecret: cfg.GitHub.WebhookSecret,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/internal/workspaces/import", h.ImportWorkspaceHandler)
	mux.HandleFunc("/internal/workspaces/", h.InternalWorkspaceHandler)
	mux.HandleFunc("/webhook", h.WebhookHandler)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      mux,
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Info().Int("port", cfg.Server.Port).Msg("adapter-service listening")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("listen")
		}
	}()

	<-done
	log.Info().Msg("adapter-service: shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("shutdown")
	}
	return nil
}
