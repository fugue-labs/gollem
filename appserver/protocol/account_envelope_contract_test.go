package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestAccountEnvelopeSchemasAreExact(t *testing.T) {
	nullableRef := func(name string) Schema {
		return Schema{"anyOf": []any{Schema{"$ref": "#/$defs/" + name}, Schema{"type": "null"}}}
	}
	object := func(properties Schema, required ...string) Schema {
		out := Schema{"type": "object", "properties": properties}
		if len(required) != 0 {
			out["required"] = required
		}
		return out
	}
	want := map[string]Schema{
		"Account": {"oneOf": []any{
			Schema{
				"properties": Schema{"type": Schema{"enum": []any{"apiKey"}, "title": "ApiKeyAccountType", "type": "string"}},
				"required":   []string{"type"}, "title": "ApiKeyAccount", "type": "object",
			},
			Schema{
				"properties": Schema{
					"email":    Schema{"type": []any{"string", "null"}},
					"planType": Schema{"$ref": "#/$defs/PlanType"},
					"type":     Schema{"enum": []any{"chatgpt"}, "title": "ChatgptAccountType", "type": "string"},
				},
				"required": []string{"email", "planType", "type"}, "title": "ChatgptAccount", "type": "object",
			},
			Schema{
				"properties": Schema{
					"credentialSource": Schema{
						"allOf":   []any{Schema{"$ref": "#/$defs/AmazonBedrockCredentialSource"}},
						"default": "awsManaged",
					},
					"type": Schema{"enum": []any{"amazonBedrock"}, "title": "AmazonBedrockAccountType", "type": "string"},
				},
				"required": []string{"type"}, "title": "AmazonBedrockAccount", "type": "object",
			},
		}},
		"AccountUpdatedNotification": object(Schema{
			"authMode": nullableRef("AuthMode"), "planType": nullableRef("PlanType"),
		}),
		"GetAccountParams": object(Schema{
			"refreshToken": Schema{
				"description": "When `true`, requests a proactive token refresh before returning.\n\n" +
					"In managed auth mode this triggers the normal refresh-token flow. In external auth mode this flag is ignored. Clients should refresh tokens themselves and call `account/login/start` with `chatgptAuthTokens`.",
				"type": "boolean",
			},
		}),
		"GetAccountResponse": object(Schema{
			"account": nullableRef("Account"), "requiresOpenaiAuth": Schema{"type": "boolean"},
		}, "requiresOpenaiAuth"),
		"GetAccountTokenUsageParams": object(Schema{
			"threadId": Schema{
				"description": "When present, read estimated usage for this thread instead of account-wide token activity.",
				"type":        []any{"string", "null"},
			},
		}),
		"GetAccountTokenUsageResponse": object(Schema{
			"dailyUsageBuckets": Schema{
				"items": Schema{"$ref": "#/$defs/AccountTokenUsageDailyBucket"},
				"type":  []any{"array", "null"},
			},
			"summary": Schema{"$ref": "#/$defs/AccountTokenUsageSummary"},
		}, "summary"),
		"SendAddCreditsNudgeEmailParams": object(Schema{
			"creditType": Schema{"$ref": "#/$defs/AddCreditsNudgeCreditType"},
		}, "creditType"),
		"SendAddCreditsNudgeEmailResponse": object(Schema{
			"status": Schema{"$ref": "#/$defs/AddCreditsNudgeEmailStatus"},
		}, "status"),
	}
	definitions := JSONSchema()["$defs"].(Schema)
	for name, expected := range want {
		if got := definitions[name]; !reflect.DeepEqual(got, expected) {
			t.Errorf("%s = %#v, want %#v", name, got, expected)
		}
	}
}

