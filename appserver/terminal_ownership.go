package appserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
	toolprocess "github.com/fugue-labs/gollem/appserver/tools/process"
)

// initializeTerminalOwnership makes restart recovery opt-in for stores that
// can durably retain redacted terminal lifecycle evidence. A new server never
// adopts a previous process: any saved running record without a current
// process-service counterpart becomes owner_lost.
func (s *Server) initializeTerminalOwnership() {
	if s == nil || s.process == nil || s.store == nil {
		return
	}
	ledger, ok := s.store.(store.TerminalOwnershipStore)
	if !ok {
		return
	}
	s.terminalOwnership = ledger
	s.terminalWorkspaceKey = terminalOwnershipWorkspaceKey(s.process.Root())
	s.process.AddLifecycleSink(func(snapshot toolprocess.Snapshot) {
		s.saveTerminalOwnership(snapshot)
	})

	ctx := context.Background()
	snapshots, err := s.process.List(ctx)
	if err != nil {
		return
	}
	liveProcessIDs := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		liveProcessIDs = append(liveProcessIDs, snapshot.ID)
	}
	if _, err := ledger.RecoverTerminalOwnership(ctx, store.RecoverTerminalOwnershipRequest{
		WorkspaceKey:   s.terminalWorkspaceKey,
		LiveProcessIDs: liveProcessIDs,
		RecoveredAt:    time.Now().UTC(),
	}); err != nil {
		return
	}
	for _, snapshot := range snapshots {
		s.saveTerminalOwnership(snapshot)
	}
}

func (s *Server) saveTerminalOwnership(snapshot toolprocess.Snapshot) {
	if s == nil || s.terminalOwnership == nil || s.process == nil {
		return
	}
	record := terminalOwnershipRecord(s.process.Root(), s.terminalWorkspaceKey, snapshot)
	_, _ = s.terminalOwnership.SaveTerminalOwnership(context.Background(), record)
}

func terminalOwnershipWorkspaceKey(root string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(root)))
	return "workspace-" + hex.EncodeToString(sum[:])
}

