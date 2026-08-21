package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestModelSafetyNotificationSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	assertStringEnum(t, defs["ModelRerouteReason"], "highRiskCyberActivity")
	assertStringEnum(t, defs["ModelVerification"], "trustedAccessForCyber")

	rerouted := defs["ModelReroutedNotification"].(Schema)
	assertClosedObjectSchema(t, rerouted, "threadId", "turnId", "fromModel", "toModel", "reason")
	reroutedProperties := rerouted["properties"].(Schema)
	for _, field := range []string{"threadId", "turnId", "fromModel", "toModel"} {
		if !reflect.DeepEqual(reroutedProperties[field], Schema{"type": "string"}) {
			t.Errorf("ModelReroutedNotification.%s = %#v", field, reroutedProperties[field])
		}
	}
	if !reflect.DeepEqual(reroutedProperties["reason"], Schema{"$ref": "#/$defs/ModelRerouteReason"}) {
		t.Fatalf("ModelReroutedNotification.reason = %#v", reroutedProperties["reason"])
	}

	verification := defs["ModelVerificationNotification"].(Schema)
	assertClosedObjectSchema(t, verification, "threadId", "turnId", "verifications")
	verificationProperties := verification["properties"].(Schema)
	verifications := verificationProperties["verifications"].(Schema)
	if verifications["type"] != "array" || !reflect.DeepEqual(verifications["items"], Schema{"$ref": "#/$defs/ModelVerification"}) {
		t.Fatalf("ModelVerificationNotification.verifications = %#v", verifications)
	}
	if _, nullable := verifications["anyOf"]; nullable {
		t.Fatalf("ModelVerificationNotification.verifications is nullable: %#v", verifications)
	}

	moderation := defs["TurnModerationMetadataNotification"].(Schema)
	assertClosedObjectSchema(t, moderation, "threadId", "turnId", "metadata")
	if !reflect.DeepEqual(moderation["properties"].(Schema)["metadata"], Schema{"$ref": "#/$defs/JsonValue"}) {
		t.Fatalf("TurnModerationMetadataNotification.metadata = %#v", moderation["properties"].(Schema)["metadata"])
	}

	buffering := defs["ModelSafetyBufferingUpdatedNotification"].(Schema)
	assertClosedObjectSchema(
		t,
		buffering,
		"threadId",
		"turnId",
		"model",
		"useCases",
		"reasons",
		"showBufferingUi",
		"fasterModel",
	)
	bufferingProperties := buffering["properties"].(Schema)
	for _, field := range []string{"useCases", "reasons"} {
		property := bufferingProperties[field].(Schema)
		if !reflect.DeepEqual(property, Schema{"type": "array", "items": Schema{"type": "string"}}) {
			t.Errorf("ModelSafetyBufferingUpdatedNotification.%s = %#v", field, property)
		}
	}
	if !reflect.DeepEqual(bufferingProperties["showBufferingUi"], Schema{"type": "boolean"}) {
		t.Fatalf("ModelSafetyBufferingUpdatedNotification.showBufferingUi = %#v", bufferingProperties["showBufferingUi"])
	}
	assertNullableStringSchema(t, bufferingProperties["fasterModel"])
}

func TestModelSafetyNotificationsAcceptExactWireValues(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
		value func() any
	}{
		{"reroute reason", `"highRiskCyberActivity"`, `"highRiskCyberActivity"`, func() any { return new(ModelRerouteReason) }},
		{"verification", `"trustedAccessForCyber"`, `"trustedAccessForCyber"`, func() any { return new(ModelVerification) }},
		{"rerouted", `{"threadId":"","turnId":"","fromModel":"","toModel":"","reason":"highRiskCyberActivity"}`, `{"threadId":"","turnId":"","fromModel":"","toModel":"","reason":"highRiskCyberActivity"}`, func() any { return new(ModelReroutedNotification) }},
		{"empty verifications", `{"threadId":"thread","turnId":"turn","verifications":[]}`, `{"threadId":"thread","turnId":"turn","verifications":[]}`, func() any { return new(ModelVerificationNotification) }},
		{"verification list", `{"threadId":"thread","turnId":"turn","verifications":["trustedAccessForCyber"]}`, `{"threadId":"thread","turnId":"turn","verifications":["trustedAccessForCyber"]}`, func() any { return new(ModelVerificationNotification) }},
		{"null metadata", `{"threadId":"thread","turnId":"turn","metadata":null}`, `{"threadId":"thread","turnId":"turn","metadata":null}`, func() any { return new(TurnModerationMetadataNotification) }},
		{"nested metadata", `{"threadId":"thread","turnId":"turn","metadata":{"score":9007199254740993,"flags":[true,null]}}`, `{"threadId":"thread","turnId":"turn","metadata":{"flags":[true,null],"score":9007199254740993}}`, func() any { return new(TurnModerationMetadataNotification) }},
		{"omitted faster model", `{"threadId":"thread","turnId":"turn","model":"model","useCases":[],"reasons":[],"showBufferingUi":false}`, `{"threadId":"thread","turnId":"turn","model":"model","useCases":[],"reasons":[],"showBufferingUi":false,"fasterModel":null}`, func() any { return new(ModelSafetyBufferingUpdatedNotification) }},
		{"buffering", `{"threadId":"","turnId":"","model":"","useCases":["cyber"],"reasons":["risk"],"showBufferingUi":true,"fasterModel":"fast"}`, `{"threadId":"","turnId":"","model":"","useCases":["cyber"],"reasons":["risk"],"showBufferingUi":true,"fasterModel":"fast"}`, func() any { return new(ModelSafetyBufferingUpdatedNotification) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := tc.value()
			if err := json.Unmarshal([]byte(tc.input), value); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			encoded, err := json.Marshal(value)
			if err != nil || string(encoded) != tc.want {
				t.Fatalf("round trip = %s, %v; want %s", encoded, err, tc.want)
			}
		})
	}
}

