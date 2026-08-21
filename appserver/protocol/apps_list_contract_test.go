package protocol

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const (
	appsListMinimalInfoWire   = `{"id":"id","name":"name"}`
	appsListCanonicalInfoWire = `{"appMetadata":null,"branding":null,"description":null,"distributionChannel":null,` +
		`"iconAssets":null,"iconDarkAssets":null,"id":"id","installUrl":null,"isAccessible":false,` +
		`"isEnabled":true,"labels":null,"logoUrl":null,"logoUrlDark":null,"name":"name","pluginDisplayNames":[]}`
)

func TestAppsListSchemasAreExact(t *testing.T) {
	wantParams := Schema{
		"type":        "object",
		"description": "EXPERIMENTAL - list available apps/connectors.",
		"properties": Schema{
			"cursor": Schema{
				"description": "Opaque pagination cursor returned by a previous call.",
				"type":        []any{"string", "null"},
			},
			"forceRefetch": Schema{
				"description": "When true, bypass app caches and fetch the latest data from sources.",
				"type":        "boolean",
			},
			"limit": Schema{
				"description": "Optional page size; defaults to a reasonable server-side value.",
				"format":      "uint32", "minimum": float64(0), "type": []any{"integer", "null"},
			},
			"threadId": Schema{
				"description": "Optional thread id used to evaluate app feature gating from that thread's config.",
				"type":        []any{"string", "null"},
			},
		},
	}
	wantResponse := Schema{
		"type":        "object",
		"description": "EXPERIMENTAL - app list response.",
		"properties": Schema{
			"data": Schema{"type": "array", "items": Schema{"$ref": "#/$defs/AppInfo"}},
			"nextCursor": Schema{
				"description": "Opaque cursor to pass to the next call to continue after the last item. If None, there are no more items to return.",
				"type":        []any{"string", "null"},
			},
		},
		"required": []string{"data"},
	}
	definitions := JSONSchema()["$defs"].(Schema)
	for name, want := range map[string]Schema{
		"AppsListParams":   wantParams,
		"AppsListResponse": wantResponse,
	} {
		if got := definitions[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestAppsListParamsAcceptsSerdeWireForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{`{}`, `{"cursor":null,"limit":null,"threadId":null}`},
		{`{"cursor":null,"limit":null,"threadId":null}`, `{"cursor":null,"limit":null,"threadId":null}`},
		{`{"cursor":"","limit":0,"threadId":" ","forceRefetch":false}`, `{"cursor":"","limit":0,"threadId":" "}`},
		{`{"cursor":" next ","limit":4294967295,"threadId":"thread","forceRefetch":true}`, `{"cursor":" next ","limit":4294967295,"threadId":"thread","forceRefetch":true}`},
		{`{"future":1,"future":2,"forceRefetch":true}`, `{"cursor":null,"limit":null,"threadId":null,"forceRefetch":true}`},
	} {
		var value AppsListParams
		if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
	limit := uint32(math.MaxUint32)
	if encoded, err := json.Marshal(AppsListParams{Limit: &limit}); err != nil ||
		string(encoded) != `{"cursor":null,"limit":4294967295,"threadId":null}` {
		t.Fatalf("max limit = %s, %v", encoded, err)
	}
}

func TestAppsListParamsRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`,
		`{"cursor":1}`, `{"limit":-1}`, `{"limit":1.5}`, `{"limit":4294967296}`,
		`{"limit":"1"}`, `{"threadId":false}`, `{"forceRefetch":null}`,
		`{"forceRefetch":0}`, `{"cursor":null,"cursor":"next"}`,
		`{"forceRefetch":false,"forceRefetch":true}`, `{} {}`, `{} x`,
	} {
		assertJSONRejects[AppsListParams](t, input)
	}
}

func TestAppsListResponseAcceptsSerdeWireForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{`{"data":[]}`, `{"data":[],"nextCursor":null}`},
		{`{"future":true,"data":[],"nextCursor":null}`, `{"data":[],"nextCursor":null}`},
		{`{"data":[` + appsListMinimalInfoWire + `],"nextCursor":""}`, `{"data":[` + appsListCanonicalInfoWire + `],"nextCursor":""}`},
		{`{"data":[` + appsListMinimalInfoWire + `,` + appsListMinimalInfoWire + `],"nextCursor":" next "}`, `{"data":[` + appsListCanonicalInfoWire + `,` + appsListCanonicalInfoWire + `],"nextCursor":" next "}`},
	} {
		var value AppsListResponse
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

func TestAppsListResponseRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"data":null}`, `{"data":{}}`, `{"data":[null]}`, `{"data":[{}]}`,
		`{"data":[{"id":"id"}]}`, `{"data":[{"name":"name"}]}`,
		`{"data":[],"nextCursor":1}`, `{"data":[],"data":[]}`,
		`{"data":[],"nextCursor":null,"nextCursor":"next"}`, `{"data":[]} {}`, `{"data":[]} x`,
	} {
		assertJSONRejects[AppsListResponse](t, input)
	}
}

func TestAppsListNilReceiversAndInvalidMarshal(t *testing.T) {
	var params *AppsListParams
	if err := params.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil AppsListParams receiver succeeded")
	}
	var response *AppsListResponse
	if err := response.UnmarshalJSON([]byte(`{"data":[]}`)); err == nil {
		t.Fatal("nil AppsListResponse receiver succeeded")
	}
	if _, err := json.Marshal(AppsListResponse{}); err == nil {
		t.Fatal("nil response data marshaled")
	}
}

func TestAppsListContractsRemainStandaloneAndDeferred(t *testing.T) {
	names := []string{"AppsListParams", "AppsListResponse"}
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

func TestAppsListTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		"export type AppsListParams = {\n",
		`  "cursor"?: string | null;`,
		`  "forceRefetch"?: boolean;`,
		`  "limit"?: number | null;`,
		`  "threadId"?: string | null;`,
		"export type AppsListResponse = {\n",
		`  "data": Array<AppInfo>;`,
		`  "nextCursor": string | null;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

var (
	_ json.Marshaler   = AppsListParams{}
	_ json.Unmarshaler = (*AppsListParams)(nil)
	_ json.Marshaler   = AppsListResponse{}
	_ json.Unmarshaler = (*AppsListResponse)(nil)
)
