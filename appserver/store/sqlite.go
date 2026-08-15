package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

const timeFormat = time.RFC3339Nano

var _ Store = (*SQLiteStore)(nil)
var _ RuntimeRecoveryStore = (*SQLiteStore)(nil)
var _ ThreadForkRecoveryStore = (*SQLiteStore)(nil)
var _ FileChangeRecoveryStore = (*SQLiteStore)(nil)
var _ TerminalOwnershipStore = (*SQLiteStore)(nil)

// SQLiteStore persists app-server state in SQLite.
type SQLiteStore struct {
	db *sql.DB
	mu sync.Mutex
}

// NewSQLiteStore opens or creates an app-server SQLite store.
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	if dbPath == "" {
		return nil, errors.New("appserver/store: db path must not be empty")
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)

	s := &SQLiteStore{db: db}
	if err := s.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func sqliteDSN(dbPath string) string {
	if dbPath == ":memory:" {
		return dbPath
	}
	separator := "?"
	if strings.Contains(dbPath, "?") {
		separator = "&"
	}
	return dbPath + separator + "_txlock=immediate"
}

// Close closes the database handle.
func (s *SQLiteStore) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	db := s.db
	s.db = nil
	return db.Close()
}

func (s *SQLiteStore) init() error {
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `PRAGMA journal_mode = WAL`); err != nil {
		return fmt.Errorf("set sqlite journal mode: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA busy_timeout = 5000`); err != nil {
		return fmt.Errorf("set sqlite busy timeout: %w", err)
	}
	schema := []string{
		`CREATE TABLE IF NOT EXISTS app_threads (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			payload BLOB NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_app_threads_status_updated ON app_threads(status, updated_at, id)`,
		`CREATE TABLE IF NOT EXISTS app_turns (
			id TEXT PRIMARY KEY,
			thread_id TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			payload BLOB NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_app_turns_thread_created ON app_turns(thread_id, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_app_turns_status_created ON app_turns(status, created_at, id)`,
		`CREATE TABLE IF NOT EXISTS app_items (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			id TEXT NOT NULL UNIQUE,
			thread_id TEXT NOT NULL,
			turn_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			payload BLOB NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_app_items_thread_seq ON app_items(thread_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_app_items_turn_seq ON app_items(turn_id, seq)`,
		`CREATE TABLE IF NOT EXISTS app_thread_forks (
			idempotency_key TEXT PRIMARY KEY,
			source_thread_id TEXT NOT NULL,
			fork_thread_id TEXT NOT NULL UNIQUE,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_app_thread_forks_source ON app_thread_forks(source_thread_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS app_file_change_recovery (
			item_id TEXT PRIMARY KEY,
			thread_id TEXT NOT NULL,
			turn_id TEXT NOT NULL,
			status TEXT NOT NULL,
			idempotency_key TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL,
			payload BLOB NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_app_file_change_recovery_thread ON app_file_change_recovery(thread_id, turn_id, item_id)`,
		`CREATE TABLE IF NOT EXISTS app_terminal_ownership (
			id TEXT PRIMARY KEY,
			workspace_key TEXT NOT NULL,
			process_id TEXT NOT NULL,
			status TEXT NOT NULL,
			started_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			payload BLOB NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_app_terminal_ownership_workspace ON app_terminal_ownership(workspace_key, started_at DESC, id ASC)`,
	}
	for _, stmt := range schema {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("initialize appserver store schema: %w", err)
		}
	}
	return nil
}

func (s *SQLiteStore) withTx(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	db, err := s.dbLocked()
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sqlite transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
			return
		}
		err = tx.Commit()
	}()
	err = fn(tx)
	return err
}

func (s *SQLiteStore) dbLocked() (*sql.DB, error) {
	if s == nil || s.db == nil {
		return nil, ErrStoreClosed
	}
	return s.db, nil
}

func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// SaveTerminalOwnership writes a redacted terminal lifecycle snapshot. Older
// events cannot overwrite a newer terminal state, which keeps concurrent exit
// and startup reconciliation from reviving an owner-lost record.
func (s *SQLiteStore) SaveTerminalOwnership(ctx context.Context, record TerminalOwnershipRecord) (*TerminalOwnershipRecord, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	record = cloneTerminalOwnershipRecord(record)
	if err := normalizeTerminalOwnershipRecord(&record); err != nil {
		return nil, err
	}
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		existing, err := loadTerminalOwnershipTx(ctx, tx, record.ID)
		if err == nil && !record.UpdatedAt.After(existing.UpdatedAt) {
			record = *existing
			return nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return saveTerminalOwnershipTx(ctx, tx, &record)
	}); err != nil {
		return nil, err
	}
	return terminalOwnershipRecordPtr(record), nil
}

