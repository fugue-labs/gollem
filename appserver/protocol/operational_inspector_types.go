package protocol

import "time"

// OperationalListParams requests one bounded page from an operational
// inventory. Cursors are opaque and scoped to the method that issued them.
type OperationalListParams struct {
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

// BackgroundTerminalStatus is the lifecycle state of a workspace-owned
// background process.
type BackgroundTerminalStatus string

const (
	BackgroundTerminalStatusRunning   BackgroundTerminalStatus = "running"
	BackgroundTerminalStatusCompleted BackgroundTerminalStatus = "completed"
	BackgroundTerminalStatusFailed    BackgroundTerminalStatus = "failed"
	BackgroundTerminalStatusKilled    BackgroundTerminalStatus = "killed"
	BackgroundTerminalStatusTimedOut  BackgroundTerminalStatus = "timed_out"
	BackgroundTerminalStatusOwnerLost BackgroundTerminalStatus = "owner_lost"
)

// BackgroundTerminal is the bounded, redacted process record exposed to
// desktop clients. Process arguments, output, environment, and raw errors are
// intentionally absent.
type BackgroundTerminal struct {
	ID                string                   `json:"id"`
	TerminalID        string                   `json:"terminalId"`
	ProcessID         string                   `json:"processId"`
	PID               int                      `json:"pid"`
	Title             string                   `json:"title"`
	Command           string                   `json:"command"`
	WorkDir           string                   `json:"workDir"`
	Status            BackgroundTerminalStatus `json:"status" jsonschema:"enum=running|completed|failed|killed|timed_out|owner_lost"`
	ExitCode          *int                     `json:"exitCode,omitempty"`
	StartedAt         time.Time                `json:"startedAt"`
	EndedAt           *time.Time               `json:"endedAt,omitempty"`
	OwnerLostAt       *time.Time               `json:"ownerLostAt,omitempty"`
	ArgumentCount     int                      `json:"argumentCount"`
	CommandRedacted   bool                     `json:"commandRedacted"`
	MetadataTruncated bool                     `json:"metadataTruncated"`
	PTY               bool                     `json:"pty"`
	PTYSize           *ProcessTerminalSize     `json:"ptySize,omitempty"`
}

// BackgroundTerminalListResponse is one stable page of the process inventory.
// The alias arrays preserve the existing wire surface while sharing the same
// bounded records.
type BackgroundTerminalListResponse struct {
	Terminals           []BackgroundTerminal `json:"terminals" jsonschema:"nonnullable=true"`
	BackgroundTerminals []BackgroundTerminal `json:"backgroundTerminals" jsonschema:"nonnullable=true"`
	Data                []BackgroundTerminal `json:"data" jsonschema:"nonnullable=true"`
	Total               int                  `json:"total"`
	Truncated           bool                 `json:"truncated"`
	SnapshotID          string               `json:"snapshotId"`
	NextCursor          string               `json:"nextCursor,omitempty"`
	ObservedAt          time.Time            `json:"observedAt"`
}

// BackgroundTerminalTerminateParams accepts the historical identifier aliases.
// Main-process callers should use id.
type BackgroundTerminalTerminateParams struct {
	ID                   string `json:"id,omitempty"`
	TerminalID           string `json:"terminalId,omitempty"`
	BackgroundTerminalID string `json:"backgroundTerminalId,omitempty"`
	ProcessID            string `json:"processId,omitempty"`
}

func (p BackgroundTerminalTerminateParams) EffectiveID() string {
	for _, value := range []string{p.ID, p.TerminalID, p.BackgroundTerminalID, p.ProcessID} {
		if value != "" {
			return value
		}
	}
	return ""
}

type BackgroundTerminalTerminateResponse struct {
	OK       bool               `json:"ok"`
	ID       string             `json:"id"`
	Terminal BackgroundTerminal `json:"terminal"`
}

// BackgroundTerminalReadParams identifies one terminal from the current
// process inventory. An owner_lost terminal remains inspectable in the list
// but cannot be read because the previous owner did not retain its output.
type BackgroundTerminalReadParams struct {
	ID string `json:"id"`
}

// BackgroundTerminalReadResponse contains only the bounded tail currently
// retained by the running Gollem process. Output is base64 encoded so the wire
// contract is safe for arbitrary process bytes. It is never added to terminal
// inventory records or persisted by this method.
type BackgroundTerminalReadResponse struct {
	Terminal        BackgroundTerminal `json:"terminal"`
	StdoutBase64    string             `json:"stdoutBase64"`
	StderrBase64    string             `json:"stderrBase64"`
	StdoutTruncated bool               `json:"stdoutTruncated"`
	StderrTruncated bool               `json:"stderrTruncated"`
	ObservedAt      time.Time          `json:"observedAt"`
}

// BackgroundTerminalWriteParams delivers one bounded UTF-8 text input to a
// currently running terminal in the active Gollem process. The input is never
// retained or echoed by this operational receipt.
type BackgroundTerminalWriteParams struct {
	ID    string `json:"id"`
	Input string `json:"input"`
}

// BackgroundTerminalWriteResponse acknowledges one accepted input without
// returning the input itself. Owner-lost terminals are intentionally rejected
// because Gollem never reattaches their prior process or PTY after restart.
type BackgroundTerminalWriteResponse struct {
	OK           bool               `json:"ok"`
	Terminal     BackgroundTerminal `json:"terminal"`
	WrittenBytes int                `json:"writtenBytes"`
	ObservedAt   time.Time          `json:"observedAt"`
}

// BackgroundTerminalResizeParams changes the cell dimensions of one current
// PTY-backed terminal. It neither creates a PTY nor persists terminal state.
type BackgroundTerminalResizeParams struct {
	ID   string              `json:"id"`
	Size ProcessTerminalSize `json:"size"`
}

// BackgroundTerminalResizeResponse acknowledges a current terminal resize
// without exposing raw process internals.
type BackgroundTerminalResizeResponse struct {
	OK         bool               `json:"ok"`
	Terminal   BackgroundTerminal `json:"terminal"`
	ObservedAt time.Time          `json:"observedAt"`
}

type BackgroundTerminalCleanResponse struct {
	Removed             []BackgroundTerminal `json:"removed" jsonschema:"nonnullable=true"`
	BackgroundTerminals []BackgroundTerminal `json:"backgroundTerminals" jsonschema:"nonnullable=true"`
	Data                []BackgroundTerminal `json:"data" jsonschema:"nonnullable=true"`
	RemovedCount        int                  `json:"removedCount"`
	Truncated           bool                 `json:"truncated"`
	ObservedAt          time.Time            `json:"observedAt"`
}

// GitStatusEntry is one bounded porcelain status record.
type GitStatusEntry struct {
	Code      string `json:"code"`
	Path      string `json:"path"`
	Truncated bool   `json:"truncated"`
}

type GitStatusSnapshot struct {
	BranchLine       string           `json:"branchLine"`
	BranchTruncated  bool             `json:"branchTruncated"`
	Entries          []GitStatusEntry `json:"entries" jsonschema:"nonnullable=true"`
	Clean            bool             `json:"clean"`
	EntryCount       int              `json:"entryCount"`
	EntriesTruncated bool             `json:"entriesTruncated"`
}

type GitStatusResponse struct {
	Status     GitStatusSnapshot `json:"status"`
	SnapshotID string            `json:"snapshotId"`
	NextCursor string            `json:"nextCursor,omitempty"`
	ObservedAt time.Time         `json:"observedAt"`
}

// GitWorktree is bounded metadata for one repository worktree. Desktop main
// owns the path and may project an opaque identifier to the renderer.
type GitWorktree struct {
	Path              string `json:"path"`
	Head              string `json:"head"`
	Branch            string `json:"branch"`
	Detached          bool   `json:"detached"`
	Bare              bool   `json:"bare"`
	MetadataTruncated bool   `json:"metadataTruncated"`
}

type GitWorktreeListResponse struct {
	Worktrees  []GitWorktree `json:"worktrees" jsonschema:"nonnullable=true"`
	Total      int           `json:"total"`
	Truncated  bool          `json:"truncated"`
	SnapshotID string        `json:"snapshotId"`
	NextCursor string        `json:"nextCursor,omitempty"`
	ObservedAt time.Time     `json:"observedAt"`
}
