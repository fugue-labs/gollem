package protocol

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestExternalAgentConfigMigrationItemTypeSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	assertStringEnum(
		t,
		defs["ExternalAgentConfigMigrationItemType"],
		"AGENTS_MD",
		"CONFIG",
		"SKILLS",
		"PLUGINS",
		"MCP_SERVER_CONFIG",
		"SUBAGENTS",
		"HOOKS",
		"COMMANDS",
		"SESSIONS",
	)
}

func TestExternalAgentConfigMigrationItemTypeAcceptsExactValues(t *testing.T) {
	for _, input := range []string{
		`"AGENTS_MD"`,
		`"CONFIG"`,
		`"SKILLS"`,
		`"PLUGINS"`,
		`"MCP_SERVER_CONFIG"`,
		`"SUBAGENTS"`,
		`"HOOKS"`,
		`"COMMANDS"`,
		`"SESSIONS"`,
	} {
		var itemType ExternalAgentConfigMigrationItemType
		if err := json.Unmarshal([]byte(input), &itemType); err != nil {
			t.Fatalf("unmarshal ExternalAgentConfigMigrationItemType %s: %v", input, err)
		}
		encoded, err := json.Marshal(itemType)
		if err != nil {
			t.Fatalf("marshal ExternalAgentConfigMigrationItemType %s: %v", input, err)
		}
		if got := string(encoded); got != input {
			t.Fatalf("ExternalAgentConfigMigrationItemType round trip = %s, want %s", got, input)
		}
	}
}

func TestExternalAgentConfigMigrationItemTypeRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `""`, `"OTHER"`, `"agents_md"`, `"AgentsMd"`,
		`"AGENT_MD"`, `"MCP_SERVER"`, `"mcp_server_config"`,
		`"SUB_AGENTS"`, `"COMMAND"`, `"SESSION"`,
		`1`, `true`, `{}`, `[]`, `"CONFIG" {}`, `"SESSIONS" x`,
	} {
		assertJSONRejects[ExternalAgentConfigMigrationItemType](t, input)
	}
}

func TestExternalAgentConfigMigrationItemTypeNilReceiverAndInvalidMarshalFailClosed(t *testing.T) {
	var itemType *ExternalAgentConfigMigrationItemType
	if err := itemType.UnmarshalJSON([]byte(`"CONFIG"`)); err == nil {
		t.Fatal("nil ExternalAgentConfigMigrationItemType receiver succeeded")
	}
	if _, err := json.Marshal(ExternalAgentConfigMigrationItemType("OTHER")); err == nil {
		t.Fatal("invalid ExternalAgentConfigMigrationItemType marshaled")
	}
}

func TestExternalAgentConfigMigrationItemTypeRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "ExternalAgentConfigMigrationItemType") ||
			slices.Contains(binding.Result, "ExternalAgentConfigMigrationItemType") {
			t.Fatalf("ExternalAgentConfigMigrationItemType unexpectedly bound to %s", binding.Method)
		}
	}
	for _, method := range []string{"externalAgentConfig/detect", "externalAgentConfig/import"} {
		info, ok := LookupMethod(method)
		if !ok || info.State != MethodDeferredStub {
			t.Errorf("%s = %#v, %v; want deferred stub", method, info, ok)
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

func TestExternalAgentConfigMigrationItemTypeTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type ExternalAgentConfigMigrationItemType = "AGENTS_MD" | "CONFIG" | "SKILLS" | "PLUGINS" | "MCP_SERVER_CONFIG" | "SUBAGENTS" | "HOOKS" | "COMMANDS" | "SESSIONS";`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

var (
	_ json.Marshaler   = ExternalAgentConfigMigrationItemType("")
	_ json.Unmarshaler = (*ExternalAgentConfigMigrationItemType)(nil)
)
