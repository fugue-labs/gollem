package protocol

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestMcpServerOauthLoginSchemasAreExact(t *testing.T) {
	nullableString := Schema{"type": []any{"string", "null"}}
	wants := map[string]Schema{
		"McpServerOauthLoginParams": {
			"type": "object",
			"properties": Schema{
				"name": Schema{"type": "string"},
				"scopes": Schema{
					"type":  []any{"array", "null"},
					"items": Schema{"type": "string"},
				},
				"threadId": nullableString,
				"timeoutSecs": Schema{
					"type": []any{"integer", "null"}, "format": "int64",
				},
			},
			"required": []string{"name"},
		},
		"McpServerOauthLoginResponse": {
			"type": "object",
			"properties": Schema{
				"authorizationUrl": Schema{"type": "string"},
			},
			"required": []string{"authorizationUrl"},
		},
		"McpServerOauthLoginCompletedNotification": {
			"type": "object",
			"properties": Schema{
				"error":    nullableString,
				"name":     Schema{"type": "string"},
				"success":  Schema{"type": "boolean"},
				"threadId": nullableString,
			},
			"required": []string{"name", "success"},
		},
	}
	definitions := JSONSchema()["$defs"].(Schema)
	for name, want := range wants {
		if got := definitions[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestMcpServerOauthLoginParamsPreserveSerdeWireForms(t *testing.T) {
	minimum, maximum := int64(math.MinInt64), int64(math.MaxInt64)
	for _, tc := range []struct {
		input string
		want  McpServerOauthLoginParams
		json  string
	}{
		{
			`{"name":""}`,
			McpServerOauthLoginParams{},
			`{"name":"","threadId":null}`,
		},
		{
			`{"name":"server","threadId":null,"scopes":null,"timeoutSecs":null}`,
			McpServerOauthLoginParams{Name: "server"},
			`{"name":"server","threadId":null}`,
		},
		{
			`{"future":1,"future":2,"name":" server ","threadId":" thread ","scopes":["","scope","scope"],"timeoutSecs":-9223372036854775808}`,
			McpServerOauthLoginParams{
				Name: " server ", ThreadID: stringPointer(" thread "),
				Scopes: &[]string{"", "scope", "scope"}, TimeoutSecs: &minimum,
			},
			`{"name":" server ","threadId":" thread ","scopes":["","scope","scope"],"timeoutSecs":-9223372036854775808}`,
		},
		{
			`{"name":"server","scopes":[],"timeoutSecs":9223372036854775807}`,
			McpServerOauthLoginParams{Name: "server", Scopes: &[]string{}, TimeoutSecs: &maximum},
			`{"name":"server","threadId":null,"scopes":[],"timeoutSecs":9223372036854775807}`,
		},
	} {
		var params McpServerOauthLoginParams
		if err := json.Unmarshal([]byte(tc.input), &params); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		if !reflect.DeepEqual(params, tc.want) {
			t.Errorf("params %s = %#v, want %#v", tc.input, params, tc.want)
		}
		encoded, err := json.Marshal(params)
		if err != nil || string(encoded) != tc.json {
			t.Errorf("marshal %#v = %s, %v; want %s", params, encoded, err, tc.json)
		}
	}

	var nilScopes []string
	encoded, err := json.Marshal(McpServerOauthLoginParams{Name: "server", Scopes: &nilScopes})
	if err != nil || string(encoded) != `{"name":"server","threadId":null,"scopes":[]}` {
		t.Fatalf("marshal nil scope slice = %s, %v", encoded, err)
	}
}

func TestMcpServerOauthLoginParamsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"name":null}`, `{"name":1}`, `{"name":true}`,
		`{"name":"server","threadId":1}`, `{"name":"server","threadId":true}`,
		`{"name":"server","scopes":{}}`, `{"name":"server","scopes":[null]}`,
		`{"name":"server","scopes":[1]}`, `{"name":"server","scopes":[true]}`,
		`{"name":"server","timeoutSecs":"0"}`, `{"name":"server","timeoutSecs":true}`,
		`{"name":"server","timeoutSecs":0.5}`, `{"name":"server","timeoutSecs":1e3}`,
		`{"name":"server","timeoutSecs":9223372036854775808}`,
		`{"name":"server","timeoutSecs":-9223372036854775809}`,
		`{"name":"a","name":"b"}`, `{"name":"server","threadId":null,"threadId":"id"}`,
		`{"name":"server","scopes":null,"scopes":[]}`,
		`{"name":"server","timeoutSecs":null,"timeoutSecs":0}`,
		`{"name":"server"} {}`, `{"name":"server"} x`,
	} {
		assertJSONRejects[McpServerOauthLoginParams](t, input)
	}
}

func TestMcpServerOauthLoginResponsePreservesSerdeWireForms(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`{"authorizationUrl":""}`, `{"authorizationUrl":""}`},
		{`{"future":true,"authorizationUrl":" https://example.test/callback?x=1 "}`, `{"authorizationUrl":" https://example.test/callback?x=1 "}`},
	} {
		var response McpServerOauthLoginResponse
		if err := json.Unmarshal([]byte(tc.input), &response); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(response)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"authorizationUrl":null}`, `{"authorizationUrl":1}`,
		`{"authorizationUrl":"a","authorizationUrl":"b"}`,
		`{"authorizationUrl":"url"} {}`, `{"authorizationUrl":"url"} x`,
	} {
		assertJSONRejects[McpServerOauthLoginResponse](t, input)
	}
}

