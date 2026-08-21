package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestGuardianApprovalReviewActionSchemaIsExact(t *testing.T) {
	definition := JSONSchema()["$defs"].(Schema)["GuardianApprovalReviewAction"].(Schema)
	variants, ok := definition["oneOf"].([]any)
	if !ok || len(variants) != 6 {
		t.Fatalf("GuardianApprovalReviewAction oneOf = %#v, want six variants", definition["oneOf"])
	}
	want := map[string]struct {
		required []string
		refs     map[string]string
	}{
		"command":            {required: []string{"type", "source", "command", "cwd"}, refs: map[string]string{"source": "GuardianCommandSource", "cwd": "AbsolutePathBuf"}},
		"execve":             {required: []string{"type", "source", "program", "argv", "cwd"}, refs: map[string]string{"source": "GuardianCommandSource", "cwd": "AbsolutePathBuf"}},
		"applyPatch":         {required: []string{"type", "cwd", "files"}, refs: map[string]string{"cwd": "AbsolutePathBuf"}},
		"networkAccess":      {required: []string{"type", "target", "host", "protocol", "port"}, refs: map[string]string{"protocol": "NetworkApprovalProtocol"}},
		"mcpToolCall":        {required: []string{"type", "server", "toolName"}},
		"requestPermissions": {required: []string{"type", "permissions"}, refs: map[string]string{"permissions": "RequestPermissionProfile"}},
	}
	seen := map[string]bool{}
	for _, raw := range variants {
		variant := raw.(Schema)
		if variant["type"] != "object" || variant["additionalProperties"] != false {
			t.Fatalf("GuardianApprovalReviewAction variant is not closed: %#v", variant)
		}
		properties := variant["properties"].(Schema)
		typeSchema := properties["type"].(Schema)
		tags := typeSchema["enum"].([]any)
		if len(tags) != 1 {
			t.Fatalf("GuardianApprovalReviewAction tag = %#v", typeSchema)
		}
		tag := tags[0].(string)
		expected, found := want[tag]
		if !found {
			t.Fatalf("unexpected GuardianApprovalReviewAction variant %q", tag)
		}
		seen[tag] = true
		gotRequired := append([]string(nil), variant["required"].([]string)...)
		slices.Sort(gotRequired)
		wantRequired := append([]string(nil), expected.required...)
		slices.Sort(wantRequired)
		if !reflect.DeepEqual(gotRequired, wantRequired) {
			t.Errorf("%s required = %v, want %v", tag, gotRequired, wantRequired)
		}
		for field, ref := range expected.refs {
			if got := properties[field].(Schema)["$ref"]; got != "#/$defs/"+ref {
				t.Errorf("%s.%s ref = %v, want %s", tag, field, got, ref)
			}
		}
		switch tag {
		case "execve":
			assertGuardianActionArraySchema(t, properties["argv"], Schema{"type": "string"})
		case "applyPatch":
			assertGuardianActionArraySchema(t, properties["files"], Schema{"$ref": "#/$defs/AbsolutePathBuf"})
		case "networkAccess":
			if got := properties["port"]; !reflect.DeepEqual(got, Schema{"type": "integer", "minimum": 0, "maximum": 65535}) {
				t.Errorf("networkAccess.port = %#v", got)
			}
		case "mcpToolCall":
			for _, field := range []string{"connectorId", "connectorName", "toolTitle"} {
				assertGuardianActionNullableStringSchema(t, properties[field])
			}
		case "requestPermissions":
			assertGuardianActionNullableStringSchema(t, properties["reason"])
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("GuardianApprovalReviewAction variants = %v, want %v", seen, want)
	}
}

func TestGuardianApprovalReviewActionAcceptsSerdeWireForms(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		canonical string
		kind      string
	}{
		{
			name:      "command normalizes cwd and discards unknown fields",
			input:     `{"unknown":true,"type":"command","source":"shell","command":"","cwd":"/workspace/../workspace","port":7}`,
			canonical: `{"type":"command","source":"shell","command":"","cwd":"/workspace"}`,
			kind:      "command",
		},
		{
			name:      "execve preserves ordered duplicate arguments",
			input:     `{"type":"execve","source":"unifiedExec","program":"","argv":["-x","-x",""],"cwd":"/workspace"}`,
			canonical: `{"type":"execve","source":"unifiedExec","program":"","argv":["-x","-x",""],"cwd":"/workspace"}`,
			kind:      "execve",
		},
		{
			name:      "apply patch preserves empty files",
			input:     `{"type":"applyPatch","cwd":"/workspace","files":[]}`,
			canonical: `{"type":"applyPatch","cwd":"/workspace","files":[]}`,
			kind:      "applyPatch",
		},
		{
			name:      "apply patch normalizes duplicate paths",
			input:     `{"type":"applyPatch","cwd":"/workspace/./","files":["/tmp/a/../b","/tmp/b"]}`,
			canonical: `{"type":"applyPatch","cwd":"/workspace","files":["/tmp/b","/tmp/b"]}`,
			kind:      "applyPatch",
		},
		{
			name:      "network access accepts uint16 maximum and empty strings",
			input:     `{"type":"networkAccess","target":"","host":"","protocol":"socks5Udp","port":65535}`,
			canonical: `{"type":"networkAccess","target":"","host":"","protocol":"socks5Udp","port":65535}`,
			kind:      "networkAccess",
		},
		{
			name:      "MCP omitted options become explicit null output",
			input:     `{"type":"mcpToolCall","server":"server","toolName":"tool"}`,
			canonical: `{"type":"mcpToolCall","server":"server","toolName":"tool","connectorId":null,"connectorName":null,"toolTitle":null}`,
			kind:      "mcpToolCall",
		},
		{
			name:      "MCP explicit options",
			input:     `{"type":"mcpToolCall","server":"","toolName":"","connectorId":"","connectorName":"name","toolTitle":null}`,
			canonical: `{"type":"mcpToolCall","server":"","toolName":"","connectorId":"","connectorName":"name","toolTitle":null}`,
			kind:      "mcpToolCall",
		},
		{
			name:      "permission omitted reason becomes explicit null output",
			input:     `{"type":"requestPermissions","permissions":{}}`,
			canonical: `{"type":"requestPermissions","reason":null,"permissions":{"network":null,"fileSystem":null}}`,
			kind:      "requestPermissions",
		},
		{
			name:      "permission explicit empty reason",
			input:     `{"type":"requestPermissions","reason":"","permissions":{"network":null,"fileSystem":null}}`,
			canonical: `{"type":"requestPermissions","reason":"","permissions":{"network":null,"fileSystem":null}}`,
			kind:      "requestPermissions",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var action GuardianApprovalReviewAction
			if err := json.Unmarshal([]byte(test.input), &action); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if action.Type() != test.kind {
				t.Errorf("Type() = %q, want %q", action.Type(), test.kind)
			}
			encoded, err := json.Marshal(action)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(encoded) != test.canonical {
				t.Errorf("canonical = %s, want %s", encoded, test.canonical)
			}
		})
	}
}

func TestGuardianApprovalReviewActionRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `1`, `true`, `"value"`, `{`, `{}`,
		`{"type":null}`, `{"type":"other"}`, `{"type":"Command"}`, `{"type":1}`,
		`{"type":"command","source":"shell","command":"c"}`,
		`{"type":"command","source":null,"command":"c","cwd":"/w"}`,
		`{"type":"command","source":"other","command":"c","cwd":"/w"}`,
		`{"type":"command","source":"shell","command":null,"cwd":"/w"}`,
		`{"type":"command","source":"shell","command":"c","cwd":"relative"}`,
		`{"type":"execve","source":"other","program":"p","argv":[],"cwd":"/w"}`,
		`{"type":"execve","source":"shell","program":null,"argv":[],"cwd":"/w"}`,
		`{"type":"execve","source":"shell","program":"p","argv":null,"cwd":"/w"}`,
		`{"type":"execve","source":"shell","program":"p","argv":[null],"cwd":"/w"}`,
		`{"type":"execve","source":"shell","program":"p","argv":[1],"cwd":"/w"}`,
		`{"type":"execve","source":"shell","program":"p","argv":[],"cwd":"relative"}`,
		`{"type":"applyPatch","cwd":"relative","files":[]}`,
		`{"type":"applyPatch","cwd":"/w","files":null}`,
		`{"type":"applyPatch","cwd":"/w","files":["relative"]}`,
		`{"type":"applyPatch","cwd":"/w","files":[null]}`,
		`{"type":"networkAccess","target":"t","host":"h","protocol":"http"}`,
		`{"type":"networkAccess","target":null,"host":"h","protocol":"http","port":1}`,
		`{"type":"networkAccess","target":"t","host":false,"protocol":"http","port":1}`,
		`{"type":"networkAccess","target":"t","host":"h","protocol":"other","port":1}`,
		`{"type":"networkAccess","target":"t","host":"h","protocol":"http","port":-1}`,
		`{"type":"networkAccess","target":"t","host":"h","protocol":"http","port":65536}`,
		`{"type":"networkAccess","target":"t","host":"h","protocol":"http","port":1.5}`,
		`{"type":"mcpToolCall","server":"s"}`,
		`{"type":"mcpToolCall","server":null,"toolName":"t"}`,
		`{"type":"mcpToolCall","server":"s","toolName":"t","connectorId":1}`,
		`{"type":"mcpToolCall","server":"s","toolName":"t","connectorName":false}`,
		`{"type":"mcpToolCall","server":"s","toolName":"t","toolTitle":[]}`,
		`{"type":"requestPermissions","permissions":null}`,
		`{"type":"requestPermissions","permissions":[]}`,
		`{"type":"requestPermissions","reason":1,"permissions":{}}`,
		`{"type":"command","type":"execve","source":"shell","command":"c","cwd":"/w"}`,
		`{"type":"command","source":"shell","source":"unifiedExec","command":"c","cwd":"/w"}`,
		`{"type":"mcpToolCall","server":"s","toolName":"t","connectorId":null,"connectorId":"id"}`,
		`{"type":"requestPermissions","reason":null,"reason":"r","permissions":{}}`,
		`{"type":"command","source":"shell","command":"c","cwd":"/w"} {}`,
		`{"type":"command","source":"shell","command":"c","cwd":"/w"} x`,
	} {
		assertJSONRejects[GuardianApprovalReviewAction](t, input)
	}
}

