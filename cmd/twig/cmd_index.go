package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Temerai/twig/internal/parser"
)

func newIndexCmd() *cobra.Command {
	var files string

	cmd := &cobra.Command{
		Use:   "index <path>",
		Short: "Build or update the codebase graph index",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rootPath := args[0]

			store, err := parser.NewStore(parser.DBPathForRoot(rootPath))
			if err != nil {
				return fmt.Errorf("opening store: %w", err)
			}
			defer store.Close()

			indexer := parser.NewIndexer(store, rootPath)

			if files != "" {
				parts := strings.Split(files, ",")
				var changedFiles []string
				for _, f := range parts {
					f = strings.TrimSpace(f)
					if f != "" {
						changedFiles = append(changedFiles, f)
					}
				}
				if len(changedFiles) == 0 {
					return fmt.Errorf("--files specified but no valid file paths provided")
				}
				fmt.Printf("Reindexing %d file(s)...\n", len(changedFiles))
				if err := indexer.Reindex(changedFiles); err != nil {
					return fmt.Errorf("reindexing: %w", err)
				}
			} else {
				fmt.Printf("Indexing %s...\n", rootPath)
				if err := indexer.Index(rootPath); err != nil {
					return fmt.Errorf("indexing: %w", err)
				}
			}

			nodeCount, edgeCount, err := store.Stats()
			if err != nil {
				return fmt.Errorf("reading stats: %w", err)
			}
			fmt.Printf("Index complete: %d nodes, %d edges\n", nodeCount, edgeCount)
			return nil
		},
	}

	cmd.Flags().StringVar(&files, "files", "", "comma-separated list of files to reindex incrementally")
	return cmd
}
