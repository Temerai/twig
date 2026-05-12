package integration_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Temerai/twig/internal/graphagent"
	"github.com/Temerai/twig/internal/graphintel"
	"github.com/Temerai/twig/internal/parser"
	"github.com/Temerai/twig/internal/types"
)

// suite holds all shared state for the integration tests.
type suite struct {
	store *parser.Store
	agent *graphagent.GraphAgent
	intel *graphintel.GraphIntel
	root  string
}

var (
	sharedSuite *suite
	suiteOnce   sync.Once
	suiteErr    error
)

// getSuite returns the shared suite, indexing the codebase on the first call.
func getSuite(t *testing.T) *suite {
	t.Helper()
	suiteOnce.Do(func() {
		root := findRoot(t)
		tmpFile, err := os.CreateTemp("", "twig_integration_*.db")
		if err != nil {
			suiteErr = err
			return
		}
		dbPath := tmpFile.Name()
		tmpFile.Close()
		os.Remove(dbPath)
		store, err := parser.NewStore(dbPath)
		if err != nil {
			suiteErr = err
			return
		}
		idx := parser.NewIndexer(store, root)
		if err := idx.Index(root); err != nil {
			suiteErr = err
			return
		}
		// Change to the module root so that relative node file paths resolve
		// correctly when readNodeSource reads snippets from disk.
		if err := os.Chdir(root); err != nil {
			suiteErr = fmt.Errorf("chdir to module root: %w", err)
			return
		}
		sharedSuite = &suite{
			store: store,
			agent: graphagent.NewGraphAgent(store),
			intel: graphintel.NewGraphIntel(store),
			root:  root,
		}
	})
	if suiteErr != nil {
		t.Fatalf("suite setup failed: %v", suiteErr)
	}
	return sharedSuite
}

// findRoot walks up from the working directory until it finds go.mod.
func findRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find module root (go.mod)")
		}
		dir = parent
	}
}

// checkFTS returns true when FTS is available. Tests calling this should be
// skipped when it returns false.
func checkFTS(t *testing.T, s *suite) bool {
	t.Helper()
	_, err := s.store.SearchFTS("x", 1)
	if err != nil && strings.Contains(err.Error(), "FTS5 not available") {
		t.Skip("FTS not available: rebuild with sqlite_fts5 build tag")
		return false
	}
	return true
}

// ---- Test 1: TestIndexStats --------------------------------------------------------

func TestIndexStats(t *testing.T) {
	s := getSuite(t)

	nodeCount, edgeCount, err := s.store.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if nodeCount <= 100 {
		t.Errorf("expected nodeCount > 100, got %d", nodeCount)
	}
	if edgeCount <= 500 {
		t.Errorf("expected edgeCount > 500, got %d", edgeCount)
	}

	ds, err := s.store.DetailedStats()
	if err != nil {
		t.Fatalf("DetailedStats: %v", err)
	}

	for _, kind := range []string{"function", "method", "struct"} {
		if ds.NodesByKind[kind] == 0 {
			t.Errorf("expected NodesByKind to have key %q with count > 0", kind)
		}
	}

	for _, kind := range []string{"CALLS", "IMPORTS", "USES"} {
		if ds.EdgesByKind[kind] == 0 {
			t.Errorf("expected EdgesByKind to have key %q with count > 0", kind)
		}
	}

	if ds.Languages["go"] == 0 {
		t.Error("expected Languages to contain 'go'")
	}

	if ds.EdgesByKind["USES"] == 0 {
		t.Error("expected USES edge count > 0")
	}
}

// ---- Test 2: TestGetNodeByName -----------------------------------------------------

