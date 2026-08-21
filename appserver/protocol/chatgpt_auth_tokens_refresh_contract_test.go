package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestChatgptAuthTokensRefreshSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	reason := Schema{
		"oneOf": []any{Schema{
			"description": "Codex attempted a backend request and received `401 Unauthorized`.",
			"enum":        []any{"unauthorized"},
			"type":        "string",
		}},
	}
	if !reflect.DeepEqual(defs["ChatgptAuthTokensRefreshReason"], reason) {
		t.Fatalf("ChatgptAuthTokensRefreshReason = %#v, want %#v", defs["ChatgptAuthTokensRefreshReason"], reason)
	}

	params := Schema{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"properties": Schema{
			"reason": Schema{"$ref": "#/$defs/ChatgptAuthTokensRefreshReason"},
			"previousAccountId": Schema{
				"description": "Workspace/account identifier that Codex was previously using.\n\n" +
					"Clients that manage multiple accounts/workspaces can use this as a hint to " +
					"refresh the token for the correct workspace.\n\n" +
					"This may be `null` when the prior auth state did not include a workspace " +
					"identifier (`chatgpt_account_id`).",
				"type": []any{"string", "null"},
			},
		},
		"required": []string{"reason"},
		"title":    "ChatgptAuthTokensRefreshParams",
		"type":     "object",
	}
	if !reflect.DeepEqual(defs["ChatgptAuthTokensRefreshParams"], params) {
		t.Fatalf("ChatgptAuthTokensRefreshParams = %#v, want %#v", defs["ChatgptAuthTokensRefreshParams"], params)
	}

	response := closedThreadSessionParamSchema(Schema{
		"accessToken":      Schema{"type": "string"},
		"chatgptAccountId": Schema{"type": "string"},
		"chatgptPlanType":  nullableStringSchema(),
	}, []string{"accessToken", "chatgptAccountId"})
	if !reflect.DeepEqual(defs["ChatgptAuthTokensRefreshResponse"], response) {
		t.Fatalf("ChatgptAuthTokensRefreshResponse = %#v, want %#v", defs["ChatgptAuthTokensRefreshResponse"], response)
	}
}

func TestChatgptAuthTokensRefreshReasonAcceptsExactValue(t *testing.T) {
	var reason ChatgptAuthTokensRefreshReason
	if err := json.Unmarshal([]byte(`"unauthorized"`), &reason); err != nil {
		t.Fatalf("unmarshal reason: %v", err)
	}
	if reason != ChatgptAuthTokensRefreshReasonUnauthorized {
		t.Fatalf("reason = %q, want unauthorized", reason)
	}
	encoded, err := json.Marshal(reason)
	if err != nil || string(encoded) != `"unauthorized"` {
		t.Fatalf("marshal reason = %s, %v", encoded, err)
	}
}

