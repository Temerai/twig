package logger

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// RunRecord represents a single execution run persisted in the log.
type RunRecord struct {
	ID            string
	TaskType      string
	PromptVersion int
	Model         string
	Input         string
	Output        string
	GraphQueries  []GraphQueryLog // serialized to JSON in graph_queries column
	TokensIn      int
	TokensOut     int
	LatencyMs     int64
	CreatedAt     time.Time
}

// GraphQueryLog captures metadata about a single graph query within a run.
type GraphQueryLog struct {
	Question    string `json:"question"`
	TokenBudget int    `json:"token_budget"`
	Strategy    string `json:"strategy"`
	TokensUsed  int    `json:"tokens_used"`
	NodeCount   int    `json:"node_count"`
}

// QueryFilter controls which run records are returned by Query.
type QueryFilter struct {
	TaskType string
	Limit    int
}

// Logger provides append-only SQLite-backed execution logging.
type Logger struct {
	db *sql.DB
}

const createTableSQL = `
CREATE TABLE IF NOT EXISTS runs(
    id TEXT PRIMARY KEY,
    task_type TEXT,
    prompt_version INT,
    model TEXT,
    input TEXT,
    output TEXT,
    graph_queries TEXT,
    tokens_in INT,
    tokens_out INT,
    latency_ms INT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);`

// NewLogger opens (or creates) the SQLite database at dbPath and ensures
// the runs table exists.
func NewLogger(dbPath string) (*Logger, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("logger: open db: %w", err)
	}
	if _, err := db.Exec(createTableSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("logger: create table: %w", err)
	}
	return &Logger{db: db}, nil
}

// generateID produces a 32-character hex string from 16 random bytes.
func generateID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("logger: generate id: %w", err)
	}
	return fmt.Sprintf("%x", b), nil
}

// Write inserts a RunRecord into the log. If record.ID is empty a new
// random hex ID is generated.
func (l *Logger) Write(record RunRecord) error {
	if record.ID == "" {
		id, err := generateID()
		if err != nil {
			return err
		}
		record.ID = id
	}

	gqJSON, err := json.Marshal(record.GraphQueries)
	if err != nil {
		return fmt.Errorf("logger: marshal graph_queries: %w", err)
	}

	_, err = l.db.Exec(
		`INSERT INTO runs (id, task_type, prompt_version, model, input, output, graph_queries, tokens_in, tokens_out, latency_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID,
		record.TaskType,
		record.PromptVersion,
		record.Model,
		record.Input,
		record.Output,
		string(gqJSON),
		record.TokensIn,
		record.TokensOut,
		record.LatencyMs,
	)
	if err != nil {
		return fmt.Errorf("logger: insert run: %w", err)
	}
	return nil
}

// Query returns run records matching the filter, ordered by created_at DESC.
// If filter.TaskType is non-empty only matching rows are returned.
// If filter.Limit is <= 0 a default of 100 is used.
func (l *Logger) Query(filter QueryFilter) ([]RunRecord, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}

	var rows *sql.Rows
	var err error

	if filter.TaskType != "" {
		rows, err = l.db.Query(
			`SELECT id, task_type, prompt_version, model, input, output, graph_queries, tokens_in, tokens_out, latency_ms, created_at
			 FROM runs WHERE task_type = ? ORDER BY created_at DESC LIMIT ?`,
			filter.TaskType, limit,
		)
	} else {
		rows, err = l.db.Query(
			`SELECT id, task_type, prompt_version, model, input, output, graph_queries, tokens_in, tokens_out, latency_ms, created_at
			 FROM runs ORDER BY created_at DESC LIMIT ?`,
			limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("logger: query runs: %w", err)
	}
	defer rows.Close()

	return scanRunRecords(rows)
}

// GetRun retrieves a single run record by its ID. Returns nil if not found.
func (l *Logger) GetRun(id string) (*RunRecord, error) {
	row := l.db.QueryRow(
		`SELECT id, task_type, prompt_version, model, input, output, graph_queries, tokens_in, tokens_out, latency_ms, created_at
		 FROM runs WHERE id = ?`,
		id,
	)

	var rec RunRecord
	var gqJSON string
	err := row.Scan(
		&rec.ID,
		&rec.TaskType,
		&rec.PromptVersion,
		&rec.Model,
		&rec.Input,
		&rec.Output,
		&gqJSON,
		&rec.TokensIn,
		&rec.TokensOut,
		&rec.LatencyMs,
		&rec.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("logger: get run: %w", err)
	}

	if gqJSON != "" {
		if err := json.Unmarshal([]byte(gqJSON), &rec.GraphQueries); err != nil {
			return nil, fmt.Errorf("logger: unmarshal graph_queries: %w", err)
		}
	}

	return &rec, nil
}

// Close closes the underlying database connection.
func (l *Logger) Close() error {
	return l.db.Close()
}

// scanRunRecords reads all rows into a slice of RunRecord.
func scanRunRecords(rows *sql.Rows) ([]RunRecord, error) {
	var records []RunRecord
	for rows.Next() {
		var rec RunRecord
		var gqJSON string
		err := rows.Scan(
			&rec.ID,
			&rec.TaskType,
			&rec.PromptVersion,
			&rec.Model,
			&rec.Input,
			&rec.Output,
			&gqJSON,
			&rec.TokensIn,
			&rec.TokensOut,
			&rec.LatencyMs,
			&rec.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("logger: scan run: %w", err)
		}
		if gqJSON != "" {
			if err := json.Unmarshal([]byte(gqJSON), &rec.GraphQueries); err != nil {
				return nil, fmt.Errorf("logger: unmarshal graph_queries: %w", err)
			}
		}
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("logger: rows iteration: %w", err)
	}
	return records, nil
}