func TestGetNodeByName(t *testing.T) {
	s := getSuite(t)

	t.Run("NewStore returns exactly 1 function node", func(t *testing.T) {
		nodes, err := s.store.GetNodeByName("NewStore")
		if err != nil {
			t.Fatalf("GetNodeByName: %v", err)
		}
		if len(nodes) != 1 {
			t.Errorf("expected exactly 1 node for NewStore, got %d", len(nodes))
		}
		if len(nodes) > 0 && nodes[0].Kind != "function" {
			t.Errorf("expected Kind=function, got %q", nodes[0].Kind)
		}
	})

	t.Run("Store returns at least 1 struct node", func(t *testing.T) {
		nodes, err := s.store.GetNodeByName("Store")
		if err != nil {
			t.Fatalf("GetNodeByName: %v", err)
		}
		if len(nodes) == 0 {
			t.Fatal("expected at least 1 node for Store, got 0")
		}
		hasStruct := false
		for _, n := range nodes {
			if n.Kind == "struct" {
				hasStruct = true
				break
			}
		}
		if !hasStruct {
			t.Errorf("expected at least one node with Kind=struct among Store results")
		}
	})

	t.Run("nonexistent symbol returns empty slice", func(t *testing.T) {
		nodes, err := s.store.GetNodeByName("nonexistentXYZABC")
		if err != nil {
			t.Fatalf("GetNodeByName returned unexpected error: %v", err)
		}
		if len(nodes) != 0 {
			t.Errorf("expected empty slice for nonexistent name, got %d nodes", len(nodes))
		}
	})
}

// ---- Test 3: TestGetSymbol_KindFilter -----------------------------------------------

func TestGetSymbol_KindFilter(t *testing.T) {
	s := getSuite(t)

	nodes, err := s.store.GetNodeByName("Store")
	if err != nil {
		t.Fatalf("GetNodeByName: %v", err)
	}

	var structs, functions []types.Node
	for _, n := range nodes {
		switch n.Kind {
		case "struct":
			structs = append(structs, n)
		case "function":
			functions = append(functions, n)
		}
	}

	t.Run("struct kind present", func(t *testing.T) {
		if len(structs) == 0 {
			t.Error("expected at least 1 node with Kind=struct for 'Store'")
		}
	})

	t.Run("function kind absent for Store", func(t *testing.T) {
		if len(functions) > 0 {
			t.Errorf("expected 0 nodes with Kind=function for 'Store', got %d", len(functions))
		}
	})
}

// ---- Test 4: TestAnalyzeImpact_StoreType -------------------------------------------

func TestAnalyzeImpact_StoreType(t *testing.T) {
	s := getSuite(t)
	ctx := context.Background()

	report, err := s.intel.ImpactOf(ctx, "Store", 5)
	if err != nil {
		t.Fatalf("ImpactOf(Store): %v", err)
	}

	if report.RiskScore == 0 {
		t.Error("expected RiskScore > 0 for Store")
	}
	if len(report.DirectUsers) <= 5 {
		t.Errorf("expected len(DirectUsers) > 5, got %d", len(report.DirectUsers))
	}
	if len(report.AffectedFiles) <= 3 {
		t.Errorf("expected len(AffectedFiles) > 3, got %d", len(report.AffectedFiles))
	}

	// At least one direct user should have a name referencing GraphAgent, Server, or Indexer.
	found := false
	for _, n := range report.DirectUsers {
		if strings.Contains(n.Name, "GraphAgent") || strings.Contains(n.Name, "Server") || strings.Contains(n.Name, "Indexer") {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, len(report.DirectUsers))
		for i, n := range report.DirectUsers {
			names[i] = n.Name
		}
		t.Errorf("expected DirectUsers to contain GraphAgent, Server, or Indexer; got: %v", names)
	}
}

// ---- Test 5: TestAnalyzeImpact_NodeType ---------------------------------------------

func TestAnalyzeImpact_NodeType(t *testing.T) {
	s := getSuite(t)
	ctx := context.Background()

	report, err := s.intel.ImpactOf(ctx, "Node", 5)
	if err != nil {
		t.Fatalf("ImpactOf(Node): %v", err)
	}

	if report.RiskScore == 0 {
		t.Error("expected RiskScore > 0 for Node")
	}
	if len(report.DirectUsers) <= 10 {
		t.Errorf("expected len(DirectUsers) > 10, got %d", len(report.DirectUsers))
	}

	// At least one direct user should come from extract.go.
	foundExtract := false
	for _, n := range report.DirectUsers {
		if strings.Contains(n.File, "extract.go") {
			foundExtract = true
			break
		}
	}
	if !foundExtract {
		t.Error("expected at least one DirectUser from extract.go (walkers use Node)")
	}
}

