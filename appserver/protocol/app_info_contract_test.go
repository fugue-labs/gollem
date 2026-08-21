package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestAppInfoSchemaIsExact(t *testing.T) {
	want := closedThreadSessionParamSchema(Schema{
		"id":                  Schema{"type": "string"},
		"name":                Schema{"type": "string"},
		"description":         nullableStringSchema(),
		"logoUrl":             nullableStringSchema(),
		"logoUrlDark":         nullableStringSchema(),
		"iconAssets":          nullableAppInfoStringMapSchema(),
		"iconDarkAssets":      nullableAppInfoStringMapSchema(),
		"distributionChannel": nullableStringSchema(),
		"branding":            nullableAppMetadataRefSchema("AppBranding"),
		"appMetadata":         nullableAppMetadataRefSchema("AppMetadata"),
		"labels":              nullableAppInfoStringMapSchema(),
		"installUrl":          nullableStringSchema(),
		"isAccessible":        Schema{"type": "boolean", "default": false},
		"isEnabled": Schema{
			"type": "boolean", "default": true,
			"description": "Whether this app is enabled in config.toml. Example: ```toml [apps.bad_app] enabled = false ```",
		},
		"pluginDisplayNames": Schema{
			"type": "array", "items": Schema{"type": "string"}, "default": []any{},
		},
	}, []string{"id", "name"})
	want["description"] = "EXPERIMENTAL - app metadata returned by app-list APIs."
	got := JSONSchema()["$defs"].(Schema)["AppInfo"]
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AppInfo = %#v, want %#v", got, want)
	}
}