func terminalOwnershipID(workspaceKey string, snapshot toolprocess.Snapshot) string {
	value := strings.Join([]string{
		workspaceKey,
		snapshot.ID,
		snapshot.StartedAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")
	sum := sha256.Sum256([]byte(value))
	return "terminal-" + hex.EncodeToString(sum[:])
}

func terminalOwnershipRecord(root, workspaceKey string, snapshot toolprocess.Snapshot) store.TerminalOwnershipRecord {
	terminal := operationalBackgroundTerminal(root, &snapshot)
	updatedAt := snapshot.StartedAt.UTC()
	if !snapshot.EndedAt.IsZero() {
		updatedAt = snapshot.EndedAt.UTC()
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	record := store.TerminalOwnershipRecord{
		ID:                terminalOwnershipID(workspaceKey, snapshot),
		WorkspaceKey:      workspaceKey,
		ProcessID:         snapshot.ID,
		Title:             terminal.Title,
		Command:           terminal.Command,
		WorkDir:           terminal.WorkDir,
		Status:            store.TerminalOwnershipStatus(terminal.Status),
		ExitCode:          terminal.ExitCode,
		StartedAt:         terminal.StartedAt,
		EndedAt:           terminal.EndedAt,
		ArgumentCount:     terminal.ArgumentCount,
		CommandRedacted:   terminal.CommandRedacted,
		MetadataTruncated: terminal.MetadataTruncated,
		PTY:               terminal.PTY,
		UpdatedAt:         updatedAt,
	}
	if terminal.PTYSize != nil {
		record.PTYRows = terminal.PTYSize.Rows
		record.PTYCols = terminal.PTYSize.Cols
	}
	return record
}

func terminalOwnershipTerminal(record *store.TerminalOwnershipRecord) protocol.BackgroundTerminal {
	if record == nil {
		return protocol.BackgroundTerminal{}
	}
	terminal := protocol.BackgroundTerminal{
		ID:                record.ID,
		TerminalID:        record.ID,
		ProcessID:         record.ID,
		Title:             record.Title,
		Command:           record.Command,
		WorkDir:           record.WorkDir,
		Status:            protocol.BackgroundTerminalStatus(record.Status),
		ExitCode:          cloneTerminalExitCode(record.ExitCode),
		StartedAt:         record.StartedAt,
		EndedAt:           cloneTerminalTime(record.EndedAt),
		OwnerLostAt:       cloneTerminalTime(record.OwnerLostAt),
		ArgumentCount:     record.ArgumentCount,
		CommandRedacted:   record.CommandRedacted,
		MetadataTruncated: record.MetadataTruncated,
		PTY:               record.PTY,
	}
	if record.PTY {
		terminal.PTYSize = &protocol.ProcessTerminalSize{Rows: record.PTYRows, Cols: record.PTYCols}
	}
	return terminal
}

func cloneTerminalExitCode(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneTerminalTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (s *Server) backgroundTerminalInventory(ctx context.Context, snapshots []toolprocess.Snapshot) ([]protocol.BackgroundTerminal, error) {
	if s == nil || s.process == nil || s.terminalOwnership == nil {
		root := ""
		if s != nil && s.process != nil {
			root = s.process.Root()
		}
		return operationalBackgroundTerminals(root, snapshots), nil
	}
	records, err := s.terminalOwnership.ListTerminalOwnership(ctx, s.terminalWorkspaceKey)
	if err != nil {
		return nil, err
	}
	recordByID := make(map[string]*store.TerminalOwnershipRecord, len(records))
	for _, record := range records {
		recordByID[record.ID] = record
	}
	seen := make(map[string]struct{}, len(snapshots))
	terminals := make([]protocol.BackgroundTerminal, 0, len(records)+len(snapshots))
	for _, snapshot := range snapshots {
		ledgerID := terminalOwnershipID(s.terminalWorkspaceKey, snapshot)
		seen[ledgerID] = struct{}{}
		terminal := operationalBackgroundTerminal(s.process.Root(), &snapshot)
		if _, ok := recordByID[ledgerID]; ok {
			terminal.ID = ledgerID
			terminal.TerminalID = ledgerID
			terminal.ProcessID = ledgerID
		}
		terminals = append(terminals, terminal)
	}
	for _, record := range records {
		if _, live := seen[record.ID]; live {
			continue
		}
		terminals = append(terminals, terminalOwnershipTerminal(record))
	}
	sort.Slice(terminals, func(i, j int) bool {
		if terminals[i].StartedAt.Equal(terminals[j].StartedAt) {
			return terminals[i].ID < terminals[j].ID
		}
		return terminals[i].StartedAt.After(terminals[j].StartedAt)
	})
	return terminals, nil
}

func (s *Server) resolveBackgroundTerminalID(ctx context.Context, id string) (string, *store.TerminalOwnershipRecord, error) {
	id = strings.TrimSpace(id)
	if s == nil || s.terminalOwnership == nil || id == "" {
		return id, nil, nil
	}
	records, err := s.terminalOwnership.ListTerminalOwnership(ctx, s.terminalWorkspaceKey)
	if err != nil {
		return "", nil, err
	}
	for _, record := range records {
		if record.ID == id {
			return record.ProcessID, record, nil
		}
	}
	return id, nil, nil
}

func operationalBackgroundTerminalWithOwnership(root string, snapshot *toolprocess.Snapshot, record *store.TerminalOwnershipRecord) protocol.BackgroundTerminal {
	terminal := operationalBackgroundTerminal(root, snapshot)
	if record == nil {
		return terminal
	}
	terminal.ID = record.ID
	terminal.TerminalID = record.ID
	terminal.ProcessID = record.ID
	return terminal
}

func ownerLostTerminalUnavailable(method string) *protocol.Error {
	return protocol.MethodUnavailableErrorWithReason(
		method,
		"background terminal is unavailable because its execution owner exited",
	)
}
