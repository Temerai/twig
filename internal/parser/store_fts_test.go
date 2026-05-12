package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Temerai/twig/internal/types"
)

func TestSearchFTS_ReturnsMatchingNodes(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	nodes := []types.Node{
		// Two nodes that mention "sqlite" — should be returned by the query.
		{ID: "main.go:OpenDB", File: "main.go", Language: "go", Kind: "function", Name: "OpenDB", Signature: "func OpenDB(dsn string) error", Lines: "1-10", Source: "func OpenDB(dsn string) error { return sqlite.Open(dsn) }"},
		{ID: "main.go:CloseDB", File: "main.go", Language: "go", Kind: "function", Name: "CloseDB", Signature: "func CloseDB() error", Lines: "12-15", Source: "func CloseDB() error { return sqlite.Close() }"},
		// One node that has nothing to do with sqlite — must NOT appear in results.
		{ID: "handler.go:HandleRequest", File: "handler.go", Language: "go", Kind: "function", Name: "HandleRequest", Signature: "func HandleRequest()", Lines: "1-20", Source: "func HandleRequest() {}"},
	}

	if err := store.UpsertNodes(nodes); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}
	if err := store.RebuildFTS(); err != nil {
		t.Fatalf("RebuildFTS: %v", err)
	}

	// Search for "sqlite" should match OpenDB and CloseDB but NOT HandleRequest.
	results, err := store.SearchFTS("sqlite", 10)
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results for 'sqlite' query, got %d", len(results))
	}

	// Build a set of returned names for easy assertion.
	got := make(map[string]bool)
	for _, r := range results {
		if r.ID == "" || r.Name == "" || r.File == "" {
			t.Errorf("result has empty fields: %+v", r)
		}
		got[r.Name] = true
	}

	// Matching nodes must be present.
	for _, want := range []string{"OpenDB", "CloseDB"} {
		if !got[want] {
			t.Errorf("expected %q in FTS results, but it was absent; got %v", want, got)
		}
	}

	// Non-matching node must be absent.
	if got["HandleRequest"] {
		t.Error("HandleRequest should not appear in 'sqlite' FTS results")
	}
}

func TestSearchFTS_EmptyResults(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	nodes := []types.Node{
		{ID: "a.go:Foo", File: "a.go", Language: "go", Kind: "function", Name: "Foo", Signature: "func Foo()", Lines: "1-3", Source: "func Foo() {}"},
	}
	if err := store.UpsertNodes(nodes); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}
	if err := store.RebuildFTS(); err != nil {
		t.Fatalf("RebuildFTS: %v", err)
	}

	results, err := store.SearchFTS("nonexistentxyz", 10)
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearchFTS_DefaultLimit(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Insert 25 nodes that all match the term "widget" so the default limit of
	// 20 is actually exercised.
	nodes := make([]types.Node, 25)
	for i := range nodes {
		id := fmt.Sprintf("pkg.go:Widget%d", i)
		nodes[i] = types.Node{
			ID:        id,
			File:      "pkg.go",
			Language:  "go",
			Kind:      "function",
			Name:      fmt.Sprintf("Widget%d", i),
			Signature: fmt.Sprintf("func Widget%d()", i),
			Lines:     "1-2",
			Source:    fmt.Sprintf("func Widget%d() { /* widget implementation */ }", i),
		}
	}
	if err := store.UpsertNodes(nodes); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}
	if err := store.RebuildFTS(); err != nil {
		t.Fatalf("RebuildFTS: %v", err)
	}

	// Passing limit <= 0 should default to 20.
	results, err := store.SearchFTS("widget", 0)
	if err != nil {
		t.Fatalf("SearchFTS with limit 0: %v", err)
	}
	if len(results) != 20 {
		t.Errorf("expected exactly 20 results with default limit, got %d", len(results))
	}
}