// ---- Test 6: TestAnalyzeImpact_Constructor -----------------------------------------

func TestAnalyzeImpact_Constructor(t *testing.T) {
	s := getSuite(t)
	ctx := context.Background()

	report, err := s.intel.ImpactOf(ctx, "NewStore", 5)
	if err != nil {
		t.Fatalf("ImpactOf(NewStore): %v", err)
	}

	hasImpact := len(report.DirectCallers) > 0 || len(report.TransitiveDeps) > 0
	if !hasImpact {
		t.Error("expected DirectCallers or TransitiveDeps to be non-empty for NewStore")
	}

	foundCmd := false
	for _, f := range report.AffectedFiles {
		if strings.Contains(f, "cmd") || strings.Contains(f, "components") {
			foundCmd = true
			break
		}
	}
	if !foundCmd {
		t.Errorf("expected AffectedFiles to contain a path with 'cmd' or 'components'; got %v", report.AffectedFiles)
	}
}

// ---- Test 7: TestCallers_Traversal --------------------------------------------------

func TestCallers_Traversal(t *testing.T) {
	s := getSuite(t)
	ctx := context.Background()

	t.Run("RebuildFTS has at least 2 callers", func(t *testing.T) {
		callers, err := s.intel.Callers(ctx, "RebuildFTS", 1)
		if err != nil {
			t.Fatalf("Callers(RebuildFTS): %v", err)
		}
		if len(callers) < 2 {
			names := make([]string, len(callers))
			for i, n := range callers {
				names[i] = n.Name
			}
			t.Errorf("expected >= 2 callers for RebuildFTS, got %d: %v", len(callers), names)
		}
	})

	t.Run("NewStore has at least 3 callers", func(t *testing.T) {
		callers, err := s.intel.Callers(ctx, "NewStore", 1)
		if err != nil {
			t.Fatalf("Callers(NewStore): %v", err)
		}
		if len(callers) < 3 {
			names := make([]string, len(callers))
			for i, n := range callers {
				names[i] = n.Name
			}
			t.Errorf("expected >= 3 callers for NewStore, got %d: %v", len(callers), names)
		}
	})
}

// ---- Test 8: TestUsers_Traversal ----------------------------------------------------

func TestUsers_Traversal(t *testing.T) {
	s := getSuite(t)
	ctx := context.Background()

	t.Run("Store users >= 5", func(t *testing.T) {
		users, err := s.intel.Users(ctx, "Store", 1)
		if err != nil {
			t.Fatalf("Users(Store): %v", err)
		}
		if len(users) < 5 {
			t.Errorf("expected >= 5 users for Store, got %d", len(users))
		}
	})

	t.Run("Node users >= 10", func(t *testing.T) {
		users, err := s.intel.Users(ctx, "Node", 1)
		if err != nil {
			t.Fatalf("Users(Node): %v", err)
		}
		if len(users) < 10 {
			t.Errorf("expected >= 10 users for Node, got %d", len(users))
		}
	})
}

// ---- Test 9: TestCallees_Traversal --------------------------------------------------

func TestCallees_Traversal(t *testing.T) {
	s := getSuite(t)
	ctx := context.Background()

	callees, err := s.intel.Callees(ctx, "ImpactOf", 1)
	if err != nil {
		t.Fatalf("Callees(ImpactOf): %v", err)
	}
	if len(callees) < 3 {
		names := make([]string, len(callees))
		for i, n := range callees {
			names[i] = n.Name
		}
		t.Errorf("expected >= 3 callees for ImpactOf, got %d: %v", len(callees), names)
	}

	// Expect Callers or Users to appear in the callees list.
	found := false
	for _, n := range callees {
		if n.Name == "GraphIntel.Callers" || n.Name == "GraphIntel.Users" {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, len(callees))
		for i, n := range callees {
			names[i] = n.Name
		}
		t.Errorf("expected GraphIntel.Callers or GraphIntel.Users in callees; got: %v", names)
	}
}

// ---- Test 10: TestQueryCodebase_Strategies ------------------------------------------

