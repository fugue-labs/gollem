package appserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
	toolprocess "github.com/fugue-labs/gollem/appserver/tools/process"
)

func TestBackgroundTerminalOwnerLostAfterProcessServiceRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "threads.db")
	st, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	firstProcess, err := toolprocess.NewService(root)
	if err != nil {
		t.Fatalf("NewService first: %v", err)
	}
	_ = readyServer(WithStore(st), WithProcess(firstProcess))
	first, err := firstProcess.Start(ctx, toolprocess.StartRequest{
		ID:      "reused-process-id",
		Command: "sh",
		Args:    []string{"-c", "sleep 30 # super-secret-terminal-argument"},
	})
	if err != nil {
		t.Fatalf("Start first: %v", err)
	}
	t.Cleanup(func() {
		_ = firstProcess.Kill(context.Background(), first.ID)
		_, _ = firstProcess.Wait(context.Background(), first.ID)
	})
	if err := st.Close(); err != nil {
		t.Fatalf("Close first store: %v", err)
	}
	st, err = store.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}

	restartedProcess, err := toolprocess.NewService(root)
	if err != nil {
		t.Fatalf("NewService restarted: %v", err)
	}
	restartedServer := readyServer(WithStore(st), WithProcess(restartedProcess))

	listResponse := restartedServer.HandleRequest(ctx, request("thread/backgroundTerminals/list", nil))
	if listResponse.Error != nil {
		t.Fatalf("owner-lost list error: %v", listResponse.Error)
	}
	if strings.Contains(string(listResponse.Result), "super-secret-terminal-argument") {
		t.Fatalf("owner-lost list leaked process arguments: %s", listResponse.Result)
	}
	var list protocol.BackgroundTerminalListResponse
	decodeResult(t, listResponse, &list)
	if len(list.Terminals) != 1 {
		t.Fatalf("owner-lost terminals = %#v", list.Terminals)
	}
	lost := list.Terminals[0]
	if lost.Status != protocol.BackgroundTerminalStatusOwnerLost || lost.OwnerLostAt == nil ||
		lost.PID != 0 || lost.ID == first.ID || lost.ProcessID != lost.ID {
		t.Fatalf("owner-lost terminal = %#v", lost)
	}

	for method, params := range map[string]any{
		"thread/backgroundTerminals/read":      map[string]any{"id": lost.ID},
		"thread/backgroundTerminals/write":     map[string]any{"id": lost.ID, "input": "echo should-not-run\n"},
		"thread/backgroundTerminals/resize":    map[string]any{"id": lost.ID, "size": map[string]any{"rows": 24, "cols": 80}},
		"thread/backgroundTerminals/terminate": map[string]any{"id": lost.ID},
	} {
		response := restartedServer.HandleRequest(ctx, request(method, params))
		if response.Error == nil || response.Error.Code != protocol.CodeMethodUnavailable ||
			!strings.Contains(string(response.Error.Data), "owner exited") {
			t.Fatalf("%s owner-lost response = %#v", method, response)
		}
	}

	// A new service may reuse the old process ID. Its durable terminal identity
	// must still be distinct so controls cannot target the owner-lost record.
	current, err := restartedProcess.Start(ctx, toolprocess.StartRequest{
		ID:      first.ID,
		Command: "sh",
		Args:    []string{"-c", "sleep 30"},
	})
	if err != nil {
		t.Fatalf("Start current: %v", err)
	}
	t.Cleanup(func() {
		_ = restartedProcess.Kill(context.Background(), current.ID)
		_, _ = restartedProcess.Wait(context.Background(), current.ID)
	})

	listResponse = restartedServer.HandleRequest(ctx, request("thread/backgroundTerminals/list", nil))
	if listResponse.Error != nil {
		t.Fatalf("mixed terminal list error: %v", listResponse.Error)
	}
	decodeResult(t, listResponse, &list)
	if len(list.Terminals) != 2 {
		t.Fatalf("mixed terminals = %#v", list.Terminals)
	}
	var currentTerminal protocol.BackgroundTerminal
	for _, terminal := range list.Terminals {
		if terminal.Status == protocol.BackgroundTerminalStatusRunning {
			currentTerminal = terminal
		}
	}
	if currentTerminal.ID == "" || currentTerminal.ID == lost.ID || currentTerminal.PID == 0 {
		t.Fatalf("current terminal identity = %#v", currentTerminal)
	}
	terminateResponse := restartedServer.HandleRequest(ctx, request("thread/backgroundTerminals/terminate", map[string]any{"id": currentTerminal.ID}))
	if terminateResponse.Error != nil {
		t.Fatalf("terminate current terminal: %v", terminateResponse.Error)
	}

	cleanResponse := restartedServer.HandleRequest(ctx, request("thread/backgroundTerminals/clean", nil))
	if cleanResponse.Error != nil {
		t.Fatalf("clean terminal ledger: %v", cleanResponse.Error)
	}
	deadline := time.Now().Add(time.Second)
	for {
		response := restartedServer.HandleRequest(ctx, request("thread/backgroundTerminals/list", nil))
		if response.Error != nil {
			t.Fatalf("list after clean: %v", response.Error)
		}
		decodeResult(t, response, &list)
		if len(list.Terminals) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal ledger remained after clean: %#v", list.Terminals)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