// ListTerminalOwnership returns terminal lifecycle records for one opaque
// workspace identity. Results are newest-first to make live operational views
// useful without exposing the workspace path used to scope the record.
func (s *SQLiteStore) ListTerminalOwnership(ctx context.Context, workspaceKey string) ([]*TerminalOwnershipRecord, error) {
	ctx = normalizeContext(ctx)
	workspaceKey = strings.TrimSpace(workspaceKey)
	if workspaceKey == "" {
		return nil, errors.New("appserver/store: terminal ownership workspace key is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	db, err := s.dbLocked()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT process_id, payload FROM app_terminal_ownership WHERE workspace_key = ? ORDER BY started_at DESC, id ASC`, workspaceKey)
	if err != nil {
		return nil, fmt.Errorf("list terminal ownership: %w", err)
	}
	defer rows.Close()
	var records []*TerminalOwnershipRecord
	for rows.Next() {
		record, err := scanTerminalOwnership(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, terminalOwnershipRecordPtr(*record))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate terminal ownership: %w", err)
	}
	return records, nil
}

// RecoverTerminalOwnership terminalizes only records that were still running
// and have no matching live process in the newly-created process service.
// This is deliberately not a reconnect mechanism: the old process/output is
// unavailable to the new execution owner.
func (s *SQLiteStore) RecoverTerminalOwnership(ctx context.Context, req RecoverTerminalOwnershipRequest) ([]*TerminalOwnershipRecord, error) {
	ctx = normalizeContext(ctx)
	workspaceKey := strings.TrimSpace(req.WorkspaceKey)
	if workspaceKey == "" {
		return nil, errors.New("appserver/store: terminal ownership workspace key is required")
	}
	recoveredAt := req.RecoveredAt.UTC()
	if recoveredAt.IsZero() {
		recoveredAt = time.Now().UTC()
	}
	live := make(map[string]struct{}, len(req.LiveProcessIDs))
	for _, id := range req.LiveProcessIDs {
		if id = strings.TrimSpace(id); id != "" {
			live[id] = struct{}{}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	var recovered []*TerminalOwnershipRecord
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		records, err := loadTerminalOwnershipForWorkspaceTx(ctx, tx, workspaceKey)
		if err != nil {
			return err
		}
		for _, record := range records {
			if record.Status != TerminalOwnershipRunning {
				continue
			}
			if _, ok := live[record.ProcessID]; ok {
				continue
			}
			record.Status = TerminalOwnershipOwnerLost
			record.OwnerLostAt = timePtr(recoveredAt)
			record.EndedAt = timePtr(recoveredAt)
			record.ExitCode = nil
			record.UpdatedAt = recoveredAt
			if err := saveTerminalOwnershipTx(ctx, tx, record); err != nil {
				return err
			}
			recovered = append(recovered, terminalOwnershipRecordPtr(*record))
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return recovered, nil
}

// DeleteInactiveTerminalOwnership clears completed or owner-lost records from
// a workspace after the operational terminal inventory is cleaned.
func (s *SQLiteStore) DeleteInactiveTerminalOwnership(ctx context.Context, workspaceKey string) error {
	ctx = normalizeContext(ctx)
	workspaceKey = strings.TrimSpace(workspaceKey)
	if workspaceKey == "" {
		return errors.New("appserver/store: terminal ownership workspace key is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	db, err := s.dbLocked()
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM app_terminal_ownership WHERE workspace_key = ? AND status <> ?`, workspaceKey, TerminalOwnershipRunning); err != nil {
		return fmt.Errorf("delete inactive terminal ownership: %w", err)
	}
	return nil
}

// CreateThread implements Store.
func (s *SQLiteStore) CreateThread(ctx context.Context, req CreateThreadRequest) (*Thread, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	thread := &Thread{
		ID:        newID("thread"),
		Title:     req.Title,
		Workspace: req.Workspace,
		Status:    ThreadActive,
		Settings:  cloneMap(req.Settings),
		Metadata:  cloneMap(req.Metadata),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		return saveThreadTx(ctx, tx, thread)
	}); err != nil {
		return nil, err
	}
	return cloneThread(thread), nil
}

// GetThread implements Store.
func (s *SQLiteStore) GetThread(ctx context.Context, id string) (*Thread, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	db, err := s.dbLocked()
	if err != nil {
		return nil, err
	}
	thread, err := loadThread(ctx, db, id)
	if err != nil {
		return nil, err
	}
	return cloneThread(thread), nil
}

// ListThreads implements Store.
func (s *SQLiteStore) ListThreads(ctx context.Context, filter ThreadFilter) ([]*Thread, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.dbLocked()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT payload FROM app_threads ORDER BY updated_at DESC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list threads: %w", err)
	}
	defer rows.Close()

	var out []*Thread
	for rows.Next() {
		thread, err := scanThread(rows)
		if err != nil {
			return nil, err
		}
		if !matchesThreadFilter(thread, filter) {
			continue
		}
		out = append(out, cloneThread(thread))
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate threads: %w", err)
	}
	return out, nil
}

// ArchiveThread implements Store.
func (s *SQLiteStore) ArchiveThread(ctx context.Context, id string) (*Thread, error) {
	return s.setThreadStatus(ctx, id, ThreadArchived)
}

// UnarchiveThread implements Store.
func (s *SQLiteStore) UnarchiveThread(ctx context.Context, id string) (*Thread, error) {
	return s.setThreadStatus(ctx, id, ThreadActive)
}

// DeleteThread implements Store. Deletion is soft so recovery and audit
// surfaces can still inspect the thread.
func (s *SQLiteStore) DeleteThread(ctx context.Context, id string) (*Thread, error) {
	return s.setThreadStatus(ctx, id, ThreadDeleted)
}

func (s *SQLiteStore) setThreadStatus(ctx context.Context, id string, status ThreadStatus) (*Thread, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	var thread *Thread
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		loaded, err := loadThreadTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if loaded.Status == ThreadDeleted && status != ThreadDeleted {
			return ErrThreadDeleted
		}
		if status == ThreadArchived || status == ThreadDeleted {
			if err := ensureThreadHasNoActiveTurnTx(ctx, tx, loaded.ID); err != nil {
				return err
			}
		}
		now := time.Now().UTC()
		loaded.Status = status
		loaded.UpdatedAt = now
		switch status {
		case ThreadArchived:
			loaded.ArchivedAt = now
		case ThreadActive:
			loaded.ArchivedAt = time.Time{}
		case ThreadDeleted:
			loaded.DeletedAt = now
		}
		if err := saveThreadTx(ctx, tx, loaded); err != nil {
			return err
		}
		thread = loaded
		return nil
	}); err != nil {
		return nil, err
	}
	return cloneThread(thread), nil
}

// ForkThread implements Store.
func (s *SQLiteStore) ForkThread(ctx context.Context, req ForkThreadRequest) (*Thread, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	var fork *Thread
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		source, err := loadThreadTx(ctx, tx, req.SourceThreadID)
		if err != nil {
			return err
		}
		if source.Status == ThreadDeleted {
			return ErrThreadDeleted
		}
		if req.IncludeItems {
			if err := ensureThreadHasNoActiveTurnTx(ctx, tx, source.ID); err != nil {
				return err
			}
		}
		now := time.Now().UTC()
		title := req.Title
		if title == "" {
			title = source.Title
		}
		fork = &Thread{
			ID:                 newID("thread"),
			Title:              title,
			Workspace:          source.Workspace,
			Status:             ThreadActive,
			ForkedFromThreadID: source.ID,
			Settings:           cloneMap(source.Settings),
			Metadata:           mergeMaps(source.Metadata, req.Metadata),
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		if err := saveThreadTx(ctx, tx, fork); err != nil {
			return err
		}
		if req.IncludeItems {
			if err := copyThreadHistoryTx(ctx, tx, source.ID, fork.ID, now); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return cloneThread(fork), nil
}

// PrepareThreadFork atomically creates or reuses one full-history fork for an
// idempotency key. A new fork is rejected while the source has active work.
func (s *SQLiteStore) PrepareThreadFork(
	ctx context.Context,
	req PrepareThreadForkRequest,
) (*PrepareThreadForkResult, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	req.SourceThreadID = strings.TrimSpace(req.SourceThreadID)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.SourceThreadID == "" {
		return nil, ErrThreadNotFound
	}
	if req.IdempotencyKey == "" || len(req.IdempotencyKey) > 256 {
		return nil, errors.New("appserver/store: fork idempotency key must contain 1 to 256 bytes")
	}

	var result *PrepareThreadForkResult
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		var existingSourceID, existingForkID string
		err := tx.QueryRowContext(
			ctx,
			`SELECT source_thread_id, fork_thread_id
			 FROM app_thread_forks
			 WHERE idempotency_key = ?`,
			req.IdempotencyKey,
		).Scan(&existingSourceID, &existingForkID)
		switch {
		case err == nil:
			if existingSourceID != req.SourceThreadID {
				return ErrThreadForkIdempotencyConflict
			}
			fork, loadErr := loadThreadTx(ctx, tx, existingForkID)
			if loadErr != nil {
				return fmt.Errorf("load prepared fork %q: %w", existingForkID, loadErr)
			}
			result = &PrepareThreadForkResult{Thread: cloneThread(fork), Created: false}
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("read prepared thread fork: %w", err)
		}

		source, err := loadThreadTx(ctx, tx, req.SourceThreadID)
		if err != nil {
			return err
		}
		if source.Status == ThreadDeleted {
			return ErrThreadDeleted
		}
		if err := ensureThreadHasNoActiveTurnTx(ctx, tx, source.ID); err != nil {
			return err
		}

		now := time.Now().UTC()
		fork := &Thread{
			ID:                 newID("thread"),
			Title:              source.Title,
			Workspace:          source.Workspace,
			Status:             ThreadActive,
			ForkedFromThreadID: source.ID,
			Settings:           cloneMap(source.Settings),
			Metadata:           cloneMap(source.Metadata),
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		if err := saveThreadTx(ctx, tx, fork); err != nil {
			return err
		}
		if err := copyThreadHistoryTx(ctx, tx, source.ID, fork.ID, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO app_thread_forks (
				idempotency_key, source_thread_id, fork_thread_id, created_at
			) VALUES (?, ?, ?, ?)`,
			req.IdempotencyKey,
			source.ID,
			fork.ID,
			now.Format(timeFormat),
		); err != nil {
			return fmt.Errorf("save prepared thread fork: %w", err)
		}
		result = &PrepareThreadForkResult{Thread: cloneThread(fork), Created: true}
		return nil
	}); err != nil {
		return nil, err
	}
	return result, nil
}

// UpdateThreadTitle implements Store.
func (s *SQLiteStore) UpdateThreadTitle(ctx context.Context, id, title string) (*Thread, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	var thread *Thread
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		loaded, err := loadThreadTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if loaded.Status == ThreadDeleted {
			return ErrThreadDeleted
		}
		loaded.Title = title
		loaded.UpdatedAt = time.Now().UTC()
		if err := saveThreadTx(ctx, tx, loaded); err != nil {
			return err
		}
		thread = loaded
		return nil
	}); err != nil {
		return nil, err
	}
	return cloneThread(thread), nil
}

// UpdateThreadSettings implements Store.
func (s *SQLiteStore) UpdateThreadSettings(ctx context.Context, req UpdateThreadSettingsRequest) (*Thread, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	var thread *Thread
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		loaded, err := loadThreadTx(ctx, tx, req.ID)
		if err != nil {
			return err
		}
		if loaded.Status == ThreadDeleted {
			return ErrThreadDeleted
		}
		if req.Replace {
			loaded.Settings = cloneMap(req.Settings)
			loaded.Metadata = cloneMap(req.Metadata)
		} else {
			loaded.Settings = mergeMaps(loaded.Settings, req.Settings)
			loaded.Metadata = mergeMaps(loaded.Metadata, req.Metadata)
		}
		loaded.UpdatedAt = time.Now().UTC()
		if err := saveThreadTx(ctx, tx, loaded); err != nil {
			return err
		}
		thread = loaded
		return nil
	}); err != nil {
		return nil, err
	}
	return cloneThread(thread), nil
}

