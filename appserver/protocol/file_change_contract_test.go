package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestFileChangeSchemaIsExact(t *testing.T) {
	definition := JSONSchema()["$defs"].(Schema)["FileChange"].(Schema)
	want := Schema{"oneOf": []any{
		fileChangeVariantSchema("add", []string{"content", "type"}, Schema{
			"content": Schema{"type": "string"},
		}),
		fileChangeVariantSchema("delete", []string{"content", "type"}, Schema{
			"content": Schema{"type": "string"},
		}),
		fileChangeVariantSchema("update", []string{"type", "unified_diff"}, Schema{
			"unified_diff": Schema{"type": "string"},
			"move_path":    Schema{"type": []any{"string", "null"}},
		}),
	}}
	if !reflect.DeepEqual(definition, want) {
		t.Fatalf("FileChange = %#v, want %#v", definition, want)
	}
}

func TestFileChangeAcceptsSerdeWireForms(t *testing.T) {
	for _, test := range []struct {
		input    string
		want     string
		wantType string
	}{
		{input: `{"type":"add","content":""}`, want: `{"type":"add","content":""}`, wantType: "add"},
		{input: `{"future":true,"type":"delete","content":"old"}`, want: `{"type":"delete","content":"old"}`, wantType: "delete"},
		{input: `{"type":"update","unified_diff":"diff"}`, want: `{"type":"update","unified_diff":"diff","move_path":null}`, wantType: "update"},
		{input: `{"type":"update","unified_diff":"","move_path":null}`, want: `{"type":"update","unified_diff":"","move_path":null}`, wantType: "update"},
		{input: `{"type":"update","unified_diff":"diff","move_path":"next path"}`, want: `{"type":"update","unified_diff":"diff","move_path":"next path"}`, wantType: "update"},
		{input: `{"type":"add","content":"new","unified_diff":"ignored","move_path":"ignored"}`, want: `{"type":"add","content":"new"}`, wantType: "add"},
		{input: `{"type":"delete","content":"old","unified_diff":1,"unified_diff":2}`, want: `{"type":"delete","content":"old"}`, wantType: "delete"},
		{input: `{"type":"update","content":"ignored","unified_diff":"diff","future":1,"future":2}`, want: `{"type":"update","unified_diff":"diff","move_path":null}`, wantType: "update"},
		{input: `{"type":"update","content":1,"content":2,"unified_diff":"diff"}`, want: `{"type":"update","unified_diff":"diff","move_path":null}`, wantType: "update"},
	} {
		var change FileChange
		if err := json.Unmarshal([]byte(test.input), &change); err != nil {
			t.Errorf("Unmarshal(%s): %v", test.input, err)
			continue
		}
		if change.Type() != test.wantType {
			t.Errorf("Type(%s) = %q, want %q", test.input, change.Type(), test.wantType)
		}
		encoded, err := json.Marshal(change)
		if err != nil {
			t.Errorf("Marshal(%s): %v", test.input, err)
			continue
		}
		if string(encoded) != test.want {
			t.Errorf("round trip %s = %s, want %s", test.input, encoded, test.want)
		}
	}
}

func TestFileChangeRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`,
		`{}`,
		`{"type":null}`,
		`{"type":1}`,
		`{"type":"other"}`,
		`{"type":"add"}`,
		`{"type":"add","content":null}`,
		`{"type":"add","content":1}`,
		`{"type":"add","content":"one","content":"two"}`,
		`{"type":"delete"}`,
		`{"type":"delete","content":[]}`,
		`{"type":"update"}`,
		`{"type":"update","unified_diff":null}`,
		`{"type":"update","unified_diff":1}`,
		`{"type":"update","unified_diff":"diff","move_path":1}`,
		`{"type":"update","unified_diff":"one","unified_diff":"two"}`,
		`{"type":"update","unified_diff":"diff","move_path":null,"move_path":"next"}`,
		`{"type":"add","type":"delete","content":"value"}`,
		`{"type":"add","content":"value"} {}`,
		`{"type":"add","content":"value"} x`,
	} {
		assertJSONRejects[FileChange](t, input)
	}

	var change *FileChange
	if err := change.UnmarshalJSON([]byte(`{"type":"add","content":"value"}`)); err == nil {
		t.Fatal("nil FileChange receiver succeeded")
	}
	if _, err := json.Marshal(FileChange{}); err == nil {
		t.Fatal("zero FileChange marshaled")
	}
}

func TestFileChangeRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "FileChange") || slices.Contains(binding.Result, "FileChange") {
			t.Fatalf("FileChange unexpectedly bound to %s", binding.Method)
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if binding.Type == "FileChange" {
			t.Fatalf("FileChange unexpectedly bound to item %s", binding.Kind)
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

func TestFileChangeTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := "export type FileChange = {\n" +
		"  \"content\": string;\n" +
		"  \"type\": \"add\";\n" +
		"} | {\n" +
		"  \"content\": string;\n" +
		"  \"type\": \"delete\";\n" +
		"} | {\n" +
		"  \"move_path\": string | null;\n" +
		"  \"type\": \"update\";\n" +
		"  \"unified_diff\": string;\n" +
		"};"
	if !strings.Contains(string(generated), want) {
		t.Fatalf("generated TypeScript missing %q", want)
	}
}
