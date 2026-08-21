package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestFileChangeOutputDeltaNotificationSchemaIsExact(t *testing.T) {
	definition, ok := JSONSchema()["$defs"].(Schema)["FileChangeOutputDeltaNotification"].(Schema)
	if !ok {
		t.Fatal("$defs missing FileChangeOutputDeltaNotification")
	}
	want := Schema{
		"type": "object",
		"properties": Schema{
			"threadId": Schema{"type": "string"},
			"turnId":   Schema{"type": "string"},
			"itemId":   Schema{"type": "string"},
			"delta":    Schema{"type": "string"},
		},
		"required":             []string{"threadId", "turnId", "itemId", "delta"},
		"additionalProperties": false,
	}
	if !reflect.DeepEqual(definition, want) {
		t.Fatalf("FileChangeOutputDeltaNotification = %#v, want %#v", definition, want)
	}
}

func TestFileChangeOutputDeltaNotificationAcceptsSerdeWireForms(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{
			input: `{"threadId":"","turnId":"","itemId":"","delta":""}`,
			want:  `{"threadId":"","turnId":"","itemId":"","delta":""}`,
		},
		{
			input: `{"future":true,"threadId":"thread","turnId":"turn","itemId":"item","delta":"patch\nline"}`,
			want:  `{"threadId":"thread","turnId":"turn","itemId":"item","delta":"patch\nline"}`,
		},
		{
			input: `{"threadId":"thread","turnId":"turn","itemId":"item","delta":"value","future":1,"future":2}`,
			want:  `{"threadId":"thread","turnId":"turn","itemId":"item","delta":"value"}`,
		},
	} {
		var notification FileChangeOutputDeltaNotification
		if err := json.Unmarshal([]byte(test.input), &notification); err != nil {
			t.Errorf("Unmarshal(%s): %v", test.input, err)
			continue
		}
		encoded, err := json.Marshal(notification)
		if err != nil || string(encoded) != test.want {
			t.Errorf("round trip %s = %s, %v; want %s", test.input, encoded, err, test.want)
		}
	}
}

func TestFileChangeOutputDeltaNotificationRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"turnId":"turn","itemId":"item","delta":"delta"}`,
		`{"threadId":"thread","itemId":"item","delta":"delta"}`,
		`{"threadId":"thread","turnId":"turn","delta":"delta"}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item"}`,
		`{"threadId":null,"turnId":"turn","itemId":"item","delta":"delta"}`,
		`{"threadId":"thread","turnId":1,"itemId":"item","delta":"delta"}`,
		`{"threadId":"thread","turnId":"turn","itemId":[],"delta":"delta"}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","delta":false}`,
		`{"threadId":"one","threadId":"two","turnId":"turn","itemId":"item","delta":"delta"}`,
		`{"threadId":"thread","turnId":"one","turnId":"two","itemId":"item","delta":"delta"}`,
		`{"threadId":"thread","turnId":"turn","itemId":"one","itemId":"two","delta":"delta"}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","delta":"one","delta":"two"}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","delta":"delta"} {}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","delta":"delta"} x`,
	} {
		assertJSONRejects[FileChangeOutputDeltaNotification](t, input)
	}

	var notification *FileChangeOutputDeltaNotification
	if err := notification.UnmarshalJSON([]byte(`{"threadId":"thread","turnId":"turn","itemId":"item","delta":"delta"}`)); err == nil {
		t.Fatal("nil FileChangeOutputDeltaNotification receiver succeeded")
	}
}

func TestFileChangeOutputDeltaNotificationRemainsDeprecatedAndStandalone(t *testing.T) {
	if reflect.TypeFor[FileChangeOutputDeltaNotification]() == reflect.TypeFor[CommandExecutionOutputDeltaNotification]() {
		t.Fatal("legacy file delta unexpectedly aliases command delta")
	}
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "FileChangeOutputDeltaNotification") ||
			slices.Contains(binding.Result, "FileChangeOutputDeltaNotification") {
			t.Fatalf("FileChangeOutputDeltaNotification unexpectedly bound to %s", binding.Method)
		}
	}
	info, ok := LookupMethod("item/fileChange/outputDelta")
	if !ok || info.State != MethodBlocked {
		t.Fatalf("item/fileChange/outputDelta = %#v, %v; want blocked", info, ok)
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

func TestFileChangeOutputDeltaNotificationTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := "export type FileChangeOutputDeltaNotification = {\n" +
		"  \"delta\": string;\n" +
		"  \"itemId\": string;\n" +
		"  \"threadId\": string;\n" +
		"  \"turnId\": string;\n" +
		"};"
	if !strings.Contains(string(generated), want) {
		t.Fatalf("generated TypeScript missing %q", want)
	}
}