func TestChatgptAuthTokensRefreshRecordsAcceptSerdeWireForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{`{"reason":"unauthorized"}`, `{"reason":"unauthorized","previousAccountId":null}`},
		{`{"reason":"unauthorized","previousAccountId":null}`, `{"reason":"unauthorized","previousAccountId":null}`},
		{`{"reason":"unauthorized","previousAccountId":""}`, `{"reason":"unauthorized","previousAccountId":""}`},
		{`{"unknown":"ignored","reason":"unauthorized","previousAccountId":" workspace "}`, `{"reason":"unauthorized","previousAccountId":" workspace "}`},
	} {
		var params ChatgptAuthTokensRefreshParams
		if err := json.Unmarshal([]byte(tc.input), &params); err != nil {
			t.Errorf("unmarshal params %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(params)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("params round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}

	for _, tc := range []struct {
		input string
		want  string
	}{
		{`{"accessToken":"","chatgptAccountId":""}`, `{"accessToken":"","chatgptAccountId":"","chatgptPlanType":null}`},
		{`{"accessToken":"token","chatgptAccountId":"account","chatgptPlanType":null}`, `{"accessToken":"token","chatgptAccountId":"account","chatgptPlanType":null}`},
		{`{"accessToken":" token ","chatgptAccountId":" workspace ","chatgptPlanType":" pro "}`, `{"accessToken":" token ","chatgptAccountId":" workspace ","chatgptPlanType":" pro "}`},
		{`{"future":true,"accessToken":"token","chatgptAccountId":"account"}`, `{"accessToken":"token","chatgptAccountId":"account","chatgptPlanType":null}`},
	} {
		var response ChatgptAuthTokensRefreshResponse
		if err := json.Unmarshal([]byte(tc.input), &response); err != nil {
			t.Errorf("unmarshal response %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(response)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("response round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
}

func TestChatgptAuthTokensRefreshRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `""`, `"other"`, `1`, `true`, `{}`, `[]`,
		`"Unauthorized"`, `"unauthorized" {}`, `"unauthorized" x`,
	} {
		assertJSONRejects[ChatgptAuthTokensRefreshReason](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"reason":null}`, `{"reason":"other"}`, `{"reason":1}`,
		`{"reason":"unauthorized","previousAccountId":1}`,
		`{"reason":"unauthorized","reason":"unauthorized"}`,
		`{"reason":"unauthorized","previousAccountId":null,"previousAccountId":"account"}`,
		`{"reason":"unauthorized"} {}`, `{"reason":"unauthorized"} x`,
	} {
		assertJSONRejects[ChatgptAuthTokensRefreshParams](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"chatgptAccountId":"account"}`, `{"accessToken":"token"}`,
		`{"accessToken":null,"chatgptAccountId":"account"}`,
		`{"accessToken":"token","chatgptAccountId":null}`,
		`{"accessToken":1,"chatgptAccountId":"account"}`,
		`{"accessToken":"token","chatgptAccountId":1}`,
		`{"accessToken":"token","chatgptAccountId":"account","chatgptPlanType":1}`,
		`{"accessToken":"one","accessToken":"two","chatgptAccountId":"account"}`,
		`{"accessToken":"token","chatgptAccountId":"one","chatgptAccountId":"two"}`,
		`{"accessToken":"token","chatgptAccountId":"account","chatgptPlanType":null,"chatgptPlanType":"pro"}`,
		`{"accessToken":"token","chatgptAccountId":"account"} {}`,
		`{"accessToken":"token","chatgptAccountId":"account"} x`,
	} {
		assertJSONRejects[ChatgptAuthTokensRefreshResponse](t, input)
	}
}

func TestChatgptAuthTokensRefreshNilReceiversAndInvalidMarshalFailClosed(t *testing.T) {
	var reason *ChatgptAuthTokensRefreshReason
	if err := reason.UnmarshalJSON([]byte(`"unauthorized"`)); err == nil {
		t.Fatal("nil reason receiver succeeded")
	}
	var params *ChatgptAuthTokensRefreshParams
	if err := params.UnmarshalJSON([]byte(`{"reason":"unauthorized"}`)); err == nil {
		t.Fatal("nil params receiver succeeded")
	}
	var response *ChatgptAuthTokensRefreshResponse
	if err := response.UnmarshalJSON([]byte(`{"accessToken":"token","chatgptAccountId":"account"}`)); err == nil {
		t.Fatal("nil response receiver succeeded")
	}
	if _, err := json.Marshal(ChatgptAuthTokensRefreshReason("other")); err == nil {
		t.Fatal("invalid reason marshaled")
	}
	if _, err := json.Marshal(ChatgptAuthTokensRefreshParams{}); err == nil {
		t.Fatal("params with invalid zero reason marshaled")
	}
}

func TestChatgptAuthTokensRefreshRemainsStandaloneAndDeferred(t *testing.T) {
	names := []string{
		"ChatgptAuthTokensRefreshReason",
		"ChatgptAuthTokensRefreshParams",
		"ChatgptAuthTokensRefreshResponse",
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
	var found bool
	for _, method := range Methods() {
		if method.Method == "account/chatgptAuthTokens/refresh" {
			found = true
			if method.Surface != SurfaceServerRequest || method.State != MethodDeferredStub {
				t.Fatalf("refresh method = %#v, want deferred server request", method)
			}
		}
	}
	if !found {
		t.Fatal("refresh method inventory entry missing")
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

func TestChatgptAuthTokensRefreshTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`export type ChatgptAuthTokensRefreshReason = "unauthorized";`,
		"export type ChatgptAuthTokensRefreshParams = {\n" +
			"  \"previousAccountId\"?: string | null;\n" +
			"  \"reason\": ChatgptAuthTokensRefreshReason;\n" +
			"};",
		"export type ChatgptAuthTokensRefreshResponse = {\n" +
			"  \"accessToken\": string;\n" +
			"  \"chatgptAccountId\": string;\n" +
			"  \"chatgptPlanType\": string | null;\n" +
			"};",
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}
