package parser

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Temerai/twig/internal/types"
)

// DBPathForRoot returns the path to the SQLite database for the given codebase root.
func DBPathForRoot(root string) string {
	return filepath.Join(root, ".twig", "twig.db")
}

// LogPathForRoot returns the path to the execution log database for the given codebase root.
func LogPathForRoot(root string) string {
	return filepath.Join(root, ".twig", "twig.log")
}

// Store provides a SQLite-backed graph store for codebase nodes and edges.
type Store struct {
	db           *sql.DB
	ftsAvailable bool
}

// NewStore opens (or creates) a SQLite database at dbPath and initialises
// the schema. The caller must call Close when done.
func NewStore(dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("creating database directory: %w", err)
	}
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	s := &Store{db: db}
	if err := s.createTables(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) createTables() error {
	ddl := `
CREATE TABLE IF NOT EXISTS nodes(
    id TEXT PRIMARY KEY,
    file TEXT,
    language TEXT,
    kind TEXT,
    name TEXT,
    signature TEXT,
    lines TEXT,
    source TEXT
);
CREATE TABLE IF NOT EXISTS edges(
    src TEXT,
    dst TEXT,
    kind TEXT,
    PRIMARY KEY(src, dst, kind)
);
CREATE INDEX IF NOT EXISTS idx_edges_src ON edges(src);
CREATE INDEX IF NOT EXISTS idx_edges_dst ON edges(dst);
CREATE INDEX IF NOT EXISTS idx_nodes_name ON nodes(name);
CREATE INDEX IF NOT EXISTS idx_nodes_file ON nodes(file);
`
	_, err := s.db.Exec(ddl)
	if err != nil {
		return fmt.Errorf("creating tables: %w", err)
	}

	// Migrate: add source column if it doesn't exist (for pre-existing databases).
	s.db.Exec(`ALTER TABLE nodes ADD COLUMN source TEXT DEFAULT ''`)

	// Migrate: drop stale FTS5 table if schema changed (content= removed, id column added).
	var ftsSQL string
	s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='nodes_fts'`).Scan(&ftsSQL)
	if ftsSQL != "" && !strings.Contains(strings.ToUpper(ftsSQL), "UNINDEXED") {
		s.db.Exec(`DROP TABLE IF EXISTS nodes_fts`)
	}

	// Create FTS5 virtual table. Non-fatal if FTS5 module is unavailable.
	// Plain (non-content) table avoids rowid corruption on INSERT OR REPLACE upserts.
	// id is stored UNINDEXED for precise JOIN back to the nodes table.
	_, ftsErr := s.db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS nodes_fts USING fts5(id UNINDEXED, name, signature, source)`)
	if ftsErr != nil {
		s.ftsAvailable = false
	} else {
		s.ftsAvailable = true
	}

	return nil
}

// UpsertNodes inserts or replaces nodes in a single transaction.
func (s *Store) UpsertNodes(nodes []types.Node) error {
	if len(nodes) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO nodes(id, file, language, kind, name, signature, lines, source) VALUES(?,?,?,?,?,?,?,?)`)
	if err != nil {
		return fmt.Errorf("prepare upsert nodes: %w", err)
	}
	defer stmt.Close()

	for _, n := range nodes {
		if _, err := stmt.Exec(n.ID, n.File, n.Language, n.Kind, n.Name, n.Signature, n.Lines, n.Source); err != nil {
			return fmt.Errorf("upsert node %s: %w", n.ID, err)
		}
	}

	return tx.Commit()
}

// UpsertEdges inserts or replaces edges in a single transaction.
func (s *Store) UpsertEdges(edges []types.Edge) error {
	if len(edges) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO edges(src, dst, kind) VALUES(?,?,?)`)
	if err != nil {
		return fmt.Errorf("prepare upsert edges: %w", err)
	}
	defer stmt.Close()

	for _, e := range edges {
		if _, err := stmt.Exec(e.Src, e.Dst, e.Kind); err != nil {
			return fmt.Errorf("upsert edge %s->%s: %w", e.Src, e.Dst, err)
		}
	}

	return tx.Commit()
}

