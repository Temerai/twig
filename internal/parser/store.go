package parser

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Temerai/twig/internal/types"
)

// Store provides a SQLite-backed graph store for codebase nodes and edges.
type Store struct {
	db *sql.DB
}

// NewStore opens (or creates) a SQLite database at dbPath and initialises
// the schema. The caller must call Close when done.
func NewStore(dbPath string) (*Store, error) {
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
    lines TEXT
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

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO nodes(id, file, language, kind, name, signature, lines) VALUES(?,?,?,?,?,?,?)`)
	if err != nil {
		return fmt.Errorf("prepare upsert nodes: %w", err)
	}
	defer stmt.Close()

	for _, n := range nodes {
		if _, err := stmt.Exec(n.ID, n.File, n.Language, n.Kind, n.Name, n.Signature, n.Lines); err != nil {
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
	row := s.db.QueryRow(`SELECT id, file, language, kind, name, signature, lines FROM nodes WHERE id = ?`, id)
	n := &types.Node{}
	err := row.Scan(&n.ID, &n.File, &n.Language, &n.Kind, &n.Name, &n.Signature, &n.Lines)
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
	rows, err := s.db.Query(`SELECT id, file, language, kind, name, signature, lines FROM nodes WHERE name = ?`, name)
	if err != nil {
		return nil, fmt.Errorf("get nodes by name %s: %w", name, err)
	}
	defer rows.Close()

	var nodes []types.Node
	for rows.Next() {
		var n types.Node
		if err := rows.Scan(&n.ID, &n.File, &n.Language, &n.Kind, &n.Name, &n.Signature, &n.Lines); err != nil {
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
	rows, err := s.db.Query(`SELECT id, file, language, kind, name, signature, lines FROM nodes`)
	if err != nil {
		return nil, fmt.Errorf("querying all nodes: %w", err)
	}
	defer rows.Close()

	var nodes []types.Node
	for rows.Next() {
		var n types.Node
		if err := rows.Scan(&n.ID, &n.File, &n.Language, &n.Kind, &n.Name, &n.Signature, &n.Lines); err != nil {
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
	rows, err := s.db.Query(`SELECT id, file, language, kind, name, signature, lines FROM nodes WHERE name LIKE ?`, pattern)
	if err != nil {
		return nil, fmt.Errorf("search nodes by suffix %s: %w", suffix, err)
	}
	defer rows.Close()

	var nodes []types.Node
	for rows.Next() {
		var n types.Node
		if err := rows.Scan(&n.ID, &n.File, &n.Language, &n.Kind, &n.Name, &n.Signature, &n.Lines); err != nil {
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
			`SELECT id, file, language, kind, name, signature, lines FROM nodes WHERE LOWER(name) LIKE '%' || ? || '%' LIMIT 20`,
			prefix,
		)
		if err != nil {
			return nil, fmt.Errorf("fuzzy search %s: %w", term, err)
		}

		var nodes []types.Node
		for rows.Next() {
			var n types.Node
			if err := rows.Scan(&n.ID, &n.File, &n.Language, &n.Kind, &n.Name, &n.Signature, &n.Lines); err != nil {
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
	rows, err := s.db.Query(`SELECT id, file, language, kind, name, signature, lines FROM nodes WHERE file = ?`, file)
	if err != nil {
		return nil, fmt.Errorf("get nodes by file %s: %w", file, err)
	}
	defer rows.Close()

	var nodes []types.Node
	for rows.Next() {
		var n types.Node
		if err := rows.Scan(&n.ID, &n.File, &n.Language, &n.Kind, &n.Name, &n.Signature, &n.Lines); err != nil {
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
	rows, err := s.db.Query(`SELECT id, file, language, kind, name, signature, lines FROM nodes WHERE name LIKE ?`, pattern)
	if err != nil {
		return nil, fmt.Errorf("get methods of type %s: %w", typeName, err)
	}
	defer rows.Close()

	var nodes []types.Node
	for rows.Next() {
		var n types.Node
		if err := rows.Scan(&n.ID, &n.File, &n.Language, &n.Kind, &n.Name, &n.Signature, &n.Lines); err != nil {
			return nil, fmt.Errorf("scanning node: %w", err)
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}
