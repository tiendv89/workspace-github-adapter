package main

import (
	"context"
	"log"
	"time"

	rundb "github.com/tiendv89/workspace-github-adapter/database"
	"github.com/tiendv89/workspace-github-adapter/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := rundb.RunMigrations(ctx, cfg.DatabaseURL); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	log.Println("migrations applied")
}