// CreateTurn implements Store.
func (s *SQLiteStore) CreateTurn(ctx context.Context, req CreateTurnRequest) (*Turn, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	var turn *Turn
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		thread, err := loadThreadTx(ctx, tx, req.ThreadID)
		if err != nil {
			return err
		}
		switch thread.Status {
		case ThreadDeleted:
			return ErrThreadDeleted
		case ThreadArchived:
			return ErrThreadArchived
		}
		now := time.Now().UTC()
		turn = &Turn{
			ID:        newID("turn"),
			ThreadID:  thread.ID,
			Status:    TurnQueued,
			Input:     cloneRaw(req.Input),
			Metadata:  cloneMap(req.Metadata),
			CreatedAt: now,
			UpdatedAt: now,
		}
		return saveTurnTx(ctx, tx, turn)
	}); err != nil {
		return nil, err
	}
	return cloneTurn(turn), nil
}

// StartTurn implements Store.
func (s *SQLiteStore) StartTurn(ctx context.Context, id string) (*Turn, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	var turn *Turn
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		loaded, err := loadTurnTx(ctx, tx, id)
		if err != nil {
			return err
		}
		thread, err := loadThreadTx(ctx, tx, loaded.ThreadID)
		if err != nil {
			return err
		}
		switch thread.Status {
		case ThreadDeleted:
			return ErrThreadDeleted
		case ThreadArchived:
			return ErrThreadArchived
		}
		now := time.Now().UTC()
		loaded.Status = TurnRunning
		loaded.StartedAt = now
		loaded.UpdatedAt = now
		if err := saveTurnTx(ctx, tx, loaded); err != nil {
			return err
		}
		turn = loaded
		return nil
	}); err != nil {
		return nil, err
	}
	return cloneTurn(turn), nil
}

// CompleteTurn implements Store.
func (s *SQLiteStore) CompleteTurn(ctx context.Context, req CompleteTurnRequest) (*Turn, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	var turn *Turn
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		loaded, err := loadTurnTx(ctx, tx, req.ID)
		if err != nil {
			return err
		}
		thread, err := loadThreadTx(ctx, tx, loaded.ThreadID)
		if err != nil {
			return err
		}
		if thread.Status == ThreadDeleted {
			return ErrThreadDeleted
		}
		status := req.Status
		if status == "" {
			status = TurnCompleted
		}
		now := time.Now().UTC()
		loaded.Status = status
		loaded.Result = cloneRaw(req.Result)
		loaded.Error = req.Error
		loaded.Usage = cloneMap(req.Usage)
		loaded.CompletedAt = now
		loaded.UpdatedAt = now
		if err := saveTurnTx(ctx, tx, loaded); err != nil {
			return err
		}
		turn = loaded
		return nil
	}); err != nil {
		return nil, err
	}
	return cloneTurn(turn), nil
}

// GetTurn implements Store.
func (s *SQLiteStore) GetTurn(ctx context.Context, id string) (*Turn, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	db, err := s.dbLocked()
	if err != nil {
		return nil, err
	}
	turn, err := loadTurn(ctx, db, id)
	if err != nil {
		return nil, err
	}
	return cloneTurn(turn), nil
}

// ListTurns implements Store.
func (s *SQLiteStore) ListTurns(ctx context.Context, filter TurnFilter) ([]*Turn, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.dbLocked()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT payload FROM app_turns ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list turns: %w", err)
	}
	defer rows.Close()

	var out []*Turn
	for rows.Next() {
		turn, err := scanTurn(rows)
		if err != nil {
			return nil, err
		}
		if !matchesTurnFilter(turn, filter) {
			continue
		}
		out = append(out, cloneTurn(turn))
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate turns: %w", err)
	}
	return out, nil
}