func TestAppInfoAcceptsSerdeWireForms(t *testing.T) {
	const defaults = `{"appMetadata":null,"branding":null,"description":null,"distributionChannel":null,"iconAssets":null,"iconDarkAssets":null,"id":"id","installUrl":null,"isAccessible":false,"isEnabled":true,"labels":null,"logoUrl":null,"logoUrlDark":null,"name":"name","pluginDisplayNames":[]}`
	for _, tc := range []struct {
		input string
		want  string
	}{
		{`{"id":"id","name":"name"}`, defaults},
		{
			`{"future":{"ignored":true},"id":" id ","name":"","description":" description ","logoUrl":"not a url","logoUrlDark":"","iconAssets":{"":""," icon ":" value "},"iconDarkAssets":{},"distributionChannel":" arbitrary ","branding":{"category":" category ","developer":"","website":"site","privacyPolicy":"privacy","termsOfService":"terms","isDiscoverableApp":true},"appMetadata":{"review":{"status":" review "},"categories":["", " category ", " category "],"subCategories":[],"seoDescription":" seo ","screenshots":[],"developer":"dev","version":"v","versionId":"vid","versionNotes":"notes","firstPartyType":"first","firstPartyRequiresInstall":false,"showInComposerWhenUnlinked":true},"labels":{"":""," label ":" value "},"installUrl":" install ","isAccessible":true,"isEnabled":false,"pluginDisplayNames":["", " plugin ", " plugin "]}`,
			`{"appMetadata":{"categories":[""," category "," category "],"developer":"dev","firstPartyRequiresInstall":false,"firstPartyType":"first","review":{"status":" review "},"screenshots":[],"seoDescription":" seo ","showInComposerWhenUnlinked":true,"subCategories":[],"version":"v","versionId":"vid","versionNotes":"notes"},"branding":{"category":" category ","developer":"","isDiscoverableApp":true,"privacyPolicy":"privacy","termsOfService":"terms","website":"site"},"description":" description ","distributionChannel":" arbitrary ","iconAssets":{"":""," icon ":" value "},"iconDarkAssets":{},"id":" id ","installUrl":" install ","isAccessible":true,"isEnabled":false,"labels":{"":""," label ":" value "},"logoUrl":"not a url","logoUrlDark":"","name":"","pluginDisplayNames":[""," plugin "," plugin "]}`,
		},
	} {
		var value AppInfo
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

func TestAppInfoRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"name":"name"}`, `{"id":"id"}`, `{"id":null,"name":"name"}`,
		`{"id":"id","name":null}`, `{"id":1,"name":"name"}`,
		`{"id":"id","name":false}`, `{"id":"id","name":"name","description":1}`,
		`{"id":"id","name":"name","logoUrl":[]}`,
		`{"id":"id","name":"name","logoUrlDark":{}}`,
		`{"id":"id","name":"name","iconAssets":[]}`,
		`{"id":"id","name":"name","iconAssets":{"key":null}}`,
		`{"id":"id","name":"name","iconAssets":{"key":1}}`,
		`{"id":"id","name":"name","iconDarkAssets":{"key":false}}`,
		`{"id":"id","name":"name","distributionChannel":1}`,
		`{"id":"id","name":"name","branding":{}}`,
		`{"id":"id","name":"name","branding":{"isDiscoverableApp":null}}`,
		`{"id":"id","name":"name","appMetadata":[]}`,
		`{"id":"id","name":"name","appMetadata":{"categories":[null]}}`,
		`{"id":"id","name":"name","labels":{"key":null}}`,
		`{"id":"id","name":"name","installUrl":false}`,
		`{"id":"id","name":"name","isAccessible":null}`,
		`{"id":"id","name":"name","isAccessible":"true"}`,
		`{"id":"id","name":"name","isEnabled":null}`,
		`{"id":"id","name":"name","isEnabled":0}`,
		`{"id":"id","name":"name","pluginDisplayNames":null}`,
		`{"id":"id","name":"name","pluginDisplayNames":{}}`,
		`{"id":"id","name":"name","pluginDisplayNames":[null]}`,
		`{"id":"id","name":"name","pluginDisplayNames":[1]}`,
		`{"id":"id","id":"other","name":"name"}`,
		`{"id":"id","name":"name","name":"other"}`,
		`{"id":"id","name":"name","labels":null,"labels":{}}`,
		`{"id":"id","name":"name"} {}`, `{"id":"id","name":"name"} x`,
	} {
		assertJSONRejects[AppInfo](t, input)
	}
}

func TestAppInfoNilReceiverFailsClosed(t *testing.T) {
	var info *AppInfo
	if err := info.UnmarshalJSON([]byte(`{"id":"id","name":"name"}`)); err == nil {
		t.Fatal("nil AppInfo receiver succeeded")
	}
}

func TestAppInfoMarshalCanonicalizesNilPluginDisplayNames(t *testing.T) {
	encoded, err := json.Marshal(AppInfo{ID: "id", Name: "name", IsEnabled: true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"appMetadata":null,"branding":null,"description":null,"distributionChannel":null,"iconAssets":null,"iconDarkAssets":null,"id":"id","installUrl":null,"isAccessible":false,"isEnabled":true,"labels":null,"logoUrl":null,"logoUrlDark":null,"name":"name","pluginDisplayNames":[]}`
	if string(encoded) != want {
		t.Fatalf("marshal = %s, want %s", encoded, want)
	}
}

func TestAppInfoRemainsStandaloneAndDeferred(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "AppInfo") || slices.Contains(binding.Result, "AppInfo") {
			t.Fatalf("AppInfo unexpectedly bound to %s", binding.Method)
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if binding.Type == "AppInfo" {
			t.Fatalf("AppInfo unexpectedly bound to item %s", binding.Kind)
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

func TestAppInfoTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := "export type AppInfo = {\n" +
		"  \"appMetadata\": AppMetadata | null;\n" +
		"  \"branding\": AppBranding | null;\n" +
		"  \"description\": string | null;\n" +
		"  \"distributionChannel\": string | null;\n" +
		"  \"iconAssets\": { [key in string]?: string } | null;\n" +
		"  \"iconDarkAssets\": { [key in string]?: string } | null;\n" +
		"  \"id\": string;\n" +
		"  \"installUrl\": string | null;\n" +
		"  \"isAccessible\": boolean;\n" +
		"  \"isEnabled\": boolean;\n" +
		"  \"labels\": { [key in string]?: string } | null;\n" +
		"  \"logoUrl\": string | null;\n" +
		"  \"logoUrlDark\": string | null;\n" +
		"  \"name\": string;\n" +
		"  \"pluginDisplayNames\": Array<string>;\n" +
		"};"
	if !strings.Contains(string(generated), want) {
		t.Fatalf("generated TypeScript missing %q", want)
	}
}
