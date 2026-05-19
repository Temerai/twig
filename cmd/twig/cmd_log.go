package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Temerai/twig/internal/logger"
	"github.com/Temerai/twig/internal/parser"
)

// --- log command group ---

func newLogCmd() *cobra.Command {
	logCmd := &cobra.Command{
		Use:   "log",
		Short: "View run history",
	}

	logCmd.AddCommand(newLogListCmd())
	return logCmd
}

func newLogListCmd() *cobra.Command {
	var (
		taskFilter string
		last       int
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent runs",
		RunE: func(cmd *cobra.Command, args []string) error {
			log, err := logger.NewLogger(parser.LogPathForRoot(cfg.CodebaseRoot))
			if err != nil {
				return fmt.Errorf("opening logger: %w", err)
			}
			defer log.Close()

			filter := logger.QueryFilter{
				TaskType: taskFilter,
				Limit:    last,
			}

			records, err := log.Query(filter)
			if err != nil {
				return fmt.Errorf("querying logs: %w", err)
			}

			if len(records) == 0 {
				fmt.Println("No runs found.")
				return nil
			}

			fmt.Printf("%-10s %-14s %-8s %-10s %-10s %-10s %s\n",
				"ID", "TASK", "PROMPT", "TOKENS_IN", "TOKENS_OUT", "LATENCY", "CREATED_AT")
			fmt.Println(strings.Repeat("-", 85))

			for _, r := range records {
				id := r.ID
				if len(id) > 8 {
					id = id[:8]
				}
				fmt.Printf("%-10s %-14s v%-7d %-10d %-10d %-8dms %s\n",
					id,
					r.TaskType,
					r.PromptVersion,
					r.TokensIn,
					r.TokensOut,
					r.LatencyMs,
					r.CreatedAt.Format("2006-01-02 15:04:05"),
				)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&taskFilter, "task", "", "filter by task type")
	cmd.Flags().IntVar(&last, "last", 10, "number of recent runs to show")

	return cmd
}
