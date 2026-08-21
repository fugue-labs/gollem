package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestAppTemplateSummarySchemaIsExact(t *testing.T) {
	want := closedThreadSessionParamSchema(Schema{
		"templateId":           Schema{"type": "string"},
		"name":                 Schema{"type": "string"},
		"description":          nullableStringSchema(),
		"category":             nullableStringSchema(),
		"canonicalConnectorId": nullableStringSchema(),
		"logoUrl":              nullableStringSchema(),
		"logoUrlDark":          nullableStringSchema(),
		"materializedAppIds":   Schema{"type": "array", "items": Schema{"type": "string"}},
		"reason":               nullableSchemaRef("AppTemplateUnavailableReason"),
	}, []string{"templateId", "name", "materializedAppIds"})
	got := JSONSchema()["$defs"].(Schema)["AppTemplateSummary"]
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AppTemplateSummary = %#v, want %#v", got, want)
	}
}

func TestAppTemplateSummaryAcceptsSerdeWireForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{
			`{"templateId":"template","name":"name","materializedAppIds":[]}`,
			`{"canonicalConnectorId":null,"category":null,"description":null,"logoUrl":null,"logoUrlDark":null,"materializedAppIds":[],"name":"name","reason":null,"templateId":"template"}`,
		},
		{
			`{"future":{"ignored":true},"templateId":" template ","name":"","description":" description ","category":" category ","canonicalConnectorId":" connector ","logoUrl":"not a url","logoUrlDark":"","materializedAppIds":["app-2","","app-2"," app-1 "],"reason":"NO_ACTIVE_WORKSPACE"}`,
			`{"canonicalConnectorId":" connector ","category":" category ","description":" description ","logoUrl":"not a url","logoUrlDark":"","materializedAppIds":["app-2","","app-2"," app-1 "],"name":"","reason":"NO_ACTIVE_WORKSPACE","templateId":" template "}`,
		},
		{
			`{"templateId":"","name":" name ","description":null,"category":null,"canonicalConnectorId":null,"logoUrl":null,"logoUrlDark":null,"materializedAppIds":["opaque"],"reason":null}`,
			`{"canonicalConnectorId":null,"category":null,"description":null,"logoUrl":null,"logoUrlDark":null,"materializedAppIds":["opaque"],"name":" name ","reason":null,"templateId":""}`,
		},
	} {
		var value AppTemplateSummary
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

func TestAppTemplateSummaryRejectsMalformedWireForms(t *testing.T) {
	valid := `"templateId":"template","name":"name","materializedAppIds":[]`
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"name":"name","materializedAppIds":[]}`,
		`{"templateId":"template","materializedAppIds":[]}`,
		`{"templateId":"template","name":"name"}`,
		`{"templateId":null,"name":"name","materializedAppIds":[]}`,
		`{"templateId":"template","name":null,"materializedAppIds":[]}`,
		`{"templateId":"template","name":"name","materializedAppIds":null}`,
		`{"templateId":1,"name":"name","materializedAppIds":[]}`,
		`{"templateId":"template","name":false,"materializedAppIds":[]}`,
		`{"templateId":"template","name":"name","description":1,"materializedAppIds":[]}`,
		`{"templateId":"template","name":"name","category":false,"materializedAppIds":[]}`,
		`{"templateId":"template","name":"name","canonicalConnectorId":[],"materializedAppIds":[]}`,
		`{"templateId":"template","name":"name","logoUrl":{},"materializedAppIds":[]}`,
		`{"templateId":"template","name":"name","logoUrlDark":1,"materializedAppIds":[]}`,
		`{"templateId":"template","name":"name","materializedAppIds":"app"}`,
		`{"templateId":"template","name":"name","materializedAppIds":[null]}`,
		`{"templateId":"template","name":"name","materializedAppIds":[1]}`,
		`{"templateId":"template","name":"name","materializedAppIds":[],"reason":"other"}`,
		`{"templateId":"template","name":"name","materializedAppIds":[],"reason":{}}`,
		`{"templateId":"template","templateId":"other","name":"name","materializedAppIds":[]}`,
		`{"templateId":"template","name":"name","materializedAppIds":[],"reason":null,"reason":"NO_ACTIVE_WORKSPACE"}`,
		`{` + valid + `} {}`, `{` + valid + `} x`,
	} {
		assertJSONRejects[AppTemplateSummary](t, input)
	}
}

func TestAppTemplateSummaryNilReceiverAndNilMaterializedAppsFailClosed(t *testing.T) {
	var summary *AppTemplateSummary
	if err := summary.UnmarshalJSON([]byte(`{"templateId":"template","name":"name","materializedAppIds":[]}`)); err == nil {
		t.Fatal("nil AppTemplateSummary receiver succeeded")
	}
	if _, err := json.Marshal(AppTemplateSummary{TemplateID: "template", Name: "name"}); err == nil {
		t.Fatal("AppTemplateSummary with nil MaterializedAppIDs marshaled")
	}
}

func TestAppTemplateSummaryRemainsStandaloneAndUnbound(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "AppTemplateSummary") || slices.Contains(binding.Result, "AppTemplateSummary") {
			t.Fatalf("AppTemplateSummary unexpectedly bound to %s", binding.Method)
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if binding.Type == "AppTemplateSummary" {
			t.Fatalf("AppTemplateSummary unexpectedly bound to item %s", binding.Kind)
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

func TestAppTemplateSummaryTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := "export type AppTemplateSummary = {\n" +
		"  \"canonicalConnectorId\": string | null;\n" +
		"  \"category\": string | null;\n" +
		"  \"description\": string | null;\n" +
		"  \"logoUrl\": string | null;\n" +
		"  \"logoUrlDark\": string | null;\n" +
		"  \"materializedAppIds\": Array<string>;\n" +
		"  \"name\": string;\n" +
		"  \"reason\": AppTemplateUnavailableReason | null;\n" +
		"  \"templateId\": string;\n" +
		"};"
	if !strings.Contains(string(generated), want) {
		t.Fatalf("generated TypeScript missing %q", want)
	}
}
