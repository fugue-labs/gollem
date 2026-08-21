package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestAppsDiscoveryParamSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	threadID := Schema{
		"description": "Optional loaded thread id used to evaluate effective app configuration.",
		"type":        []any{"string", "null"},
	}
	want := map[string]Schema{
		"AppsInstalledParams": {
			"description": "Read the committed installed connector runtime snapshot.",
			"properties": Schema{
				"forceRefresh": Schema{
					"description": "When true and Apps are permitted, refresh and publish the hosted connector runtime tool snapshot first.",
					"type":        "boolean",
				},
				"threadId": threadID,
			},
			"type": "object",
		},
		"AppsReadParams": {
			"description": "EXPERIMENTAL - read metadata for specific apps/connectors.",
			"properties": Schema{
				"appIds": Schema{
					"description": "App ids to read. The server accepts at most 100 ids and deduplicates repeated ids while preserving their first-request order.",
					"items":       Schema{"type": "string"},
					"type":        "array",
				},
				"includeTools": Schema{
					"description": "When true, include display-only public tool summaries in the returned metadata.",
					"type":        "boolean",
				},
				"threadId": threadID,
			},
			"required": []string{"appIds"},
			"type":     "object",
		},
	}
	for name, expected := range want {
		if got := defs[name]; !reflect.DeepEqual(got, expected) {
			t.Errorf("%s = %#v, want %#v", name, got, expected)
		}
	}
}

func TestAppsDiscoveryParamsPreserveSerdeWireForms(t *testing.T) {
	assertAppsDiscoveryRoundTrip[AppsInstalledParams](t, `{"future":true}`, `{"threadId":null}`)
	assertAppsDiscoveryRoundTrip[AppsInstalledParams](t, `{"threadId":"thread","forceRefresh":true,"future":true}`, `{"threadId":"thread","forceRefresh":true}`)
	assertAppsDiscoveryRoundTrip[AppsReadParams](t, `{"appIds":[],"future":true}`, `{"appIds":[],"threadId":null}`)
	assertAppsDiscoveryRoundTrip[AppsReadParams](t, `{"appIds":["one","one","two"],"threadId":null,"includeTools":true,"future":true}`, `{"appIds":["one","one","two"],"threadId":null,"includeTools":true}`)
}

func TestAppsDiscoveryParamsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`,
		`{"threadId":1}`, `{"forceRefresh":null}`, `{"forceRefresh":"true"}`,
		`{"threadId":"thread","threadId":"duplicate"}`, `{} {}`,
	} {
		assertJSONRejects[AppsInstalledParams](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`, `{"appIds":null}`, `{"appIds":"one"}`,
		`{"appIds":[1]}`, `{"appIds":[],"threadId":1}`, `{"appIds":[],"includeTools":null}`,
		`{"appIds":[],"includeTools":1}`, `{"appIds":[],"appIds":[]}`, `{"appIds":[]} {}`,
	} {
		assertJSONRejects[AppsReadParams](t, input)
	}
}

func TestAppsDiscoveryParamsRemainStandalone(t *testing.T) {
	for _, value := range []json.Unmarshaler{(*AppsInstalledParams)(nil), (*AppsReadParams)(nil)} {
		if err := value.UnmarshalJSON([]byte(`{}`)); err == nil {
			t.Errorf("nil %T receiver succeeded", value)
		}
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			if name == "AppsInstalledParams" || name == "AppsReadParams" {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestAppsDiscoveryParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`export type AppsInstalledParams = { threadId?: string | null, forceRefresh?: boolean, };`,
		`export type AppsReadParams = { appIds: Array<string>, threadId?: string | null, includeTools?: boolean, };`,
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func assertAppsDiscoveryRoundTrip[T any](t *testing.T, input, want string) {
	t.Helper()
	var value T
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %s = %s, %v; want %s", input, encoded, err, want)
	}
}
