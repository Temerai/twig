package orchestrator

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Temerai/twig/internal/graphagent"
	"github.com/Temerai/twig/internal/logger"
	"github.com/Temerai/twig/internal/registry"
	"github.com/Temerai/twig/internal/types"
)

// Orchestrator is a context assembler: it gathers graph context, renders a
// prompt template, and returns the assembled text. No LLM calls are made;
// users run Cortex alongside an external LLM (Claude Code, Copilot, etc.).
type Orchestrator struct {
	agent *graphagent.GraphAgent
	reg   *registry.Registry
	log   *logger.Logger
}

// NewOrchestrator creates an Orchestrator wired to the given graph agent,
// prompt registry, and logger.
func NewOrchestrator(agent *graphagent.GraphAgent, reg *registry.Registry, log *logger.Logger) *Orchestrator {
	return &Orchestrator{
		agent: agent,
		reg:   reg,
		log:   log,
	}
}

// Run assembles context for a task: it selects and renders a prompt template,
// queries the graph agent for relevant code snippets, and returns the combined
// output as plain text. No LLM calls are made.
func (o *Orchestrator) Run(ctx context.Context, task types.Task) (*types.Result, error) {
	start := time.Now()

	// 1. Select prompt template: explicit version or latest.
	var pt *registry.PromptTemplate
	var err error

	if v, ok := task.Options["prompt_version"]; ok {
		version, convErr := strconv.Atoi(v)
		if convErr != nil {
			return nil, fmt.Errorf("invalid prompt_version %q: %w", v, convErr)
		}
		pt, err = o.reg.Get(task.Type, version)
	} else {
		pt, err = o.reg.Latest(task.Type)
	}
	if err != nil {
		return nil, fmt.Errorf("selecting prompt template: %w", err)
	}

	// 2. Render the template with the task input.
	systemPrompt, userMessage, err := pt.Render(task.Input)
	if err != nil {
		return nil, fmt.Errorf("rendering prompt template: %w", err)
	}

	// 3. Query the graph agent for context relevant to the task input.
	tokenBudget := 4000
	if v, ok := task.Options["token_budget"]; ok {
		if parsed, convErr := strconv.Atoi(v); convErr == nil && parsed > 0 {
			tokenBudget = parsed
		}
	}

	req := types.QueryRequest{
		NaturalLanguage: task.Input,
		TokenBudget:     tokenBudget,
		Strategy:        types.StrategyScored,
	}

	var graphQueries []types.QueryRequest
	var graphQLogs []logger.GraphQueryLog

	graphQueries = append(graphQueries, req)

	queryResult, queryErr := o.agent.Query(ctx, req)

	gqLog := logger.GraphQueryLog{
		Question:    task.Input,
		TokenBudget: tokenBudget,
		Strategy:    string(types.StrategyScored),
	}
	if queryErr == nil && queryResult != nil {
		gqLog.TokensUsed = queryResult.TokensUsed
		gqLog.NodeCount = len(queryResult.NodesVisited)
	}
	graphQLogs = append(graphQLogs, gqLog)

	// 4. Format the graph context section.
	contextSection := "No relevant context found."
	tokensFromSnippets := 0
	if queryErr == nil && queryResult != nil && len(queryResult.Snippets) > 0 {
		contextSection = formatQueryResult(queryResult)
		tokensFromSnippets = queryResult.TokensUsed
	}

	// 5. Assemble the final output.
	var sb strings.Builder
	sb.WriteString("=== System Prompt ===\n")
	sb.WriteString(systemPrompt)
	sb.WriteString("\n\n=== User Prompt ===\n")
	sb.WriteString(userMessage)
	sb.WriteString("\n\n=== Codebase Context (from graph) ===\n")
	sb.WriteString(contextSection)

	output := sb.String()
	latencyMs := time.Since(start).Milliseconds()

	// 6. Log the run.
	record := logger.RunRecord{
		TaskType:      task.Type,
		PromptVersion: pt.Version,
		Model:         "none",
		Input:         task.Input,
		Output:        output,
		GraphQueries:  graphQLogs,
		TokensIn:      tokensFromSnippets,
		TokensOut:     0,
		LatencyMs:     latencyMs,
		CreatedAt:     time.Now(),
	}
	if logErr := o.log.Write(record); logErr != nil {
		// Log write failures are non-fatal; continue returning the result.
		_ = logErr
	}

	// 7. Return the result.
	return &types.Result{
		Output:       output,
		GraphQueries: graphQueries,
		TokensIn:     tokensFromSnippets,
		TokensOut:    0,
		LatencyMs:    latencyMs,
	}, nil
}

// formatQueryResult converts a QueryResult into a human-readable string
// suitable for returning as a tool result to the model.
func formatQueryResult(qr *types.QueryResult) string {
	if qr == nil || len(qr.Snippets) == 0 {
		return "No relevant code snippets found."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d code snippet(s) (%d tokens used):\n\n", len(qr.Snippets), qr.TokensUsed))

	for i, snippet := range qr.Snippets {
		sb.WriteString(fmt.Sprintf("--- Snippet %d: %s (%s lines %s) ---\n", i+1, snippet.NodeName, snippet.FilePath, snippet.LineRange))
		sb.WriteString(snippet.SourceText)
		sb.WriteString("\n\n")
	}

	return sb.String()
}