// PrepareTurnRetry atomically creates or reuses one retry turn for an
// idempotency key. The source must already be terminal.
func (s *SQLiteStore) PrepareTurnRetry(ctx context.Context, req PrepareTurnRetryRequest) (*PrepareTurnRetryResult, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	req.SourceTurnID = strings.TrimSpace(req.SourceTurnID)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.SourceTurnID == "" {
		return nil, ErrTurnNotFound
	}
	if req.IdempotencyKey == "" || len(req.IdempotencyKey) > 256 {
		return nil, errors.New("appserver/store: retry idempotency key must contain 1 to 256 bytes")
	}

	var result *PrepareTurnRetryResult
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		source, err := loadTurnTx(ctx, tx, req.SourceTurnID)
		if err != nil {
			return err
		}
		if !terminalTurnStatus(source.Status) {
			return ErrTurnNotTerminal
		}
		thread, err := loadThreadTx(ctx, tx, source.ThreadID)
		if err != nil {
			return err
		}
		if thread.Status == ThreadDeleted {
			return ErrThreadDeleted
		}
		if thread.Status == ThreadArchived {
			return ErrThreadArchived
		}

		turns, err := loadAllTurnsTx(ctx, tx)
		if err != nil {
			return err
		}
		for _, candidate := range turns {
			if candidate.RetryIdempotencyKey != req.IdempotencyKey {
				continue
			}
			if candidate.RetryOfTurnID != source.ID {
				return ErrRetryIdempotencyConflict
			}
			result = &PrepareTurnRetryResult{
				Source:  cloneTurn(source),
				Turn:    cloneTurn(candidate),
				Created: false,
			}
			return nil
		}

		now := time.Now().UTC()
		input := cloneRaw(req.Input)
		if len(input) == 0 {
			input = cloneRaw(source.Input)
		}
		retry := &Turn{
			ID:                  newID("turn"),
			ThreadID:            source.ThreadID,
			Status:              TurnQueued,
			Input:               input,
			Metadata:            cloneMap(req.Metadata),
			CreatedAt:           now,
			UpdatedAt:           now,
			RetryOfTurnID:       source.ID,
			RetryIdempotencyKey: req.IdempotencyKey,
		}
		if err := saveTurnTx(ctx, tx, retry); err != nil {
			return err
		}
		result = &PrepareTurnRetryResult{
			Source:  cloneTurn(source),
			Turn:    cloneTurn(retry),
			Created: true,
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return result, nil
}

func ensureThreadHasNoActiveTurnTx(ctx context.Context, tx *sql.Tx, threadID string) error {
	var active int
	err := tx.QueryRowContext(
		ctx,
		`SELECT 1
		 FROM app_turns
		 WHERE thread_id = ? AND status IN (?, ?)
		 LIMIT 1`,
		threadID,
		TurnQueued,
		TurnRunning,
	).Scan(&active)
	switch {
	case err == nil:
		return ErrThreadHasActiveTurn
	case errors.Is(err, sql.ErrNoRows):
		return nil
	default:
		return fmt.Errorf("check active thread turns: %w", err)
	}
}

// RecoverOrphanedTurns terminalizes work whose in-memory execution owner did
// not survive. It is idempotent: only queued/running turns are changed.
func (s *SQLiteStore) RecoverOrphanedTurns(ctx context.Context, req RecoverOrphanedTurnsRequest) (*RecoverOrphanedTurnsResult, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	recoveredAt := req.RecoveredAt.UTC()
	if recoveredAt.IsZero() {
		recoveredAt = time.Now().UTC()
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = RuntimeOwnerLostReason
	}
	result := &RecoverOrphanedTurnsResult{}
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		turns, err := loadTurnsByStatusesTx(ctx, tx, TurnQueued, TurnRunning)
		if err != nil {
			return err
		}
		for _, turn := range turns {
			previousStatus := turn.Status
			items, err := loadItemsForTurnTx(ctx, tx, turn.ID)
			if err != nil {
				return err
			}
			var recoveredItemIDs []string
			for _, item := range items {
				if !orphanedItemStatus(item.Status) {
					continue
				}
				reconciled, err := reconcileDurableFileChangeItemTx(ctx, tx, item, recoveredAt)
				if err != nil {
					return err
				}
				if reconciled {
					result.Items = append(result.Items, cloneItem(item))
					continue
				}
				item.Status = "failed"
				item.Payload = recoveredItemPayload(item.Payload)
				item.UpdatedAt = recoveredAt
				if err := saveItemTx(ctx, tx, item); err != nil {
					return err
				}
				recoveredItemIDs = append(recoveredItemIDs, item.ID)
				result.Items = append(result.Items, cloneItem(item))
			}

			turn.Status = TurnInterrupted
			turn.Error = reason
			turn.CompletedAt = recoveredAt
			turn.UpdatedAt = recoveredAt
			if err := saveTurnTx(ctx, tx, turn); err != nil {
				return err
			}
			result.Turns = append(result.Turns, cloneTurn(turn))

			markerPayload, err := json.Marshal(map[string]any{
				"turnId":           turn.ID,
				"previousStatus":   previousStatus,
				"status":           turn.Status,
				"reason":           reason,
				"recoveredItemIds": recoveredItemIDs,
				"recoveredAt":      recoveredAt,
			})
			if err != nil {
				return fmt.Errorf("marshal runtime recovery marker: %w", err)
			}
			marker := &Item{
				ID:        newID("item"),
				ThreadID:  turn.ThreadID,
				TurnID:    turn.ID,
				Kind:      "runtimeRecovery",
				Status:    "completed",
				Payload:   markerPayload,
				CreatedAt: recoveredAt,
				UpdatedAt: recoveredAt,
			}
			if err := saveItemTx(ctx, tx, marker); err != nil {
				return err
			}
			result.Markers = append(result.Markers, cloneItem(marker))
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return result, nil
}

func reconcileDurableFileChangeItemTx(
	ctx context.Context,
	tx *sql.Tx,
	item *Item,
	reconciledAt time.Time,
) (bool, error) {
	if item == nil || item.Kind != "fileChange" {
		return false, nil
	}
	recovery, err := loadFileChangeRecoveryTx(ctx, tx, item.ID)
	if errors.Is(err, ErrFileChangeRecoveryNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if recovery.ThreadID != item.ThreadID || recovery.TurnID != item.TurnID {
		return false, errors.New("appserver/store: file-change recovery ownership mismatch")
	}
	var payload map[string]any
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		return false, fmt.Errorf("decode durable file-change item %q: %w", item.ID, err)
	}
	evidence, ok := payload["evidence"].([]any)
	if !ok || len(evidence) != 1 {
		return false, fmt.Errorf("appserver/store: durable file-change item %q has invalid evidence", item.ID)
	}
	artifact, ok := evidence[0].(map[string]any)
	if !ok {
		return false, fmt.Errorf("appserver/store: durable file-change item %q has invalid artifact evidence", item.ID)
	}
	artifact["revertSnapshotAvailable"] = true
	artifact["revertUnavailableReason"] = ""
	payload["id"] = item.ID
	payload["status"] = "completed"
	encoded, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("encode durable file-change item %q: %w", item.ID, err)
	}
	item.Status = "completed"
	item.Payload = encoded
	item.UpdatedAt = reconciledAt
	if err := saveItemTx(ctx, tx, item); err != nil {
		return false, err
	}
	return true, nil
}

// RollbackThread implements Store.
func (s *SQLiteStore) RollbackThread(ctx context.Context, req RollbackThreadRequest) (*RollbackThreadResult, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.NumTurns < 1 {
		return nil, errors.New("appserver/store: rollback num turns must be >= 1")
	}
	var result *RollbackThreadResult
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		thread, err := loadThreadTx(ctx, tx, req.ID)
		if err != nil {
			return err
		}
		if thread.Status == ThreadDeleted {
			return ErrThreadDeleted
		}
		removed, err := loadLastTurnsTx(ctx, tx, thread.ID, req.NumTurns)
		if err != nil {
			return err
		}
		if err := pruneRolledBackHistoryTx(ctx, tx, thread.ID, removed); err != nil {
			return err
		}
		now := time.Now().UTC()
		thread.UpdatedAt = now
		if err := saveThreadTx(ctx, tx, thread); err != nil {
			return err
		}
		markerPayload, err := json.Marshal(map[string]any{
			"numTurns":       req.NumTurns,
			"removedTurnIds": turnIDs(removed),
			"createdAt":      now,
		})
		if err != nil {
			return fmt.Errorf("marshal rollback marker: %w", err)
		}
		marker := &Item{
			ID:        newID("item"),
			ThreadID:  thread.ID,
			Kind:      "thread_rollback",
			Status:    "completed",
			Payload:   markerPayload,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := saveItemTx(ctx, tx, marker); err != nil {
			return err
		}
		remaining, err := loadTurnsForThreadTx(ctx, tx, thread.ID)
		if err != nil {
			return err
		}
		result = &RollbackThreadResult{
			Thread:       cloneThread(thread),
			Turns:        cloneTurns(remaining),
			RemovedTurns: cloneTurns(removed),
			Marker:       cloneItem(marker),
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return result, nil
}

// AppendItem implements Store.
func (s *SQLiteStore) AppendItem(ctx context.Context, req AppendItemRequest) (*Item, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	var item *Item
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		thread, err := loadThreadTx(ctx, tx, req.ThreadID)
		if err != nil {
			return err
		}
		if thread.Status == ThreadDeleted {
			return ErrThreadDeleted
		}
		if req.TurnID != "" {
			turn, err := loadTurnTx(ctx, tx, req.TurnID)
			if err != nil {
				return err
			}
			if turn.ThreadID != thread.ID {
				return ErrTurnNotFound
			}
		}
		now := time.Now().UTC()
		item = &Item{
			ID:           newID("item"),
			ThreadID:     thread.ID,
			TurnID:       req.TurnID,
			ParentItemID: req.ParentItemID,
			Kind:         req.Kind,
			Status:       req.Status,
			Payload:      cloneRaw(req.Payload),
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		return saveItemTx(ctx, tx, item)
	}); err != nil {
		return nil, err
	}
	return cloneItem(item), nil
}

// UpdateItem implements Store.
func (s *SQLiteStore) UpdateItem(ctx context.Context, req UpdateItemRequest) (*Item, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	var item *Item
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		loaded, err := loadItemTx(ctx, tx, req.ID)
		if err != nil {
			return err
		}
		thread, err := loadThreadTx(ctx, tx, loaded.ThreadID)
		if err != nil {
			return err
		}
		if thread.Status == ThreadDeleted {
			return ErrThreadDeleted
		}
		if req.Status != "" {
			loaded.Status = req.Status
		}
		if req.Payload != nil {
			loaded.Payload = cloneRaw(req.Payload)
		}
		loaded.UpdatedAt = time.Now().UTC()
		if err := saveItemTx(ctx, tx, loaded); err != nil {
			return err
		}
		item = loaded
		return nil
	}); err != nil {
		return nil, err
	}
	return cloneItem(item), nil
}

// GetItem implements Store.
func (s *SQLiteStore) GetItem(ctx context.Context, id string) (*Item, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	db, err := s.dbLocked()
	if err != nil {
		return nil, err
	}
	item, err := loadItem(ctx, db, id)
	if err != nil {
		return nil, err
	}
	return cloneItem(item), nil
}

