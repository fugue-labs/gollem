package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

// SQLiteStore is a persistent Store backed by SQLite (pure-Go, no CGO).
type SQLiteStore struct {
	db *sql.DB
	mu sync.Mutex
}

// NewSQLiteStore creates a persistent Store backed by SQLite.
func NewSQLiteStore(dbPath string) (Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite database: %w", err)
	}

	// Create the documents table.
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS documents (
			namespace TEXT NOT NULL,
			key       TEXT NOT NULL,
			value     TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (namespace, key)
		)
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("creating documents table: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

// Put stores a value under the given namespace and key.
func (s *SQLiteStore) Put(_ context.Context, namespace []string, key string, value any) error {
	valueMap, err := toMap(value)
	if err != nil {
		return fmt.Errorf("sqlite store put: %w", err)
	}

	valueJSON, err := json.Marshal(valueMap)
	if err != nil {
		return fmt.Errorf("marshaling value: %w", err)
	}

	ns := encodeNamespace(namespace)
	now := time.Now().UTC().Format(time.RFC3339Nano)

	s.mu.Lock()
	defer s.mu.Unlock()

	_, err = s.db.Exec(`
		INSERT INTO documents (namespace, key, value, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (namespace, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, ns, key, string(valueJSON), now, now)
	if err != nil {
		return fmt.Errorf("inserting document: %w", err)
	}

	return nil
}

// Get retrieves a single document by namespace and key.
func (s *SQLiteStore) Get(_ context.Context, namespace []string, key string) (*Document, error) {
	ns := encodeNamespace(namespace)

	s.mu.Lock()
	defer s.mu.Unlock()

	row := s.db.QueryRow(`SELECT namespace, key, value, created_at, updated_at FROM documents WHERE namespace = ? AND key = ?`, ns, key)
	return scanDocument(row)
}

// List returns all documents in the given namespace.
func (s *SQLiteStore) List(_ context.Context, namespace []string) ([]*Document, error) {
	ns := encodeNamespace(namespace)

	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`SELECT namespace, key, value, created_at, updated_at FROM documents WHERE namespace = ?`, ns)
	if err != nil {
		return nil, fmt.Errorf("listing documents: %w", err)
	}
	defer rows.Close()

	return scanDocuments(rows)
}

// Search performs a case-insensitive substring search across documents.
func (s *SQLiteStore) Search(_ context.Context, namespace []string, query string, limit int) ([]*Document, error) {
	ns := encodeNamespace(namespace)
	if limit <= 0 {
		limit = 100
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(
		`SELECT namespace, key, value, created_at, updated_at FROM documents WHERE namespace = ? AND LOWER(value) LIKE ? LIMIT ?`,
		ns, "%"+strings.ToLower(query)+"%", limit,
	)
	if err != nil {
		return nil, fmt.Errorf("searching documents: %w", err)
	}
	defer rows.Close()

	return scanDocuments(rows)
}

// Delete removes a document by namespace and key.
func (s *SQLiteStore) Delete(_ context.Context, namespace []string, key string) error {
	ns := encodeNamespace(namespace)

	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`DELETE FROM documents WHERE namespace = ? AND key = ?`, ns, key)
	if err != nil {
		return fmt.Errorf("deleting document: %w", err)
	}
	return nil
}

// Close closes the underlying database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// encodeNamespace joins namespace parts into a single string.
func encodeNamespace(namespace []string) string {
	return strings.Join(namespace, ":")
}

// scanDocument reads a single document from a query row.
func scanDocument(row *sql.Row) (*Document, error) {
	var ns, key, valueJSON, createdStr, updatedStr string
	if err := row.Scan(&ns, &key, &valueJSON, &createdStr, &updatedStr); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scanning document: %w", err)
	}
	return parseDocument(ns, key, valueJSON, createdStr, updatedStr)
}

// scanDocuments reads multiple documents from query rows.
func scanDocuments(rows *sql.Rows) ([]*Document, error) {
	var docs []*Document
	for rows.Next() {
		var ns, key, valueJSON, createdStr, updatedStr string
		if err := rows.Scan(&ns, &key, &valueJSON, &createdStr, &updatedStr); err != nil {
			return nil, fmt.Errorf("scanning document row: %w", err)
		}
		doc, err := parseDocument(ns, key, valueJSON, createdStr, updatedStr)
		if err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}

// parseDocument builds a Document from raw scanned values.
func parseDocument(ns, key, valueJSON, createdStr, updatedStr string) (*Document, error) {
	var value map[string]any
	if err := json.Unmarshal([]byte(valueJSON), &value); err != nil {
		return nil, fmt.Errorf("unmarshaling document value: %w", err)
	}

	created, err := time.Parse(time.RFC3339Nano, createdStr)
	if err != nil {
		return nil, fmt.Errorf("parsing created timestamp: %w", err)
	}
	updated, err := time.Parse(time.RFC3339Nano, updatedStr)
	if err != nil {
		return nil, fmt.Errorf("parsing updated timestamp: %w", err)
	}

	var namespace []string
	if ns != "" {
		namespace = strings.Split(ns, ":")
	}

	return &Document{
		Namespace: namespace,
		Key:       key,
		Value:     value,
		CreatedAt: created,
		UpdatedAt: updated,
	}, nil
}

// Verify SQLiteStore implements Store.
var _ Store = (*SQLiteStore)(nil)