func TestGuardianApprovalReviewActionNilReceiverAndEmptyMarshalFailClosed(t *testing.T) {
	var action *GuardianApprovalReviewAction
	if err := action.UnmarshalJSON([]byte(`{"type":"applyPatch","cwd":"/w","files":[]}`)); err == nil {
		t.Fatal("nil GuardianApprovalReviewAction receiver succeeded")
	}
	if _, err := json.Marshal(GuardianApprovalReviewAction{}); err == nil {
		t.Fatal("empty GuardianApprovalReviewAction marshaled")
	}
}

func TestGuardianApprovalReviewActionRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "GuardianApprovalReviewAction") ||
			slices.Contains(binding.Result, "GuardianApprovalReviewAction") {
			t.Fatalf("GuardianApprovalReviewAction unexpectedly bound to %s", binding.Method)
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if binding.Type == "GuardianApprovalReviewAction" {
			t.Fatalf("GuardianApprovalReviewAction unexpectedly bound to item %s", binding.Kind)
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(Methods()); got != 229 {
		t.Fatalf("methods = %d, want 229", got)
	}
	if got := len(WireTypeBindings()); got != 86 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 86/5", got, len(ItemPayloadBindings()))
	}
}

func TestGuardianApprovalReviewActionTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	start := strings.Index(source, "export type GuardianApprovalReviewAction =")
	if start < 0 {
		t.Fatal("generated TypeScript action declaration start is missing")
	}
	end := strings.Index(source[start:], "\n\nexport type GuardianApprovalReviewStatus =")
	if end < 0 {
		t.Fatal("generated TypeScript action declaration end is missing")
	}
	actionSource := source[start : start+end]
	for _, want := range []string{
		`export type GuardianApprovalReviewAction =`,
		`"type": "command";`, `"source": GuardianCommandSource;`,
		`"command": string;`, `"cwd": AbsolutePathBuf;`,
		`"type": "execve";`, `"program": string;`, `"argv": Array<string>;`,
		`"type": "applyPatch";`, `"files": Array<AbsolutePathBuf>;`,
		`"type": "networkAccess";`, `"target": string;`, `"host": string;`,
		`"protocol": NetworkApprovalProtocol;`, `"port": number;`,
		`"type": "mcpToolCall";`, `"server": string;`, `"toolName": string;`,
		`"connectorId": string | null;`, `"connectorName": string | null;`,
		`"toolTitle": string | null;`, `"type": "requestPermissions";`,
		`"reason": string | null;`, `"permissions": RequestPermissionProfile;`,
	} {
		if !strings.Contains(actionSource, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`"connectorId"?:`, `"connectorName"?:`, `"toolTitle"?:`, `"reason"?:`,
	} {
		if strings.Contains(actionSource, forbidden) {
			t.Errorf("generated TypeScript unexpectedly contains %q", forbidden)
		}
	}
}

func assertGuardianActionArraySchema(t *testing.T, got any, items Schema) {
	t.Helper()
	want := Schema{"type": "array", "items": items}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("array schema = %#v, want %#v", got, want)
	}
}

func assertGuardianActionNullableStringSchema(t *testing.T, got any) {
	t.Helper()
	want := Schema{"type": []any{"string", "null"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("nullable string schema = %#v, want %#v", got, want)
	}
}

var (
	_ json.Marshaler   = GuardianApprovalReviewAction{}
	_ json.Unmarshaler = (*GuardianApprovalReviewAction)(nil)
)
