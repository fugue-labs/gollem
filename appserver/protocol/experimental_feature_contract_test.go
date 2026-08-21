package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestExperimentalFeatureSchemasAreExact(t *testing.T) {
	want := experimentalFeatureSchemas()
	definitions := JSONSchema()["$defs"].(Schema)
	for _, name := range []string{
		"ExperimentalFeatureStage", "ExperimentalFeature", "ExperimentalFeatureListParams",
		"ExperimentalFeatureListResponse", "ExperimentalFeatureEnablementSetParams",
		"ExperimentalFeatureEnablementSetResponse",
	} {
		if got := definitions[name]; !reflect.DeepEqual(got, want[name]) {
			t.Errorf("%s = %#v, want %#v", name, got, want[name])
		}
	}
}

func TestExperimentalFeatureContractsRemainStandalone(t *testing.T) {
	names := []string{
		"ExperimentalFeatureStage", "ExperimentalFeature", "ExperimentalFeatureListParams",
		"ExperimentalFeatureListResponse", "ExperimentalFeatureEnablementSetParams",
		"ExperimentalFeatureEnablementSetResponse",
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	for _, methodName := range []string{"experimentalFeature/list", "experimentalFeature/enablement/set"} {
		method, ok := LookupMethod(methodName)
		if !ok || method.Surface != SurfaceClientRequest || method.State != MethodImplemented {
			t.Fatalf("%s = %#v, %v; want implemented client request", methodName, method, ok)
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if len(Methods()) != 229 || len(WireTypeBindings()) != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("counts = %d/%d/%d, want 229/85/5", len(Methods()), len(WireTypeBindings()), len(ItemPayloadBindings()))
	}
}

func TestExperimentalFeatureTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`export type ExperimentalFeatureStage = "beta" | "underDevelopment" | "stable" | "deprecated" | "removed";`,
		`export type ExperimentalFeatureEnablementSetParams = {`,
		`export type ExperimentalFeatureEnablementSetResponse = {`,
		`export type ExperimentalFeatureListParams = {`,
		`export type ExperimentalFeatureListResponse = {`,
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func TestExperimentalFeatureStageIsClosed(t *testing.T) {
	for _, value := range []string{"beta", "underDevelopment", "stable", "deprecated", "removed"} {
		roundTripExperimentalFeature[ExperimentalFeatureStage](t, `"`+value+`"`, `"`+value+`"`)
	}
	for _, input := range []string{``, `null`, `1`, `true`, `[]`, `{}`, `"unknown"`, `"beta" "stable"`} {
		assertJSONRejects[ExperimentalFeatureStage](t, input)
	}
	if _, err := json.Marshal(ExperimentalFeatureStage("unknown")); err == nil {
		t.Fatal("invalid experimental feature stage marshaled")
	}
	var receiver *ExperimentalFeatureStage
	if err := receiver.UnmarshalJSON([]byte(`"beta"`)); err == nil {
		t.Fatal("nil stage receiver succeeded")
	}
}

func TestExperimentalFeaturePreservesSerdeWireForms(t *testing.T) {
	canonicalNulls := `{"name":"","stage":"beta","displayName":null,"description":null,"announcement":null,"enabled":false,"defaultEnabled":false}`
	roundTripExperimentalFeature[ExperimentalFeature](t,
		`{"name":"","stage":"beta","enabled":false,"defaultEnabled":false}`,
		canonicalNulls,
	)
	roundTripExperimentalFeature[ExperimentalFeature](t,
		`{"future":1,"future":2,"name":" name ","stage":"removed","displayName":"","description":" desc ","announcement":" note ","enabled":true,"defaultEnabled":false}`,
		`{"name":" name ","stage":"removed","displayName":"","description":" desc ","announcement":" note ","enabled":true,"defaultEnabled":false}`,
	)
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"stage":"beta","enabled":false,"defaultEnabled":false}`,
		`{"name":"x","enabled":false,"defaultEnabled":false}`,
		`{"name":"x","stage":"beta","defaultEnabled":false}`,
		`{"name":"x","stage":"beta","enabled":false}`,
		`{"name":null,"stage":"beta","enabled":false,"defaultEnabled":false}`,
		`{"name":"x","stage":"unknown","enabled":false,"defaultEnabled":false}`,
		`{"name":"x","stage":"beta","displayName":1,"enabled":false,"defaultEnabled":false}`,
		`{"name":"x","stage":"beta","description":true,"enabled":false,"defaultEnabled":false}`,
		`{"name":"x","stage":"beta","announcement":[],"enabled":false,"defaultEnabled":false}`,
		`{"name":"x","stage":"beta","enabled":0,"defaultEnabled":false}`,
		`{"name":"x","stage":"beta","enabled":false,"defaultEnabled":null}`,
		`{"name":"a","name":"b","stage":"beta","enabled":false,"defaultEnabled":false}`,
		`{"name":"x","stage":"beta","stage":"stable","enabled":false,"defaultEnabled":false}`,
		`{"name":"x","stage":"beta","enabled":false,"enabled":true,"defaultEnabled":false}`,
		`{"name":"x","stage":"beta","enabled":false,"defaultEnabled":false} {}`,
	} {
		assertJSONRejects[ExperimentalFeature](t, input)
	}
	var receiver *ExperimentalFeature
	if err := receiver.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil feature receiver succeeded")
	}
}

func TestExperimentalFeatureListParamsPreserveOptionalNullableBoundaries(t *testing.T) {
	roundTripExperimentalFeature[ExperimentalFeatureListParams](t, `{}`, `{}`)
	roundTripExperimentalFeature[ExperimentalFeatureListParams](t,
		`{"cursor":null,"limit":null,"threadId":null}`,
		`{}`,
	)
	roundTripExperimentalFeature[ExperimentalFeatureListParams](t,
		`{"future":1,"cursor":" cursor ","limit":4294967295,"threadId":" thread "}`,
		`{"cursor":" cursor ","limit":4294967295,"threadId":" thread "}`,
	)
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`,
		`{"cursor":1}`, `{"threadId":false}`, `{"limit":-1}`, `{"limit":4294967296}`,
		`{"limit":1.5}`, `{"limit":"1"}`, `{"cursor":null,"cursor":"x"}`,
		`{"threadId":"x","threadId":"y"}`, `{} {}`,
	} {
		assertJSONRejects[ExperimentalFeatureListParams](t, input)
	}
	var receiver *ExperimentalFeatureListParams
	if err := receiver.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil list params receiver succeeded")
	}
}

