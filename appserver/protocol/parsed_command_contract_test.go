package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestParsedCommandSchemaIsExact(t *testing.T) {
	definition, ok := JSONSchema()["$defs"].(Schema)["ParsedCommand"].(Schema)
	if !ok {
		t.Fatal("$defs missing ParsedCommand")
	}
	want := Schema{"oneOf": []any{
		parsedCommandVariantSchema("read", []string{"cmd", "name", "path"}, Schema{
			"cmd":  Schema{"type": "string"},
			"name": Schema{"type": "string"},
			"path": Schema{
				"type": "string",
				"description": "(Best effort) Path to the file being read by the command. When " +
					"possible, this is an absolute path, though when relative, it should " +
					"be resolved against the `cwd`` that will be used to run the command " +
					"to derive the absolute path.",
			},
		}),
		parsedCommandVariantSchema("list_files", []string{"cmd"}, Schema{
			"cmd": Schema{"type": "string"}, "path": nullableStringSchema(),
		}),
		parsedCommandVariantSchema("search", []string{"cmd"}, Schema{
			"cmd": Schema{"type": "string"}, "query": nullableStringSchema(), "path": nullableStringSchema(),
		}),
		parsedCommandVariantSchema("unknown", []string{"cmd"}, Schema{
			"cmd": Schema{"type": "string"},
		}),
	}}
	if !reflect.DeepEqual(definition, want) {
		t.Fatalf("ParsedCommand = %#v, want %#v", definition, want)
	}
}

func TestParsedCommandAcceptsSerdeWireForms(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: `{"type":"read","cmd":"","name":"","path":""}`, want: `{"type":"read","cmd":"","name":"","path":""}`},
		{input: `{"type":"read","cmd":"cat file","name":"cat","path":"relative/../file"}`, want: `{"type":"read","cmd":"cat file","name":"cat","path":"relative/../file"}`},
		{input: `{"type":"list_files","cmd":"ls"}`, want: `{"type":"list_files","cmd":"ls","path":null}`},
		{input: `{"type":"list_files","cmd":"ls","path":null}`, want: `{"type":"list_files","cmd":"ls","path":null}`},
		{input: `{"type":"list_files","cmd":"ls src","path":"relative/src"}`, want: `{"type":"list_files","cmd":"ls src","path":"relative/src"}`},
		{input: `{"type":"search","cmd":"rg"}`, want: `{"type":"search","cmd":"rg","query":null,"path":null}`},
		{input: `{"type":"search","cmd":"rg","query":null,"path":null}`, want: `{"type":"search","cmd":"rg","query":null,"path":null}`},
		{input: `{"type":"search","cmd":"rg needle","query":"needle","path":null}`, want: `{"type":"search","cmd":"rg needle","query":"needle","path":null}`},
		{input: `{"type":"unknown","cmd":"custom --flag"}`, want: `{"type":"unknown","cmd":"custom --flag"}`},
		{input: `{"future":true,"type":"unknown","cmd":"run","path":"discarded"}`, want: `{"type":"unknown","cmd":"run"}`},
		{input: `{"type":"unknown","cmd":"run","path":"one","path":"two"}`, want: `{"type":"unknown","cmd":"run"}`},
		{input: `{"type":"read","cmd":"cat","name":"cat","path":"file","query":"discarded","future":1,"future":2}`, want: `{"type":"read","cmd":"cat","name":"cat","path":"file"}`},
		{input: `{"type":"read","cmd":"cat","name":"cat","path":"file","query":"one","query":"two"}`, want: `{"type":"read","cmd":"cat","name":"cat","path":"file"}`},
	} {
		var command ParsedCommand
		if err := json.Unmarshal([]byte(test.input), &command); err != nil {
			t.Errorf("Unmarshal(%s): %v", test.input, err)
			continue
		}
		encoded, err := json.Marshal(command)
		if err != nil || string(encoded) != test.want {
			t.Errorf("round trip %s = %s, %v; want %s", test.input, encoded, err, test.want)
		}
	}
}

func TestParsedCommandRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"cmd":"run"}`,
		`{"type":null,"cmd":"run"}`,
		`{"type":1,"cmd":"run"}`,
		`{"type":"execute","cmd":"run"}`,
		`{"type":"read","cmd":"cat","name":"cat"}`,
		`{"type":"read","cmd":"cat","name":"cat","path":null}`,
		`{"type":"read","cmd":null,"name":"cat","path":"file"}`,
		`{"type":"read","cmd":"cat","name":null,"path":"file"}`,
		`{"type":"list_files"}`,
		`{"type":"list_files","cmd":"ls","path":1}`,
		`{"type":"search"}`,
		`{"type":"search","cmd":"rg","query":1}`,
		`{"type":"search","cmd":"rg","path":false}`,
		`{"type":"unknown"}`,
		`{"type":"unknown","cmd":null}`,
		`{"type":"unknown","type":"read","cmd":"run"}`,
		`{"type":"unknown","cmd":"one","cmd":"two"}`,
		`{"type":"read","cmd":"cat","name":"one","name":"two","path":"file"}`,
		`{"type":"list_files","cmd":"ls","path":null,"path":"src"}`,
		`{"type":"search","cmd":"rg","query":null,"query":"q"}`,
		`{"type":"search","cmd":"rg","path":null,"path":"src"}`,
		`{"type":"unknown","cmd":"run"} {}`,
		`{"type":"unknown","cmd":"run"} x`,
	} {
		assertJSONRejects[ParsedCommand](t, input)
	}

	if _, err := json.Marshal(ParsedCommand{}); err == nil {
		t.Fatal("Marshal empty ParsedCommand succeeded")
	}
	var command *ParsedCommand
	if err := command.UnmarshalJSON([]byte(`{"type":"unknown","cmd":"run"}`)); err == nil {
		t.Fatal("nil ParsedCommand receiver succeeded")
	}
}

func TestParsedCommandRemainsStandalone(t *testing.T) {
	if reflect.TypeFor[ParsedCommand]() == reflect.TypeFor[CommandAction]() ||
		reflect.TypeFor[ParsedCommand]() == reflect.TypeFor[CommandExecutionAction]() {
		t.Fatal("ParsedCommand aliases a distinct live command-action type")
	}
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "ParsedCommand") || slices.Contains(binding.Result, "ParsedCommand") {
			t.Fatalf("ParsedCommand unexpectedly bound to %s", binding.Method)
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

func TestParsedCommandTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := "export type ParsedCommand = {\n" +
		"  \"cmd\": string;\n" +
		"  \"name\": string;\n" +
		"  \"path\": string;\n" +
		"  \"type\": \"read\";\n" +
		"} | {\n" +
		"  \"cmd\": string;\n" +
		"  \"path\": string | null;\n" +
		"  \"type\": \"list_files\";\n" +
		"} | {\n" +
		"  \"cmd\": string;\n" +
		"  \"path\": string | null;\n" +
		"  \"query\": string | null;\n" +
		"  \"type\": \"search\";\n" +
		"} | {\n" +
		"  \"cmd\": string;\n" +
		"  \"type\": \"unknown\";\n" +
		"};"
	if !strings.Contains(string(generated), want) {
		t.Fatalf("generated TypeScript missing %q", want)
	}
}