func TestQueryCodebase_Strategies(t *testing.T) {
	s := getSuite(t)
	ctx := context.Background()

	strategies := []types.TraversalStrategy{
		types.StrategyScored,
		types.StrategyBFS,
		types.StrategyDeep,
		types.StrategyCallers,
	}

	for _, strategy := range strategies {
		strategy := strategy
		t.Run(string(strategy), func(t *testing.T) {
			req := types.QueryRequest{
				NaturalLanguage: "how does NewStore open the database",
				Strategy:        strategy,
				TokenBudget:     4000,
			}
			result, err := s.agent.Query(ctx, req)
			if err != nil {
				t.Fatalf("Query(%s): %v", strategy, err)
			}
			if len(result.Snippets) == 0 {
				t.Errorf("strategy %s: expected len(Snippets) > 0", strategy)
			}
			if strategy == types.StrategyScored && result.TokensUsed == 0 {
				t.Errorf("strategy scored: expected TokensUsed > 0")
			}
		})
	}
}

// ---- Test 11: TestQueryCodebase_CallerStrategy_ReturnsUsers -------------------------

func TestQueryCodebase_CallerStrategy_ReturnsUsers(t *testing.T) {
	s := getSuite(t)
	ctx := context.Background()

	req := types.QueryRequest{
		NaturalLanguage: "Store",
		Strategy:        types.StrategyCallers,
		TokenBudget:     4000,
	}
	result, err := s.agent.Query(ctx, req)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(result.Snippets) <= 5 {
		t.Errorf("expected len(Snippets) > 5, got %d", len(result.Snippets))
	}

	found := false
	for _, sn := range result.Snippets {
		if strings.Contains(sn.NodeName, "GraphAgent") || strings.Contains(sn.NodeName, "Server") || strings.Contains(sn.NodeName, "Indexer") {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, len(result.Snippets))
		for i, sn := range result.Snippets {
			names[i] = sn.NodeName
		}
		t.Errorf("expected a snippet with NodeName containing GraphAgent, Server, or Indexer; got: %v", names)
	}
}

// ---- Test 12: TestQueryCodebase_ScoredStrategy_SymbolMatch --------------------------

func TestQueryCodebase_ScoredStrategy_SymbolMatch(t *testing.T) {
	s := getSuite(t)
	ctx := context.Background()

	req := types.QueryRequest{
		NaturalLanguage: "how does ImpactOf calculate risk score",
		Strategy:        types.StrategyScored,
		TokenBudget:     3000,
	}
	result, err := s.agent.Query(ctx, req)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	found := false
	for _, sn := range result.Snippets {
		if strings.Contains(sn.NodeName, "ImpactOf") {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, len(result.Snippets))
		for i, sn := range result.Snippets {
			names[i] = sn.NodeName
		}
		t.Errorf("expected at least one snippet with NodeName containing 'ImpactOf'; got: %v", names)
	}
}

// ---- Test 13: TestQueryCodebase_BudgetRespected -------------------------------------

func TestQueryCodebase_BudgetRespected(t *testing.T) {
	s := getSuite(t)
	ctx := context.Background()

	req := types.QueryRequest{
		NaturalLanguage: "Store",
		Strategy:        types.StrategyScored,
		TokenBudget:     500,
	}
	result, err := s.agent.Query(ctx, req)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	tolerance := 600 // ~20% over budget allowed
	if result.TokensUsed > tolerance {
		t.Errorf("expected TokensUsed <= %d (budget 500 + 20%%), got %d", tolerance, result.TokensUsed)
	}
}

// ---- Test 14: TestQueryCodebase_UnknownSymbol_NoError --------------------------------

func TestQueryCodebase_UnknownSymbol_NoError(t *testing.T) {
	s := getSuite(t)
	ctx := context.Background()

	req := types.QueryRequest{
		NaturalLanguage: "xyznonexistentquery12345",
		Strategy:        types.StrategyScored,
		TokenBudget:     1000,
	}
	result, err := s.agent.Query(ctx, req)
	if err != nil {
		t.Fatalf("expected no error for unknown symbol, got: %v", err)
	}
	if len(result.Snippets) != 0 {
		t.Errorf("expected 0 snippets for unknown symbol, got %d", len(result.Snippets))
	}
}

