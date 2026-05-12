package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Temerai/twig/internal/types"
)

// readNodeSourceText reads the lines specified by lineRange (e.g. "2-4") from
// the given file and returns them joined by newlines. Returns an error if the
// range is invalid or the start line is beyond the end of file.
func readNodeSourceText(filePath, lineRange string) (string, error) {
	parts := strings.SplitN(lineRange, "-", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid line range %q", lineRange)
	}
	start, err := strconv.Atoi(parts[0])
	if err != nil || start < 1 {
		return "", fmt.Errorf("invalid start in line range %q", lineRange)
	}
	end, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", fmt.Errorf("invalid end in line range %q", lineRange)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")

	if end < start {
		return "", fmt.Errorf("end line %d before start line %d", end, start)
	}
	if start > len(lines) {
		return "", fmt.Errorf("start line %d beyond file length %d", start, len(lines))
	}
	if end > len(lines) {
		// Clamp end to file length (preserve trailing newline behaviour).
		return strings.Join(lines[start-1:], "\n"), nil
	}
	return strings.Join(lines[start-1:end], "\n"), nil
}

// extractLinesFromSlice parses lineRange ("start-end", 1-based) and returns
// the matching lines from the pre-read slice joined by newlines.
func extractLinesFromSlice(lines []string, lineRange string) string {
	parts := strings.SplitN(lineRange, "-", 2)
	if len(parts) != 2 {
		return ""
	}
	start, err1 := strconv.Atoi(parts[0])
	end, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || start < 1 || end < start {
		return ""
	}
	if start > len(lines) {
		return ""
	}
	if end > len(lines) {
		return strings.Join(lines[start-1:], "\n")
	}
	return strings.Join(lines[start-1:end], "\n")
}

// skipDirs lists directory names that should be skipped during indexing.
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"bin":          true,
	"obj":          true,
	".idea":        true,
	".vs":          true,
}

// Indexer walks a codebase, parses supported files, and stores the resulting
// graph of nodes and edges in a SQLite store.
type Indexer struct {
	store    *Store
	registry *GrammarRegistry
	rootPath string
}

// NewIndexer creates an Indexer with a pre-configured grammar registry.
func NewIndexer(store *Store, rootPath string) *Indexer {
	return &Indexer{
		store:    store,
		registry: NewGrammarRegistry(),
		rootPath: rootPath,
	}
}

// RootPath returns the root path configured for this indexer.
func (idx *Indexer) RootPath() string {
	return idx.rootPath
}

// Index walks the directory tree starting at rootPath, parses every supported
// file, stores the extracted nodes and edges, and resolves CALLS edges.
func (idx *Indexer) Index(rootPath string) error {
	extractor := NewExtractor()
	supportedExts := make(map[string]bool)
	for _, ext := range idx.registry.SupportedExtensions() {
		supportedExts[ext] = true
	}

	var allNodes []types.Node
	var allEdges []types.Edge
	fileCount := 0

	err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip files we can't read
		}

		// Skip excluded directories.
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		ext := filepath.Ext(path)
		if !supportedExts[ext] {
			return nil
		}

		lang, langName, ok := idx.registry.GetLanguage(ext)
		if !ok {
			return nil
		}

		source, err := os.ReadFile(path)
		if err != nil {
			return nil // skip unreadable files
		}

		// Use forward slashes and make path relative to rootPath.
		relPath, err := filepath.Rel(rootPath, path)
		if err != nil {
			relPath = path
		}
		relPath = filepath.ToSlash(relPath)

		nodes, edges := extractor.Extract(source, lang, langName, relPath)
		allNodes = append(allNodes, nodes...)
		allEdges = append(allEdges, edges...)

		fileCount++
		if fileCount%50 == 0 {
			fmt.Fprintf(os.Stderr, "  indexed %d files...\n", fileCount)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("walking directory tree: %w", err)
	}

	fmt.Fprintf(os.Stderr, "  parsed %d files, found %d nodes, %d edges\n", fileCount, len(allNodes), len(allEdges))

	// Populate Source for each node, grouped by file to avoid redundant reads.
	fileNodeIndices := make(map[string][]int)
	for i, n := range allNodes {
		absFile := filepath.Join(rootPath, filepath.FromSlash(n.File))
		fileNodeIndices[absFile] = append(fileNodeIndices[absFile], i)
	}
	for absFile, indices := range fileNodeIndices {
		data, err := os.ReadFile(absFile)
		if err != nil {
			continue // skip files we can't read
		}
		lines := strings.Split(string(data), "\n")
		for _, i := range indices {
			allNodes[i].Source = extractLinesFromSlice(lines, allNodes[i].Lines)
		}
	}

	// Store nodes first so resolution can look them up.
	if err := idx.store.UpsertNodes(allNodes); err != nil {
		return fmt.Errorf("storing nodes: %w", err)
	}

	// Resolve CALLS/USES edges: replace bare function names with full node IDs.
	resolved := idx.resolveCallEdges(allEdges)

	if err := idx.store.UpsertEdges(resolved); err != nil {
		return fmt.Errorf("storing edges: %w", err)
	}

	if err := idx.store.RebuildFTS(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: FTS rebuild failed: %v\n", err)
	}

	nodeCount, edgeCount, _ := idx.store.Stats()
	fmt.Fprintf(os.Stderr, "  index complete: %d nodes, %d edges in store\n", nodeCount, edgeCount)

	return nil
}

