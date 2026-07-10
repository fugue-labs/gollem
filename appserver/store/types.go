package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type ThreadStatus string

const (
	ThreadActive   ThreadStatus = "active"
	ThreadArchived ThreadStatus = "archived"
	ThreadDeleted  ThreadStatus = "deleted"
)

type TurnStatus string

const (
	TurnQueued      TurnStatus = "queued"
	TurnRunning     TurnStatus = "running"
	TurnCompleted   TurnStatus = "completed"
	TurnFailed      TurnStatus = "failed"
	TurnInterrupted TurnStatus = "interrupted"
)

var (
	ErrThreadNotFound = errors.New("appserver/store: thread not found")
	ErrTurnNotFound   = errors.New("appserver/store: turn not found")
	ErrItemNotFound   = errors.New("appserver/store: item not found")
	ErrThreadDeleted  = errors.New("appserver/store: thread is deleted")
	ErrStoreClosed    = errors.New("appserver/store: store is closed")
)

// Thread is a durable conversation container.
type Thread struct {
	ID                 string         `json:"id"`
	Title              string         `json:"title,omitempty"`
	Workspace          string         `json:"workspace,omitempty"`
	Status             ThreadStatus   `json:"status"`
	ForkedFromThreadID string         `json:"forkedFromThreadId,omitempty"`
	Settings           map[string]any `json:"settings,omitempty"`
	Metadata           map[string]any `json:"metadata,omitempty"`
	CreatedAt          time.Time      `json:"createdAt"`
	UpdatedAt          time.Time      `json:"updatedAt"`
	ArchivedAt         time.Time      `json:"archivedAt,omitempty"`
	DeletedAt          time.Time      `json:"deletedAt,omitempty"`
}

// Turn is one model run attempt within a thread.
type Turn struct {
	ID          string          `json:"id"`
	ThreadID    string          `json:"threadId"`
	Status      TurnStatus      `json:"status"`
	Input       json.RawMessage `json:"input,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       string          `json:"error,omitempty"`
	Usage       map[string]any  `json:"usage,omitempty"`
	Metadata    map[string]any  `json:"metadata,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
	StartedAt   time.Time       `json:"startedAt,omitempty"`
	CompletedAt time.Time       `json:"completedAt,omitempty"`
}

// Item is an ordered timeline entry for messages, reasoning, tools, commands,
// diffs, artifacts, and future app-server event types.
type Item struct {
	ID           string          `json:"id"`
	ThreadID     string          `json:"threadId"`
	TurnID       string          `json:"turnId,omitempty"`
	ParentItemID string          `json:"parentItemId,omitempty"`
	Seq          int64           `json:"seq"`
	Kind         string          `json:"kind"`
	Status       string          `json:"status,omitempty"`
	Payload      json.RawMessage `json:"payload,omitempty"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
}

type CreateThreadRequest struct {
	Title     string
	Workspace string
	Settings  map[string]any
	Metadata  map[string]any
}

type ThreadFilter struct {
	Statuses       []ThreadStatus
	IncludeDeleted bool
	Limit          int
}

type ForkThreadRequest struct {
	SourceThreadID string
	Title          string
	Metadata       map[string]any
	IncludeItems   bool
}

type UpdateThreadSettingsRequest struct {
	ID       string
	Settings map[string]any
	Metadata map[string]any
	Replace  bool
}

type CreateTurnRequest struct {
	ThreadID string
	Input    json.RawMessage
	Metadata map[string]any
}

type CompleteTurnRequest struct {
	ID     string
	Status TurnStatus
	Result json.RawMessage
	Error  string
	Usage  map[string]any
}

type TurnFilter struct {
	ThreadID string
	Statuses []TurnStatus
	Limit    int
}

type RollbackThreadRequest struct {
	ID       string
	NumTurns int
}

type RollbackThreadResult struct {
	Thread       *Thread
	Turns        []*Turn
	RemovedTurns []*Turn
	Marker       *Item
}

type AppendItemRequest struct {
	ThreadID     string
	TurnID       string
	ParentItemID string
	Kind         string
	Status       string
	Payload      json.RawMessage
}

type UpdateItemRequest struct {
	ID      string
	Status  string
	Payload json.RawMessage
}

type ItemFilter struct {
	ThreadID string
	TurnID   string
	AfterSeq int64
	Limit    int
}

// Store is the durable app-server persistence contract.
type Store interface {
	CreateThread(context.Context, CreateThreadRequest) (*Thread, error)
	GetThread(context.Context, string) (*Thread, error)
	ListThreads(context.Context, ThreadFilter) ([]*Thread, error)
	ArchiveThread(context.Context, string) (*Thread, error)
	UnarchiveThread(context.Context, string) (*Thread, error)
	DeleteThread(context.Context, string) (*Thread, error)
	ForkThread(context.Context, ForkThreadRequest) (*Thread, error)
	UpdateThreadTitle(context.Context, string, string) (*Thread, error)
	UpdateThreadSettings(context.Context, UpdateThreadSettingsRequest) (*Thread, error)

	CreateTurn(context.Context, CreateTurnRequest) (*Turn, error)
	StartTurn(context.Context, string) (*Turn, error)
	CompleteTurn(context.Context, CompleteTurnRequest) (*Turn, error)
	GetTurn(context.Context, string) (*Turn, error)
	ListTurns(context.Context, TurnFilter) ([]*Turn, error)
	RollbackThread(context.Context, RollbackThreadRequest) (*RollbackThreadResult, error)

	AppendItem(context.Context, AppendItemRequest) (*Item, error)
	UpdateItem(context.Context, UpdateItemRequest) (*Item, error)
	GetItem(context.Context, string) (*Item, error)
	ListItems(context.Context, ItemFilter) ([]*Item, error)
}
