package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
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
	if err = pool.Ping(ctx); err != nil {
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

	h := &handler.ServiceHandler{
		DB:            dbadapter.New(pool),
		Q:             database.New(pool),
		Pool:          pool,
		GitHub:        ghAdapter,
		Queue:         client,
		WebhookSecret: cfg.GitHub.WebhookSecret,
	}

	if cfg.API.HTTP.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	router.HandleMethodNotAllowed = true
	router.Use(gin.Recovery())

	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.POST("/internal/workspaces/import", h.ImportWorkspaceHandler)
	router.POST("/internal/workspaces/:id/sync", h.SyncWorkspaceHandler)
	router.POST("/webhook", h.WebhookHandler)

	srv := &http.Server{
		Addr:         cfg.API.HTTP.Address,
		Handler:      router,
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