func TestMcpServerOauthLoginCompletionPreservesSerdeWireForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{`{"name":"","success":false}`, `{"name":"","threadId":null,"success":false}`},
		{`{"name":"server","threadId":null,"success":true,"error":null}`, `{"name":"server","threadId":null,"success":true}`},
		{`{"future":true,"name":" server ","threadId":" thread ","success":false,"error":" denied "}`, `{"name":" server ","threadId":" thread ","success":false,"error":" denied "}`},
		{`{"name":"server","success":false,"error":""}`, `{"name":"server","threadId":null,"success":false,"error":""}`},
	} {
		var notification McpServerOauthLoginCompletedNotification
		if err := json.Unmarshal([]byte(tc.input), &notification); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(notification)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"success":true}`, `{"name":"server"}`,
		`{"name":null,"success":true}`, `{"name":1,"success":true}`,
		`{"name":"server","success":null}`, `{"name":"server","success":"true"}`,
		`{"name":"server","threadId":1,"success":true}`,
		`{"name":"server","success":true,"error":1}`,
		`{"name":"a","name":"b","success":true}`,
		`{"name":"server","threadId":null,"threadId":"id","success":true}`,
		`{"name":"server","success":true,"success":false}`,
		`{"name":"server","success":true,"error":null,"error":"failure"}`,
		`{"name":"server","success":true} {}`, `{"name":"server","success":true} x`,
	} {
		assertJSONRejects[McpServerOauthLoginCompletedNotification](t, input)
	}
}

func TestMcpServerOauthLoginNilReceiversFailClosed(t *testing.T) {
	var params *McpServerOauthLoginParams
	if err := params.UnmarshalJSON([]byte(`{"name":"server"}`)); err == nil {
		t.Fatal("nil params receiver succeeded")
	}
	var response *McpServerOauthLoginResponse
	if err := response.UnmarshalJSON([]byte(`{"authorizationUrl":"url"}`)); err == nil {
		t.Fatal("nil response receiver succeeded")
	}
	var notification *McpServerOauthLoginCompletedNotification
	if err := notification.UnmarshalJSON([]byte(`{"name":"server","success":true}`)); err == nil {
		t.Fatal("nil notification receiver succeeded")
	}
}

func TestMcpServerOauthLoginContractsRemainStandaloneAndBlocked(t *testing.T) {
	names := []string{
		"McpServerOauthLoginParams",
		"McpServerOauthLoginResponse",
		"McpServerOauthLoginCompletedNotification",
	}
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
	request, ok := LookupMethod("mcpServer/oauth/login")
	if !ok || request.Surface != SurfaceClientRequest || request.State != MethodBlocked {
		t.Fatalf("mcpServer/oauth/login = %#v, %v; want blocked client request", request, ok)
	}
	notification, ok := LookupMethod("mcpServer/oauthLogin/completed")
	if !ok || notification.Surface != SurfaceServerNotification || notification.State != MethodBlocked {
		t.Fatalf("mcpServer/oauthLogin/completed = %#v, %v; want blocked server notification", notification, ok)
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if len(Methods()) != 229 || len(WireTypeBindings()) != 86 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("counts = %d/%d/%d, want 229/86/5", len(Methods()), len(WireTypeBindings()), len(ItemPayloadBindings()))
	}
}

func TestMcpServerOauthLoginTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		"export type McpServerOauthLoginParams = {\n" +
			"  \"name\": string;\n" +
			"  \"scopes\"?: Array<string> | null;\n" +
			"  \"threadId\"?: string | null;\n" +
			"  \"timeoutSecs\"?: bigint | null;\n" +
			"};",
		"export type McpServerOauthLoginResponse = {\n" +
			"  \"authorizationUrl\": string;\n" +
			"};",
		"export type McpServerOauthLoginCompletedNotification = {\n" +
			"  \"error\"?: string;\n" +
			"  \"name\": string;\n" +
			"  \"success\": boolean;\n" +
			"  \"threadId\": string | null;\n" +
			"};",
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}
