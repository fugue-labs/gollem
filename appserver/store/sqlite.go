package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

const timeFormat = time.RFC3339Nano

var _ Store = (*SQLiteStore)(nil)

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
	db, err := sql.Open("sqlite", dbPath)
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
		if thread.Status == ThreadDeleted {
			return ErrThreadDeleted
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
		if thread.Status == ThreadDeleted {
			return ErrThreadDeleted
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
		if _, err := tx.ExecContext(ctx, `DELETE FROM app_items WHERE thread_id = ? AND seq >= ?`, threadID, cutoff); err != nil {
			return fmt.Errorf("delete rolled back items: %w", err)
		}
	} else {
		for id := range removedIDs {
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
