package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type runtimeWireFixture struct {
	ProtocolVersion string                   `json:"protocolVersion"`
	SchemaVersion   string                   `json:"schemaVersion"`
	Cases           []runtimeWireFixtureCase `json:"cases"`
}

type runtimeWireFixtureCase struct {
	Name        string          `json:"name"`
	Surface     Surface         `json:"surface"`
	Method      string          `json:"method"`
	ParamsType  string          `json:"paramsType,omitempty"`
	ResultType  string          `json:"resultType,omitempty"`
	PayloadType string          `json:"payloadType,omitempty"`
	Message     json.RawMessage `json:"message"`
}

func TestRuntimeWireV1FixtureUsesExportedContracts(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "runtime_wire_v1.json"))
	if err != nil {
		t.Fatalf("read runtime fixture: %v", err)
	}
	var fixture runtimeWireFixture
	if err := decodeRuntimeFixture(data, &fixture); err != nil {
		t.Fatalf("decode runtime fixture: %v", err)
	}
	if fixture.ProtocolVersion != ProtocolVersion || fixture.SchemaVersion != SchemaVersion {
		t.Fatalf("fixture versions = %s/%s, want %s/%s", fixture.ProtocolVersion, fixture.SchemaVersion, ProtocolVersion, SchemaVersion)
	}

	wantCases := map[string]bool{
		"dynamic-tool-started":    false,
		"command-completed":       false,
		"file-change-completed":   false,
		"mcp-call-completed":      false,
		"context-compaction-item": false,
		"thread-compacted":        false,
		"token-usage-updated":     false,
		"turn-diff-updated":       false,
		"file-change-approval":    false,
		"daemon-status":           false,
	}
	bindings := WireTypeBindings()
	payloadBindings := ItemPayloadBindings()
	seen := make(map[string]bool, len(fixture.Cases))
	for _, fixtureCase := range fixture.Cases {
		if seen[fixtureCase.Name] {
			t.Errorf("duplicate fixture case %q", fixtureCase.Name)
			continue
		}
		seen[fixtureCase.Name] = true
		if _, ok := wantCases[fixtureCase.Name]; !ok {
			t.Errorf("unexpected fixture case %q", fixtureCase.Name)
		} else {
			wantCases[fixtureCase.Name] = true
		}

		info, ok := LookupMethod(fixtureCase.Method)
		if !ok {
			t.Errorf("%s: unknown method %q", fixtureCase.Name, fixtureCase.Method)
			continue
		}
		if info.Surface != fixtureCase.Surface {
			t.Errorf("%s: surface = %s, registry has %s", fixtureCase.Name, fixtureCase.Surface, info.Surface)
			continue
		}

		payload, err := fixtureMessagePayload(fixtureCase)
		if err != nil {
			t.Errorf("%s: %v", fixtureCase.Name, err)
			continue
		}
		target := runtimeFixtureTarget(firstFixtureType(fixtureCase))
		if target == nil {
			t.Errorf("%s: unsupported fixture type", fixtureCase.Name)
			continue
		}
		if err := decodeRuntimeFixture(payload, target); err != nil {
			t.Errorf("%s: decode %s: %v", fixtureCase.Name, firstFixtureType(fixtureCase), err)
			continue
		}

		if fixtureCase.ParamsType != "" {
			assertBinding(t, bindings, fixtureCase.Method, fixtureCase.Surface, fixtureCase.ParamsType)
		}
		if fixtureCase.ResultType != "" {
			assertBinding(t, bindings, fixtureCase.Method, fixtureCase.Surface, fixtureCase.ResultType)
		}
		if fixtureCase.PayloadType != "" {
			lifecycle := target.(*ItemLifecycleNotificationParams)
			if lifecycle.Item == nil {
				t.Errorf("%s: lifecycle item is nil", fixtureCase.Name)
				continue
			}
			if !hasItemPayloadBinding(payloadBindings, lifecycle.Item.Kind, fixtureCase.PayloadType) {
				t.Errorf("%s: no payload binding for %s/%s", fixtureCase.Name, lifecycle.Item.Kind, fixtureCase.PayloadType)
			}
			payloadTarget := runtimeFixtureTarget(fixtureCase.PayloadType)
			if err := decodeRuntimeFixture(lifecycle.Item.Payload, payloadTarget); err != nil {
				t.Errorf("%s: decode nested %s: %v", fixtureCase.Name, fixtureCase.PayloadType, err)
			}
		}
	}
	for name, found := range wantCases {
		if !found {
			t.Errorf("fixture missing %s", name)
		}
	}
}

func fixtureMessagePayload(fixtureCase runtimeWireFixtureCase) (json.RawMessage, error) {
	switch fixtureCase.Surface {
	case SurfaceServerNotification:
		var notification Notification
		if err := decodeRuntimeFixture(fixtureCase.Message, &notification); err != nil {
			return nil, err
		}
		if notification.Method != fixtureCase.Method {
			return nil, fmt.Errorf("notification method = %q, want %q", notification.Method, fixtureCase.Method)
		}
		return notification.Params, nil
	case SurfaceServerRequest:
		var request Request
		if err := decodeRuntimeFixture(fixtureCase.Message, &request); err != nil {
			return nil, err
		}
		if request.Method != fixtureCase.Method {
			return nil, fmt.Errorf("request method = %q, want %q", request.Method, fixtureCase.Method)
		}
		return request.Params, nil
	case SurfaceGollemExtension:
		var response Response
		if err := decodeRuntimeFixture(fixtureCase.Message, &response); err != nil {
			return nil, err
		}
		return response.Result, nil
	default:
		return nil, fmt.Errorf("unsupported fixture surface %q", fixtureCase.Surface)
	}
}

func firstFixtureType(fixtureCase runtimeWireFixtureCase) string {
	if fixtureCase.ParamsType != "" {
		return fixtureCase.ParamsType
	}
	return fixtureCase.ResultType
}

func runtimeFixtureTarget(name string) any {
	switch name {
	case "DynamicToolCallItemStartedNotificationParams":
		return new(DynamicToolCallItemStartedNotificationParams)
	case "CommandExecutionItemCompletedNotificationParams":
		return new(CommandExecutionItemCompletedNotificationParams)
	case "FileChangeItemCompletedNotificationParams":
		return new(FileChangeItemCompletedNotificationParams)
	case "MCPToolCallItemCompletedNotificationParams":
		return new(MCPToolCallItemCompletedNotificationParams)
	case "ItemLifecycleNotificationParams":
		return new(ItemLifecycleNotificationParams)
	case "ContextCompactionItem":
		return new(ContextCompactionItem)
	case "ThreadCompactedNotificationParams":
		return new(ThreadCompactedNotificationParams)
	case "ThreadTokenUsageUpdatedNotificationParams":
		return new(ThreadTokenUsageUpdatedNotificationParams)
	case "TurnDiffUpdatedNotificationParams":
		return new(TurnDiffUpdatedNotificationParams)
	case "FileChangeApprovalRequestParams":
		return new(FileChangeApprovalRequestParams)
	case "DaemonStatus":
		return new(DaemonStatus)
	default:
		return nil
	}
}

func hasItemPayloadBinding(bindings []ItemPayloadBinding, kind, typeName string) bool {
	for _, binding := range bindings {
		if binding.Kind == kind && binding.Type == typeName {
			return true
		}
	}
	return false
}

func decodeRuntimeFixture(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