// ListItems implements Store.
func (s *SQLiteStore) ListItems(ctx context.Context, filter ItemFilter) ([]*Item, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	db, err := s.dbLocked()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT payload FROM app_items WHERE seq > ? ORDER BY seq ASC`, filter.AfterSeq)
	if err != nil {
		return nil, fmt.Errorf("list items: %w", err)
	}
	defer rows.Close()

	var out []*Item
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		if !matchesItemFilter(item, filter) {
			continue
		}
		out = append(out, cloneItem(item))
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate items: %w", err)
	}
	return out, nil
}

// SaveFileChangeRecovery persists private exact-revert evidence for a public
// fileChange item.
func (s *SQLiteStore) SaveFileChangeRecovery(ctx context.Context, req SaveFileChangeRecoveryRequest) (*FileChangeRecovery, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()

	recovery := cloneFileChangeRecovery(&req.Recovery)
	if recovery == nil || recovery.ItemID == "" || recovery.ThreadID == "" || recovery.TurnID == "" || recovery.Path == "" {
		return nil, errors.New("appserver/store: incomplete file-change recovery")
	}
	if recovery.Status == "" {
		recovery.Status = FileChangeRecoveryAvailable
	}
	if recovery.Status != FileChangeRecoveryAvailable {
		return nil, errors.New("appserver/store: new file-change recovery must be available")
	}
	now := time.Now().UTC()
	if recovery.CreatedAt.IsZero() {
		recovery.CreatedAt = now
	}
	recovery.UpdatedAt = now

	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		item, err := loadItemTx(ctx, tx, recovery.ItemID)
		if err != nil {
			return err
		}
		if item.ThreadID != recovery.ThreadID || item.TurnID != recovery.TurnID || item.Kind != "fileChange" {
			return ErrItemNotFound
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_file_change_recovery WHERE item_id = ?`, recovery.ItemID).Scan(&count); err != nil {
			return fmt.Errorf("check file-change recovery: %w", err)
		}
		if count != 0 {
			return errors.New("appserver/store: file-change recovery already exists")
		}
		return saveFileChangeRecoveryTx(ctx, tx, recovery)
	}); err != nil {
		return nil, err
	}
	return cloneFileChangeRecovery(recovery), nil
}

// GetFileChangeRecovery loads private exact-revert evidence.
func (s *SQLiteStore) GetFileChangeRecovery(ctx context.Context, itemID string) (*FileChangeRecovery, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	db, err := s.dbLocked()
	if err != nil {
		return nil, err
	}
	recovery, err := loadFileChangeRecovery(ctx, db, itemID)
	if err != nil {
		return nil, err
	}
	return cloneFileChangeRecovery(recovery), nil
}

// PrepareFileChangeRevert durably binds one idempotency key before filesystem
// mutation. Reusing the same key resumes or returns the prior operation.
func (s *SQLiteStore) PrepareFileChangeRevert(ctx context.Context, req PrepareFileChangeRevertRequest) (*PrepareFileChangeRevertResult, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.ItemID == "" || req.IdempotencyKey == "" {
		return nil, errors.New("appserver/store: file-change revert item and idempotency key are required")
	}
	preparedAt := req.PreparedAt.UTC()
	if preparedAt.IsZero() {
		preparedAt = time.Now().UTC()
	}
	var result PrepareFileChangeRevertResult
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		recovery, err := loadFileChangeRecoveryTx(ctx, tx, req.ItemID)
		if err != nil {
			return err
		}
		switch recovery.Status {
		case FileChangeRecoveryAvailable:
			recovery.Status = FileChangeRecoveryPending
			recovery.IdempotencyKey = req.IdempotencyKey
			recovery.UpdatedAt = preparedAt
			if err := saveFileChangeRecoveryTx(ctx, tx, recovery); err != nil {
				return err
			}
		case FileChangeRecoveryPending, FileChangeRecoveryReverted:
			if recovery.IdempotencyKey != req.IdempotencyKey {
				return ErrFileChangeRevertIdempotencyConflict
			}
			result.Reused = true
		default:
			return fmt.Errorf("appserver/store: unsupported file-change recovery status %q", recovery.Status)
		}
		if recovery.MarkerID != "" {
			result.Marker, err = loadItemTx(ctx, tx, recovery.MarkerID)
			if err != nil {
				return err
			}
		}
		result.Recovery = cloneFileChangeRecovery(recovery)
		return nil
	}); err != nil {
		return nil, err
	}
	result.Marker = cloneItem(result.Marker)
	return &result, nil
}

// AbortFileChangeRevert releases an idempotency key after the caller has
// independently proven that no filesystem mutation occurred.
func (s *SQLiteStore) AbortFileChangeRevert(ctx context.Context, req AbortFileChangeRevertRequest) (*FileChangeRecovery, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.ItemID == "" || req.IdempotencyKey == "" {
		return nil, errors.New("appserver/store: file-change revert item and idempotency key are required")
	}
	abortedAt := req.AbortedAt.UTC()
	if abortedAt.IsZero() {
		abortedAt = time.Now().UTC()
	}
	var recovery *FileChangeRecovery
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		loaded, err := loadFileChangeRecoveryTx(ctx, tx, req.ItemID)
		if err != nil {
			return err
		}
		if loaded.Status != FileChangeRecoveryPending || loaded.IdempotencyKey != req.IdempotencyKey {
			return ErrFileChangeRevertIdempotencyConflict
		}
		loaded.Status = FileChangeRecoveryAvailable
		loaded.IdempotencyKey = ""
		loaded.UpdatedAt = abortedAt
		if err := saveFileChangeRecoveryTx(ctx, tx, loaded); err != nil {
			return err
		}
		recovery = loaded
		return nil
	}); err != nil {
		return nil, err
	}
	return cloneFileChangeRecovery(recovery), nil
}

// CompleteFileChangeRevert atomically marks the recovery record reverted and
// appends its durable receipt marker.
func (s *SQLiteStore) CompleteFileChangeRevert(ctx context.Context, req CompleteFileChangeRevertRequest) (*CompleteFileChangeRevertResult, error) {
	ctx = normalizeContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.ItemID == "" || req.IdempotencyKey == "" {
		return nil, errors.New("appserver/store: file-change revert item and idempotency key are required")
	}
	revertedAt := req.RevertedAt.UTC()
	if revertedAt.IsZero() {
		revertedAt = time.Now().UTC()
	}
	var result CompleteFileChangeRevertResult
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		recovery, err := loadFileChangeRecoveryTx(ctx, tx, req.ItemID)
		if err != nil {
			return err
		}
		if recovery.IdempotencyKey != req.IdempotencyKey {
			return ErrFileChangeRevertIdempotencyConflict
		}
		if recovery.Status == FileChangeRecoveryReverted {
			if recovery.MarkerID == "" {
				return errors.New("appserver/store: reverted file change has no receipt marker")
			}
			result.Marker, err = loadItemTx(ctx, tx, recovery.MarkerID)
			if err != nil {
				return err
			}
			result.Recovery = cloneFileChangeRecovery(recovery)
			result.Reused = true
			return nil
		}
		if recovery.Status != FileChangeRecoveryPending {
			return errors.New("appserver/store: file-change recovery is not pending")
		}
		payload, err := json.Marshal(map[string]any{
			"itemId":         recovery.ItemID,
			"path":           recovery.Path,
			"beforeSha256":   recovery.BeforeSHA256,
			"afterSha256":    recovery.AfterSHA256,
			"idempotencyKey": recovery.IdempotencyKey,
			"revertedAt":     revertedAt,
		})
		if err != nil {
			return fmt.Errorf("marshal file-change revert receipt: %w", err)
		}
		marker := &Item{
			ID:        newID("item"),
			ThreadID:  recovery.ThreadID,
			TurnID:    recovery.TurnID,
			Kind:      "fileChangeRevert",
			Status:    "completed",
			Payload:   payload,
			CreatedAt: revertedAt,
			UpdatedAt: revertedAt,
		}
		if err := saveItemTx(ctx, tx, marker); err != nil {
			return err
		}
		recovery.Status = FileChangeRecoveryReverted
		recovery.MarkerID = marker.ID
		recovery.RevertedAt = revertedAt
		recovery.UpdatedAt = revertedAt
		if err := saveFileChangeRecoveryTx(ctx, tx, recovery); err != nil {
			return err
		}
		result.Recovery = cloneFileChangeRecovery(recovery)
		result.Marker = cloneItem(marker)
		return nil
	}); err != nil {
		return nil, err
	}
	return &result, nil
}

