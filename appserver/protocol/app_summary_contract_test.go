package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestAppSummarySchemaIsExact(t *testing.T) {
	want := closedThreadSessionParamSchema(Schema{
		"id":          Schema{"type": "string"},
		"name":        Schema{"type": "string"},
		"description": nullableStringSchema(),
		"installUrl":  nullableStringSchema(),
		"category":    nullableStringSchema(),
	}, []string{"id", "name"})
	want["description"] = "EXPERIMENTAL - app metadata summary for plugin responses."
	got := JSONSchema()["$defs"].(Schema)["AppSummary"]
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AppSummary = %#v, want %#v", got, want)
	}
}

func TestAppSummaryAcceptsSerdeWireForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{
			`{"id":"id","name":"name"}`,
			`{"category":null,"description":null,"id":"id","installUrl":null,"name":"name"}`,
		},
		{
			`{"future":true,"id":" id ","name":"","description":" description ","installUrl":"not a url","category":" category "}`,
			`{"category":" category ","description":" description ","id":" id ","installUrl":"not a url","name":""}`,
		},
		{
			`{"id":"","name":" name ","description":null,"installUrl":"","category":null}`,
			`{"category":null,"description":null,"id":"","installUrl":"","name":" name "}`,
		},
	} {
		var value AppSummary
		if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
}

func TestAppSummaryRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"name":"name"}`, `{"id":"id"}`, `{"id":null,"name":"name"}`,
		`{"id":"id","name":null}`, `{"id":1,"name":"name"}`,
		`{"id":"id","name":false}`, `{"id":"id","name":"name","description":1}`,
		`{"id":"id","name":"name","installUrl":[]}`,
		`{"id":"id","name":"name","category":false}`,
		`{"id":"id","id":"other","name":"name"}`,
		`{"id":"id","name":"name","category":null,"category":"other"}`,
		`{"id":"id","name":"name"} {}`, `{"id":"id","name":"name"} x`,
	} {
		assertJSONRejects[AppSummary](t, input)
	}
}

func TestAppSummaryNilReceiverFailsClosed(t *testing.T) {
	var summary *AppSummary
	if err := summary.UnmarshalJSON([]byte(`{"id":"id","name":"name"}`)); err == nil {
		t.Fatal("nil AppSummary receiver succeeded")
	}
}

func TestAppSummaryRemainsStandaloneAndUnbound(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "AppSummary") || slices.Contains(binding.Result, "AppSummary") {
			t.Fatalf("AppSummary unexpectedly bound to %s", binding.Method)
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if binding.Type == "AppSummary" {
			t.Fatalf("AppSummary unexpectedly bound to item %s", binding.Kind)
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

func TestAppSummaryTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := "export type AppSummary = {\n" +
		"  \"category\": string | null;\n" +
		"  \"description\": string | null;\n" +
		"  \"id\": string;\n" +
		"  \"installUrl\": string | null;\n" +
		"  \"name\": string;\n" +
		"};"
	if !strings.Contains(string(generated), want) {
		t.Fatalf("generated TypeScript missing %q", want)
	}
}
