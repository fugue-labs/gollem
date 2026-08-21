package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const emptyMigrationDetailsJSON = `{"plugins":[],"skills":[],"sessions":[],"mcpServers":[],"hooks":[],"subagents":[],"commands":[]}`

func TestMigrationDetailsSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	got, ok := defs["MigrationDetails"].(Schema)
	if !ok {
		t.Fatal("$defs missing MigrationDetails")
	}
	want := closedThreadSessionParamSchema(Schema{
		"plugins":    migrationDetailsArraySchema("PluginsMigration"),
		"skills":     migrationDetailsArraySchema("SkillMigration"),
		"sessions":   migrationDetailsArraySchema("SessionMigration"),
		"mcpServers": migrationDetailsArraySchema("McpServerMigration"),
		"hooks":      migrationDetailsArraySchema("HookMigration"),
		"subagents":  migrationDetailsArraySchema("SubagentMigration"),
		"commands":   migrationDetailsArraySchema("CommandMigration"),
	}, nil)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MigrationDetails = %#v, want %#v", got, want)
	}
	if _, ok := got["required"]; ok {
		t.Fatal("MigrationDetails schema unexpectedly requires serde-defaulted fields")
	}
}

func TestMigrationDetailsAcceptsRustWireForms(t *testing.T) {
	title := "thread"
	cases := []struct {
		name  string
		input string
		want  MigrationDetails
	}{
		{name: "all omitted", input: `{}`, want: MigrationDetails{}},
		{
			name:  "partial and unknown",
			input: `{"skills":[{"name":""}],"future":{"nested":true}}`,
			want:  MigrationDetails{Skills: []SkillMigration{{Name: ""}}},
		},
		{
			name: "full",
			input: `{"plugins":[{"marketplaceName":"market","pluginNames":["one","one"]}],` +
				`"skills":[{"name":"skill"}],` +
				`"sessions":[{"path":"session","cwd":"repo","title":"thread"}],` +
				`"mcpServers":[{"name":"mcp"}],"hooks":[{"name":"hook"}],` +
				`"subagents":[{"name":"agent"}],"commands":[{"name":"command"},{"name":"command"}]}`,
			want: MigrationDetails{
				Plugins:    []PluginsMigration{{MarketplaceName: "market", PluginNames: []string{"one", "one"}}},
				Skills:     []SkillMigration{{Name: "skill"}},
				Sessions:   []SessionMigration{{Path: "session", CWD: "repo", Title: &title}},
				MCPServers: []McpServerMigration{{Name: "mcp"}},
				Hooks:      []HookMigration{{Name: "hook"}},
				Subagents:  []SubagentMigration{{Name: "agent"}},
				Commands:   []CommandMigration{{Name: "command"}, {Name: "command"}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var details MigrationDetails
			if err := json.Unmarshal([]byte(tc.input), &details); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !reflect.DeepEqual(details, canonicalMigrationDetails(tc.want)) {
				t.Fatalf("details = %#v, want %#v", details, canonicalMigrationDetails(tc.want))
			}
			encoded, err := json.Marshal(details)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var roundTrip MigrationDetails
			if err := json.Unmarshal(encoded, &roundTrip); err != nil || !reflect.DeepEqual(roundTrip, details) {
				t.Fatalf("round trip = %#v, %v; want %#v", roundTrip, err, details)
			}
		})
	}

	encoded, err := json.Marshal(MigrationDetails{})
	if err != nil || string(encoded) != emptyMigrationDetailsJSON {
		t.Fatalf("zero-value canonical = %s, %v; want %s", encoded, err, emptyMigrationDetailsJSON)
	}
}

func TestMigrationDetailsRejectsMalformedWireForms(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`,
		`{"plugins":null}`,
		`{"skills":null}`,
		`{"sessions":null}`,
		`{"mcpServers":null}`,
		`{"hooks":null}`,
		`{"subagents":null}`,
		`{"commands":null}`,
		`{"plugins":{}}`,
		`{"skills":"skill"}`,
		`{"sessions":[null]}`,
		`{"mcpServers":[{"name":1}]}`,
		`{"hooks":[{"name":null}]}`,
		`{"subagents":[{}]}`,
		`{"commands":[{"name":"first","name":"second"}]}`,
		`{"commands":[],"commands":[]}`,
		`{"commands":[]`,
		`{"commands":[]} {}`,
	}
	for _, input := range invalid {
		var details MigrationDetails
		if err := json.Unmarshal([]byte(input), &details); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}

	var details *MigrationDetails
	if err := details.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil MigrationDetails receiver succeeded")
	}
}

func TestDecodeMigrationDetailsObjectRejectsMalformedJSON(t *testing.T) {
	invalid := []string{
		``,
		`{"unterminated`,
		`{"commands":`,
		`{"commands":[]`,
		`{} {}`,
		`{} trailing`,
	}
	for _, input := range invalid {
		if _, err := decodeMigrationDetailsObject([]byte(input)); err == nil {
			t.Errorf("decodeMigrationDetailsObject(%q) succeeded", input)
		}
	}
}

func TestMigrationDetailsPreservesArrayOrderAndDuplicates(t *testing.T) {
	input := `{"skills":[{"name":"second"},{"name":"first"},{"name":"second"}],` +
		`"commands":[{"name":"two"},{"name":"one"},{"name":"two"}]}`
	var details MigrationDetails
	if err := json.Unmarshal([]byte(input), &details); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got := []string{details.Skills[0].Name, details.Skills[1].Name, details.Skills[2].Name}; !reflect.DeepEqual(got, []string{"second", "first", "second"}) {
		t.Errorf("skill order = %v", got)
	}
	if got := []string{details.Commands[0].Name, details.Commands[1].Name, details.Commands[2].Name}; !reflect.DeepEqual(got, []string{"two", "one", "two"}) {
		t.Errorf("command order = %v", got)
	}
}

func TestMigrationDetailsRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "MigrationDetails") ||
			slices.Contains(binding.Result, "MigrationDetails") {
			t.Fatalf("MigrationDetails unexpectedly bound: %#v", binding)
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
	if got := len(WireTypeBindings()); got != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 85/5", got, len(ItemPayloadBindings()))
	}
}

func TestMigrationDetailsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	want := "export type MigrationDetails = {\n" +
		"  \"commands\": Array<CommandMigration>;\n" +
		"  \"hooks\": Array<HookMigration>;\n" +
		"  \"mcpServers\": Array<McpServerMigration>;\n" +
		"  \"plugins\": Array<PluginsMigration>;\n" +
		"  \"sessions\": Array<SessionMigration>;\n" +
		"  \"skills\": Array<SkillMigration>;\n" +
		"  \"subagents\": Array<SubagentMigration>;\n" +
		"};"
	if !strings.Contains(source, want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
	if !strings.Contains(source, `"appBrand"?: LoginAppBrand | null;`) {
		t.Error("MigrationDetails override made unrelated defaulted fields required")
	}
}

func canonicalMigrationDetails(details MigrationDetails) MigrationDetails {
	encoded, err := json.Marshal(details)
	if err != nil {
		panic(err)
	}
	var canonical MigrationDetails
	if err := json.Unmarshal(encoded, &canonical); err != nil {
		panic(err)
	}
	return canonical
}

var (
	_ json.Marshaler   = MigrationDetails{}
	_ json.Unmarshaler = (*MigrationDetails)(nil)
)
