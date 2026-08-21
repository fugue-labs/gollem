package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestFeedbackUploadParamsSchemaIsExact(t *testing.T) {
	got := JSONSchema()["$defs"].(Schema)["FeedbackUploadParams"]
	want := Schema{
		"properties": Schema{
			"classification": Schema{"type": "string"},
			"extraLogFiles": Schema{
				"items": Schema{"type": "string"},
				"type":  []any{"array", "null"},
			},
			"includeLogs": Schema{"type": "boolean"},
			"reason":      Schema{"type": []any{"string", "null"}},
			"tags": Schema{
				"additionalProperties": Schema{"type": "string"},
				"type":                 []any{"object", "null"},
			},
			"threadId": Schema{"type": []any{"string", "null"}},
		},
		"required": []string{"classification"},
		"type":     "object",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FeedbackUploadParams = %#v, want %#v", got, want)
	}
}

func TestFeedbackUploadParamsPreserveSerdeWireForms(t *testing.T) {
	assertFeedbackUploadRoundTrip(
		t,
		`{"classification":"bug","future":true}`,
		`{"classification":"bug","reason":null,"threadId":null,"extraLogFiles":null,"tags":null}`,
	)
	assertFeedbackUploadRoundTrip(
		t,
		`{"classification":"bug","reason":"details","threadId":"thread","includeLogs":true,"extraLogFiles":["relative.log","/tmp/feedback"],"tags":{"z":"last","a":"first"},"future":true}`,
		`{"classification":"bug","reason":"details","threadId":"thread","includeLogs":true,"extraLogFiles":["relative.log","/tmp/feedback"],"tags":{"a":"first","z":"last"}}`,
	)
}

func TestFeedbackUploadParamsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"classification":null}`, `{"classification":1}`, `{"reason":1}`,
		`{"threadId":1}`, `{"includeLogs":null}`, `{"includeLogs":"true"}`,
		`{"extraLogFiles":"log"}`, `{"extraLogFiles":[1]}`,
		`{"tags":[]}`, `{"tags":{"label":1}}`,
		`{"classification":"bug","classification":"other"}`, `{"classification":"bug"} {}`,
	} {
		assertJSONRejects[FeedbackUploadParams](t, input)
	}
}

func TestFeedbackUploadParamsRemainStandalone(t *testing.T) {
	var nilParams *FeedbackUploadParams
	if err := nilParams.UnmarshalJSON([]byte(`{"classification":"bug"}`)); err == nil {
		t.Fatal("nil FeedbackUploadParams receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			if name == "FeedbackUploadParams" {
				t.Fatalf("FeedbackUploadParams unexpectedly bound to %s", binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestFeedbackUploadParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type FeedbackUploadParams = { classification: string, reason?: string | null, threadId?: string | null, includeLogs?: boolean, extraLogFiles?: Array<string> | null, tags?: { [key in string]?: string } | null, };`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

func assertFeedbackUploadRoundTrip(t *testing.T, input, want string) {
	t.Helper()
	var value FeedbackUploadParams
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %s = %s, %v; want %s", input, encoded, err, want)
	}
}
