package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestAppListUpdatedNotificationSchemaIsExact(t *testing.T) {
	want := closedThreadSessionParamSchema(Schema{
		"data": Schema{"type": "array", "items": Schema{"$ref": "#/$defs/AppInfo"}},
	}, []string{"data"})
	want["description"] = "EXPERIMENTAL - notification emitted when the app list changes."
	got := JSONSchema()["$defs"].(Schema)["AppListUpdatedNotification"]
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AppListUpdatedNotification = %#v, want %#v", got, want)
	}
}

func TestAppListUpdatedNotificationAcceptsSerdeWireForms(t *testing.T) {
	const minimalInfo = `{"id":"id","name":"name"}`
	const canonicalInfo = `{"appMetadata":null,"branding":null,"description":null,"distributionChannel":null,"iconAssets":null,"iconDarkAssets":null,"id":"id","installUrl":null,"isAccessible":false,"isEnabled":true,"labels":null,"logoUrl":null,"logoUrlDark":null,"name":"name","pluginDisplayNames":[]}`
	for _, tc := range []struct {
		input string
		want  string
	}{
		{`{"data":[]}`, `{"data":[]}`},
		{`{"future":true,"data":[` + minimalInfo + `]}`, `{"data":[` + canonicalInfo + `]}`},
		{`{"data":[` + minimalInfo + `,` + minimalInfo + `]}`, `{"data":[` + canonicalInfo + `,` + canonicalInfo + `]}`},
	} {
		var value AppListUpdatedNotification
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

func TestAppListUpdatedNotificationPreservesNestedOrderAndOpaqueValues(t *testing.T) {
	input := `{"data":[` +
		`{"id":" second ","name":"","pluginDisplayNames":[" x "," x "]},` +
		`{"id":"first","name":" name ","labels":{"":""," key ":" value "}}]}`
	var value AppListUpdatedNotification
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := []string{value.Data[0].ID, value.Data[1].ID}; !slices.Equal(got, []string{" second ", "first"}) {
		t.Fatalf("ids = %q", got)
	}
	if !slices.Equal(value.Data[0].PluginDisplayNames, []string{" x ", " x "}) {
		t.Fatalf("plugin display names = %q", value.Data[0].PluginDisplayNames)
	}
	if value.Data[1].Labels == nil || (*value.Data[1].Labels)[" key "] != " value " {
		t.Fatalf("labels = %#v", value.Data[1].Labels)
	}
}

func TestAppListUpdatedNotificationRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"future":true}`, `{"data":null}`, `{"data":{}}`, `{"data":"apps"}`,
		`{"data":[null]}`, `{"data":[1]}`, `{"data":[{}]}`,
		`{"data":[{"id":"id"}]}`, `{"data":[{"name":"name"}]}`,
		`{"data":[{"id":null,"name":"name"}]}`,
		`{"data":[{"id":"id","name":"name","isEnabled":null}]}`,
		`{"data":[{"id":"id","name":"name","pluginDisplayNames":[null]}]}`,
		`{"data":[],"data":[]}`, `{"data":[]} {}`, `{"data":[]} x`,
	} {
		assertJSONRejects[AppListUpdatedNotification](t, input)
	}
}

func TestAppListUpdatedNotificationNilReceiverFailsClosed(t *testing.T) {
	var notification *AppListUpdatedNotification
	if err := notification.UnmarshalJSON([]byte(`{"data":[]}`)); err == nil {
		t.Fatal("nil AppListUpdatedNotification receiver succeeded")
	}
}

func TestAppListUpdatedNotificationMarshalRejectsNilData(t *testing.T) {
	if _, err := json.Marshal(AppListUpdatedNotification{}); err == nil {
		t.Fatal("nil notification data marshaled")
	}
}

func TestAppListUpdatedNotificationRemainsStandaloneAndDeferred(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "AppListUpdatedNotification") ||
			slices.Contains(binding.Result, "AppListUpdatedNotification") {
			t.Fatalf("AppListUpdatedNotification unexpectedly bound to %s", binding.Method)
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if binding.Type == "AppListUpdatedNotification" {
			t.Fatalf("AppListUpdatedNotification unexpectedly bound to item %s", binding.Kind)
		}
	}
	method, ok := LookupMethod("app/list/updated")
	if !ok || method.Surface != SurfaceServerNotification || method.State != MethodDeferredStub {
		t.Fatalf("app/list/updated = %#v, %v; want deferred server notification", method, ok)
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

func TestAppListUpdatedNotificationTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := "export type AppListUpdatedNotification = {\n" +
		"  \"data\": Array<AppInfo>;\n" +
		"};"
	if !strings.Contains(string(generated), want) {
		t.Fatalf("generated TypeScript missing %q", want)
	}
}