func TestModelSafetyNotificationsRejectMalformedWireValues(t *testing.T) {
	for _, input := range []string{`null`, `""`, `"other"`, `1`, `"highRiskCyberActivity" {}`} {
		var value ModelRerouteReason
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("ModelRerouteReason accepted %s", input)
		}
	}
	for _, input := range []string{`null`, `""`, `"other"`, `false`, `"trustedAccessForCyber" {}`} {
		var value ModelVerification
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("ModelVerification accepted %s", input)
		}
	}

	reroutedInvalid := []string{
		`null`, `{}`, `{"threadId":"thread","turnId":"turn","fromModel":"from","toModel":"to"}`,
		`{"threadId":null,"turnId":"turn","fromModel":"from","toModel":"to","reason":"highRiskCyberActivity"}`,
		`{"threadId":"thread","turnId":null,"fromModel":"from","toModel":"to","reason":"highRiskCyberActivity"}`,
		`{"threadId":"thread","turnId":"turn","fromModel":null,"toModel":"to","reason":"highRiskCyberActivity"}`,
		`{"threadId":"thread","turnId":"turn","fromModel":"from","toModel":null,"reason":"highRiskCyberActivity"}`,
		`{"threadId":"thread","turnId":"turn","fromModel":"from","toModel":"to","reason":"other"}`,
		`{"threadId":"thread","turnId":"turn","fromModel":"from","toModel":"to","reason":"highRiskCyberActivity","at":"now"}`,
		`{"threadId":"thread","turnId":"turn","fromModel":"from","toModel":"to","reason":"highRiskCyberActivity"} {}`,
	}
	for _, input := range reroutedInvalid {
		var value ModelReroutedNotification
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("ModelReroutedNotification accepted %s", input)
		}
	}

	verificationInvalid := []string{
		`null`, `{}`, `{"threadId":"thread","turnId":"turn"}`,
		`{"threadId":"thread","turnId":null,"verifications":[]}`,
		`{"threadId":"thread","turnId":"turn","verifications":null}`,
		`{"threadId":"thread","turnId":"turn","verifications":[null]}`,
		`{"threadId":"thread","turnId":"turn","verifications":["other"]}`,
		`{"threadId":"thread","turnId":"turn","verifications":[],"extra":true}`,
		`{"threadId":"thread","turnId":"turn","verifications":[]} {}`,
	}
	for _, input := range verificationInvalid {
		var value ModelVerificationNotification
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("ModelVerificationNotification accepted %s", input)
		}
	}

	moderationInvalid := []string{
		`null`, `{}`, `{"threadId":"thread","turnId":"turn"}`,
		`{"threadId":null,"turnId":"turn","metadata":null}`,
		`{"threadId":"thread","turnId":null,"metadata":null}`,
		`{"threadId":"thread","turnId":"turn","metadata":undefined}`,
		`{"threadId":"thread","turnId":"turn","metadata":null,"extra":true}`,
		`{"threadId":"thread","turnId":"turn","metadata":null} {}`,
	}
	for _, input := range moderationInvalid {
		var value TurnModerationMetadataNotification
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("TurnModerationMetadataNotification accepted %s", input)
		}
	}

	bufferingInvalid := []string{
		`null`, `{}`,
		`{"threadId":"thread","turnId":null,"model":"model","useCases":[],"reasons":[],"showBufferingUi":false,"fasterModel":null}`,
		`{"threadId":"thread","turnId":"turn","model":null,"useCases":[],"reasons":[],"showBufferingUi":false,"fasterModel":null}`,
		`{"threadId":"thread","turnId":"turn","model":"model","reasons":[],"showBufferingUi":false,"fasterModel":null}`,
		`{"threadId":"thread","turnId":"turn","model":"model","useCases":[],"showBufferingUi":false,"fasterModel":null}`,
		`{"threadId":"thread","turnId":"turn","model":"model","useCases":null,"reasons":[],"showBufferingUi":false,"fasterModel":null}`,
		`{"threadId":"thread","turnId":"turn","model":"model","useCases":[null],"reasons":[],"showBufferingUi":false,"fasterModel":null}`,
		`{"threadId":"thread","turnId":"turn","model":"model","useCases":[],"reasons":[1],"showBufferingUi":false,"fasterModel":null}`,
		`{"threadId":"thread","turnId":"turn","model":"model","useCases":[],"reasons":[],"showBufferingUi":"false","fasterModel":null}`,
		`{"threadId":"thread","turnId":"turn","model":"model","useCases":[],"reasons":[],"showBufferingUi":false,"fasterModel":1}`,
		`{"threadId":"thread","turnId":"turn","model":"model","useCases":[],"reasons":[],"showBufferingUi":false,"fasterModel":null,"extra":true}`,
		`{"threadId":"thread","turnId":"turn","model":"model","useCases":[],"reasons":[],"showBufferingUi":false,"fasterModel":null} {}`,
	}
	for _, input := range bufferingInvalid {
		var value ModelSafetyBufferingUpdatedNotification
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("ModelSafetyBufferingUpdatedNotification accepted %s", input)
		}
	}
	if _, err := decodeRequiredModelSafetyJSONValue(
		map[string]json.RawMessage{"metadata": json.RawMessage(`undefined`)},
		"metadata",
	); err == nil {
		t.Error("invalid raw moderation metadata succeeded")
	}
}

