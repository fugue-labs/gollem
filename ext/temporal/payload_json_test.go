package temporal

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fugue-labs/gollem/core"
)

func TestWorkflowOutputJSONEmbedsNestedJSON(t *testing.T) {
	output := WorkflowOutput{
		Completed:  true,
		OutputJSON: json.RawMessage(`{"summary":"ok"}`),
		Snapshot: &core.SerializedRunSnapshot{
			Messages: []core.SerializedMessage{{Kind: "request", Data: json.RawMessage(`{"parts":[]}`)}},
		},
		Trace: &core.RunTrace{
			Steps: []core.TraceStep{{Kind: core.TraceModelRequest}},
		},
	}

	data, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal workflow output: %v", err)
	}

	encoded := string(data)
	if !strings.Contains(encoded, `"output_json":{"summary":"ok"}`) {
		t.Fatalf("expected output_json to be embedded JSON, got %s", encoded)
	}
	if !strings.Contains(encoded, `"snapshot":{"messages":[{"kind":"request","data":{"parts":[]}}]`) {
		t.Fatalf("expected snapshot to be embedded JSON, got %s", encoded)
	}
	if !strings.Contains(encoded, `"trace":{"run_id":"","prompt":"","start_time":"0001-01-01T00:00:00Z","end_time":"0001-01-01T00:00:00Z","duration":0,"steps":[{"kind":"model_request"`) {
		t.Fatalf("expected trace to be embedded JSON, got %s", encoded)
	}
	if strings.Contains(encoded, `"snapshot_json"`) {
		t.Fatalf("expected deprecated snapshot_json to be omitted, got %s", encoded)
	}
	if strings.Contains(encoded, `"trace_json"`) {
		t.Fatalf("expected deprecated trace_json to be omitted, got %s", encoded)
	}
}

func TestModelActivityInputJSONEmbedsMessages(t *testing.T) {
	input := ModelActivityInput{
		Messages: []core.SerializedMessage{{Kind: "request", Data: json.RawMessage(`{"parts":[]}`)}},
	}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal model activity input: %v", err)
	}

	encoded := string(data)
	if !strings.Contains(encoded, `"messages":[{"kind":"request","data":{"parts":[]}}]`) {
		t.Fatalf("expected messages to be embedded JSON, got %s", encoded)
	}
	if strings.Contains(encoded, `"messages_json"`) {
		t.Fatalf("expected deprecated messages_json to be omitted, got %s", encoded)
	}
}

func TestWorkflowStatusJSONEmbedsMessages(t *testing.T) {
	status := WorkflowStatus{
		RunID:    "run-1",
		Messages: []core.SerializedMessage{{Kind: "request", Data: json.RawMessage(`{}`)}},
		Snapshot: &core.SerializedRunSnapshot{RunID: "run-1"},
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal workflow status: %v", err)
	}

	encoded := string(data)
	if !strings.Contains(encoded, `"messages":[{"kind":"request","data":{}}]`) {
		t.Fatalf("expected messages to be embedded JSON, got %s", encoded)
	}
	if !strings.Contains(encoded, `"snapshot":{"messages":null,"usage":{"requests":0,"tool_calls":0},"last_input_tokens":0,"retries":0,"run_id":"run-1"`) {
		t.Fatalf("expected snapshot to be embedded JSON, got %s", encoded)
	}
	if strings.Contains(encoded, `"messages_json"`) {
		t.Fatalf("expected deprecated messages_json to be omitted, got %s", encoded)
	}
	if strings.Contains(encoded, `"snapshot_json"`) {
		t.Fatalf("expected deprecated snapshot_json to be omitted, got %s", encoded)
	}
}
