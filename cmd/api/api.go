package api

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
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	dbadapter "github.com/tiendv89/workspace-github-adapter/internal/adapter/db"
	ghadapter "github.com/tiendv89/workspace-github-adapter/internal/github"

	"github.com/tiendv89/workspace-github-adapter/configs"
	"github.com/tiendv89/workspace-github-adapter/internal/database"
	"github.com/tiendv89/workspace-github-adapter/internal/handler"
	"github.com/tiendv89/workspace-github-adapter/pkg/queue"
)

var Command = &cobra.Command{
	Use:   "api",
	Short: "Start the adapter HTTP service",
	RunE:  runServe,
}

func runServe(_ *cobra.Command, _ []string) error {
	cfg := configs.G

	if cfg.GitHub.WebhookSecret == "" {
		log.Fatal().Msg("GITHUB_WEBHOOK_SECRET is required for api webhooks")
	}

	redisOpt := queue.RedisOpt(cfg.Redis.Addr())
	client := asynq.NewClient(redisOpt)
	defer func() { _ = client.Close() }()

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
		Addr:         cfg.API.HTTP.Address,
		Handler:      mux,
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Info().Str("addr", cfg.API.HTTP.Address).Msg("api listening")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("listen")
		}
	}()

	<-done
	log.Info().Msg("api: shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("shutdown")
	}
	return nil
}
