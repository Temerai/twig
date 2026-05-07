package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Temerai/twig/internal/types"
)

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
			fmt.Printf("  indexed %d files...\n", fileCount)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("walking directory tree: %w", err)
	}

	fmt.Printf("  parsed %d files, found %d nodes, %d edges\n", fileCount, len(allNodes), len(allEdges))

	// Store nodes first so resolution can look them up.
	if err := idx.store.UpsertNodes(allNodes); err != nil {
		return fmt.Errorf("storing nodes: %w", err)
	}

	// Resolve CALLS edges: replace bare function names with full node IDs.
	resolved := idx.resolveCallEdges(allEdges)

	if err := idx.store.UpsertEdges(resolved); err != nil {
		return fmt.Errorf("storing edges: %w", err)
	}

	nodeCount, edgeCount, _ := idx.store.Stats()
	fmt.Printf("  index complete: %d nodes, %d edges in store\n", nodeCount, edgeCount)

	return nil
}

// Reindex re-processes a list of changed files: deletes their old data,
// re-extracts, stores, and re-resolves CALLS edges.
func (idx *Indexer) Reindex(changedFiles []string) error {
	extractor := NewExtractor()

	var allNodes []types.Node
	var allEdges []types.Edge

	for _, file := range changedFiles {
		// Delete old data for this file.
		// The file path stored in the DB is relative (forward-slash), so normalise.
		relPath := filepath.ToSlash(file)
		if err := idx.store.DeleteByFile(relPath); err != nil {
			return fmt.Errorf("deleting old data for %s: %w", file, err)
		}

		// Determine the absolute path for reading.
		absPath := file
		if !filepath.IsAbs(file) {
			absPath = filepath.Join(idx.rootPath, file)
		}

		ext := filepath.Ext(absPath)
		lang, langName, ok := idx.registry.GetLanguage(ext)
		if !ok {
			continue
		}

		source, err := os.ReadFile(absPath)
		if err != nil {
			fmt.Printf("  warning: cannot read %s: %v\n", file, err)
			continue
		}

		nodes, edges := extractor.Extract(source, lang, langName, relPath)
		allNodes = append(allNodes, nodes...)
		allEdges = append(allEdges, edges...)
	}

	if err := idx.store.UpsertNodes(allNodes); err != nil {
		return fmt.Errorf("storing nodes: %w", err)
	}

	resolved := idx.resolveCallEdges(allEdges)

	if err := idx.store.UpsertEdges(resolved); err != nil {
		return fmt.Errorf("storing edges: %w", err)
	}

	fmt.Printf("  reindexed %d files (%d nodes, %d edges)\n", len(changedFiles), len(allNodes), len(resolved))
	return nil
}

// resolveCallEdges attempts to match bare function names in CALLS edges to
// fully-qualified node IDs from the store. If no unique match is found, the
// edge is kept as-is.
func (idx *Indexer) resolveCallEdges(edges []types.Edge) []types.Edge {
	resolved := make([]types.Edge, 0, len(edges))
	for _, e := range edges {
		if e.Kind != "CALLS" {
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

		// Step 5: No unique match found — keep as-is.
		resolved = append(resolved, e)
	}
	return resolved
}
