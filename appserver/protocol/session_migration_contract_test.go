package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestSessionMigrationSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	got, ok := defs["SessionMigration"].(Schema)
	if !ok {
		t.Fatal("$defs missing SessionMigration")
	}
	want := closedThreadSessionParamSchema(Schema{
		"path": Schema{"type": "string"},
		"cwd":  Schema{"type": "string"},
		"title": Schema{
			"anyOf": []any{Schema{"type": "string"}, Schema{"type": "null"}},
		},
	}, []string{"path", "cwd", "title"})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SessionMigration = %#v, want %#v", got, want)
	}
}

func TestSessionMigrationAcceptsRustWireFormsAndCanonicalizesTitle(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{
			input: `{"path":"","cwd":""}`,
			want:  `{"path":"","cwd":"","title":null}`,
		},
		{
			input: `{"path":"sessions/session.jsonl","cwd":"repo/../repo","title":null}`,
			want:  `{"path":"sessions/session.jsonl","cwd":"repo/../repo","title":null}`,
		},
		{
			input: `{"path":"/tmp/session.jsonl","cwd":"/tmp/repo","title":""}`,
			want:  `{"path":"/tmp/session.jsonl","cwd":"/tmp/repo","title":""}`,
		},
		{
			input: `{"path":"./session","cwd":".","title":"Title","session_path":"ignored","future":{"nested":true}}`,
			want:  `{"path":"./session","cwd":".","title":"Title"}`,
		},
	}
	for _, tc := range cases {
		var migration SessionMigration
		if err := json.Unmarshal([]byte(tc.input), &migration); err != nil {
			t.Errorf("Unmarshal(%s): %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(migration)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("canonical = %s, %v; want %s", encoded, err, tc.want)
		}
	}
}

func TestSessionMigrationRejectsMalformedWireForms(t *testing.T) {
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"path":"session"}`,
		`{"cwd":"repo"}`,
		`{"path":null,"cwd":"repo"}`,
		`{"path":1,"cwd":"repo"}`,
		`{"path":true,"cwd":"repo"}`,
		`{"path":{},"cwd":"repo"}`,
		`{"path":[],"cwd":"repo"}`,
		`{"path":"session","cwd":null}`,
		`{"path":"session","cwd":1}`,
		`{"path":"session","cwd":true}`,
		`{"path":"session","cwd":{}}`,
		`{"path":"session","cwd":[]}`,
		`{"path":"session","cwd":"repo","title":1}`,
		`{"path":"session","cwd":"repo","title":true}`,
		`{"path":"session","cwd":"repo","title":{}}`,
		`{"path":"session","cwd":"repo","title":[]}`,
		`{"path":"one","path":"two","cwd":"repo"}`,
		`{"path":"session","cwd":"one","cwd":"two"}`,
		`{"path":"session","cwd":"repo","title":null,"title":"two"}`,
		`{"path":"session","cwd":"repo"`,
		`{"path":"session","cwd":"repo"} {}`,
	}
	for _, input := range invalid {
		var migration SessionMigration
		if err := json.Unmarshal([]byte(input), &migration); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}

	var migration *SessionMigration
	if err := migration.UnmarshalJSON([]byte(`{"path":"session","cwd":"repo"}`)); err == nil {
		t.Fatal("nil SessionMigration receiver succeeded")
	}
}

func TestDecodeSessionMigrationObjectRejectsMalformedJSON(t *testing.T) {
	invalid := []string{
		``,
		`{"unterminated`,
		`{"path":`,
		`{"path":"session","cwd":"repo"`,
		`{} {}`,
		`{} trailing`,
	}
	for _, input := range invalid {
		if _, err := decodeSessionMigrationObject([]byte(input)); err == nil {
			t.Errorf("decodeSessionMigrationObject(%q) succeeded", input)
		}
	}
}

func TestSessionMigrationRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "SessionMigration") ||
			slices.Contains(binding.Result, "SessionMigration") {
			t.Fatalf("SessionMigration unexpectedly bound: %#v", binding)
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

func TestSessionMigrationTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	want := "export type SessionMigration = {\n  \"cwd\": string;\n  \"path\": string;\n  \"title\": string | null;\n};"
	if !strings.Contains(source, want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

var _ json.Unmarshaler = (*SessionMigration)(nil)