// ---- Test 15: TestSearchFTS_SimpleSymbol --------------------------------------------

func TestSearchFTS_SimpleSymbol(t *testing.T) {
	s := getSuite(t)
	if !checkFTS(t, s) {
		return
	}

	results, err := s.store.SearchFTS("NewStore", 10)
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	if len(results) < 1 {
		t.Fatal("expected at least 1 result for 'NewStore'")
	}
	// FTS ranks by BM25; the test file itself also contains "NewStore" so the
	// exact rank-0 result is non-deterministic. Assert any result has the name.
	found := false
	for _, r := range results {
		if r.Name == "NewStore" {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, len(results))
		for i, r := range results {
			names[i] = r.Name
		}
		t.Errorf("expected a result with Name=='NewStore'; got: %v", names)
	}
}

// ---- Test 16: TestSearchFTS_DotInQuery ----------------------------------------------

func TestSearchFTS_DotInQuery(t *testing.T) {
	s := getSuite(t)
	if !checkFTS(t, s) {
		return
	}

	results, err := s.store.SearchFTS("sql.Open", 10)
	if err != nil {
		t.Fatalf("SearchFTS('sql.Open') returned error: %v", err)
	}
	if len(results) < 1 {
		t.Fatal("expected at least 1 result for 'sql.Open'")
	}

	found := false
	for _, r := range results {
		if strings.Contains(r.Source, "sql.Open") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected at least one result whose Source contains 'sql.Open'")
	}
}

// ---- Test 17: TestSearchFTS_ORQuery -------------------------------------------------

func TestSearchFTS_ORQuery(t *testing.T) {
	s := getSuite(t)
	if !checkFTS(t, s) {
		return
	}

	results, err := s.store.SearchFTS("RebuildFTS OR SearchFTS", 20)
	if err != nil {
		t.Fatalf("SearchFTS OR query: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected >= 2 results for OR query, got %d", len(results))
	}

	gotNames := make(map[string]bool)
	for _, r := range results {
		gotNames[r.Name] = true
	}

	for _, want := range []string{"Store.RebuildFTS", "Store.SearchFTS"} {
		if !gotNames[want] {
			t.Errorf("expected %q in OR query results; got names: %v", want, gotNames)
		}
	}
}

// ---- Test 18: TestSearchFTS_NoMatch -------------------------------------------------

func TestSearchFTS_NoMatch(t *testing.T) {
	s := getSuite(t)
	if !checkFTS(t, s) {
		return
	}

	results, err := s.store.SearchFTS("xyznonexistentterm9999", 10)
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	// The search term appears as a literal string inside this test file, so
	// FTS may return this test function itself. Filter out hits from the test
	// file and assert no real-source matches exist.
	var nonTestResults []types.Node
	for _, r := range results {
		if !strings.Contains(r.File, "functionality_test.go") {
			nonTestResults = append(nonTestResults, r)
		}
	}
	if len(nonTestResults) != 0 {
		names := make([]string, len(nonTestResults))
		for i, r := range nonTestResults {
			names[i] = r.Name
		}
		t.Errorf("expected 0 non-test results for nonsense term, got %v", names)
	}
}

// ---- Test 19: TestSearchFTS_LimitRespected ------------------------------------------

func TestSearchFTS_LimitRespected(t *testing.T) {
	s := getSuite(t)
	if !checkFTS(t, s) {
		return
	}

	results, err := s.store.SearchFTS("func", 3)
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	if len(results) > 3 {
		t.Errorf("expected <= 3 results with limit=3, got %d", len(results))
	}
}

// ---- Test 20: TestIncrementalReindex_PreservesCount ---------------------------------