func saveThreadTx(ctx context.Context, tx *sql.Tx, thread *Thread) error {
	payload, err := json.Marshal(thread)
	if err != nil {
		return fmt.Errorf("marshal thread: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO app_threads (id, status, created_at, updated_at, payload)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			status = excluded.status,
			updated_at = excluded.updated_at,
			payload = excluded.payload
	`, thread.ID, string(thread.Status), formatTime(thread.CreatedAt), formatTime(thread.UpdatedAt), payload)
	if err != nil {
		return fmt.Errorf("save thread: %w", err)
	}
	return nil
}

func saveTurnTx(ctx context.Context, tx *sql.Tx, turn *Turn) error {
	payload, err := json.Marshal(turn)
	if err != nil {
		return fmt.Errorf("marshal turn: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO app_turns (id, thread_id, status, created_at, updated_at, payload)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			thread_id = excluded.thread_id,
			status = excluded.status,
			updated_at = excluded.updated_at,
			payload = excluded.payload
	`, turn.ID, turn.ThreadID, string(turn.Status), formatTime(turn.CreatedAt), formatTime(turn.UpdatedAt), payload)
	if err != nil {
		return fmt.Errorf("save turn: %w", err)
	}
	return nil
}

func saveItemTx(ctx context.Context, tx *sql.Tx, item *Item) error {
	payload, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("marshal item: %w", err)
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO app_items (id, thread_id, turn_id, kind, status, created_at, updated_at, payload)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			thread_id = excluded.thread_id,
			turn_id = excluded.turn_id,
			kind = excluded.kind,
			status = excluded.status,
			updated_at = excluded.updated_at,
			payload = excluded.payload
	`, item.ID, item.ThreadID, item.TurnID, item.Kind, item.Status, formatTime(item.CreatedAt), formatTime(item.UpdatedAt), payload)
	if err != nil {
		return fmt.Errorf("save item: %w", err)
	}
	if item.Seq == 0 {
		seq, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("read item sequence: %w", err)
		}
		item.Seq = seq
		payload, err = json.Marshal(item)
		if err != nil {
			return fmt.Errorf("marshal item with sequence: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE app_items SET payload = ? WHERE id = ?`, payload, item.ID); err != nil {
			return fmt.Errorf("save item sequence: %w", err)
		}
	}
	return nil
}

func saveFileChangeRecoveryTx(ctx context.Context, tx *sql.Tx, recovery *FileChangeRecovery) error {
	payload, err := json.Marshal(recovery)
	if err != nil {
		return fmt.Errorf("marshal file-change recovery: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO app_file_change_recovery (
			item_id, thread_id, turn_id, status, idempotency_key, updated_at, payload
		)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(item_id) DO UPDATE SET
			thread_id = excluded.thread_id,
			turn_id = excluded.turn_id,
			status = excluded.status,
			idempotency_key = excluded.idempotency_key,
			updated_at = excluded.updated_at,
			payload = excluded.payload
	`, recovery.ItemID, recovery.ThreadID, recovery.TurnID, string(recovery.Status), recovery.IdempotencyKey, formatTime(recovery.UpdatedAt), payload)
	if err != nil {
		return fmt.Errorf("save file-change recovery: %w", err)
	}
	return nil
}

func saveTerminalOwnershipTx(ctx context.Context, tx *sql.Tx, record *TerminalOwnershipRecord) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal terminal ownership: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO app_terminal_ownership (
			id, workspace_key, process_id, status, started_at, updated_at, payload
		)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			workspace_key = excluded.workspace_key,
			process_id = excluded.process_id,
			status = excluded.status,
			started_at = excluded.started_at,
			updated_at = excluded.updated_at,
			payload = excluded.payload
	`, record.ID, record.WorkspaceKey, record.ProcessID, string(record.Status), formatTime(record.StartedAt), formatTime(record.UpdatedAt), payload)
	if err != nil {
		return fmt.Errorf("save terminal ownership: %w", err)
	}
	return nil
}

func loadThread(ctx context.Context, db *sql.DB, id string) (*Thread, error) {
	return scanThread(db.QueryRowContext(ctx, `SELECT payload FROM app_threads WHERE id = ?`, id))
}

func loadThreadTx(ctx context.Context, tx *sql.Tx, id string) (*Thread, error) {
	return scanThread(tx.QueryRowContext(ctx, `SELECT payload FROM app_threads WHERE id = ?`, id))
}

func loadTurn(ctx context.Context, db *sql.DB, id string) (*Turn, error) {
	return scanTurn(db.QueryRowContext(ctx, `SELECT payload FROM app_turns WHERE id = ?`, id))
}

func loadTurnTx(ctx context.Context, tx *sql.Tx, id string) (*Turn, error) {
	return scanTurn(tx.QueryRowContext(ctx, `SELECT payload FROM app_turns WHERE id = ?`, id))
}

func loadFileChangeRecovery(ctx context.Context, db *sql.DB, itemID string) (*FileChangeRecovery, error) {
	return scanFileChangeRecovery(db.QueryRowContext(ctx, `SELECT payload FROM app_file_change_recovery WHERE item_id = ?`, itemID))
}

func loadFileChangeRecoveryTx(ctx context.Context, tx *sql.Tx, itemID string) (*FileChangeRecovery, error) {
	return scanFileChangeRecovery(tx.QueryRowContext(ctx, `SELECT payload FROM app_file_change_recovery WHERE item_id = ?`, itemID))
}

func loadTerminalOwnershipTx(ctx context.Context, tx *sql.Tx, id string) (*TerminalOwnershipRecord, error) {
	return scanTerminalOwnership(tx.QueryRowContext(ctx, `SELECT process_id, payload FROM app_terminal_ownership WHERE id = ?`, id))
}

func loadTerminalOwnershipForWorkspaceTx(ctx context.Context, tx *sql.Tx, workspaceKey string) ([]*TerminalOwnershipRecord, error) {
	rows, err := tx.QueryContext(ctx, `SELECT process_id, payload FROM app_terminal_ownership WHERE workspace_key = ? ORDER BY started_at DESC, id ASC`, workspaceKey)
	if err != nil {
		return nil, fmt.Errorf("load terminal ownership: %w", err)
	}
	defer rows.Close()
	var records []*TerminalOwnershipRecord
	for rows.Next() {
		record, err := scanTerminalOwnership(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate terminal ownership: %w", err)
	}
	return records, nil
}

func loadTurnsForThreadTx(ctx context.Context, tx *sql.Tx, threadID string) ([]*Turn, error) {
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM app_turns WHERE thread_id = ? ORDER BY created_at ASC, id ASC`, threadID)
	if err != nil {
		return nil, fmt.Errorf("load thread turns: %w", err)
	}
	defer rows.Close()
	var turns []*Turn
	for rows.Next() {
		turn, err := scanTurn(rows)
		if err != nil {
			return nil, err
		}
		turns = append(turns, turn)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate thread turns: %w", err)
	}
	return turns, nil
}

func loadAllTurnsTx(ctx context.Context, tx *sql.Tx) ([]*Turn, error) {
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM app_turns ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("load turns: %w", err)
	}
	defer rows.Close()
	var turns []*Turn
	for rows.Next() {
		turn, err := scanTurn(rows)
		if err != nil {
			return nil, err
		}
		turns = append(turns, turn)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate turns: %w", err)
	}
	return turns, nil
}

func loadTurnsByStatusesTx(ctx context.Context, tx *sql.Tx, statuses ...TurnStatus) ([]*Turn, error) {
	turns, err := loadAllTurnsTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	var out []*Turn
	for _, turn := range turns {
		for _, status := range statuses {
			if turn.Status == status {
				out = append(out, turn)
				break
			}
		}
	}
	return out, nil
}

func loadItemsForTurnTx(ctx context.Context, tx *sql.Tx, turnID string) ([]*Item, error) {
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM app_items WHERE turn_id = ? ORDER BY seq ASC`, turnID)
	if err != nil {
		return nil, fmt.Errorf("load turn items: %w", err)
	}
	defer rows.Close()
	var items []*Item
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate turn items: %w", err)
	}
	return items, nil
}

func terminalTurnStatus(status TurnStatus) bool {
	switch status {
	case TurnCompleted, TurnFailed, TurnInterrupted:
		return true
	default:
		return false
	}
}

func orphanedItemStatus(status string) bool {
	switch status {
	case "inProgress", "in_progress", "pending", "queued", "running", "started":
		return true
	default:
		return false
	}
}

func recoveredItemPayload(payload json.RawMessage) json.RawMessage {
	var object map[string]any
	if len(payload) == 0 || json.Unmarshal(payload, &object) != nil || object == nil {
		return cloneRaw(payload)
	}
	if status, ok := object["status"].(string); ok && orphanedItemStatus(status) {
		object["status"] = "failed"
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return cloneRaw(payload)
	}
	return encoded
}