// DeleteByFile removes all nodes belonging to the given file and all edges
// that reference those nodes (by src or dst), enabling incremental reindex.
func (s *Store) DeleteByFile(file string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete edges where src or dst is a node from this file.
	_, err = tx.Exec(`DELETE FROM edges WHERE src IN (SELECT id FROM nodes WHERE file = ?) OR dst IN (SELECT id FROM nodes WHERE file = ?)`, file, file)
	if err != nil {
		return fmt.Errorf("deleting edges for file %s: %w", file, err)
	}

	// Also delete edges where src is the file path itself (IMPORTS edges).
	_, err = tx.Exec(`DELETE FROM edges WHERE src = ?`, file)
	if err != nil {
		return fmt.Errorf("deleting file-level edges for %s: %w", file, err)
	}

	// Delete the nodes.
	_, err = tx.Exec(`DELETE FROM nodes WHERE file = ?`, file)
	if err != nil {
		return fmt.Errorf("deleting nodes for file %s: %w", file, err)
	}

	return tx.Commit()
}

// GetNode retrieves a single node by its ID.
func (s *Store) GetNode(id string) (*types.Node, error) {
	row := s.db.QueryRow(`SELECT id, file, language, kind, name, signature, lines, source FROM nodes WHERE id = ?`, id)
	n := &types.Node{}
	err := row.Scan(&n.ID, &n.File, &n.Language, &n.Kind, &n.Name, &n.Signature, &n.Lines, &n.Source)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get node %s: %w", id, err)
	}
	return n, nil
}

