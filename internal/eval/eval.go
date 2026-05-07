package eval

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/Temerai/twig/internal/orchestrator"
	"github.com/Temerai/twig/internal/types"
)

// Fixture describes a single eval test case loaded from YAML.
type Fixture struct {
	Task   string   `yaml:"task"`
	Input  string   `yaml:"input"`
	Rubric []string `yaml:"rubric"`
}

// CriterionScore records the grading result for a single rubric criterion.
type CriterionScore struct {
	Criterion string `json:"criterion"`
	Score     int    `json:"score"` // 0 or 1
	Reason    string `json:"reason"`
}

// EvalResult holds the comparison between two prompt versions for one fixture.
type EvalResult struct {
	Fixture  Fixture
	VersionA int
	VersionB int
	ScoreA   []CriterionScore
	ScoreB   []CriterionScore
	TotalA   int
	TotalB   int
	OutputA  string
	OutputB  string
}

// Harness drives eval runs: it executes tasks through the orchestrator and
// grades the outputs against rubric criteria using substring matching.
type Harness struct {
	orch *orchestrator.Orchestrator
}

// NewHarness creates an eval Harness backed by the given orchestrator.
func NewHarness(orch *orchestrator.Orchestrator) *Harness {
	return &Harness{
		orch: orch,
	}
}

// LoadFixtures reads a YAML file containing an array of Fixture entries.
func LoadFixtures(path string) ([]Fixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading fixtures file %s: %w", path, err)
	}

	var fixtures []Fixture
	if err := yaml.Unmarshal(data, &fixtures); err != nil {
		return nil, fmt.Errorf("parsing fixtures file %s: %w", path, err)
	}

	return fixtures, nil
}

// RunEval runs each fixture through the orchestrator with two prompt versions
// (version 2 as "latest" and version 1 as "previous"), grades the outputs,
// and returns the comparison results. If the version-1 run fails (e.g. the
// version does not exist), results for that fixture contain only version-2 data.
func (h *Harness) RunEval(ctx context.Context, fixtures []Fixture) ([]EvalResult, error) {
	var results []EvalResult

	for _, fix := range fixtures {
		var res EvalResult
		res.Fixture = fix
		res.VersionA = 2
		res.VersionB = 1

		// Run with version 2 (latest).
		taskA := types.Task{
			Type:    fix.Task,
			Input:   fix.Input,
			Options: map[string]string{"prompt_version": "2"},
		}
		resultA, err := h.orch.Run(ctx, taskA)
		if err != nil {
			return nil, fmt.Errorf("running fixture %q with version 2: %w", fix.Task, err)
		}
		res.OutputA = resultA.Output

		scoresA := h.grade(resultA.Output, fix.Rubric)
		res.ScoreA = scoresA
		res.TotalA = sumScores(scoresA)

		// Run with version 1 (previous). If it fails, report only version 2.
		taskB := types.Task{
			Type:    fix.Task,
			Input:   fix.Input,
			Options: map[string]string{"prompt_version": "1"},
		}
		resultB, err := h.orch.Run(ctx, taskB)
		if err == nil {
			res.OutputB = resultB.Output

			scoresB := h.grade(resultB.Output, fix.Rubric)
			res.ScoreB = scoresB
			res.TotalB = sumScores(scoresB)
		}
		// If version 1 failed, VersionB/ScoreB/TotalB/OutputB remain zero-valued.

		results = append(results, res)
	}

	return results, nil
}

// grade evaluates an output against each rubric criterion using keyword
// matching. For each criterion it extracts words longer than 3 characters,
// normalises everything to lowercase, and scores 1 if at least 60% of those
// keywords appear in the output.
func (h *Harness) grade(output string, rubric []string) []CriterionScore {
	normalizedOutput := strings.ToLower(output)

	var scores []CriterionScore
	for _, criterion := range rubric {
		keywords := extractKeywords(criterion)

		matched := 0
		for _, kw := range keywords {
			if strings.Contains(normalizedOutput, kw) {
				matched++
			}
		}

		score := 0
		reason := "keywords not found in context"
		if len(keywords) == 0 || float64(matched)/float64(len(keywords)) >= 0.6 {
			score = 1
			reason = "matched keywords found in context"
		}

		scores = append(scores, CriterionScore{
			Criterion: criterion,
			Score:     score,
			Reason:    reason,
		})
	}

	return scores
}

// extractKeywords splits a criterion string into lowercase words longer than
// 3 characters. Words are split on whitespace and common punctuation.
func extractKeywords(criterion string) []string {
	normalized := strings.ToLower(criterion)
	words := strings.FieldsFunc(normalized, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	var keywords []string
	for _, w := range words {
		if len(w) > 3 {
			keywords = append(keywords, w)
		}
	}
	return keywords
}

// sumScores totals the scores across all criteria.
func sumScores(scores []CriterionScore) int {
	total := 0
	for _, s := range scores {
		total += s.Score
	}
	return total
}

// PrintResults prints a formatted comparison table for each eval result.
func PrintResults(results []EvalResult) {
	for _, r := range results {
		fmt.Printf("=== Eval: %s ===\n", r.Fixture.Task)

		// Show first 80 chars of input.
		inputPreview := r.Fixture.Input
		if len(inputPreview) > 80 {
			inputPreview = inputPreview[:80] + "..."
		}
		// Collapse newlines for the preview.
		inputPreview = strings.ReplaceAll(inputPreview, "\n", " ")
		fmt.Printf("Input: %s\n", inputPreview)

		total := len(r.Fixture.Rubric)

		if len(r.ScoreB) > 0 {
			// Both versions available — show side-by-side comparison.
			fmt.Printf("  Version %d: %d/%d  |  Version %d: %d/%d\n",
				r.VersionA, r.TotalA, total,
				r.VersionB, r.TotalB, total)

			for i := 0; i < len(r.Fixture.Rubric); i++ {
				markA := "[✗]"
				markB := "[✗]"
				criterionA := r.Fixture.Rubric[i]
				criterionB := r.Fixture.Rubric[i]

				if i < len(r.ScoreA) && r.ScoreA[i].Score == 1 {
					markA = "[✓]"
				}
				if i < len(r.ScoreA) {
					criterionA = r.ScoreA[i].Criterion
				}
				if i < len(r.ScoreB) && r.ScoreB[i].Score == 1 {
					markB = "[✓]"
				}
				if i < len(r.ScoreB) {
					criterionB = r.ScoreB[i].Criterion
				}

				fmt.Printf("  %s %-40s | %s %s\n", markA, criterionA, markB, criterionB)
			}
		} else {
			// Only version A available.
			fmt.Printf("  Version %d: %d/%d  |  Version %d: (not available)\n",
				r.VersionA, r.TotalA, total, r.VersionB)

			for i := 0; i < len(r.Fixture.Rubric); i++ {
				mark := "[✗]"
				criterion := r.Fixture.Rubric[i]

				if i < len(r.ScoreA) && r.ScoreA[i].Score == 1 {
					mark = "[✓]"
				}
				if i < len(r.ScoreA) {
					criterion = r.ScoreA[i].Criterion
				}

				fmt.Printf("  %s %s\n", mark, criterion)
			}
		}

		fmt.Println()
	}
}