func TestExperimentalFeatureListResponsePreservesOrderAndNulls(t *testing.T) {
	feature := `{"name":"x","stage":"stable","enabled":true,"defaultEnabled":false}`
	canonical := `{"name":"x","stage":"stable","displayName":null,"description":null,"announcement":null,"enabled":true,"defaultEnabled":false}`
	roundTripExperimentalFeature[ExperimentalFeatureListResponse](t,
		`{"data":[]}`,
		`{"data":[],"nextCursor":null}`,
	)
	roundTripExperimentalFeature[ExperimentalFeatureListResponse](t,
		`{"future":1,"data":[`+feature+`,`+feature+`],"nextCursor":" next "}`,
		`{"data":[`+canonical+`,`+canonical+`],"nextCursor":" next "}`,
	)
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"data":null}`, `{"data":{}}`, `{"data":[null]}`, `{"data":[{}]}`,
		`{"data":[],"nextCursor":1}`, `{"data":[],"data":[]}`, `{"data":[]} {}`,
	} {
		assertJSONRejects[ExperimentalFeatureListResponse](t, input)
	}
	if _, err := json.Marshal(ExperimentalFeatureListResponse{}); err == nil {
		t.Fatal("nil feature data marshaled")
	}
	var receiver *ExperimentalFeatureListResponse
	if err := receiver.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil list response receiver succeeded")
	}
}

func TestExperimentalFeatureEnablementMapsAreStrictAndLastWins(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(*testing.T, string, string)
	}{
		{"params", roundTripExperimentalFeature[ExperimentalFeatureEnablementSetParams]},
		{"response", roundTripExperimentalFeature[ExperimentalFeatureEnablementSetResponse]},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.run(t, `{"enablement":{}}`, `{"enablement":{}}`)
			test.run(t,
				`{"future":1,"enablement":{"":true,"feature":false,"feature":true}}`,
				`{"enablement":{"":true,"feature":true}}`,
			)
		})
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`, `{"enablement":null}`,
		`{"enablement":[]}`, `{"enablement":{"feature":"true"}}`,
		`{"enablement":{},"enablement":{}}`, `{"enablement":{}} {}`,
	} {
		assertJSONRejects[ExperimentalFeatureEnablementSetParams](t, input)
		assertJSONRejects[ExperimentalFeatureEnablementSetResponse](t, input)
	}
	if _, err := json.Marshal(ExperimentalFeatureEnablementSetParams{}); err == nil {
		t.Fatal("nil params map marshaled")
	}
	if _, err := json.Marshal(ExperimentalFeatureEnablementSetResponse{}); err == nil {
		t.Fatal("nil response map marshaled")
	}
	var params *ExperimentalFeatureEnablementSetParams
	if err := params.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil params receiver succeeded")
	}
	var response *ExperimentalFeatureEnablementSetResponse
	if err := response.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil response receiver succeeded")
	}
}

func roundTripExperimentalFeature[T any](t *testing.T, input, want string) {
	t.Helper()
	var value T
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("unmarshal %s: %v", input, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %s = %s, %v; want %s", input, encoded, err, want)
	}
}
