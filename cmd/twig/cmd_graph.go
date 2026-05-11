package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// --- graph command group ---

func newGraphCmd() *cobra.Command {
	graphCmd := &cobra.Command{
		Use:   "graph",
		Short: "Query the codebase graph",
	}

	graphCmd.AddCommand(
		newGraphQueryCmd(),
		newGraphCallersCmd(),
		newGraphCalleesCmd(),
		newGraphDepsCmd(),
		newGraphImpactCmd(),
	)

	return graphCmd
}

func newGraphQueryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "query <question>",
		Short: "Natural language graph query",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			question := args[0]

			gc, err := initGraphComponents()
			if err != nil {
				return err
			}
			defer gc.Close()

			ctx := cmd.Context()
			answer, err := gc.intel.NaturalQuery(ctx, question)
			if err != nil {
				return fmt.Errorf("graph query: %w", err)
			}

			fmt.Println(answer.Summary)
			if len(answer.Nodes) > 0 {
				fmt.Printf("\nNodes found (%d):\n", len(answer.Nodes))
				for _, n := range answer.Nodes {
					fmt.Printf("  %-30s  %s:%s  [%s]\n", n.Name, n.File, n.Lines, n.Kind)
				}
			}
			return nil
		},
	}
}

func newGraphCallersCmd() *cobra.Command {
	var depth int

	cmd := &cobra.Command{
		Use:   "callers <symbol>",
		Short: "Find callers of a symbol",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			symbol := args[0]

			gc, err := initGraphComponents()
			if err != nil {
				return err
			}
			defer gc.Close()

			ctx := cmd.Context()
			nodes, err := gc.intel.Callers(ctx, symbol, depth)
			if err != nil {
				return fmt.Errorf("callers: %w", err)
			}

			if len(nodes) == 0 {
				fmt.Printf("No callers found for %s\n", symbol)
				return nil
			}

			fmt.Printf("Callers of %s (depth %d):\n", symbol, depth)
			for _, n := range nodes {
				fmt.Printf("  %-30s  %s:%s  [%s]\n", n.Name, n.File, n.Lines, n.Kind)
			}
			return nil
		},
	}

	cmd.Flags().IntVar(&depth, "depth", 3, "traversal depth")
	return cmd
}

func newGraphCalleesCmd() *cobra.Command {
	var depth int

	cmd := &cobra.Command{
		Use:   "callees <symbol>",
		Short: "Find callees of a symbol",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			symbol := args[0]

			gc, err := initGraphComponents()
			if err != nil {
				return err
			}
			defer gc.Close()

			ctx := cmd.Context()
			nodes, err := gc.intel.Callees(ctx, symbol, depth)
			if err != nil {
				return fmt.Errorf("callees: %w", err)
			}

			if len(nodes) == 0 {
				fmt.Printf("No callees found for %s\n", symbol)
				return nil
			}

			fmt.Printf("Callees of %s (depth %d):\n", symbol, depth)
			for _, n := range nodes {
				fmt.Printf("  %-30s  %s:%s  [%s]\n", n.Name, n.File, n.Lines, n.Kind)
			}
			return nil
		},
	}

	cmd.Flags().IntVar(&depth, "depth", 3, "traversal depth")
	return cmd
}

func newGraphDepsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deps <symbol>",
		Short: "Show dependency chain of a symbol",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			symbol := args[0]

			gc, err := initGraphComponents()
			if err != nil {
				return err
			}
			defer gc.Close()

			ctx := cmd.Context()
			nodes, err := gc.intel.Dependencies(ctx, symbol)
			if err != nil {
				return fmt.Errorf("dependencies: %w", err)
			}

			if len(nodes) == 0 {
				fmt.Printf("No dependencies found for %s\n", symbol)
				return nil
			}

			fmt.Printf("Dependencies of %s:\n", symbol)
			for _, n := range nodes {
				fmt.Printf("  %-30s  %s:%s  [%s]\n", n.Name, n.File, n.Lines, n.Kind)
			}
			return nil
		},
	}
}

func newGraphImpactCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "impact <symbol>",
		Short: "Analyze impact of changing a symbol",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			symbol := args[0]

			gc, err := initGraphComponents()
			if err != nil {
				return err
			}
			defer gc.Close()

			ctx := cmd.Context()
			report, err := gc.intel.ImpactOf(ctx, symbol)
			if err != nil {
				return fmt.Errorf("impact analysis: %w", err)
			}

			fmt.Printf("Impact analysis for %s:\n", symbol)
			fmt.Printf("  Risk score:       %d\n", report.RiskScore)
			fmt.Printf("  Direct callers:   %d\n", len(report.DirectCallers))
			fmt.Printf("  Transitive deps:  %d\n", len(report.TransitiveDeps))
			fmt.Printf("  Affected files:   %d\n", len(report.AffectedFiles))

			if len(report.AffectedFiles) > 0 {
				fmt.Println("\n  Affected files:")
				for _, f := range report.AffectedFiles {
					fmt.Printf("    %s\n", f)
				}
			}

			if len(report.DirectCallers) > 0 {
				fmt.Println("\n  Direct callers:")
				for _, n := range report.DirectCallers {
					fmt.Printf("    %-30s  %s:%s\n", n.Name, n.File, n.Lines)
				}
			}

			return nil
		},
	}
}