func TestModelSafetyNotificationNilReceiversAndInvalidMarshal(t *testing.T) {
	var rerouteReason *ModelRerouteReason
	var verification *ModelVerification
	var rerouted *ModelReroutedNotification
	var verificationNotification *ModelVerificationNotification
	var moderation *TurnModerationMetadataNotification
	var buffering *ModelSafetyBufferingUpdatedNotification
	for name, decode := range map[string]func() error{
		"reroute reason":            func() error { return rerouteReason.UnmarshalJSON([]byte(`null`)) },
		"verification":              func() error { return verification.UnmarshalJSON([]byte(`null`)) },
		"rerouted":                  func() error { return rerouted.UnmarshalJSON([]byte(`{}`)) },
		"verification notification": func() error { return verificationNotification.UnmarshalJSON([]byte(`{}`)) },
		"moderation":                func() error { return moderation.UnmarshalJSON([]byte(`{}`)) },
		"buffering":                 func() error { return buffering.UnmarshalJSON([]byte(`{}`)) },
	} {
		if err := decode(); err == nil {
			t.Errorf("nil %s receiver succeeded", name)
		}
	}
	for name, value := range map[string]any{
		"reroute reason":    ModelRerouteReason("other"),
		"verification":      ModelVerification("other"),
		"nil verifications": ModelVerificationNotification{Verifications: nil},
		"nil use cases":     ModelSafetyBufferingUpdatedNotification{UseCases: nil, Reasons: []string{}},
		"nil reasons":       ModelSafetyBufferingUpdatedNotification{UseCases: []string{}, Reasons: nil},
		"empty metadata":    TurnModerationMetadataNotification{},
	} {
		if _, err := json.Marshal(value); err == nil {
			t.Errorf("%s marshal succeeded", name)
		}
	}
}

func TestModelSafetyNotificationContractsRemainStandalone(t *testing.T) {
	names := []string{
		"ModelRerouteReason",
		"ModelReroutedNotification",
		"ModelVerification",
		"ModelVerificationNotification",
		"TurnModerationMetadataNotification",
		"ModelSafetyBufferingUpdatedNotification",
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	for _, method := range []string{
		"model/rerouted",
		"model/verification",
		"turn/moderationMetadata",
		"model/safetyBuffering/updated",
	} {
		info, ok := LookupMethod(method)
		if !ok || info.State != MethodBlocked {
			t.Errorf("%s = %#v, %v; want blocked", method, info, ok)
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(WireTypeBindings()); got != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 85/5", got, len(ItemPayloadBindings()))
	}
}

func TestModelSafetyNotificationTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type ModelRerouteReason = "highRiskCyberActivity";`,
		`export type ModelVerification = "trustedAccessForCyber";`,
		`export type ModelReroutedNotification = {`,
		`"reason": ModelRerouteReason;`,
		`export type ModelVerificationNotification = {`,
		`"verifications": Array<ModelVerification>;`,
		`export type TurnModerationMetadataNotification = {`,
		`"metadata": JsonValue;`,
		`export type ModelSafetyBufferingUpdatedNotification = {`,
		`"useCases": Array<string>;`,
		`"reasons": Array<string>;`,
		`"showBufferingUi": boolean;`,
		`"fasterModel": string | null;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}
