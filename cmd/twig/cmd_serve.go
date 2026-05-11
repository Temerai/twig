package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Temerai/twig/internal/mcp"
)

// --- serve command ---

func newServeCmd() *cobra.Command {
	var mcpFlag bool

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the twig server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !mcpFlag {
				return fmt.Errorf("--mcp flag is required (only MCP server mode is currently supported)")
			}

			sc, err := initServeComponents()
			if err != nil {
				return err
			}
			defer sc.Close()

			// Set up signal-aware context for graceful shutdown.
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			server := mcp.NewServer(sc.store, sc.indexer, sc.agent, sc.intel)

			fmt.Fprintln(os.Stderr, "twig MCP server started")
			return server.Serve(ctx)
		},
	}

	cmd.Flags().BoolVar(&mcpFlag, "mcp", false, "start in MCP server mode")

	return cmd
}