// GetNodeByName returns all nodes whose name matches exactly.
func (s *Store) GetNodeByName(name string) ([]types.Node, error) {
	rows, err := s.db.Query(`SELECT id, file, language, kind, name, signature, lines, source FROM nodes WHERE name = ?`, name)
	if err != nil {
		return nil, fmt.Errorf("get nodes by name %s: %w", name, err)
	}
	defer rows.Close()

	var nodes []types.Node
	for rows.Next() {
		var n types.Node
		if err := rows.Scan(&n.ID, &n.File, &n.Language, &n.Kind, &n.Name, &n.Signature, &n.Lines, &n.Source); err != nil {
			return nil, fmt.Errorf("scanning node: %w", err)
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// GetEdges returns all edges FROM the given node with the specified kind.
// If kind is empty, all outgoing edges are returned.
func (s *Store) GetEdges(nodeID string, kind string) ([]types.Edge, error) {
	var rows *sql.Rows
	var err error
	if kind == "" {
		rows, err = s.db.Query(`SELECT src, dst, kind FROM edges WHERE src = ?`, nodeID)
	} else {
		rows, err = s.db.Query(`SELECT src, dst, kind FROM edges WHERE src = ? AND kind = ?`, nodeID, kind)
	}
	if err != nil {
		return nil, fmt.Errorf("get edges from %s: %w", nodeID, err)
	}
	defer rows.Close()

	var edges []types.Edge
	for rows.Next() {
		var e types.Edge
		if err := rows.Scan(&e.Src, &e.Dst, &e.Kind); err != nil {
			return nil, fmt.Errorf("scanning edge: %w", err)
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

// GetInEdges returns all edges TO the given node with the specified kind.
// If kind is empty, all incoming edges are returned.
func (s *Store) GetInEdges(nodeID string, kind string) ([]types.Edge, error) {
	var rows *sql.Rows
	var err error
	if kind == "" {
		rows, err = s.db.Query(`SELECT src, dst, kind FROM edges WHERE dst = ?`, nodeID)
	} else {
		rows, err = s.db.Query(`SELECT src, dst, kind FROM edges WHERE dst = ? AND kind = ?`, nodeID, kind)
	}
	if err != nil {
		return nil, fmt.Errorf("get in-edges to %s: %w", nodeID, err)
	}
	defer rows.Close()

	var edges []types.Edge
	for rows.Next() {
		var e types.Edge
		if err := rows.Scan(&e.Src, &e.Dst, &e.Kind); err != nil {
			return nil, fmt.Errorf("scanning edge: %w", err)
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

// AllNodes returns every node in the store.
func (s *Store) AllNodes() ([]types.Node, error) {
	rows, err := s.db.Query(`SELECT id, file, language, kind, name, signature, lines, source FROM nodes`)
	if err != nil {
		return nil, fmt.Errorf("querying all nodes: %w", err)
	}
	defer rows.Close()

	var nodes []types.Node
	for rows.Next() {
		var n types.Node
		if err := rows.Scan(&n.ID, &n.File, &n.Language, &n.Kind, &n.Name, &n.Signature, &n.Lines, &n.Source); err != nil {
			return nil, fmt.Errorf("scanning node: %w", err)
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// Stats returns the total count of nodes and edges in the store.
func (s *Store) Stats() (nodeCount int, edgeCount int, err error) {
	err = s.db.QueryRow(`SELECT COUNT(*) FROM nodes`).Scan(&nodeCount)
	if err != nil {
		return 0, 0, fmt.Errorf("counting nodes: %w", err)
	}
	err = s.db.QueryRow(`SELECT COUNT(*) FROM edges`).Scan(&edgeCount)
	if err != nil {
		return 0, 0, fmt.Errorf("counting edges: %w", err)
	}
	return nodeCount, edgeCount, nil
}

// SearchNodesBySuffix returns nodes whose name ends with ".<suffix>".
func (s *Store) SearchNodesBySuffix(suffix string) ([]types.Node, error) {
	pattern := "%." + suffix
	rows, err := s.db.Query(`SELECT id, file, language, kind, name, signature, lines, source FROM nodes WHERE name LIKE ?`, pattern)
	if err != nil {
		return nil, fmt.Errorf("search nodes by suffix %s: %w", suffix, err)
	}
	defer rows.Close()

	var nodes []types.Node
	for rows.Next() {
		var n types.Node
		if err := rows.Scan(&n.ID, &n.File, &n.Language, &n.Kind, &n.Name, &n.Signature, &n.Lines, &n.Source); err != nil {
			return nil, fmt.Errorf("scanning node: %w", err)
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// SearchNodesFuzzy finds nodes whose name contains a prefix of the search term.
// It tries progressively shorter prefixes (down to 3 chars) until matches are found.
func (s *Store) SearchNodesFuzzy(term string) ([]types.Node, error) {
	lowered := strings.ToLower(term)
	for length := len(lowered); length >= 3; length-- {
		prefix := lowered[:length]
		rows, err := s.db.Query(
			`SELECT id, file, language, kind, name, signature, lines, source FROM nodes WHERE LOWER(name) LIKE '%' || ? || '%' LIMIT 20`,
			prefix,
		)
		if err != nil {
			return nil, fmt.Errorf("fuzzy search %s: %w", term, err)
		}

		var nodes []types.Node
		for rows.Next() {
			var n types.Node
			if err := rows.Scan(&n.ID, &n.File, &n.Language, &n.Kind, &n.Name, &n.Signature, &n.Lines, &n.Source); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scanning node: %w", err)
			}
			nodes = append(nodes, n)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if len(nodes) > 0 {
			return nodes, nil
		}
	}
	return nil, nil
}

// GetNodesByFile returns all nodes belonging to the given file.
func (s *Store) GetNodesByFile(file string) ([]types.Node, error) {
	rows, err := s.db.Query(`SELECT id, file, language, kind, name, signature, lines, source FROM nodes WHERE file = ?`, file)
	if err != nil {
		return nil, fmt.Errorf("get nodes by file %s: %w", file, err)
	}
	defer rows.Close()

	var nodes []types.Node
	for rows.Next() {
		var n types.Node
		if err := rows.Scan(&n.ID, &n.File, &n.Language, &n.Kind, &n.Name, &n.Signature, &n.Lines, &n.Source); err != nil {
			return nil, fmt.Errorf("scanning node: %w", err)
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// SearchFilesByKeyword returns distinct file paths that contain the keyword.
func (s *Store) SearchFilesByKeyword(keyword string) ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT file FROM nodes WHERE LOWER(file) LIKE '%' || LOWER(?) || '%'`, keyword)
	if err != nil {
		return nil, fmt.Errorf("search files by keyword %s: %w", keyword, err)
	}
	defer rows.Close()

	var files []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, fmt.Errorf("scanning file: %w", err)
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// GetMethodsOfType returns all nodes whose name starts with "typeName.".
func (s *Store) GetMethodsOfType(typeName string) ([]types.Node, error) {
	pattern := typeName + ".%"
	rows, err := s.db.Query(`SELECT id, file, language, kind, name, signature, lines, source FROM nodes WHERE name LIKE ?`, pattern)
	if err != nil {
		return nil, fmt.Errorf("get methods of type %s: %w", typeName, err)
	}
	defer rows.Close()

	var nodes []types.Node
	for rows.Next() {
		var n types.Node
		if err := rows.Scan(&n.ID, &n.File, &n.Language, &n.Kind, &n.Name, &n.Signature, &n.Lines, &n.Source); err != nil {
			return nil, fmt.Errorf("scanning node: %w", err)
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// RebuildFTS rebuilds the FTS5 index from the nodes table.
// Full resync: delete all FTS rows then reinsert from nodes.
// This is correct for a plain (non-content) FTS5 table.
func (s *Store) RebuildFTS() error {
	if !s.ftsAvailable {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin RebuildFTS transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM nodes_fts`); err != nil {
		return fmt.Errorf("clearing FTS index: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO nodes_fts(id, name, signature, source) SELECT id, name, signature, source FROM nodes`); err != nil {
		return fmt.Errorf("populating FTS index: %w", err)
	}
	return tx.Commit()
}

// sanitizeFTSQuery wraps a plain search phrase in double quotes so that FTS5
// special characters (parentheses, dots, hyphens, etc.) do not cause parse
// errors. If the query already contains FTS5 operator syntax it is returned
// unchanged, allowing callers to construct advanced queries deliberately.
func sanitizeFTSQuery(q string) string {
	operators := []string{`"`, `(`, `)`, " OR ", " AND ", " NOT ", " NEAR "}
	for _, op := range operators {
		if strings.Contains(q, op) {
			return q
		}
	}
	// Trailing * is a valid FTS5 prefix query (e.g., "foo*").
	if strings.HasSuffix(q, "*") {
		return q
	}
	return `"` + strings.ReplaceAll(q, `"`, `""`) + `"`
}

// SearchFTS performs a full-text search over node names, signatures, and source text.
func (s *Store) SearchFTS(query string, limit int) ([]types.Node, error) {
	if !s.ftsAvailable {
		return nil, fmt.Errorf("FTS5 not available: rebuild with sqlite_fts5 build tag")
	}
	if limit <= 0 {
		limit = 20
	}
	sanitized := sanitizeFTSQuery(query)
	rows, err := s.db.Query(`
		SELECT n.id, n.file, n.language, n.kind, n.name, n.signature, n.lines, n.source
		FROM nodes_fts f
		JOIN nodes n ON n.id = f.id
		WHERE nodes_fts MATCH ?
		ORDER BY rank
		LIMIT ?`, sanitized, limit)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}
	defer rows.Close()

	var results []types.Node
	for rows.Next() {
		var n types.Node
		if err := rows.Scan(&n.ID, &n.File, &n.Language, &n.Kind, &n.Name, &n.Signature, &n.Lines, &n.Source); err != nil {
			return nil, fmt.Errorf("scanning FTS result: %w", err)
		}
		results = append(results, n)
	}
	return results, rows.Err()
}

// DetailedStats returns a breakdown of graph contents by node/edge kind and language.
func (s *Store) DetailedStats() (*types.DetailedStats, error) {
	stats := &types.DetailedStats{
		NodesByKind: make(map[string]int),
		EdgesByKind: make(map[string]int),
		Languages:   make(map[string]int),
	}

	if err := s.db.QueryRow(`SELECT COUNT(*) FROM nodes`).Scan(&stats.NodeCount); err != nil {
		return nil, fmt.Errorf("counting nodes: %w", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM edges`).Scan(&stats.EdgeCount); err != nil {
		return nil, fmt.Errorf("counting edges: %w", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(DISTINCT file) FROM nodes`).Scan(&stats.FileCount); err != nil {
		return nil, fmt.Errorf("counting files: %w", err)
	}

	rows, err := s.db.Query(`SELECT kind, COUNT(*) FROM nodes GROUP BY kind`)
	if err != nil {
		return nil, fmt.Errorf("nodes by kind: %w", err)
	}
	for rows.Next() {
		var kind string
		var count int
		if err := rows.Scan(&kind, &count); err != nil {
			rows.Close()
			return nil, err
		}
		stats.NodesByKind[kind] = count
	}
	rows.Close()

	rows, err = s.db.Query(`SELECT kind, COUNT(*) FROM edges GROUP BY kind`)
	if err != nil {
		return nil, fmt.Errorf("edges by kind: %w", err)
	}
	for rows.Next() {
		var kind string
		var count int
		if err := rows.Scan(&kind, &count); err != nil {
			rows.Close()
			return nil, err
		}
		stats.EdgesByKind[kind] = count
	}
	rows.Close()

	rows, err = s.db.Query(`SELECT language, COUNT(*) FROM nodes GROUP BY language`)
	if err != nil {
		return nil, fmt.Errorf("languages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var lang string
		var count int
		if err := rows.Scan(&lang, &count); err != nil {
			return nil, err
		}
		stats.Languages[lang] = count
	}

	return stats, rows.Err()
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}