func loadLastTurnsTx(ctx context.Context, tx *sql.Tx, threadID string, limit int) ([]*Turn, error) {
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM app_turns WHERE thread_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, threadID, limit)
	if err != nil {
		return nil, fmt.Errorf("load rollback turns: %w", err)
	}
	defer rows.Close()
	var turns []*Turn
	for rows.Next() {
		turn, err := scanTurn(rows)
		if err != nil {
			return nil, err
		}
		turns = append(turns, turn)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rollback turns: %w", err)
	}
	return turns, nil
}

func pruneRolledBackHistoryTx(ctx context.Context, tx *sql.Tx, threadID string, removed []*Turn) error {
	if len(removed) == 0 {
		return nil
	}
	removedIDs := make(map[string]struct{}, len(removed))
	for _, turn := range removed {
		removedIDs[turn.ID] = struct{}{}
	}
	cutoff, err := firstRemovedItemSeqTx(ctx, tx, threadID, removed)
	if err != nil {
		return err
	}
	if cutoff > 0 {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM app_file_change_recovery
			WHERE item_id IN (
				SELECT id FROM app_items WHERE thread_id = ? AND seq >= ?
			)
		`, threadID, cutoff); err != nil {
			return fmt.Errorf("delete rolled back file-change recovery: %w", err)
		}
		preservedReceipts, err := retainedFileChangeReceiptIDsTx(ctx, tx, threadID)
		if err != nil {
			return err
		}
		if err := deleteItemsFromSeqTx(ctx, tx, threadID, cutoff, preservedReceipts); err != nil {
			return err
		}
	} else {
		for id := range removedIDs {
			if _, err := tx.ExecContext(ctx, `
				DELETE FROM app_file_change_recovery
				WHERE item_id IN (
					SELECT id FROM app_items WHERE thread_id = ? AND turn_id = ?
				)
			`, threadID, id); err != nil {
				return fmt.Errorf("delete rolled back turn file-change recovery: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM app_items WHERE thread_id = ? AND turn_id = ?`, threadID, id); err != nil {
				return fmt.Errorf("delete rolled back turn items: %w", err)
			}
		}
	}
	for id := range removedIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM app_turns WHERE id = ?`, id); err != nil {
			return fmt.Errorf("delete rolled back turn: %w", err)
		}
	}
	return nil
}

func retainedFileChangeReceiptIDsTx(ctx context.Context, tx *sql.Tx, threadID string) (map[string]struct{}, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT payload
		FROM app_file_change_recovery
		WHERE thread_id = ? AND status = ?
	`, threadID, string(FileChangeRecoveryReverted))
	if err != nil {
		return nil, fmt.Errorf("load retained file-change receipts: %w", err)
	}
	defer rows.Close()
	preserved := make(map[string]struct{})
	for rows.Next() {
		recovery, err := scanFileChangeRecovery(rows)
		if err != nil {
			return nil, err
		}
		if recovery.MarkerID != "" {
			preserved[recovery.MarkerID] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate retained file-change receipts: %w", err)
	}
	return preserved, nil
}

func deleteItemsFromSeqTx(
	ctx context.Context,
	tx *sql.Tx,
	threadID string,
	cutoff int64,
	preserved map[string]struct{},
) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id
		FROM app_items
		WHERE thread_id = ? AND seq >= ?
		ORDER BY seq ASC
	`, threadID, cutoff)
	if err != nil {
		return fmt.Errorf("load rolled back items: %w", err)
	}
	var deleteIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan rolled back item id: %w", err)
		}
		if _, keep := preserved[id]; !keep {
			deleteIDs = append(deleteIDs, id)
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close rolled back item rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate rolled back item ids: %w", err)
	}
	for _, id := range deleteIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM app_items WHERE id = ?`, id); err != nil {
			return fmt.Errorf("delete rolled back item %q: %w", id, err)
		}
	}
	return nil
}