// TestSearchFTS_IncrementalReindex is the key regression test for the FTS sync
// path. It verifies that after a DeleteByFile + re-insert + RebuildFTS cycle,
// the old nodes are gone from FTS results and the replacement nodes appear.
func TestSearchFTS_IncrementalReindex(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// Step 1: insert initial nodes and build the FTS index.
	initial := []types.Node{
		{ID: "alpha.go:OldFunc", File: "alpha.go", Language: "go", Kind: "function", Name: "OldFunc", Signature: "func OldFunc()", Lines: "1-3", Source: "func OldFunc() { /* grizzly bear */ }"},
		{ID: "beta.go:StableFunc", File: "beta.go", Language: "go", Kind: "function", Name: "StableFunc", Signature: "func StableFunc()", Lines: "1-3", Source: "func StableFunc() { /* stable */ }"},
	}
	if err := store.UpsertNodes(initial); err != nil {
		t.Fatalf("UpsertNodes initial: %v", err)
	}
	if err := store.RebuildFTS(); err != nil {
		t.Fatalf("RebuildFTS after initial insert: %v", err)
	}

	// Sanity-check: OldFunc is findable.
	res, err := store.SearchFTS("grizzly", 10)
	if err != nil {
		t.Fatalf("SearchFTS grizzly (initial): %v", err)
	}
	if len(res) == 0 {
		t.Fatal("expected OldFunc to appear before deletion")
	}

	// Step 2: delete all nodes for alpha.go.
	if err := store.DeleteByFile("alpha.go"); err != nil {
		t.Fatalf("DeleteByFile: %v", err)
	}

	// Step 3: insert replacement nodes for alpha.go.
	replacement := []types.Node{
		{ID: "alpha.go:NewFunc", File: "alpha.go", Language: "go", Kind: "function", Name: "NewFunc", Signature: "func NewFunc()", Lines: "1-3", Source: "func NewFunc() { /* penguin colony */ }"},
	}
	if err := store.UpsertNodes(replacement); err != nil {
		t.Fatalf("UpsertNodes replacement: %v", err)
	}

	// Step 4: rebuild FTS to sync the plain table.
	if err := store.RebuildFTS(); err != nil {
		t.Fatalf("RebuildFTS after replacement: %v", err)
	}

	// Old term must no longer appear.
	oldRes, err := store.SearchFTS("grizzly", 10)
	if err != nil {
		t.Fatalf("SearchFTS grizzly (after rebuild): %v", err)
	}
	if len(oldRes) != 0 {
		names := make([]string, len(oldRes))
		for i, r := range oldRes {
			names[i] = r.Name
		}
		t.Errorf("expected no results for 'grizzly' after deletion, got %v", names)
	}

	// New term must be present.
	newRes, err := store.SearchFTS("penguin", 10)
	if err != nil {
		t.Fatalf("SearchFTS penguin: %v", err)
	}
	if len(newRes) == 0 {
		t.Fatal("expected NewFunc to appear after replacement and rebuild")
	}
	if newRes[0].Name != "NewFunc" {
		t.Errorf("expected NewFunc, got %s", newRes[0].Name)
	}

	// Unrelated node in beta.go must still be findable.
	stableRes, err := store.SearchFTS("stable", 10)
	if err != nil {
		t.Fatalf("SearchFTS stable: %v", err)
	}
	if len(stableRes) == 0 {
		t.Fatal("expected StableFunc to still appear after alpha.go was replaced")
	}
}

func TestRebuildFTS_OnEmptyDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// RebuildFTS on empty DB should succeed.
	if err := store.RebuildFTS(); err != nil {
		t.Fatalf("RebuildFTS on empty DB: %v", err)
	}
}

func TestReadNodeSourceText(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.go")
	content := "line1\nline2\nline3\nline4\nline5\n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		lineRange string
		want      string
		wantErr   bool
	}{
		{"valid range", "2-4", "line2\nline3\nline4", false},
		{"single line", "1-1", "line1", false},
		{"full range", "1-5", "line1\nline2\nline3\nline4\nline5", false},
		{"invalid format", "abc", "", true},
		{"zero start", "0-3", "", true},
		{"start beyond file", "100-200", "", true},
		{"end beyond file clamps", "4-100", "line4\nline5\n", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readNodeSourceText(filePath, tt.lineRange)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUpsertNodes_WithSource(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	nodes := []types.Node{
		{ID: "a.go:Foo", File: "a.go", Language: "go", Kind: "function", Name: "Foo", Signature: "func Foo()", Lines: "1-3", Source: "func Foo() { return }"},
	}
	if err := store.UpsertNodes(nodes); err != nil {
		t.Fatalf("UpsertNodes: %v", err)
	}

	// Existing queries should still work (they don't read source).
	got, err := store.GetNode("a.go:Foo")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if got == nil {
		t.Fatal("expected node, got nil")
	}
	if got.Name != "Foo" {
		t.Errorf("expected name Foo, got %s", got.Name)
	}
	if got.Source != "func Foo() { return }" {
		t.Errorf("expected Source to round-trip, got %q", got.Source)
	}
}
