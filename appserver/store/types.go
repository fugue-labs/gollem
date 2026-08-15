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
	ErrThreadNotFound                      = errors.New("appserver/store: thread not found")
	ErrTurnNotFound                        = errors.New("appserver/store: turn not found")
	ErrItemNotFound                        = errors.New("appserver/store: item not found")
	ErrFileChangeRecoveryNotFound          = errors.New("appserver/store: file-change recovery not found")
	ErrThreadDeleted                       = errors.New("appserver/store: thread is deleted")
	ErrThreadArchived                      = errors.New("appserver/store: thread is archived")
	ErrStoreClosed                         = errors.New("appserver/store: store is closed")
	ErrTurnNotTerminal                     = errors.New("appserver/store: turn is not terminal")
	ErrRetryIdempotencyConflict            = errors.New("appserver/store: retry idempotency key is already bound to another turn")
	ErrThreadHasActiveTurn                 = errors.New("appserver/store: thread has an active turn")
	ErrThreadForkIdempotencyConflict       = errors.New("appserver/store: fork idempotency key is already bound")
	ErrFileChangeRevertIdempotencyConflict = errors.New("appserver/store: file-change revert idempotency key is already bound")
)

const RuntimeOwnerLostReason = "appserver/runtime: execution owner exited before terminal state"

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
	ID                  string          `json:"id"`
	ThreadID            string          `json:"threadId"`
	Status              TurnStatus      `json:"status"`
	Input               json.RawMessage `json:"input,omitempty"`
	Result              json.RawMessage `json:"result,omitempty"`
	Error               string          `json:"error,omitempty"`
	Usage               map[string]any  `json:"usage,omitempty"`
	Metadata            map[string]any  `json:"metadata,omitempty"`
	CreatedAt           time.Time       `json:"createdAt"`
	UpdatedAt           time.Time       `json:"updatedAt"`
	StartedAt           time.Time       `json:"startedAt,omitempty"`
	CompletedAt         time.Time       `json:"completedAt,omitempty"`
	RetryOfTurnID       string          `json:"retryOfTurnId,omitempty"`
	RetryIdempotencyKey string          `json:"retryIdempotencyKey,omitempty"`
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

type PrepareThreadForkRequest struct {
	SourceThreadID string
	IdempotencyKey string
}