// Reindex re-processes a list of changed files: deletes their old data,
// re-extracts, stores, and re-resolves CALLS edges.
func (idx *Indexer) Reindex(changedFiles []string) error {
	extractor := NewExtractor()

	var allNodes []types.Node
	var allEdges []types.Edge

	for _, file := range changedFiles {
		// Compute relative path (forward-slash) for DB lookups, and absolute path for reading.
		var relPath, absPath string
		if filepath.IsAbs(file) {
			absPath = file
			rel, err := filepath.Rel(idx.rootPath, file)
			if err != nil {
				relPath = filepath.ToSlash(file)
			} else {
				relPath = filepath.ToSlash(rel)
			}
		} else {
			relPath = filepath.ToSlash(file)
			absPath = filepath.Join(idx.rootPath, file)
		}

		if err := idx.store.DeleteByFile(relPath); err != nil {
			return fmt.Errorf("deleting old data for %s: %w", file, err)
		}

		ext := filepath.Ext(absPath)
		lang, langName, ok := idx.registry.GetLanguage(ext)
		if !ok {
			continue
		}

		source, err := os.ReadFile(absPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warning: cannot read %s: %v\n", file, err)
			continue
		}

		nodes, edges := extractor.Extract(source, lang, langName, relPath)
		allNodes = append(allNodes, nodes...)
		allEdges = append(allEdges, edges...)
	}

	// Populate Source grouped by file.
	fileNodeIndices := make(map[string][]int)
	for i, n := range allNodes {
		absFile := n.File
		if !filepath.IsAbs(absFile) {
			absFile = filepath.Join(idx.rootPath, filepath.FromSlash(n.File))
		}
		fileNodeIndices[absFile] = append(fileNodeIndices[absFile], i)
	}
	for absFile, indices := range fileNodeIndices {
		data, err := os.ReadFile(absFile)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for _, i := range indices {
			allNodes[i].Source = extractLinesFromSlice(lines, allNodes[i].Lines)
		}
	}

	if err := idx.store.UpsertNodes(allNodes); err != nil {
		return fmt.Errorf("storing nodes: %w", err)
	}

	resolved := idx.resolveCallEdges(allEdges)

	if err := idx.store.UpsertEdges(resolved); err != nil {
		return fmt.Errorf("storing edges: %w", err)
	}

	if err := idx.store.RebuildFTS(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: FTS rebuild failed: %v\n", err)
	}

	fmt.Fprintf(os.Stderr, "  reindexed %d files (%d nodes, %d edges)\n", len(changedFiles), len(allNodes), len(resolved))
	return nil
}

// resolveCallEdges attempts to match bare function/type names in CALLS and USES
// edges to fully-qualified node IDs from the store. Unresolved CALLS edges are
// kept as-is; unresolved USES edges are silently discarded.
func (idx *Indexer) resolveCallEdges(edges []types.Edge) []types.Edge {
	resolved := make([]types.Edge, 0, len(edges))
	for _, e := range edges {
		if e.Kind != "CALLS" && e.Kind != "USES" {
			resolved = append(resolved, e)
			continue
		}

		// Step 1: If dst already looks like a qualified ID (contains ':'), skip resolution.
		if strings.Contains(e.Dst, ":") {
			resolved = append(resolved, e)
			continue
		}

		// Step 2: Exact match by full dst name.
		matches, err := idx.store.GetNodeByName(e.Dst)
		if err == nil && len(matches) == 1 {
			e.Dst = matches[0].ID
			resolved = append(resolved, e)
			continue
		}

		// Step 3: If dst contains a dot (e.g. "Indexer.Index"), split and try method part.
		if dotIdx := strings.LastIndex(e.Dst, "."); dotIdx >= 0 {
			methodPart := e.Dst[dotIdx+1:]

			// 3a: Exact match on the method part alone.
			matches, err = idx.store.GetNodeByName(methodPart)
			if err == nil && len(matches) == 1 {
				e.Dst = matches[0].ID
				resolved = append(resolved, e)
				continue
			}

			// 3b: Suffix match on the method part.
			matches, err = idx.store.SearchNodesBySuffix(methodPart)
			if err == nil && len(matches) == 1 {
				e.Dst = matches[0].ID
				resolved = append(resolved, e)
				continue
			}
		} else {
			// Step 4: Bare name (no dot) — try suffix match.
			matches, err = idx.store.SearchNodesBySuffix(e.Dst)
			if err == nil && len(matches) == 1 {
				e.Dst = matches[0].ID
				resolved = append(resolved, e)
				continue
			}
		}

		// Step 5: No unique match found.
		// USES edges that can't be resolved are dropped (cross-package types).
		// CALLS edges are kept as-is.
		if e.Kind == "USES" {
			continue // drop unresolved USES edges — cross-package types can't be resolved
		}
		resolved = append(resolved, e)
	}
	return resolved
}
