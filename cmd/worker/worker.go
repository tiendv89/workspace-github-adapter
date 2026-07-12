package worker

import (
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var Command = &cobra.Command{
	Use:   "worker",
	Short: "Start the adapter Asynq worker",
	RunE:  runWork,
}

func runWork(_ *cobra.Command, _ []string) error {
	log.Info().Msg("workspace-github-adapter worker is decommissioned — no jobs to process")
	return nil
}
