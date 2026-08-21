package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestPermissionsRequestApprovalParamsSchemaIsExact(t *testing.T) {
	definition, ok := JSONSchema()["$defs"].(Schema)["PermissionsRequestApprovalParams"].(Schema)
	if !ok {
		t.Fatal("$defs missing PermissionsRequestApprovalParams")
	}
	want := sourceApprovalParamsSchema("PermissionsRequestApprovalParams", Schema{
		"cwd":           Schema{"$ref": "#/$defs/AbsolutePathBuf"},
		"environmentId": Schema{"default": nil, "type": []any{"string", "null"}},
		"itemId":        Schema{"type": "string"},
		"permissions":   Schema{"$ref": "#/$defs/RequestPermissionProfile"},
		"reason":        Schema{"type": []any{"string", "null"}},
		"startedAtMs": Schema{
			"description": "Unix timestamp (in milliseconds) when this approval request started.",
			"format":      "int64",
			"type":        "integer",
		},
		"threadId": Schema{"type": "string"},
		"turnId":   Schema{"type": "string"},
	}, []string{"cwd", "itemId", "permissions", "startedAtMs", "threadId", "turnId"})
	if !reflect.DeepEqual(definition, want) {
		t.Fatalf("PermissionsRequestApprovalParams = %#v, want %#v", definition, want)
	}
}

func TestPermissionsRequestApprovalParamsAcceptsSerdeWireForms(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{
			input: `{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"cwd":"/workspace","permissions":{}}`,
			want:  `{"threadId":"thread","turnId":"turn","itemId":"item","environmentId":null,"startedAtMs":1,"cwd":"/workspace","reason":null,"permissions":{"network":null,"fileSystem":null}}`,
		},
		{
			input: `{"future":1,"future":2,"threadId":"thread","turnId":"turn","itemId":"item","environmentId":"env","startedAtMs":2,"cwd":"/workspace/../workspace/project","reason":"review","permissions":{"network":null,"fileSystem":null},"other":{"ignored":true}}`,
			want:  `{"threadId":"thread","turnId":"turn","itemId":"item","environmentId":"env","startedAtMs":2,"cwd":"/workspace/project","reason":"review","permissions":{"network":null,"fileSystem":null}}`,
		},
	} {
		var params PermissionsRequestApprovalParams
		if err := json.Unmarshal([]byte(test.input), &params); err != nil {
			t.Errorf("Unmarshal(%s): %v", test.input, err)
			continue
		}
		encoded, err := json.Marshal(params)
		if err != nil || string(encoded) != test.want {
			t.Errorf("round trip %s = %s, %v; want %s", test.input, encoded, err, test.want)
		}
	}
}

func TestPermissionsRequestApprovalParamsRejectsMalformedWireForms(t *testing.T) {
	valid := `"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"cwd":"/workspace","permissions":{}`
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"turnId":"turn","itemId":"item","startedAtMs":1,"cwd":"/workspace","permissions":{}}`,
		`{"threadId":"thread","itemId":"item","startedAtMs":1,"cwd":"/workspace","permissions":{}}`,
		`{"threadId":"thread","turnId":"turn","startedAtMs":1,"cwd":"/workspace","permissions":{}}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","cwd":"/workspace","permissions":{}}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"permissions":{}}`,
		`{"threadId":null,"turnId":"turn","itemId":"item","startedAtMs":1,"cwd":"/workspace","permissions":{}}`,
		`{"threadId":"thread","turnId":null,"itemId":"item","startedAtMs":1,"cwd":"/workspace","permissions":{}}`,
		`{"threadId":"thread","turnId":"turn","itemId":null,"startedAtMs":1,"cwd":"/workspace","permissions":{}}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":null,"cwd":"/workspace","permissions":{}}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"cwd":null,"permissions":{}}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"cwd":"relative","permissions":{}}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"cwd":"/workspace","permissions":null}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","environmentId":1,"startedAtMs":1,"cwd":"/workspace","permissions":{}}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"cwd":"/workspace","reason":1,"permissions":{}}`,
		`{"threadId":"one","threadId":"two","turnId":"turn","itemId":"item","startedAtMs":1,"cwd":"/workspace","permissions":{}}`,
		`{"threadId":"thread","turnId":"one","turnId":"two","itemId":"item","startedAtMs":1,"cwd":"/workspace","permissions":{}}`,
		`{"threadId":"thread","turnId":"turn","itemId":"one","itemId":"two","startedAtMs":1,"cwd":"/workspace","permissions":{}}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","environmentId":null,"environmentId":"env","startedAtMs":1,"cwd":"/workspace","permissions":{}}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"startedAtMs":2,"cwd":"/workspace","permissions":{}}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"cwd":"/workspace","cwd":"/other","permissions":{}}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"cwd":"/workspace","reason":null,"reason":"review","permissions":{}}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"cwd":"/workspace","permissions":{},"permissions":{}}`,
		`{` + valid + `} {}`,
		`{` + valid + `} x`,
	} {
		assertJSONRejects[PermissionsRequestApprovalParams](t, input)
	}

	var params *PermissionsRequestApprovalParams
	if err := params.UnmarshalJSON([]byte(`{` + valid + `}`)); err == nil {
		t.Fatal("nil PermissionsRequestApprovalParams receiver succeeded")
	}
}

func TestPermissionsRequestApprovalParamsRemainsStandalone(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	if _, ok := defs["PermissionsRequestApprovalParams"]; !ok {
		t.Fatal("PermissionsRequestApprovalParams missing")
	}
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "PermissionsRequestApprovalParams") ||
			slices.Contains(binding.Result, "PermissionsRequestApprovalParams") {
			t.Fatalf("PermissionsRequestApprovalParams unexpectedly bound to %s", binding.Method)
		}
	}
	if got := len(defs); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestPermissionsRequestApprovalParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := "export type PermissionsRequestApprovalParams = {\n" +
		"  \"cwd\": AbsolutePathBuf;\n" +
		"  \"environmentId\": string | null;\n" +
		"  \"itemId\": string;\n" +
		"  \"permissions\": RequestPermissionProfile;\n" +
		"  \"reason\": string | null;\n" +
		"  \"startedAtMs\": number;\n" +
		"  \"threadId\": string;\n" +
		"  \"turnId\": string;\n" +
		"};"
	if !strings.Contains(string(generated), want) {
		t.Fatalf("generated TypeScript missing %q", want)
	}
}
