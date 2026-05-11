package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Temerai/twig/internal/config"
	"github.com/Temerai/twig/internal/eval"
	"github.com/Temerai/twig/internal/graphagent"
	"github.com/Temerai/twig/internal/graphintel"
	"github.com/Temerai/twig/internal/logger"
	"github.com/Temerai/twig/internal/mcp"
	"github.com/Temerai/twig/internal/orchestrator"
	"github.com/Temerai/twig/internal/parser"
	"github.com/Temerai/twig/internal/registry"
	"github.com/Temerai/twig/internal/types"
	"github.com/Temerai/twig/internal/version"
)

// cfg holds the loaded application configuration, populated in PersistentPreRun.
var cfg *config.Config

// resolveInput reads the input value for the run command.
// If the string starts with "@", the remainder is treated as a file path
// whose contents are returned. Otherwise the string is used as-is.
func resolveInput(input string) (string, error) {
	if strings.HasPrefix(input, "@") {
		path := input[1:]
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("reading input file %s: %w", path, err)
		}
		return string(data), nil
	}
	return input, nil
}

// initComponents creates the full set of runtime components that most commands
// need: store, indexer, graph agent, graph intel, registry, logger, and
// orchestrator. The caller is responsible for closing the store and logger.
type components struct {
	store   *parser.Store
	indexer *parser.Indexer
	agent   *graphagent.GraphAgent
	intel   *graphintel.GraphIntel
	reg     *registry.Registry
	log     *logger.Logger
	orch    *orchestrator.Orchestrator
}

func initComponents() (*components, error) {
	store, err := parser.NewStore(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("opening store: %w", err)
	}

	indexer := parser.NewIndexer(store, cfg.CodebaseRoot)
	agent := graphagent.NewGraphAgent(store)
	intel := graphintel.NewGraphIntel(store)

	promptDir := filepath.Join(cfg.CodebaseRoot, "config", "prompts")
	reg, err := registry.NewRegistry(promptDir)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("loading prompt registry: %w", err)
	}

	log, err := logger.NewLogger(cfg.DBPath + ".log")
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("opening logger: %w", err)
	}

	orch := orchestrator.NewOrchestrator(agent, reg, log)

	return &components{
		store:   store,
		indexer: indexer,
		agent:   agent,
		intel:   intel,
		reg:     reg,
		log:     log,
		orch:    orch,
	}, nil
}

func (c *components) Close() {
	if c.log != nil {
		c.log.Close()
	}
	if c.store != nil {
		c.store.Close()
	}
}

type graphComponents struct {
	store *parser.Store
	intel *graphintel.GraphIntel
}

func initGraphComponents() (*graphComponents, error) {
	store, err := parser.NewStore(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("opening store: %w", err)
	}
	intel := graphintel.NewGraphIntel(store)
	return &graphComponents{store: store, intel: intel}, nil
}

func (gc *graphComponents) Close() {
	if gc.store != nil {
		gc.store.Close()
	}
}

// serveComponents holds the subset of components needed by the MCP server:
// store, indexer, graph agent, and graph intel. No registry, logger, or
// orchestrator required.
type serveComponents struct {
	store   *parser.Store
	indexer *parser.Indexer
	agent   *graphagent.GraphAgent
	intel   *graphintel.GraphIntel
}

func initServeComponents() (*serveComponents, error) {
	store, err := parser.NewStore(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("opening store: %w", err)
	}
	indexer := parser.NewIndexer(store, cfg.CodebaseRoot)
	agent := graphagent.NewGraphAgent(store)
	intel := graphintel.NewGraphIntel(store)
	return &serveComponents{
		store:   store,
		indexer: indexer,
		agent:   agent,
		intel:   intel,
	}, nil
}

func (sc *serveComponents) Close() {
	if sc.store != nil {
		sc.store.Close()
	}
}

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

// --- index command ---

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

// --- run command ---

func newRunCmd() *cobra.Command {
	var (
		input         string
		tokenBudget   int
		promptVersion int
	)

	cmd := &cobra.Command{
		Use:   "run <task_type>",
		Short: "Assemble a prompt with graph context (code_review, test_gen, explain, find_bug)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskType := args[0]

			resolvedInput, err := resolveInput(input)
			if err != nil {
				return err
			}

			comp, err := initComponents()
			if err != nil {
				return err
			}
			defer comp.Close()

			task := types.Task{
				Type:    taskType,
				Input:   resolvedInput,
				Options: make(map[string]string),
			}

			if tokenBudget > 0 {
				task.Options["token_budget"] = strconv.Itoa(tokenBudget)
			}
			if promptVersion > 0 {
				task.Options["prompt_version"] = strconv.Itoa(promptVersion)
			}

			ctx := cmd.Context()
			result, err := comp.orch.Run(ctx, task)
			if err != nil {
				return fmt.Errorf("running task: %w", err)
			}

			fmt.Println(result.Output)
			fmt.Println("\n--- Assembled prompt ready. Paste into Claude Code or your preferred LLM. ---")

			return nil
		},
	}

	cmd.Flags().StringVar(&input, "input", "", "input text or @filepath (required)")
	cmd.MarkFlagRequired("input")
	cmd.Flags().IntVar(&tokenBudget, "token-budget", 0, "token budget for graph queries")
	cmd.Flags().IntVar(&promptVersion, "prompt-version", 0, "prompt template version to use")

	return cmd
}

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
			log, err := logger.NewLogger(cfg.DBPath + ".log")
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