func firstRemovedItemSeqTx(ctx context.Context, tx *sql.Tx, threadID string, removed []*Turn) (int64, error) {
	removedIDs := make(map[string]struct{}, len(removed))
	var earliest time.Time
	for _, turn := range removed {
		removedIDs[turn.ID] = struct{}{}
		if !turn.CreatedAt.IsZero() && (earliest.IsZero() || turn.CreatedAt.Before(earliest)) {
			earliest = turn.CreatedAt
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM app_items WHERE thread_id = ? ORDER BY seq ASC`, threadID)
	if err != nil {
		return 0, fmt.Errorf("load rollback items: %w", err)
	}
	defer rows.Close()
	var fallback int64
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return 0, err
		}
		if _, ok := removedIDs[item.TurnID]; ok {
			return item.Seq, nil
		}
		if fallback == 0 && !earliest.IsZero() && !item.CreatedAt.Before(earliest) {
			fallback = item.Seq
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate rollback items: %w", err)
	}
	return fallback, nil
}

func loadItem(ctx context.Context, db *sql.DB, id string) (*Item, error) {
	return scanItem(db.QueryRowContext(ctx, `SELECT payload FROM app_items WHERE id = ?`, id))
}

func loadItemTx(ctx context.Context, tx *sql.Tx, id string) (*Item, error) {
	return scanItem(tx.QueryRowContext(ctx, `SELECT payload FROM app_items WHERE id = ?`, id))
}

func scanThread(row interface{ Scan(dest ...any) error }) (*Thread, error) {
	var payload []byte
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrThreadNotFound
		}
		return nil, fmt.Errorf("scan thread: %w", err)
	}
	var thread Thread
	if err := json.Unmarshal(payload, &thread); err != nil {
		return nil, fmt.Errorf("unmarshal thread: %w", err)
	}
	return &thread, nil
}

func scanTurn(row interface{ Scan(dest ...any) error }) (*Turn, error) {
	var payload []byte
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTurnNotFound
		}
		return nil, fmt.Errorf("scan turn: %w", err)
	}
	var turn Turn
	if err := json.Unmarshal(payload, &turn); err != nil {
		return nil, fmt.Errorf("unmarshal turn: %w", err)
	}
	return &turn, nil
}

func scanItem(row interface{ Scan(dest ...any) error }) (*Item, error) {
	var payload []byte
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrItemNotFound
		}
		return nil, fmt.Errorf("scan item: %w", err)
	}
	var item Item
	if err := json.Unmarshal(payload, &item); err != nil {
		return nil, fmt.Errorf("unmarshal item: %w", err)
	}
	return &item, nil
}

func scanFileChangeRecovery(row interface{ Scan(dest ...any) error }) (*FileChangeRecovery, error) {
	var payload []byte
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFileChangeRecoveryNotFound
		}
		return nil, fmt.Errorf("scan file-change recovery: %w", err)
	}
	var recovery FileChangeRecovery
	if err := json.Unmarshal(payload, &recovery); err != nil {
		return nil, fmt.Errorf("unmarshal file-change recovery: %w", err)
	}
	return &recovery, nil
}

func scanTerminalOwnership(row interface{ Scan(dest ...any) error }) (*TerminalOwnershipRecord, error) {
	var processID string
	var payload []byte
	if err := row.Scan(&processID, &payload); err != nil {
		return nil, fmt.Errorf("scan terminal ownership: %w", err)
	}
	var record TerminalOwnershipRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		return nil, fmt.Errorf("unmarshal terminal ownership: %w", err)
	}
	record.ProcessID = processID
	return &record, nil
}

func copyThreadHistoryTx(ctx context.Context, tx *sql.Tx, sourceThreadID, forkThreadID string, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM app_turns WHERE thread_id = ? ORDER BY created_at ASC, id ASC`, sourceThreadID)
	if err != nil {
		return fmt.Errorf("load source turns: %w", err)
	}
	var turns []*Turn
	for rows.Next() {
		turn, err := scanTurn(rows)
		if err != nil {
			rows.Close()
			return err
		}
		turns = append(turns, turn)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate source turns: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close source turns: %w", err)
	}

	turnMap := make(map[string]string, len(turns))
	for _, turn := range turns {
		oldID := turn.ID
		turn.ID = newID("turn")
		turn.ThreadID = forkThreadID
		turn.CreatedAt = now
		turn.UpdatedAt = now
		turnMap[oldID] = turn.ID
		if err := saveTurnTx(ctx, tx, turn); err != nil {
			return err
		}
	}

	itemRows, err := tx.QueryContext(ctx, `SELECT payload FROM app_items WHERE thread_id = ? ORDER BY seq ASC`, sourceThreadID)
	if err != nil {
		return fmt.Errorf("load source items: %w", err)
	}
	var items []*Item
	for itemRows.Next() {
		item, err := scanItem(itemRows)
		if err != nil {
			itemRows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := itemRows.Err(); err != nil {
		itemRows.Close()
		return fmt.Errorf("iterate source items: %w", err)
	}
	if err := itemRows.Close(); err != nil {
		return fmt.Errorf("close source items: %w", err)
	}

	itemMap := make(map[string]string, len(items))
	for _, item := range items {
		itemMap[item.ID] = newID("item")
	}
	for _, item := range items {
		oldID := item.ID
		oldParentID := item.ParentItemID
		item.ID = itemMap[oldID]
		item.Payload = remapForkedItemPayloadID(item.Payload, oldID, item.ID)
		if item.Kind == "fileChangeRevert" {
			item.Payload = remapForkedFileChangeReceiptTarget(item.Payload, itemMap)
		}
		item.Payload = disableForkedFileChangeRevert(item.Payload)
		item.ThreadID = forkThreadID
		item.TurnID = turnMap[item.TurnID]
		item.ParentItemID = itemMap[oldParentID]
		item.Seq = 0
		item.CreatedAt = now
		item.UpdatedAt = now
		if err := saveItemTx(ctx, tx, item); err != nil {
			return err
		}
	}
	return nil
}

func remapForkedFileChangeReceiptTarget(raw json.RawMessage, itemMap map[string]string) json.RawMessage {
	if len(raw) == 0 || len(itemMap) == 0 {
		return raw
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(raw, &payload) != nil {
		return raw
	}
	var sourceItemID string
	if json.Unmarshal(payload["itemId"], &sourceItemID) != nil {
		return raw
	}
	forkedItemID := itemMap[sourceItemID]
	if forkedItemID == "" {
		return raw
	}
	encodedItemID, err := json.Marshal(forkedItemID)
	if err != nil {
		return raw
	}
	payload["itemId"] = encodedItemID
	encoded, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return encoded
}

func disableForkedFileChangeRevert(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(raw, &payload) != nil {
		return raw
	}
	var itemType string
	if json.Unmarshal(payload["type"], &itemType) != nil || itemType != "fileChange" {
		return raw
	}
	var evidence []map[string]json.RawMessage
	if json.Unmarshal(payload["evidence"], &evidence) != nil {
		return raw
	}
	reason, err := json.Marshal("file-change recovery does not transfer to forked threads")
	if err != nil {
		return raw
	}
	for _, entry := range evidence {
		entry["revertSnapshotAvailable"] = json.RawMessage("false")
		entry["revertUnavailableReason"] = reason
	}
	encodedEvidence, err := json.Marshal(evidence)
	if err != nil {
		return raw
	}
	payload["evidence"] = encodedEvidence
	encoded, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return encoded
}

func remapForkedItemPayloadID(raw json.RawMessage, oldID, newID string) json.RawMessage {
	if len(raw) == 0 || oldID == "" || newID == "" {
		return raw
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(raw, &payload) != nil {
		return raw
	}
	var payloadID string
	if json.Unmarshal(payload["id"], &payloadID) != nil || payloadID != oldID {
		return raw
	}
	encodedID, err := json.Marshal(newID)
	if err != nil {
		return raw
	}
	payload["id"] = encodedID
	updated, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return updated
}

func matchesThreadFilter(thread *Thread, filter ThreadFilter) bool {
	if thread.Status == ThreadDeleted && !filter.IncludeDeleted {
		return false
	}
	if len(filter.Statuses) == 0 {
		return true
	}
	for _, status := range filter.Statuses {
		if thread.Status == status {
			return true
		}
	}
	return false
}

func matchesTurnFilter(turn *Turn, filter TurnFilter) bool {
	if filter.ThreadID != "" && turn.ThreadID != filter.ThreadID {
		return false
	}
	if len(filter.Statuses) == 0 {
		return true
	}
	for _, status := range filter.Statuses {
		if turn.Status == status {
			return true
		}
	}
	return false
}

func matchesItemFilter(item *Item, filter ItemFilter) bool {
	if filter.ThreadID != "" && item.ThreadID != filter.ThreadID {
		return false
	}
	if filter.TurnID != "" && item.TurnID != filter.TurnID {
		return false
	}
	return true
}

func cloneThread(src *Thread) *Thread {
	if src == nil {
		return nil
	}
	dst := *src
	dst.Settings = cloneMap(src.Settings)
	dst.Metadata = cloneMap(src.Metadata)
	return &dst
}

func cloneTurn(src *Turn) *Turn {
	if src == nil {
		return nil
	}
	dst := *src
	dst.Input = cloneRaw(src.Input)
	dst.Result = cloneRaw(src.Result)
	dst.Usage = cloneMap(src.Usage)
	dst.Metadata = cloneMap(src.Metadata)
	return &dst
}

func cloneTurns(src []*Turn) []*Turn {
	out := make([]*Turn, 0, len(src))
	for _, turn := range src {
		out = append(out, cloneTurn(turn))
	}
	return out
}

func cloneItem(src *Item) *Item {
	if src == nil {
		return nil
	}
	dst := *src
	dst.Payload = cloneRaw(src.Payload)
	return &dst
}

func cloneFileChangeRecovery(src *FileChangeRecovery) *FileChangeRecovery {
	if src == nil {
		return nil
	}
	dst := *src
	dst.BeforeContent = append([]byte(nil), src.BeforeContent...)
	return &dst
}

func cloneTerminalOwnershipRecord(src TerminalOwnershipRecord) TerminalOwnershipRecord {
	dst := src
	if src.ExitCode != nil {
		exitCode := *src.ExitCode
		dst.ExitCode = &exitCode
	}
	if src.EndedAt != nil {
		endedAt := *src.EndedAt
		dst.EndedAt = &endedAt
	}
	if src.OwnerLostAt != nil {
		ownerLostAt := *src.OwnerLostAt
		dst.OwnerLostAt = &ownerLostAt
	}
	return dst
}

func terminalOwnershipRecordPtr(record TerminalOwnershipRecord) *TerminalOwnershipRecord {
	copy := cloneTerminalOwnershipRecord(record)
	return &copy
}

func normalizeTerminalOwnershipRecord(record *TerminalOwnershipRecord) error {
	if record == nil || strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.WorkspaceKey) == "" || strings.TrimSpace(record.ProcessID) == "" {
		return errors.New("appserver/store: incomplete terminal ownership record")
	}
	switch record.Status {
	case TerminalOwnershipRunning, TerminalOwnershipCompleted, TerminalOwnershipFailed, TerminalOwnershipKilled, TerminalOwnershipTimedOut, TerminalOwnershipOwnerLost:
	default:
		return fmt.Errorf("appserver/store: unsupported terminal ownership status %q", record.Status)
	}
	record.StartedAt = record.StartedAt.UTC()
	if record.StartedAt.IsZero() {
		return errors.New("appserver/store: terminal ownership started at is required")
	}
	record.UpdatedAt = record.UpdatedAt.UTC()
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = time.Now().UTC()
	}
	if record.EndedAt != nil {
		endedAt := record.EndedAt.UTC()
		record.EndedAt = &endedAt
	}
	if record.OwnerLostAt != nil {
		ownerLostAt := record.OwnerLostAt.UTC()
		record.OwnerLostAt = &ownerLostAt
	}
	if record.Status == TerminalOwnershipOwnerLost && record.OwnerLostAt == nil {
		return errors.New("appserver/store: owner-lost terminal ownership requires owner-lost time")
	}
	return nil
}

func timePtr(value time.Time) *time.Time {
	copy := value
	return &copy
}

func turnIDs(turns []*Turn) []string {
	ids := make([]string, 0, len(turns))
	for _, turn := range turns {
		if turn != nil && turn.ID != "" {
			ids = append(ids, turn.ID)
		}
	}
	return ids
}

func cloneMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func mergeMaps(base, override map[string]any) map[string]any {
	out := cloneMap(base)
	if len(override) == 0 {
		return out
	}
	if out == nil {
		out = make(map[string]any, len(override))
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

func cloneRaw(src json.RawMessage) json.RawMessage {
	if len(src) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), src...)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeFormat)
}

func newID(prefix string) string {
	return prefix + "_" + uuid.NewString()
}
