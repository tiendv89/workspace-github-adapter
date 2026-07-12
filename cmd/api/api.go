package api

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/tiendv89/workspace-github-adapter/configs"
)

var Command = &cobra.Command{
	Use:   "api",
	Short: "Start the adapter HTTP service",
	RunE:  runServe,
}

func runServe(_ *cobra.Command, _ []string) error {
	cfg := configs.G

	if cfg.API.HTTP.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	router.HandleMethodNotAllowed = true
	router.Use(gin.Recovery())

	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

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
