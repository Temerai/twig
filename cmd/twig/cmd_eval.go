package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Temerai/twig/internal/eval"
)

// --- eval command ---

func newEvalCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "eval <fixtures.yaml>",
		Short: "Run the eval harness against fixture definitions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fixturesPath := args[0]

			comp, err := initComponents()
			if err != nil {
				return err
			}
			defer comp.Close()

			fixtures, err := eval.LoadFixtures(fixturesPath)
			if err != nil {
				return fmt.Errorf("loading fixtures: %w", err)
			}

			harness := eval.NewHarness(comp.orch)

			ctx := cmd.Context()
			results, err := harness.RunEval(ctx, fixtures)
			if err != nil {
				return fmt.Errorf("running eval: %w", err)
			}

			eval.PrintResults(results)
			return nil
		},
	}
}
