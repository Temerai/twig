package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Temerai/twig/internal/config"
	"github.com/Temerai/twig/internal/version"
)

// cfg holds the loaded application configuration, populated in PersistentPreRun.
var cfg *config.Config

func main() {
	rootCmd := &cobra.Command{
		Use:          "twig",
		Short:        "twig - codebase graph for token-efficient LLM context",
		Long:         "twig parses your codebase into a graph with Tree-sitter and serves only the relevant code snippets to your LLM via MCP. Less tokens, same awareness.",
		Version:      version.Version,
		SilenceUsage: true,
	}
	rootCmd.SetVersionTemplate(fmt.Sprintf(
		"twig %s (commit: %s, built: %s)\n",
		version.Version, version.Commit, version.BuildDate,
	))

	var configFile string
	rootCmd.PersistentFlags().StringVar(&configFile, "config", "config.yaml", "path to config file")

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		// Change to the directory containing the config file so that relative
		// paths in the config (db_path, codebase_root) resolve correctly.
		if configFile != "config.yaml" {
			dir := filepath.Dir(configFile)
			if dir != "." && dir != "" {
				if err := os.Chdir(dir); err != nil {
					return fmt.Errorf("changing to config directory %s: %w", dir, err)
				}
			}
		}

		var err error
		cfg, err = config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		return nil
	}

	rootCmd.AddCommand(
		newIndexCmd(),
		newRunCmd(),
		newGraphCmd(),
		newLogCmd(),
		newEvalCmd(),
		newServeCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
