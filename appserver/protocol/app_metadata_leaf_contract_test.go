package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestAppMetadataLeafSchemasAreExact(t *testing.T) {
	wants := map[string]Schema{
		"AppBranding": closedThreadSessionParamSchema(Schema{
			"category":          nullableStringSchema(),
			"developer":         nullableStringSchema(),
			"website":           nullableStringSchema(),
			"privacyPolicy":     nullableStringSchema(),
			"termsOfService":    nullableStringSchema(),
			"isDiscoverableApp": Schema{"type": "boolean"},
		}, []string{"isDiscoverableApp"}),
		"AppReview": closedThreadSessionParamSchema(Schema{
			"status": Schema{"type": "string"},
		}, []string{"status"}),
		"AppScreenshot": closedThreadSessionParamSchema(Schema{
			"url":        nullableStringSchema(),
			"fileId":     nullableStringSchema(),
			"userPrompt": Schema{"type": "string"},
		}, []string{"userPrompt"}),
	}
	defs := JSONSchema()["$defs"].(Schema)
	for name, want := range wants {
		if got := defs[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestAppMetadataLeavesAcceptSerdeWireForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{`{"isDiscoverableApp":false}`, `{"category":null,"developer":null,"isDiscoverableApp":false,"privacyPolicy":null,"termsOfService":null,"website":null}`},
		{`{"category":null,"developer":null,"website":null,"privacyPolicy":null,"termsOfService":null,"isDiscoverableApp":true}`, `{"category":null,"developer":null,"isDiscoverableApp":true,"privacyPolicy":null,"termsOfService":null,"website":null}`},
		{`{"future":true,"category":" category ","developer":"","website":"not a url","privacyPolicy":" privacy ","termsOfService":" terms ","isDiscoverableApp":true}`, `{"category":" category ","developer":"","isDiscoverableApp":true,"privacyPolicy":" privacy ","termsOfService":" terms ","website":"not a url"}`},
	} {
		var value AppBranding
		if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Errorf("unmarshal AppBranding %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("AppBranding round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}

	for _, input := range []string{`{"status":""}`, `{"future":true,"status":" arbitrary review state "}`} {
		var value AppReview
		if err := json.Unmarshal([]byte(input), &value); err != nil {
			t.Errorf("unmarshal AppReview %s: %v", input, err)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Errorf("marshal AppReview %s: %v", input, err)
		}
		var canonical map[string]string
		if err := json.Unmarshal(encoded, &canonical); err != nil || canonical["status"] != value.Status {
			t.Errorf("AppReview round trip %s = %s, %v", input, encoded, err)
		}
	}

	for _, tc := range []struct {
		input string
		want  string
	}{
		{`{"userPrompt":""}`, `{"fileId":null,"url":null,"userPrompt":""}`},
		{`{"url":null,"fileId":null,"userPrompt":" prompt "}`, `{"fileId":null,"url":null,"userPrompt":" prompt "}`},
		{`{"future":true,"url":"not a url","fileId":" file ","userPrompt":" arbitrary "}`, `{"fileId":" file ","url":"not a url","userPrompt":" arbitrary "}`},
	} {
		var value AppScreenshot
		if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Errorf("unmarshal AppScreenshot %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("AppScreenshot round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
}

func TestAppMetadataLeavesRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"isDiscoverableApp":null}`, `{"isDiscoverableApp":"true"}`,
		`{"category":1,"isDiscoverableApp":true}`,
		`{"developer":false,"isDiscoverableApp":true}`,
		`{"website":{},"isDiscoverableApp":true}`,
		`{"privacyPolicy":[],"isDiscoverableApp":true}`,
		`{"termsOfService":1,"isDiscoverableApp":true}`,
		`{"isDiscoverableApp":true,"isDiscoverableApp":false}`,
		`{"isDiscoverableApp":true} {}`, `{"isDiscoverableApp":true} x`,
	} {
		assertJSONRejects[AppBranding](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"status":null}`, `{"status":1}`, `{"status":false}`,
		`{"status":"ok","status":"other"}`, `{"status":"ok"} {}`, `{"status":"ok"} x`,
	} {
		assertJSONRejects[AppReview](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"userPrompt":null}`, `{"userPrompt":1}`,
		`{"url":1,"userPrompt":"x"}`, `{"fileId":false,"userPrompt":"x"}`,
		`{"userPrompt":"x","userPrompt":"y"}`,
		`{"url":null,"url":"x","userPrompt":"y"}`,
		`{"userPrompt":"x"} {}`, `{"userPrompt":"x"} x`,
	} {
		assertJSONRejects[AppScreenshot](t, input)
	}
}

func TestAppMetadataLeavesNilReceiversFailClosed(t *testing.T) {
	var branding *AppBranding
	if err := branding.UnmarshalJSON([]byte(`{"isDiscoverableApp":true}`)); err == nil {
		t.Fatal("nil AppBranding receiver succeeded")
	}
	var review *AppReview
	if err := review.UnmarshalJSON([]byte(`{"status":"ok"}`)); err == nil {
		t.Fatal("nil AppReview receiver succeeded")
	}
	var screenshot *AppScreenshot
	if err := screenshot.UnmarshalJSON([]byte(`{"userPrompt":"x"}`)); err == nil {
		t.Fatal("nil AppScreenshot receiver succeeded")
	}
}

func TestAppMetadataLeavesRemainStandaloneAndDeferred(t *testing.T) {
	names := []string{"AppBranding", "AppReview", "AppScreenshot"}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if slices.Contains(names, binding.Type) {
			t.Fatalf("%s unexpectedly bound to item %s", binding.Type, binding.Kind)
		}
	}
	method, ok := LookupMethod("app/list")
	if !ok || method.Surface != SurfaceClientRequest || method.State != MethodDeferredStub {
		t.Fatalf("app/list = %#v, %v; want deferred client request", method, ok)
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

func TestAppMetadataLeavesTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		"export type AppBranding = {\n  \"category\": string | null;\n  \"developer\": string | null;\n  \"isDiscoverableApp\": boolean;\n  \"privacyPolicy\": string | null;\n  \"termsOfService\": string | null;\n  \"website\": string | null;\n};",
		"export type AppReview = {\n  \"status\": string;\n};",
		"export type AppScreenshot = {\n  \"fileId\": string | null;\n  \"url\": string | null;\n  \"userPrompt\": string;\n};",
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}
