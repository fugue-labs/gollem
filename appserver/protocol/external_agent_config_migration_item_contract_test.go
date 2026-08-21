package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestExternalAgentConfigMigrationItemSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	got, ok := defs["ExternalAgentConfigMigrationItem"].(Schema)
	if !ok {
		t.Fatal("$defs missing ExternalAgentConfigMigrationItem")
	}
	want := closedThreadSessionParamSchema(Schema{
		"itemType":    Schema{"$ref": "#/$defs/ExternalAgentConfigMigrationItemType"},
		"description": Schema{"type": "string"},
		"cwd": Schema{"anyOf": []any{
			Schema{"type": "string"}, Schema{"type": "null"},
		}},
		"details": Schema{"anyOf": []any{
			Schema{"$ref": "#/$defs/MigrationDetails"}, Schema{"type": "null"},
		}},
	}, []string{"itemType", "description"})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExternalAgentConfigMigrationItem = %#v, want %#v", got, want)
	}
}

func TestExternalAgentConfigMigrationItemAcceptsRustWireForms(t *testing.T) {
	relative := "repo/../repo"
	absolute := "/tmp/repo"
	cases := []struct {
		name  string
		input string
		want  ExternalAgentConfigMigrationItem
	}{
		{
			name:  "options omitted",
			input: `{"itemType":"CONFIG","description":"config"}`,
			want: ExternalAgentConfigMigrationItem{
				ItemType:    ExternalAgentConfigMigrationItemTypeConfig,
				Description: "config",
			},
		},
		{
			name: "explicit null and unknown",
			input: `{"itemType":"AGENTS_MD","description":"","cwd":null,"details":null,` +
				`"future":{"nested":true}}`,
			want: ExternalAgentConfigMigrationItem{
				ItemType: ExternalAgentConfigMigrationItemTypeAgentsMD,
			},
		},
		{
			name:  "relative generic path",
			input: `{"itemType":"SESSIONS","description":"sessions","cwd":"repo/../repo"}`,
			want: ExternalAgentConfigMigrationItem{
				ItemType:    ExternalAgentConfigMigrationItemTypeSessions,
				Description: "sessions",
				CWD:         &relative,
			},
		},
		{
			name: "absolute path and defaulted details",
			input: `{"itemType":"SKILLS","description":"skills","cwd":"/tmp/repo",` +
				`"details":{"skills":[{"name":"one"}]}}`,
			want: ExternalAgentConfigMigrationItem{
				ItemType:    ExternalAgentConfigMigrationItemTypeSkills,
				Description: "skills",
				CWD:         &absolute,
				Details:     &MigrationDetails{Skills: []SkillMigration{{Name: "one"}}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var item ExternalAgentConfigMigrationItem
			if err := json.Unmarshal([]byte(tc.input), &item); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !reflect.DeepEqual(item, canonicalExternalAgentConfigMigrationItem(tc.want)) {
				t.Fatalf("item = %#v, want %#v", item, canonicalExternalAgentConfigMigrationItem(tc.want))
			}
			encoded, err := json.Marshal(item)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var roundTrip ExternalAgentConfigMigrationItem
			if err := json.Unmarshal(encoded, &roundTrip); err != nil || !reflect.DeepEqual(roundTrip, item) {
				t.Fatalf("round trip = %#v, %v; want %#v", roundTrip, err, item)
			}
		})
	}

	var emptyPath ExternalAgentConfigMigrationItem
	if err := json.Unmarshal([]byte(`{"itemType":"HOOKS","description":"hook","cwd":""}`), &emptyPath); err != nil {
		t.Fatalf("empty generic cwd: %v", err)
	}
	if emptyPath.CWD == nil || *emptyPath.CWD != "" {
		t.Fatalf("empty cwd = %#v, want explicit empty string", emptyPath.CWD)
	}
}

func TestExternalAgentConfigMigrationItemCanonicalizesNullableFields(t *testing.T) {
	input := `{"itemType":"COMMANDS","description":"commands"}`
	var item ExternalAgentConfigMigrationItem
	if err := json.Unmarshal([]byte(input), &item); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	encoded, err := json.Marshal(item)
	want := `{"itemType":"COMMANDS","description":"commands","cwd":null,"details":null}`
	if err != nil || string(encoded) != want {
		t.Fatalf("canonical = %s, %v; want %s", encoded, err, want)
	}
}

func TestExternalAgentConfigMigrationItemRejectsMalformedWireForms(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"description":"config"}`,
		`{"itemType":"CONFIG"}`,
		`{"itemType":null,"description":"config"}`,
		`{"itemType":"OTHER","description":"config"}`,
		`{"itemType":1,"description":"config"}`,
		`{"itemType":"CONFIG","description":null}`,
		`{"itemType":"CONFIG","description":1}`,
		`{"itemType":"CONFIG","description":true}`,
		`{"itemType":"CONFIG","description":{}}`,
		`{"itemType":"CONFIG","description":[]}`,
		`{"itemType":"CONFIG","description":"config","cwd":1}`,
		`{"itemType":"CONFIG","description":"config","cwd":true}`,
		`{"itemType":"CONFIG","description":"config","cwd":{}}`,
		`{"itemType":"CONFIG","description":"config","cwd":[]}`,
		`{"itemType":"CONFIG","description":"config","details":"details"}`,
		`{"itemType":"CONFIG","description":"config","details":[]}`,
		`{"itemType":"CONFIG","description":"config","details":{"skills":null}}`,
		`{"itemType":"CONFIG","itemType":"SKILLS","description":"config"}`,
		`{"itemType":"CONFIG","description":"one","description":"two"}`,
		`{"itemType":"CONFIG","description":"config","cwd":null,"cwd":"repo"}`,
		`{"itemType":"CONFIG","description":"config","details":null,"details":{}}`,
		`{"itemType":"CONFIG","description":"config"`,
		`{"itemType":"CONFIG","description":"config"} {}`,
	}
	for _, input := range invalid {
		var item ExternalAgentConfigMigrationItem
		if err := json.Unmarshal([]byte(input), &item); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}

	var item *ExternalAgentConfigMigrationItem
	if err := item.UnmarshalJSON([]byte(`{"itemType":"CONFIG","description":"config"}`)); err == nil {
		t.Fatal("nil ExternalAgentConfigMigrationItem receiver succeeded")
	}
}

func TestDecodeExternalAgentConfigMigrationItemObjectRejectsMalformedEnvelopes(t *testing.T) {
	invalid := []string{
		``,
		`null`,
		`{"`,
		`{"itemType":}`,
		`{"unknown":1`,
		`{} {}`,
		`{} {`,
	}
	for _, input := range invalid {
		if _, err := decodeExternalAgentConfigMigrationItemObject([]byte(input)); err == nil {
			t.Errorf("decodeExternalAgentConfigMigrationItemObject(%q) succeeded", input)
		}
	}
}

func TestExternalAgentConfigMigrationItemRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "ExternalAgentConfigMigrationItem") ||
			slices.Contains(binding.Result, "ExternalAgentConfigMigrationItem") {
			t.Fatalf("ExternalAgentConfigMigrationItem unexpectedly bound: %#v", binding)
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

func TestExternalAgentConfigMigrationItemTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := "export type ExternalAgentConfigMigrationItem = {\n" +
		"  \"cwd\": string | null;\n" +
		"  \"description\": string;\n" +
		"  \"details\": MigrationDetails | null;\n" +
		"  \"itemType\": ExternalAgentConfigMigrationItemType;\n" +
		"};"
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

func canonicalExternalAgentConfigMigrationItem(item ExternalAgentConfigMigrationItem) ExternalAgentConfigMigrationItem {
	encoded, err := json.Marshal(item)
	if err != nil {
		panic(err)
	}
	var canonical ExternalAgentConfigMigrationItem
	if err := json.Unmarshal(encoded, &canonical); err != nil {
		panic(err)
	}
	return canonical
}

var (
	_ json.Marshaler   = ExternalAgentConfigMigrationItem{}
	_ json.Unmarshaler = (*ExternalAgentConfigMigrationItem)(nil)
)
