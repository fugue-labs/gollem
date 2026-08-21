package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestLoginAccountSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	params := loginAccountSchemaVariants(t, defs["LoginAccountParams"], 5)
	response := loginAccountSchemaVariants(t, defs["LoginAccountResponse"], 5)

	wantParamsRequired := map[string][]string{
		"apiKey":            {"apiKey", "type"},
		"chatgpt":           {"type"},
		"chatgptDeviceCode": {"type"},
		"chatgptAuthTokens": {"accessToken", "chatgptAccountId", "type"},
		"amazonBedrock":     {"apiKey", "region", "type"},
	}
	wantResponseRequired := map[string][]string{
		"apiKey":            {"type"},
		"chatgpt":           {"authUrl", "loginId", "type"},
		"chatgptDeviceCode": {"loginId", "type", "userCode", "verificationUrl"},
		"chatgptAuthTokens": {"type"},
		"amazonBedrock":     {"type"},
	}
	assertLoginAccountVariantRequired(t, params, wantParamsRequired)
	assertLoginAccountVariantRequired(t, response, wantResponseRequired)

	chatgptProperties := params["chatgpt"]["properties"].(Schema)
	for _, name := range []string{"codexStreamlinedLogin", "useHostedLoginSuccessPage"} {
		if got := chatgptProperties[name]; !reflect.DeepEqual(got, Schema{"type": "boolean"}) {
			t.Errorf("LoginAccountParams.chatgpt.%s = %#v", name, got)
		}
	}
	assertLoginAccountNullableSchema(
		t,
		chatgptProperties["appBrand"],
		Schema{"$ref": "#/$defs/LoginAppBrand"},
	)
	if _, ok := chatgptProperties["appBrand"].(Schema)["default"]; !ok {
		t.Error("LoginAccountParams.chatgpt.appBrand has no null default")
	}

	tokenProperties := params["chatgptAuthTokens"]["properties"].(Schema)
	assertLoginAccountNullableSchema(
		t,
		tokenProperties["chatgptPlanType"],
		Schema{"type": "string"},
	)
}

func TestLoginAccountParamsAcceptAndCanonicalizeExactWireForms(t *testing.T) {
	for _, tc := range []struct {
		name      string
		input     string
		canonical string
		kind      string
	}{
		{
			name:      "API key empty and unknown input discarded",
			input:     `{"type":"apiKey","apiKey":"","future":{"ok":true}}`,
			canonical: `{"type":"apiKey","apiKey":""}`,
			kind:      "apiKey",
		},
		{
			name:      "ChatGPT defaults",
			input:     `{"type":"chatgpt"}`,
			canonical: `{"type":"chatgpt","appBrand":null}`,
			kind:      "chatgpt",
		},
		{
			name:      "ChatGPT explicit false and null canonicalize",
			input:     `{"type":"chatgpt","codexStreamlinedLogin":false,"useHostedLoginSuccessPage":false,"appBrand":null}`,
			canonical: `{"type":"chatgpt","appBrand":null}`,
			kind:      "chatgpt",
		},
		{
			name:      "ChatGPT full Codex brand",
			input:     `{"type":"chatgpt","codexStreamlinedLogin":true,"useHostedLoginSuccessPage":true,"appBrand":"codex"}`,
			canonical: `{"type":"chatgpt","codexStreamlinedLogin":true,"useHostedLoginSuccessPage":true,"appBrand":"codex"}`,
			kind:      "chatgpt",
		},
		{
			name:      "ChatGPT ChatGPT brand",
			input:     `{"type":"chatgpt","appBrand":"chatgpt"}`,
			canonical: `{"type":"chatgpt","appBrand":"chatgpt"}`,
			kind:      "chatgpt",
		},
		{
			name:      "device code",
			input:     `{"type":"chatgptDeviceCode"}`,
			canonical: `{"type":"chatgptDeviceCode"}`,
			kind:      "chatgptDeviceCode",
		},
		{
			name:      "auth tokens omitted plan",
			input:     `{"type":"chatgptAuthTokens","accessToken":"","chatgptAccountId":""}`,
			canonical: `{"type":"chatgptAuthTokens","accessToken":"","chatgptAccountId":"","chatgptPlanType":null}`,
			kind:      "chatgptAuthTokens",
		},
		{
			name:      "auth tokens explicit plan",
			input:     `{"type":"chatgptAuthTokens","accessToken":"token","chatgptAccountId":"account","chatgptPlanType":"pro"}`,
			canonical: `{"type":"chatgptAuthTokens","accessToken":"token","chatgptAccountId":"account","chatgptPlanType":"pro"}`,
			kind:      "chatgptAuthTokens",
		},
		{
			name:      "Bedrock empty strings",
			input:     `{"type":"amazonBedrock","apiKey":"","region":""}`,
			canonical: `{"type":"amazonBedrock","apiKey":"","region":""}`,
			kind:      "amazonBedrock",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var value LoginAccountParams
			if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := value.Type(); got != tc.kind {
				t.Errorf("Type() = %q, want %q", got, tc.kind)
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if got := string(encoded); got != tc.canonical {
				t.Errorf("canonical = %s, want %s", got, tc.canonical)
			}
		})
	}
}

