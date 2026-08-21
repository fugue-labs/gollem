package protocol

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestMcpServerStatusDetailSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	assertStringEnum(
		t,
		defs["McpServerStatusDetail"],
		"full",
		"toolsAndAuthOnly",
	)
}

func TestMcpServerStatusDetailAcceptsExactValues(t *testing.T) {
	tests := map[string]McpServerStatusDetail{
		`"full"`:             McpServerStatusDetailFull,
		`"toolsAndAuthOnly"`: McpServerStatusDetailToolsAndAuthOnly,
	}
	for input, want := range tests {
		var detail McpServerStatusDetail
		if err := json.Unmarshal([]byte(input), &detail); err != nil {
			t.Fatalf("unmarshal McpServerStatusDetail %s: %v", input, err)
		}
		if detail != want {
			t.Fatalf("McpServerStatusDetail = %q, want %q", detail, want)
		}
		encoded, err := json.Marshal(detail)
		if err != nil {
			t.Fatalf("marshal McpServerStatusDetail %s: %v", input, err)
		}
		if got := string(encoded); got != input {
			t.Fatalf("McpServerStatusDetail round trip = %s, want %s", got, input)
		}
	}
}

func TestMcpServerStatusDetailRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `""`, `"other"`, `"Full"`, `"ToolsAndAuthOnly"`,
		`"tools_and_auth_only"`, `"toolsOnly"`, `1`, `true`, `{}`, `[]`,
		`"full" {}`, `"toolsAndAuthOnly" x`,
	} {
		assertJSONRejects[McpServerStatusDetail](t, input)
	}
}

func TestMcpServerStatusDetailNilReceiverAndInvalidMarshalFailClosed(t *testing.T) {
	var detail *McpServerStatusDetail
	if err := detail.UnmarshalJSON([]byte(`"full"`)); err == nil {
		t.Fatal("nil McpServerStatusDetail receiver succeeded")
	}
	for _, value := range []McpServerStatusDetail{"", "other"} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatalf("invalid McpServerStatusDetail %q marshaled", value)
		}
	}
}

func TestMcpServerStatusDetailRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "McpServerStatusDetail") ||
			slices.Contains(binding.Result, "McpServerStatusDetail") {
			t.Fatalf("McpServerStatusDetail unexpectedly bound to %s", binding.Method)
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(Methods()); got != 229 {
		t.Fatalf("methods = %d, want 229", got)
	}
	if got := len(WireTypeBindings()); got != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 85/5", got, len(ItemPayloadBindings()))
	}
}

func TestMcpServerStatusDetailTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type McpServerStatusDetail = "full" | "toolsAndAuthOnly";`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

var (
	_ json.Marshaler   = McpServerStatusDetail("")
	_ json.Unmarshaler = (*McpServerStatusDetail)(nil)
)