func TestAccountAcceptsExactSerdeWireForms(t *testing.T) {
	for _, tc := range []struct {
		input    string
		want     string
		wantType string
	}{
		{`{"type":"apiKey"}`, `{"type":"apiKey"}`, "apiKey"},
		{`{"future":1,"future":2,"type":"apiKey","email":"ignored"}`, `{"type":"apiKey"}`, "apiKey"},
		{`{"type":"chatgpt","planType":"free"}`, `{"type":"chatgpt","email":null,"planType":"free"}`, "chatgpt"},
		{`{"type":"chatgpt","email":null,"planType":"future-plan"}`, `{"type":"chatgpt","email":null,"planType":"unknown"}`, "chatgpt"},
		{`{"type":"chatgpt","email":" user@example.invalid ","planType":"enterprise"}`, `{"type":"chatgpt","email":" user@example.invalid ","planType":"enterprise"}`, "chatgpt"},
		{`{"type":"amazonBedrock"}`, `{"type":"amazonBedrock","credentialSource":"awsManaged"}`, "amazonBedrock"},
		{`{"type":"amazonBedrock","credentialSource":"codexManaged"}`, `{"type":"amazonBedrock","credentialSource":"codexManaged"}`, "amazonBedrock"},
	} {
		var value Account
		if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != tc.want || value.Type() != tc.wantType {
			t.Errorf("round trip %s = %s, %v, type %q; want %s, %q", tc.input, encoded, err, value.Type(), tc.want, tc.wantType)
		}
	}
}

func TestAccountRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"type":null}`, `{"type":1}`, `{"type":"future"}`, `{"type":"api_key"}`,
		`{"type":"chatgpt"}`, `{"type":"chatgpt","email":1,"planType":"free"}`,
		`{"type":"chatgpt","email":null,"planType":null}`,
		`{"type":"chatgpt","email":null,"planType":1}`,
		`{"type":"amazonBedrock","credentialSource":null}`,
		`{"type":"amazonBedrock","credentialSource":"future"}`,
		`{"type":"apiKey","type":"chatgpt"}`,
		`{"type":"chatgpt","email":null,"email":"value","planType":"free"}`,
		`{"type":"amazonBedrock","credentialSource":"awsManaged","credentialSource":"codexManaged"}`,
		`{"type":"apiKey"} {}`, `{"type":"apiKey"} x`,
	} {
		assertJSONRejects[Account](t, input)
	}
}

func TestGetAccountParamsPreservesSerdeDefault(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`{}`, `{}`},
		{`{"refreshToken":false}`, `{}`},
		{`{"refreshToken":true}`, `{"refreshToken":true}`},
		{`{"future":1,"future":2,"refreshToken":true}`, `{"refreshToken":true}`},
	} {
		var value GetAccountParams
		if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{"refreshToken":null}`,
		`{"refreshToken":1}`, `{"refreshToken":"true"}`,
		`{"refreshToken":false,"refreshToken":true}`, `{} {}`, `{} x`,
	} {
		assertJSONRejects[GetAccountParams](t, input)
	}
}