func TestIncrementalReindex_PreservesCount(t *testing.T) {
	s := getSuite(t)

	nodesBefore, _, err := s.store.Stats()
	if err != nil {
		t.Fatalf("Stats before reindex: %v", err)
	}

	idx := parser.NewIndexer(s.store, s.root)
	if err := idx.Reindex([]string{"internal/parser/store.go"}); err != nil {
		t.Fatalf("Reindex: %v", err)
	}

	nodesAfter, _, err := s.store.Stats()
	if err != nil {
		t.Fatalf("Stats after reindex: %v", err)
	}
	delta := nodesAfter - nodesBefore
	if delta < 0 {
		delta = -delta
	}
	if delta > 5 {
		t.Errorf("expected node count to stay within ±5 after reindex; before=%d after=%d delta=%d",
			nodesBefore, nodesAfter, delta)
	}

	// Restore full index so subsequent tests see complete cross-file edge state.
	if err := idx.Index(s.root); err != nil {
		t.Fatalf("restore full index after reindex test: %v", err)
	}
}

// ---- Test 21: TestDetailedStats_Structure -------------------------------------------

func TestDetailedStats_Structure(t *testing.T) {
	s := getSuite(t)

	ds, err := s.store.DetailedStats()
	if err != nil {
		t.Fatalf("DetailedStats: %v", err)
	}

	if ds.FileCount <= 10 {
		t.Errorf("expected FileCount > 10, got %d", ds.FileCount)
	}

	// NodeCount must equal sum of all NodesByKind values.
	sumNodes := 0
	for _, c := range ds.NodesByKind {
		sumNodes += c
	}
	if sumNodes != ds.NodeCount {
		t.Errorf("NodeCount=%d does not equal sum of NodesByKind=%d", ds.NodeCount, sumNodes)
	}

	// EdgeCount must equal sum of all EdgesByKind values.
	sumEdges := 0
	for _, c := range ds.EdgesByKind {
		sumEdges += c
	}
	if sumEdges != ds.EdgeCount {
		t.Errorf("EdgeCount=%d does not equal sum of EdgesByKind=%d", ds.EdgeCount, sumEdges)
	}

	if len(ds.Languages) < 1 {
		t.Error("expected at least 1 language in Languages map")
	}
}

// ---- Test 22: TestFTSAvailable ------------------------------------------------------

func TestFTSAvailable(t *testing.T) {
	s := getSuite(t)

	_, err := s.store.SearchFTS("any", 1)
	if err != nil && strings.Contains(err.Error(), "FTS5 not available") {
		t.Fatal("FTS5 is not available; rebuild with -tags sqlite_fts5")
	}
	// Any other error (e.g., no results) is fine — we only care that the
	// "FTS5 not available" error does NOT occur.
}

// ---- Test 23: TestUSESEdges_GoStruct ------------------------------------------------

func TestUSESEdges_GoStruct(t *testing.T) {
	s := getSuite(t)
	ctx := context.Background()

	// Verify Store struct has incoming USES edges.
	storeNodes, err := s.store.GetNodeByName("Store")
	if err != nil {
		t.Fatalf("GetNodeByName(Store): %v", err)
	}
	if len(storeNodes) == 0 {
		t.Fatal("Store node not found")
	}

	var storeNode *types.Node
	for i, n := range storeNodes {
		if n.Kind == "struct" {
			storeNode = &storeNodes[i]
			break
		}
	}
	if storeNode == nil {
		t.Fatal("no struct node named Store found")
	}

	edges, err := s.store.GetInEdges(storeNode.ID, "USES")
	if err != nil {
		t.Fatalf("GetInEdges(Store, USES): %v", err)
	}
	if len(edges) == 0 {
		t.Error("expected USES in-edges for Store node, got 0")
	}

	// Additionally, Users traversal must return at least one node from agent.go.
	users, err := s.intel.Users(ctx, "Store", 1)
	if err != nil {
		t.Fatalf("Users(Store): %v", err)
	}

	foundAgent := false
	for _, n := range users {
		if strings.Contains(n.File, "agent.go") {
			foundAgent = true
			break
		}
	}
	if !foundAgent {
		files := make([]string, len(users))
		for i, n := range users {
			files[i] = n.File
		}
		t.Errorf("expected at least one user of Store from agent.go; got files: %v", files)
	}
}

// ---- Test 24: TestUSESEdges_GoFunc --------------------------------------------------

