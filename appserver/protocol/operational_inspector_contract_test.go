package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOperationalInspectorBindingsAreExact(t *testing.T) {
	bindings := WireTypeBindings()
	assertBinding(t, bindings, "thread/backgroundTerminals/list", SurfaceClientRequest, "OperationalListParams")
	assertBinding(t, bindings, "thread/backgroundTerminals/list", SurfaceClientRequest, "BackgroundTerminalListResponse")
	assertBinding(t, bindings, "thread/backgroundTerminals/read", SurfaceClientRequest, "BackgroundTerminalReadParams")
	assertBinding(t, bindings, "thread/backgroundTerminals/read", SurfaceClientRequest, "BackgroundTerminalReadResponse")
	assertBinding(t, bindings, "thread/backgroundTerminals/write", SurfaceClientRequest, "BackgroundTerminalWriteParams")
	assertBinding(t, bindings, "thread/backgroundTerminals/write", SurfaceClientRequest, "BackgroundTerminalWriteResponse")
	assertBinding(t, bindings, "thread/backgroundTerminals/resize", SurfaceClientRequest, "BackgroundTerminalResizeParams")
	assertBinding(t, bindings, "thread/backgroundTerminals/resize", SurfaceClientRequest, "BackgroundTerminalResizeResponse")
	assertBinding(t, bindings, "thread/backgroundTerminals/terminate", SurfaceClientRequest, "BackgroundTerminalTerminateParams")
	assertBinding(t, bindings, "thread/backgroundTerminals/terminate", SurfaceClientRequest, "BackgroundTerminalTerminateResponse")
	assertBinding(t, bindings, "thread/backgroundTerminals/clean", SurfaceClientRequest, "BackgroundTerminalCleanResponse")
	assertBinding(t, bindings, "git/status", SurfaceGollemExtension, "OperationalListParams")
	assertBinding(t, bindings, "git/status", SurfaceGollemExtension, "GitStatusResponse")
	assertBinding(t, bindings, "git/worktree/list", SurfaceGollemExtension, "OperationalListParams")
	assertBinding(t, bindings, "git/worktree/list", SurfaceGollemExtension, "GitWorktreeListResponse")

	defs := JSONSchema()["$defs"].(Schema)
	for _, name := range []string{
		"OperationalListParams",
		"BackgroundTerminal",
		"BackgroundTerminalStatus",
		"BackgroundTerminalListResponse",
		"BackgroundTerminalReadParams",
		"BackgroundTerminalReadResponse",
		"BackgroundTerminalResizeParams",
		"BackgroundTerminalResizeResponse",
		"BackgroundTerminalWriteParams",
		"BackgroundTerminalWriteResponse",
		"BackgroundTerminalTerminateParams",
		"BackgroundTerminalTerminateResponse",
		"BackgroundTerminalCleanResponse",
		"GitStatusEntry",
		"GitStatusSnapshot",
		"GitStatusResponse",
		"GitWorktree",
		"GitWorktreeListResponse",
	} {
		if _, ok := defs[name]; !ok {
			t.Errorf("$defs missing %s", name)
		}
	}
	assertSchemaEnum(t, defs, "BackgroundTerminalStatus", []any{
		"running", "completed", "failed", "killed", "timed_out", "owner_lost",
	})
}

func TestOperationalInspectorRecordsDoNotExposeProcessOutputOrArguments(t *testing.T) {
	for _, field := range []string{"Args", "Stdout", "Stderr", "Error", "Process", "Path"} {
		if _, ok := reflect.TypeFor[BackgroundTerminal]().FieldByName(field); ok {
			t.Errorf("BackgroundTerminal exposes native field %s", field)
		}
	}

	now := time.Now().UTC()
	exitCode := 0
	terminal := BackgroundTerminal{
		ID:                "process-1",
		TerminalID:        "process-1",
		ProcessID:         "process-1",
		PID:               42,
		Title:             "printf",
		Command:           "printf",
		WorkDir:           ".",
		Status:            BackgroundTerminalStatusCompleted,
		ExitCode:          &exitCode,
		StartedAt:         now,
		EndedAt:           &now,
		ArgumentCount:     2,
		CommandRedacted:   true,
		MetadataTruncated: false,
	}
	response := BackgroundTerminalListResponse{
		Terminals:           []BackgroundTerminal{terminal},
		BackgroundTerminals: []BackgroundTerminal{terminal},
		Data:                []BackgroundTerminal{terminal},
		Total:               1,
		SnapshotID:          "sha256:terminal-snapshot",
		ObservedAt:          now,
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var decoded BackgroundTerminalListResponse
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !reflect.DeepEqual(decoded, response) {
		t.Fatalf("round trip = %#v, want %#v", decoded, response)
	}
}

func TestOperationalInspectorTypeScriptExportsBoundMethods(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`"thread/backgroundTerminals/list": OperationalListParams;`,
		`"thread/backgroundTerminals/read": BackgroundTerminalReadParams;`,
		`"thread/backgroundTerminals/terminate": BackgroundTerminalTerminateParams;`,
		`"thread/backgroundTerminals/read": BackgroundTerminalReadResponse;`,
		`"thread/backgroundTerminals/write": BackgroundTerminalWriteParams;`,
		`"thread/backgroundTerminals/write": BackgroundTerminalWriteResponse;`,
		`"thread/backgroundTerminals/resize": BackgroundTerminalResizeParams;`,
		`"thread/backgroundTerminals/resize": BackgroundTerminalResizeResponse;`,
		`"thread/backgroundTerminals/clean": BackgroundTerminalCleanResponse;`,
		`"git/status": GitStatusResponse;`,
		`"git/worktree/list": GitWorktreeListResponse;`,
		"export type BackgroundTerminalListResponse = {\n",
		`"terminals": Array<BackgroundTerminal>;`,
		`"entries": Array<GitStatusEntry>;`,
		"export type GitStatusResponse = {\n",
		`"worktrees": Array<GitWorktree>;`,
		"export type GitWorktreeListResponse = {\n",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}
