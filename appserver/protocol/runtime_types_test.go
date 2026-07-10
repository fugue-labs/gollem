package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRuntimeItemTypesPreserveCurrentWireShapes(t *testing.T) {
	now := time.Date(2026, 7, 10, 16, 30, 0, 0, time.FixedZone("EDT", -4*60*60))
	items := []struct {
		name string
		item any
		want []string
	}{
		{
			name: "dynamic",
			item: DynamicToolCallItem{
				Type:         "dynamicToolCall",
				ID:           "item-dynamic",
				Tool:         "workspace_read_file",
				Arguments:    map[string]any{"path": "README.md"},
				Status:       "inProgress",
				ContentItems: nil,
			},
			want: []string{`"type":"dynamicToolCall"`, `"namespace":null`, `"contentItems":null`, `"success":null`, `"durationMs":null`},
		},
		{
			name: "command",
			item: CommandExecutionItem{
				Type:           "commandExecution",
				ID:             "item-command",
				Command:        "go test ./...",
				CWD:            "/workspace",
				Source:         "agent",
				Status:         "inProgress",
				CommandActions: []CommandExecutionAction{{Type: "unknown", Command: "go test ./..."}},
				StartedAt:      now,
			},
			want: []string{`"type":"commandExecution"`, `"processId":null`, `"aggregatedOutput":null`, `"exitCode":null`, `"completedAt":null`},
		},
		{
			name: "file",
			item: FileChangeItem{
				Type:   "fileChange",
				ID:     "item-file",
				Status: "completed",
				Changes: []FileUpdateChange{{
					Path: "notes.txt",
					Kind: PatchChangeKind{Type: "add"},
					Diff: "+hello\n",
				}},
			},
			want: []string{`"type":"fileChange"`, `"path":"notes.txt"`, `"kind":{"type":"add"}`},
		},
		{
			name: "mcp",
			item: MCPToolCallItem{
				Type:       "mcpToolCall",
				ID:         "item-mcp",
				Server:     "repo",
				Tool:       "search",
				Status:     "completed",
				Arguments:  map[string]any{"query": "history"},
				AppContext: nil,
			},
			want: []string{`"type":"mcpToolCall"`, `"appContext":null`, `"mcpAppResourceUri":null`, `"pluginId":null`, `"result":null`, `"error":null`},
		},
		{
			name: "compaction",
			item: ContextCompactionItem{Type: "contextCompaction", Summary: "summary", CreatedAt: now},
			want: []string{`"type":"contextCompaction"`, `"summary":"summary"`, `"createdAt":"2026-07-10T16:30:00-04:00"`},
		},
	}

	for _, tt := range items {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := json.Marshal(tt.item)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			for _, want := range tt.want {
				if !containsJSONFragment(string(encoded), want) {
					t.Fatalf("wire payload = %s, want fragment %s", encoded, want)
				}
			}
		})
	}
}

func TestPatchChangeKindUpdateRoundTripsMovePath(t *testing.T) {
	movePath := "renamed.txt"
	original := PatchChangeKind{Type: "update", MovePath: &movePath}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(encoded) != `{"movePath":"renamed.txt","type":"update"}` && string(encoded) != `{"type":"update","movePath":"renamed.txt"}` {
		t.Fatalf("encoded = %s", encoded)
	}
	var decoded PatchChangeKind
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Type != original.Type || decoded.MovePath == nil || *decoded.MovePath != movePath {
		t.Fatalf("decoded = %#v", decoded)
	}
}

func TestApprovalAndDaemonTypesArePublicWireContracts(t *testing.T) {
	request := FileChangeApprovalRequestParams{
		ApprovalRequestBase: ApprovalRequestBase{
			ThreadID:    "thread-1",
			TurnID:      "turn-1",
			ItemID:      "item-1",
			StartedAtMS: 123,
			Reason:      "write file",
		},
		Operation: "write",
		Path:      "notes.txt",
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal approval: %v", err)
	}
	for _, want := range []string{`"threadId":"thread-1"`, `"operation":"write"`, `"grantRoot":null`} {
		if !containsJSONFragment(string(encoded), want) {
			t.Fatalf("approval = %s, want %s", encoded, want)
		}
	}

	status := DaemonStatus{
		Status:          "running",
		Name:            "gollem-appserver",
		Version:         "dev",
		ProtocolVersion: ProtocolVersion,
		PID:             42,
		StartedAt:       time.Unix(0, 0).UTC(),
		Shutdown:        DaemonShutdownState{},
	}
	encoded, err = json.Marshal(status)
	if err != nil {
		t.Fatalf("Marshal daemon status: %v", err)
	}
	if !containsJSONFragment(string(encoded), `"protocolVersion":"`+ProtocolVersion+`"`) {
		t.Fatalf("daemon status = %s", encoded)
	}
}

func containsJSONFragment(value, fragment string) bool {
	return strings.Contains(value, fragment)
}