func TestGetAccountTokenUsageParamsPreservesSerdeWireForms(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`{}`, `{"threadId":null}`},
		{`{"threadId":null}`, `{"threadId":null}`},
		{`{"threadId":"thread-123"}`, `{"threadId":"thread-123"}`},
		{`{"future":1,"future":2,"threadId":"thread-123"}`, `{"threadId":"thread-123"}`},
	} {
		var value GetAccountTokenUsageParams
		if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{"threadId":1}`,
		`{"threadId":false}`, `{"threadId":{},"future":true}`,
		`{"threadId":"one","threadId":"two"}`, `{} {}`, `{} x`,
	} {
		assertJSONRejects[GetAccountTokenUsageParams](t, input)
	}
}

func TestAccountEnvelopeObjectsPreserveExactSerdeWireForms(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  string
		run   func([]byte) ([]byte, error)
	}{
		{"get account absent", `{"requiresOpenaiAuth":false}`, `{"account":null,"requiresOpenaiAuth":false}`, roundTripAccountResponse},
		{"get account null", `{"account":null,"requiresOpenaiAuth":true}`, `{"account":null,"requiresOpenaiAuth":true}`, roundTripAccountResponse},
		{"get account value", `{"future":1,"account":{"type":"amazonBedrock"},"requiresOpenaiAuth":false}`, `{"account":{"type":"amazonBedrock","credentialSource":"awsManaged"},"requiresOpenaiAuth":false}`, roundTripAccountResponse},
		{"updated absent", `{}`, `{"authMode":null,"planType":null}`, roundTripAccountUpdated},
		{"updated null", `{"authMode":null,"planType":null}`, `{"authMode":null,"planType":null}`, roundTripAccountUpdated},
		{"updated value", `{"future":true,"authMode":"chatgpt","planType":"future"}`, `{"authMode":"chatgpt","planType":"unknown"}`, roundTripAccountUpdated},
		{"usage absent", `{"summary":{}}`, `{"summary":{"lifetimeTokens":null,"peakDailyTokens":null,"longestRunningTurnSec":null,"currentStreakDays":null,"longestStreakDays":null},"dailyUsageBuckets":null}`, roundTripAccountUsage},
		{"usage null", `{"summary":{},"dailyUsageBuckets":null}`, `{"summary":{"lifetimeTokens":null,"peakDailyTokens":null,"longestRunningTurnSec":null,"currentStreakDays":null,"longestStreakDays":null},"dailyUsageBuckets":null}`, roundTripAccountUsage},
		{"usage values", `{"summary":{"lifetimeTokens":-9223372036854775808},"dailyUsageBuckets":[{"startDate":"","tokens":9223372036854775807},{"startDate":"","tokens":9223372036854775807}]}`, `{"summary":{"lifetimeTokens":-9223372036854775808,"peakDailyTokens":null,"longestRunningTurnSec":null,"currentStreakDays":null,"longestStreakDays":null},"dailyUsageBuckets":[{"startDate":"","tokens":9223372036854775807},{"startDate":"","tokens":9223372036854775807}]}`, roundTripAccountUsage},
		{"nudge params", `{"future":true,"creditType":"usage_limit"}`, `{"creditType":"usage_limit"}`, roundTripNudgeParams},
		{"nudge response", `{"future":true,"status":"cooldown_active"}`, `{"status":"cooldown_active"}`, roundTripNudgeResponse},
	} {
		got, err := tc.run([]byte(tc.input))
		if err != nil || string(got) != tc.want {
			t.Errorf("%s round trip = %s, %v; want %s", tc.name, got, err, tc.want)
		}
	}
}

func TestAccountEnvelopeObjectsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `{`, `{}`, `{"account":null}`, `{"requiresOpenaiAuth":null}`,
		`{"requiresOpenaiAuth":0}`, `{"account":{"type":"future"},"requiresOpenaiAuth":false}`,
		`{"requiresOpenaiAuth":false,"requiresOpenaiAuth":true}`, `{"requiresOpenaiAuth":false} {}`,
	} {
		assertJSONRejects[GetAccountResponse](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `{`, `{"authMode":"future"}`, `{"authMode":1}`,
		`{"planType":1}`, `{"authMode":null,"authMode":"chatgpt"}`, `{} {}`,
	} {
		assertJSONRejects[AccountUpdatedNotification](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `{`, `{}`, `{"summary":null}`, `{"summary":[]}`,
		`{"summary":{},"dailyUsageBuckets":{}}`, `{"summary":{},"dailyUsageBuckets":[null]}`,
		`{"summary":{},"summary":{}}`, `{"summary":{},"dailyUsageBuckets":null,"dailyUsageBuckets":[]}`,
		`{"summary":{}} {}`,
	} {
		assertJSONRejects[GetAccountTokenUsageResponse](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `{`, `{}`, `{"creditType":null}`, `{"creditType":"future"}`,
		`{"creditType":"credits","creditType":"usage_limit"}`, `{"creditType":"credits"} {}`,
	} {
		assertJSONRejects[SendAddCreditsNudgeEmailParams](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `{`, `{}`, `{"status":null}`, `{"status":"future"}`,
		`{"status":"sent","status":"cooldown_active"}`, `{"status":"sent"} {}`,
	} {
		assertJSONRejects[SendAddCreditsNudgeEmailResponse](t, input)
	}
}

func TestAccountEnvelopeNilReceiversAndInvalidMarshal(t *testing.T) {
	checks := map[string]func() error{
		"Account":          func() error { var v *Account; return v.UnmarshalJSON([]byte(`{"type":"apiKey"}`)) },
		"GetAccountParams": func() error { var v *GetAccountParams; return v.UnmarshalJSON([]byte(`{}`)) },
		"GetAccountTokenUsageParams": func() error {
			var v *GetAccountTokenUsageParams
			return v.UnmarshalJSON([]byte(`{}`))
		},
		"GetAccountResponse": func() error {
			var v *GetAccountResponse
			return v.UnmarshalJSON([]byte(`{"requiresOpenaiAuth":false}`))
		},
		"AccountUpdatedNotification":   func() error { var v *AccountUpdatedNotification; return v.UnmarshalJSON([]byte(`{}`)) },
		"GetAccountTokenUsageResponse": func() error { var v *GetAccountTokenUsageResponse; return v.UnmarshalJSON([]byte(`{"summary":{}}`)) },
		"SendAddCreditsNudgeEmailParams": func() error {
			var v *SendAddCreditsNudgeEmailParams
			return v.UnmarshalJSON([]byte(`{"creditType":"credits"}`))
		},
		"SendAddCreditsNudgeEmailResponse": func() error {
			var v *SendAddCreditsNudgeEmailResponse
			return v.UnmarshalJSON([]byte(`{"status":"sent"}`))
		},
	}
	for name, check := range checks {
		if err := check(); err == nil {
			t.Errorf("nil %s receiver succeeded", name)
		}
	}
	if _, err := json.Marshal(Account{}); err == nil {
		t.Fatal("empty Account marshaled")
	}
}

func TestAccountEnvelopeContractsRemainStandaloneAndDeferred(t *testing.T) {
	names := []string{
		"Account", "AccountUpdatedNotification", "GetAccountParams", "GetAccountResponse",
		"GetAccountTokenUsageParams", "GetAccountTokenUsageResponse", "SendAddCreditsNudgeEmailParams",
		"SendAddCreditsNudgeEmailResponse",
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
	for methodName, surface := range map[string]Surface{
		"account/read":                     SurfaceClientRequest,
		"account/usage/read":               SurfaceClientRequest,
		"account/sendAddCreditsNudgeEmail": SurfaceClientRequest,
		"account/updated":                  SurfaceServerNotification,
	} {
		method, ok := LookupMethod(methodName)
		if !ok || method.Surface != surface || method.State != MethodDeferredStub {
			t.Errorf("%s = %#v, %v; want deferred %s", methodName, method, ok, surface)
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if len(Methods()) != 229 || len(WireTypeBindings()) != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("counts = %d methods/%d method bindings/%d item bindings; want 229/85/5", len(Methods()), len(WireTypeBindings()), len(ItemPayloadBindings()))
	}
}

func TestAccountEnvelopeTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		"export type Account = {\n  \"type\": \"apiKey\";\n} | {\n  \"email\": string | null;\n  \"planType\": PlanType;\n  \"type\": \"chatgpt\";\n} | {\n  \"credentialSource\": AmazonBedrockCredentialSource;\n  \"type\": \"amazonBedrock\";\n};",
		"export type AccountUpdatedNotification = {\n  \"authMode\": AuthMode | null;\n  \"planType\": PlanType | null;\n};",
		"export type GetAccountParams = {\n  \"refreshToken\"?: boolean;\n};",
		"export type GetAccountResponse = {\n  \"account\": Account | null;\n  \"requiresOpenaiAuth\": boolean;\n};",
		"export type GetAccountTokenUsageParams = {\n  \"threadId\"?: string | null;\n};",
		"export type GetAccountTokenUsageResponse = {\n  \"dailyUsageBuckets\": Array<AccountTokenUsageDailyBucket> | null;\n  \"summary\": AccountTokenUsageSummary;\n};",
		"export type SendAddCreditsNudgeEmailParams = {\n  \"creditType\": AddCreditsNudgeCreditType;\n};",
		"export type SendAddCreditsNudgeEmailResponse = {\n  \"status\": AddCreditsNudgeEmailStatus;\n};",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func roundTripAccountResponse(data []byte) ([]byte, error) {
	var value GetAccountResponse
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func roundTripAccountUpdated(data []byte) ([]byte, error) {
	var value AccountUpdatedNotification
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func roundTripAccountUsage(data []byte) ([]byte, error) {
	var value GetAccountTokenUsageResponse
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func roundTripNudgeParams(data []byte) ([]byte, error) {
	var value SendAddCreditsNudgeEmailParams
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func roundTripNudgeResponse(data []byte) ([]byte, error) {
	var value SendAddCreditsNudgeEmailResponse
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
