package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Temerai/twig/internal/parser"
)

func newIndexCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "index <path>",
		Short: "Build or update the codebase graph index",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rootPath := args[0]

			store, err := parser.NewStore(cfg.DBPath)
			if err != nil {
				return fmt.Errorf("opening store: %w", err)
			}
			defer store.Close()

			indexer := parser.NewIndexer(store, rootPath)

			fmt.Printf("Indexing %s...\n", rootPath)
			if err := indexer.Index(rootPath); err != nil {
				return fmt.Errorf("indexing: %w", err)
			}

			nodeCount, edgeCount, err := store.Stats()
			if err != nil {
				return fmt.Errorf("reading stats: %w", err)
			}
			fmt.Printf("Index complete: %d nodes, %d edges\n", nodeCount, edgeCount)
			return nil
		},
	}
}