func TestUSESEdges_GoFunc(t *testing.T) {
	s := getSuite(t)
	ctx := context.Background()

	users, err := s.intel.Users(ctx, "Node", 1)
	if err != nil {
		t.Fatalf("Users(Node): %v", err)
	}
	if len(users) == 0 {
		t.Fatal("expected Users(Node) to return at least 1 node")
	}

	// Some users should be from extract.go (walkers reference Node in signatures).
	foundExtract := false
	for _, n := range users {
		if strings.Contains(n.File, "extract.go") {
			foundExtract = true
			break
		}
	}
	if !foundExtract {
		files := make([]string, len(users))
		for i, n := range users {
			files[i] = fmt.Sprintf("%s(%s)", n.Name, n.File)
		}
		t.Errorf("expected at least one user of Node from extract.go; got: %v", files)
	}
}

// ---- Test 25: TestGraphExplore_Callers ----------------------------------------------

func TestGraphExplore_Callers(t *testing.T) {
	s := getSuite(t)

	nodes, err := s.store.GetNodeByName("Store.RebuildFTS")
	if err != nil {
		t.Fatalf("GetNodeByName(Store.RebuildFTS): %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("Store.RebuildFTS node not found in index")
	}

	edges, err := s.store.GetInEdges(nodes[0].ID, "CALLS")
	if err != nil {
		t.Fatalf("GetInEdges: %v", err)
	}
	if len(edges) < 2 {
		t.Errorf("expected >= 2 CALLS in-edges for Store.RebuildFTS, got %d", len(edges))
	}

	// Collect source node files to verify callers include indexer.go and test file.
	srcFiles := make(map[string]bool)
	for _, e := range edges {
		srcNode, err := s.store.GetNode(e.Src)
		if err == nil && srcNode != nil {
			srcFiles[srcNode.File] = true
		}
	}

	if !srcFiles["internal/parser/indexer.go"] {
		t.Errorf("expected indexer.go to be a caller of Store.RebuildFTS; got files: %v", srcFiles)
	}

	foundTestFile := false
	for f := range srcFiles {
		if strings.Contains(f, "store_fts_test.go") || strings.Contains(f, "_test.go") {
			foundTestFile = true
			break
		}
	}
	if !foundTestFile {
		t.Logf("note: no test file found among callers of Store.RebuildFTS (files: %v)", srcFiles)
	}
}

// ---- Test 26: TestGraphExplore_Callees ----------------------------------------------

func TestGraphExplore_Callees(t *testing.T) {
	s := getSuite(t)
	ctx := context.Background()

	callees, err := s.intel.Callees(ctx, "ImpactOf", 2)
	if err != nil {
		t.Fatalf("Callees(ImpactOf, 2): %v", err)
	}
	if len(callees) < 3 {
		names := make([]string, len(callees))
		for i, n := range callees {
			names[i] = n.Name
		}
		t.Errorf("expected >= 3 callees at depth=2 for ImpactOf, got %d: %v", len(callees), names)
	}

	found := false
	for _, n := range callees {
		if strings.Contains(n.Name, "resolveSymbol") || strings.Contains(n.Name, "GetInEdges") {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, len(callees))
		for i, n := range callees {
			names[i] = n.Name
		}
		t.Errorf("expected callees to include resolveSymbol or GetInEdges; got: %v", names)
	}
}

// ---- Test 27: TestGraphExplore_Dependencies -----------------------------------------

func TestGraphExplore_Dependencies(t *testing.T) {
	s := getSuite(t)
	ctx := context.Background()

	deps, err := s.intel.Dependencies(ctx, "Server.callSearchCodebase")
	if err != nil {
		t.Fatalf("Dependencies(Server.callSearchCodebase): %v", err)
	}
	if len(deps) < 1 {
		t.Fatal("expected at least 1 dependency for Server.callSearchCodebase")
	}

	// Expect Store.SearchFTS to appear somewhere in the dependency list.
	found := false
	for _, n := range deps {
		if n.Name == "Store.SearchFTS" {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, len(deps))
		for i, n := range deps {
			names[i] = n.Name
		}
		t.Errorf("expected Store.SearchFTS in dependencies of Server.callSearchCodebase; got: %v", names)
	}
}
