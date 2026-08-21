package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestAppMetadataSchemaIsExact(t *testing.T) {
	want := closedThreadSessionParamSchema(Schema{
		"review": nullableAppMetadataRefSchema("AppReview"),
		"categories": Schema{"anyOf": []any{
			Schema{"type": "array", "items": Schema{"type": "string"}}, Schema{"type": "null"},
		}},
		"subCategories": Schema{"anyOf": []any{
			Schema{"type": "array", "items": Schema{"type": "string"}}, Schema{"type": "null"},
		}},
		"seoDescription": nullableStringSchema(),
		"screenshots": Schema{"anyOf": []any{
			Schema{"type": "array", "items": Schema{"$ref": "#/$defs/AppScreenshot"}},
			Schema{"type": "null"},
		}},
		"developer":      nullableStringSchema(),
		"version":        nullableStringSchema(),
		"versionId":      nullableStringSchema(),
		"versionNotes":   nullableStringSchema(),
		"firstPartyType": nullableStringSchema(),
		"firstPartyRequiresInstall": Schema{"anyOf": []any{
			Schema{"type": "boolean"}, Schema{"type": "null"},
		}},
		"showInComposerWhenUnlinked": Schema{"anyOf": []any{
			Schema{"type": "boolean"}, Schema{"type": "null"},
		}},
	}, nil)
	got := JSONSchema()["$defs"].(Schema)["AppMetadata"]
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AppMetadata = %#v, want %#v", got, want)
	}
}

func TestAppMetadataAcceptsSerdeWireForms(t *testing.T) {
	const nullCanonical = `{"categories":null,"developer":null,"firstPartyRequiresInstall":null,"firstPartyType":null,"review":null,"screenshots":null,"seoDescription":null,"showInComposerWhenUnlinked":null,"subCategories":null,"version":null,"versionId":null,"versionNotes":null}`
	for _, tc := range []struct {
		input string
		want  string
	}{
		{`{}`, nullCanonical},
		{`{"review":null,"categories":null,"subCategories":null,"seoDescription":null,"screenshots":null,"developer":null,"version":null,"versionId":null,"versionNotes":null,"firstPartyType":null,"firstPartyRequiresInstall":null,"showInComposerWhenUnlinked":null}`, nullCanonical},
		{
			`{"future":{"ignored":true},"review":{"future":1,"status":" arbitrary review "},"categories":["", " category ", " category "],"subCategories":[],"seoDescription":" seo ","screenshots":[{"future":true,"url":"not a url","fileId":" file ","userPrompt":" prompt "},{"userPrompt":""}],"developer":"","version":" v ","versionId":"id","versionNotes":" notes ","firstPartyType":" arbitrary ","firstPartyRequiresInstall":false,"showInComposerWhenUnlinked":true}`,
			`{"categories":[""," category "," category "],"developer":"","firstPartyRequiresInstall":false,"firstPartyType":" arbitrary ","review":{"status":" arbitrary review "},"screenshots":[{"fileId":" file ","url":"not a url","userPrompt":" prompt "},{"fileId":null,"url":null,"userPrompt":""}],"seoDescription":" seo ","showInComposerWhenUnlinked":true,"subCategories":[],"version":" v ","versionId":"id","versionNotes":" notes "}`,
		},
	} {
		var value AppMetadata
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

func TestAppMetadataRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`,
		`{"review":"approved"}`, `{"review":{}}`, `{"review":{"status":null}}`,
		`{"categories":"category"}`, `{"categories":[null]}`, `{"categories":[1]}`,
		`{"subCategories":{}}`, `{"subCategories":[false]}`,
		`{"seoDescription":1}`, `{"screenshots":{}}`, `{"screenshots":[null]}`,
		`{"screenshots":[{}]}`, `{"screenshots":[{"userPrompt":1}]}`,
		`{"developer":false}`, `{"version":[]}`, `{"versionId":1}`,
		`{"versionNotes":{}}`, `{"firstPartyType":true}`,
		`{"firstPartyRequiresInstall":"false"}`, `{"showInComposerWhenUnlinked":0}`,
		`{"categories":null,"categories":[]}`,
		`{"review":null,"review":{"status":"ok"}}`,
		`{"screenshots":null,"screenshots":[]}`,
		`{} {}`, `{} x`,
	} {
		assertJSONRejects[AppMetadata](t, input)
	}
}

func TestAppMetadataNilReceiverFailsClosed(t *testing.T) {
	var metadata *AppMetadata
	if err := metadata.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil AppMetadata receiver succeeded")
	}
}

func TestAppMetadataRemainsStandaloneAndDeferred(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "AppMetadata") || slices.Contains(binding.Result, "AppMetadata") {
			t.Fatalf("AppMetadata unexpectedly bound to %s", binding.Method)
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if binding.Type == "AppMetadata" {
			t.Fatalf("AppMetadata unexpectedly bound to item %s", binding.Kind)
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
	if got := len(WireTypeBindings()); got != 86 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 86/5", got, len(ItemPayloadBindings()))
	}
}

func TestAppMetadataTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := "export type AppMetadata = {\n" +
		"  \"categories\": Array<string> | null;\n" +
		"  \"developer\": string | null;\n" +
		"  \"firstPartyRequiresInstall\": boolean | null;\n" +
		"  \"firstPartyType\": string | null;\n" +
		"  \"review\": AppReview | null;\n" +
		"  \"screenshots\": Array<AppScreenshot> | null;\n" +
		"  \"seoDescription\": string | null;\n" +
		"  \"showInComposerWhenUnlinked\": boolean | null;\n" +
		"  \"subCategories\": Array<string> | null;\n" +
		"  \"version\": string | null;\n" +
		"  \"versionId\": string | null;\n" +
		"  \"versionNotes\": string | null;\n" +
		"};"
	if !strings.Contains(string(generated), want) {
		t.Fatalf("generated TypeScript missing %q", want)
	}
}

func nullableAppMetadataRefSchema(name string) Schema {
	return Schema{"anyOf": []any{
		Schema{"$ref": "#/$defs/" + name},
		Schema{"type": "null"},
	}}
}