func TestLoginAccountResponseAcceptsAndCanonicalizesExactWireForms(t *testing.T) {
	for _, tc := range []struct {
		input     string
		canonical string
		kind      string
	}{
		{`{"type":"apiKey","future":true}`, `{"type":"apiKey"}`, "apiKey"},
		{`{"type":"chatgpt","loginId":"","authUrl":""}`, `{"type":"chatgpt","loginId":"","authUrl":""}`, "chatgpt"},
		{`{"type":"chatgptDeviceCode","loginId":"id","verificationUrl":"url","userCode":"code"}`, `{"type":"chatgptDeviceCode","loginId":"id","verificationUrl":"url","userCode":"code"}`, "chatgptDeviceCode"},
		{`{"type":"chatgptAuthTokens"}`, `{"type":"chatgptAuthTokens"}`, "chatgptAuthTokens"},
		{`{"type":"amazonBedrock"}`, `{"type":"amazonBedrock"}`, "amazonBedrock"},
	} {
		var value LoginAccountResponse
		if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		if got := value.Type(); got != tc.kind {
			t.Errorf("Type(%s) = %q, want %q", tc.input, got, tc.kind)
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != tc.canonical {
			t.Errorf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.canonical)
		}
	}
}

func TestLoginAccountParamsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `{}`, `{} {}`,
		`{"type":null}`, `{"type":1}`, `{"type":""}`, `{"type":"other"}`,
		`{"type":"ApiKey","apiKey":"key"}`,
		`{"type":"apiKey"}`, `{"type":"apiKey","apiKey":null}`,
		`{"type":"apiKey","apiKey":1}`, `{"type":"apiKey","apiKey":"key","region":"us-east-1"}`,
		`{"type":"chatgpt","codexStreamlinedLogin":null}`,
		`{"type":"chatgpt","codexStreamlinedLogin":"true"}`,
		`{"type":"chatgpt","useHostedLoginSuccessPage":null}`,
		`{"type":"chatgpt","appBrand":"Codex"}`,
		`{"type":"chatgpt","appBrand":1}`,
		`{"type":"chatgpt","apiKey":"key"}`,
		`{"type":"chatgptDeviceCode","appBrand":"codex"}`,
		`{"type":"chatgptAuthTokens","accessToken":"token"}`,
		`{"type":"chatgptAuthTokens","accessToken":null,"chatgptAccountId":"account"}`,
		`{"type":"chatgptAuthTokens","accessToken":"token","chatgptAccountId":null}`,
		`{"type":"chatgptAuthTokens","accessToken":"token","chatgptAccountId":"account","chatgptPlanType":1}`,
		`{"type":"chatgptAuthTokens","accessToken":"token","chatgptAccountId":"account","region":"us-east-1"}`,
		`{"type":"amazonBedrock","apiKey":"key"}`,
		`{"type":"amazonBedrock","apiKey":null,"region":"us-east-1"}`,
		`{"type":"amazonBedrock","apiKey":"key","region":null}`,
		`{"type":"amazonBedrock","apiKey":"key","region":"us-east-1","accessToken":"token"}`,
		`{"type":"apiKey","type":"apiKey","apiKey":"key"}`,
		`{"type":"apiKey","apiKey":"one","apiKey":"two"}`,
		`{"type":"chatgpt"} {}`,
	} {
		assertJSONRejects[LoginAccountParams](t, input)
	}
}

func TestLoginAccountResponseRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `{}`, `{} {}`,
		`{"type":null}`, `{"type":1}`, `{"type":""}`, `{"type":"other"}`,
		`{"type":"Chatgpt"}`,
		`{"type":"apiKey","loginId":"id"}`,
		`{"type":"chatgpt"}`, `{"type":"chatgpt","loginId":"id"}`,
		`{"type":"chatgpt","loginId":null,"authUrl":"url"}`,
		`{"type":"chatgpt","loginId":"id","authUrl":null}`,
		`{"type":"chatgpt","loginId":"id","authUrl":"url","userCode":"code"}`,
		`{"type":"chatgptDeviceCode","loginId":"id","verificationUrl":"url"}`,
		`{"type":"chatgptDeviceCode","loginId":null,"verificationUrl":"url","userCode":"code"}`,
		`{"type":"chatgptDeviceCode","loginId":"id","verificationUrl":1,"userCode":"code"}`,
		`{"type":"chatgptDeviceCode","loginId":"id","verificationUrl":"url","userCode":null}`,
		`{"type":"chatgptDeviceCode","loginId":"id","verificationUrl":"url","userCode":"code","apiKey":"key"}`,
		`{"type":"chatgptAuthTokens","authUrl":"url"}`,
		`{"type":"amazonBedrock","loginId":"id"}`,
		`{"type":"chatgpt","type":"chatgpt","loginId":"id","authUrl":"url"}`,
		`{"type":"chatgpt","loginId":"one","loginId":"two","authUrl":"url"}`,
		`{"type":"apiKey"} {}`,
	} {
		assertJSONRejects[LoginAccountResponse](t, input)
	}
}

func TestLoginAccountDirectDecoderRejectsMalformedJSON(t *testing.T) {
	for _, input := range []string{
		``,
		`{`,
		`{1`,
		`{"type":`,
		`{"type":"apiKey"`,
		`{"type":"apiKey","apiKey":"key"} {}`,
		`{"type":"apiKey","apiKey":"key"} trailing`,
	} {
		var value LoginAccountParams
		if err := value.UnmarshalJSON([]byte(input)); err == nil {
			t.Errorf("direct UnmarshalJSON from %q succeeded", input)
		}
	}
}

func TestLoginAccountContractsNilReceiversAndInvalidMarshalFailClosed(t *testing.T) {
	var params *LoginAccountParams
	if err := params.UnmarshalJSON([]byte(`{"type":"chatgpt"}`)); err == nil {
		t.Fatal("nil LoginAccountParams receiver succeeded")
	}
	var response *LoginAccountResponse
	if err := response.UnmarshalJSON([]byte(`{"type":"apiKey"}`)); err == nil {
		t.Fatal("nil LoginAccountResponse receiver succeeded")
	}
	for _, value := range []any{
		LoginAccountParams{},
		LoginAccountParams{raw: json.RawMessage(`{"type":"apiKey"}`)},
		LoginAccountParams{raw: json.RawMessage(`{"type":"unknown"}`)},
		LoginAccountResponse{},
		LoginAccountResponse{raw: json.RawMessage(`{"type":"chatgpt"}`)},
		LoginAccountResponse{raw: json.RawMessage(`{"type":"unknown"}`)},
	} {
		if _, err := json.Marshal(value); err == nil {
			t.Errorf("invalid %T marshaled", value)
		}
	}
	for _, value := range []interface{ Type() string }{
		LoginAccountParams{},
		LoginAccountParams{raw: json.RawMessage(`not-json`)},
		LoginAccountResponse{},
		LoginAccountResponse{raw: json.RawMessage(`not-json`)},
	} {
		if got := value.Type(); got != "" {
			t.Errorf("invalid %T Type() = %q", value, got)
		}
	}
}

