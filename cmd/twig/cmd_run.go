package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Temerai/twig/internal/types"
)

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
