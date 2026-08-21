package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestW3cTraceContextSchemaIsExact(t *testing.T) {
	definitions := JSONSchema()["$defs"].(Schema)
	if got := definitions["W3cTraceContext"]; !reflect.DeepEqual(got, w3cTraceContextSchema()) {
		t.Fatalf("W3cTraceContext schema = %#v, want %#v", got, w3cTraceContextSchema())
	}
}

func TestW3cTraceContextPreservesSerdeWireForms(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`{}`, `{}`},
		{`{"traceparent":"00-abc","tracestate":"vendor=value"}`, `{"traceparent":"00-abc","tracestate":"vendor=value"}`},
		{`{"future":true,"traceparent":null,"tracestate":"vendor=value"}`, `{"tracestate":"vendor=value"}`},
	} {
		var context W3cTraceContext
		if err := json.Unmarshal([]byte(tc.input), &context); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(context)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
}

func TestW3cTraceContextRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`,
		`{"traceparent":1}`, `{"tracestate":false}`,
		`{"traceparent":"one","traceparent":"two"}`,
		`{"traceparent":"one"} {}`,
	} {
		assertJSONRejects[W3cTraceContext](t, input)
	}
}

func TestW3cTraceContextRemainsStandalone(t *testing.T) {
	var context *W3cTraceContext
	if err := context.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil W3C trace context receiver succeeded")
	}

	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "W3cTraceContext") || slices.Contains(binding.Result, "W3cTraceContext") {
			t.Fatalf("W3cTraceContext unexpectedly bound to %s", binding.Method)
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestW3cTraceContextTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := "export type W3cTraceContext = {\n" +
		"  \"traceparent\"?: string;\n" +
		"  \"tracestate\"?: string;\n" +
		"};"
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}