type PrepareThreadForkResult struct {
	Thread  *Thread
	Created bool
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

type PrepareTurnRetryRequest struct {
	SourceTurnID   string
	IdempotencyKey string
	Input          json.RawMessage
	Metadata       map[string]any
}

type PrepareTurnRetryResult struct {
	Source  *Turn
	Turn    *Turn
	Created bool
}

type RecoverOrphanedTurnsRequest struct {
	RecoveredAt time.Time
	Reason      string
}

type RecoverOrphanedTurnsResult struct {
	Turns   []*Turn
	Items   []*Item
	Markers []*Item
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

type FileChangeRecoveryStatus string

const (
	FileChangeRecoveryAvailable FileChangeRecoveryStatus = "available"
	FileChangeRecoveryPending   FileChangeRecoveryStatus = "pending"
	FileChangeRecoveryReverted  FileChangeRecoveryStatus = "reverted"
)

// FileChangeRecovery is private durable evidence for one exact file mutation.
// Contents are stored separately from the public timeline item so thread reads
// do not expose full workspace files.
type FileChangeRecovery struct {
	ItemID         string                   `json:"itemId"`
	ThreadID       string                   `json:"threadId"`
	TurnID         string                   `json:"turnId"`
	Path           string                   `json:"path"`
	BeforeExists   bool                     `json:"beforeExists"`
	AfterExists    bool                     `json:"afterExists"`
	BeforeSHA256   string                   `json:"beforeSha256,omitempty"`
	AfterSHA256    string                   `json:"afterSha256,omitempty"`
	BeforeMode     uint32                   `json:"beforeMode,omitempty"`
	AfterMode      uint32                   `json:"afterMode,omitempty"`
	BeforeContent  []byte                   `json:"beforeContent,omitempty"`
	Status         FileChangeRecoveryStatus `json:"status"`
	IdempotencyKey string                   `json:"idempotencyKey,omitempty"`
	MarkerID       string                   `json:"markerId,omitempty"`
	CreatedAt      time.Time                `json:"createdAt"`
	UpdatedAt      time.Time                `json:"updatedAt"`
	RevertedAt     time.Time                `json:"revertedAt,omitempty"`
}

type SaveFileChangeRecoveryRequest struct {
	Recovery FileChangeRecovery
}

type PrepareFileChangeRevertRequest struct {
	ItemID         string
	IdempotencyKey string
	PreparedAt     time.Time
}

type PrepareFileChangeRevertResult struct {
	Recovery *FileChangeRecovery
	Marker   *Item
	Reused   bool
}

type CompleteFileChangeRevertRequest struct {
	ItemID         string
	IdempotencyKey string
	RevertedAt     time.Time
}

type CompleteFileChangeRevertResult struct {
	Recovery *FileChangeRecovery
	Marker   *Item
	Reused   bool
}

type AbortFileChangeRevertRequest struct {
	ItemID         string
	IdempotencyKey string
	AbortedAt      time.Time
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

// RuntimeRecoveryStore is the optional persistence capability required for
// restart reconciliation and exactly-once retry creation.
type RuntimeRecoveryStore interface {
	PrepareTurnRetry(context.Context, PrepareTurnRetryRequest) (*PrepareTurnRetryResult, error)
	RecoverOrphanedTurns(context.Context, RecoverOrphanedTurnsRequest) (*RecoverOrphanedTurnsResult, error)
}

// ThreadForkRecoveryStore is the optional persistence capability required for
// response-loss-safe full-history forks.
type ThreadForkRecoveryStore interface {
	PrepareThreadFork(context.Context, PrepareThreadForkRequest) (*PrepareThreadForkResult, error)
}

// FileChangeRecoveryStore is the optional persistence capability required for
// restart-safe, exactly-once file-change reversal.
type FileChangeRecoveryStore interface {
	SaveFileChangeRecovery(context.Context, SaveFileChangeRecoveryRequest) (*FileChangeRecovery, error)
	GetFileChangeRecovery(context.Context, string) (*FileChangeRecovery, error)
	PrepareFileChangeRevert(context.Context, PrepareFileChangeRevertRequest) (*PrepareFileChangeRevertResult, error)
	AbortFileChangeRevert(context.Context, AbortFileChangeRevertRequest) (*FileChangeRecovery, error)
	CompleteFileChangeRevert(context.Context, CompleteFileChangeRevertRequest) (*CompleteFileChangeRevertResult, error)
}

// TerminalOwnershipStatus is the durable lifecycle state of a redacted
// workspace terminal record. It intentionally models ownership rather than
// attempting to reattach to a process that survived an app-server restart.
type TerminalOwnershipStatus string

const (
	TerminalOwnershipRunning   TerminalOwnershipStatus = "running"
	TerminalOwnershipCompleted TerminalOwnershipStatus = "completed"
	TerminalOwnershipFailed    TerminalOwnershipStatus = "failed"
	TerminalOwnershipKilled    TerminalOwnershipStatus = "killed"
	TerminalOwnershipTimedOut  TerminalOwnershipStatus = "timed_out"
	TerminalOwnershipOwnerLost TerminalOwnershipStatus = "owner_lost"
)

// TerminalOwnershipRecord contains only redacted terminal metadata. Process
// arguments, environment, output, and raw errors are never stored. ProcessID
// is a private service correlation key and is deliberately omitted from JSON.
type TerminalOwnershipRecord struct {
	ID                string                  `json:"id"`
	WorkspaceKey      string                  `json:"workspaceKey"`
	ProcessID         string                  `json:"-"`
	Title             string                  `json:"title"`
	Command           string                  `json:"command"`
	WorkDir           string                  `json:"workDir"`
	Status            TerminalOwnershipStatus `json:"status"`
	ExitCode          *int                    `json:"exitCode,omitempty"`
	StartedAt         time.Time               `json:"startedAt"`
	EndedAt           *time.Time              `json:"endedAt,omitempty"`
	OwnerLostAt       *time.Time              `json:"ownerLostAt,omitempty"`
	ArgumentCount     int                     `json:"argumentCount"`
	CommandRedacted   bool                    `json:"commandRedacted"`
	MetadataTruncated bool                    `json:"metadataTruncated"`
	PTY               bool                    `json:"pty"`
	PTYRows           uint16                  `json:"ptyRows,omitempty"`
	PTYCols           uint16                  `json:"ptyCols,omitempty"`
	UpdatedAt         time.Time               `json:"updatedAt"`
}

type RecoverTerminalOwnershipRequest struct {
	WorkspaceKey   string
	LiveProcessIDs []string
	RecoveredAt    time.Time
}

// TerminalOwnershipStore is the optional persistence capability required to
// report a terminal whose execution owner did not survive a restart. It does
// not persist output or provide a process-reconnect mechanism.
type TerminalOwnershipStore interface {
	SaveTerminalOwnership(context.Context, TerminalOwnershipRecord) (*TerminalOwnershipRecord, error)
	ListTerminalOwnership(context.Context, string) ([]*TerminalOwnershipRecord, error)
	RecoverTerminalOwnership(context.Context, RecoverTerminalOwnershipRequest) ([]*TerminalOwnershipRecord, error)
	DeleteInactiveTerminalOwnership(context.Context, string) error
}
