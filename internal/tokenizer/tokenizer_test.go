package tokenizer

import "testing"

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{name: "empty", text: "", want: 0},
		{name: "single word", text: "hello", want: 1},
		{name: "two words", text: "hello world", want: 2},
		{
			name: "go function",
			text: `func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}`,
			want: 56,
		},
		{
			name: "python class",
			text: `class TokenCounter:
    def count(self, text: str) -> int:
        return len(text.split())`,
			want: 28,
		},
		{
			name: "one liner with operators",
			text: `x := a + b*c - d/e`,
			want: 12,
		},
		{
			name: "long identifier gets subword penalty",
			text: "handleAuthenticationMiddlewareChain",
			want: 4,
		},
		{
			name: "nested braces",
			text: `if (a && (b || c)) { foo(); }`,
			want: 18,
		},
		{
			name: "whitespace only",
			text: "   ",
			want: 1,
		},
		{
			name: "newlines counted",
			text: "a\nb\nc",
			want: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateTokens(tt.text)
			if got != tt.want {
				t.Errorf("EstimateTokens() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestEstimateTokensReasonableRange(t *testing.T) {
	// A more realistic code block. cl100k_base gives ~85 tokens for this.
	// We check the heuristic lands within ±25%.
	code := `// traverseBFS performs a breadth-first traversal from the seed nodes.
func (ga *GraphAgent) traverseBFS(seeds []types.Node, budget int) ([]string, error) {
	visited := make(map[string]bool)
	var result []string
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
	}
	return result, nil
}`
	got := EstimateTokens(code)
	lower, upper := 64, 106
	if got < lower || got > upper {
		t.Errorf("EstimateTokens() = %d, want between %d and %d", got, lower, upper)
	}
}

func TestFlushWord(t *testing.T) {
	tests := []struct {
		length int
		want   int
	}{
		{0, 0},
		{1, 1},
		{5, 1},
		{10, 1},
		{11, 2},
		{20, 2},
		{21, 3},
		{35, 4},
	}

	for _, tt := range tests {
		got := flushWord(tt.length)
		if got != tt.want {
			t.Errorf("flushWord(%d) = %d, want %d", tt.length, got, tt.want)
		}
	}
}