func TestLoginAccountContractsRemainStandalone(t *testing.T) {
	names := []string{"LoginAccountParams", "LoginAccountResponse"}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
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

func TestLoginAccountTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type LoginAccountParams = {`,
		`"type": "apiKey";`,
		`"type": "chatgpt";`,
		`"codexStreamlinedLogin"?: boolean;`,
		`"useHostedLoginSuccessPage"?: boolean;`,
		`"appBrand"?: LoginAppBrand | null;`,
		`"type": "chatgptDeviceCode";`,
		`"type": "chatgptAuthTokens";`,
		`"chatgptPlanType"?: string | null;`,
		`"type": "amazonBedrock";`,
		`export type LoginAccountResponse = {`,
		`"authUrl": string;`,
		`"verificationUrl": string;`,
		`"userCode": string;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func loginAccountSchemaVariants(t *testing.T, definition any, wantCount int) map[string]Schema {
	t.Helper()
	schema, ok := definition.(Schema)
	if !ok {
		t.Fatalf("definition type = %T", definition)
	}
	oneOf, ok := schema["oneOf"].([]any)
	if !ok || len(oneOf) != wantCount {
		t.Fatalf("oneOf = %#v, want %d variants", schema["oneOf"], wantCount)
	}
	variants := make(map[string]Schema, wantCount)
	for _, raw := range oneOf {
		variant := raw.(Schema)
		if variant["type"] != "object" || variant["additionalProperties"] != false {
			t.Fatalf("variant is not a closed object: %#v", variant)
		}
		properties := variant["properties"].(Schema)
		typeSchema := properties["type"].(Schema)
		values := typeSchema["enum"].([]any)
		if len(values) != 1 {
			t.Fatalf("variant type = %#v", typeSchema)
		}
		kind := values[0].(string)
		if _, exists := variants[kind]; exists {
			t.Fatalf("duplicate schema variant %q", kind)
		}
		variants[kind] = variant
	}
	return variants
}

func assertLoginAccountVariantRequired(t *testing.T, variants map[string]Schema, want map[string][]string) {
	t.Helper()
	if len(variants) != len(want) {
		t.Fatalf("variants = %v, want %v", variants, want)
	}
	for kind, required := range want {
		variant, ok := variants[kind]
		if !ok {
			t.Errorf("missing variant %q", kind)
			continue
		}
		got := schemaRequiredNames(variant)
		slices.Sort(got)
		wantRequired := append([]string(nil), required...)
		slices.Sort(wantRequired)
		if !slices.Equal(got, wantRequired) {
			t.Errorf("%s required = %v, want %v", kind, got, wantRequired)
		}
	}
}

func assertLoginAccountNullableSchema(t *testing.T, raw any, wantValue Schema) {
	t.Helper()
	schema := raw.(Schema)
	variants := schema["anyOf"].([]any)
	if len(variants) != 2 ||
		!reflect.DeepEqual(variants[0], wantValue) ||
		!reflect.DeepEqual(variants[1], Schema{"type": "null"}) {
		t.Fatalf("nullable schema = %#v, want %#v or null", schema, wantValue)
	}
}

var (
	_ json.Marshaler   = LoginAccountParams{}
	_ json.Unmarshaler = (*LoginAccountParams)(nil)
	_ json.Marshaler   = LoginAccountResponse{}
	_ json.Unmarshaler = (*LoginAccountResponse)(nil)
)
