package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestReviewStartSchemasAreExact(t *testing.T) {
	definitions := JSONSchema()["$defs"].(Schema)
	wants := map[string]Schema{
		"ReviewDelivery": stringEnumSchema("inline", "detached"),
		"ReviewTarget": {
			"oneOf": []any{
				Schema{
					"type": "object",
					"properties": Schema{
						"type": Schema{"type": "string", "enum": []any{"uncommittedChanges"}},
					},
					"required": []string{"type"},
				},
				Schema{
					"type": "object",
					"properties": Schema{
						"branch": Schema{"type": "string"},
						"type":   Schema{"type": "string", "enum": []any{"baseBranch"}},
					},
					"required": []string{"branch", "type"},
				},
				Schema{
					"type": "object",
					"properties": Schema{
						"sha":   Schema{"type": "string"},
						"title": Schema{"type": []any{"string", "null"}},
						"type":  Schema{"type": "string", "enum": []any{"commit"}},
					},
					"required": []string{"sha", "type"},
				},
				Schema{
					"type": "object",
					"properties": Schema{
						"instructions": Schema{"type": "string"},
						"type":         Schema{"type": "string", "enum": []any{"custom"}},
					},
					"required": []string{"instructions", "type"},
				},
			},
		},
		"ReviewStartParams": {
			"type": "object",
			"properties": Schema{
				"delivery": Schema{"anyOf": []any{
					Schema{"$ref": "#/$defs/ReviewDelivery"}, Schema{"type": "null"},
				}},
				"target":   Schema{"$ref": "#/$defs/ReviewTarget"},
				"threadId": Schema{"type": "string"},
			},
			"required": []string{"target", "threadId"},
		},
		"ReviewStartResponse": {
			"type": "object",
			"properties": Schema{
				"reviewThreadId": Schema{"type": "string"},
				"turn":           Schema{"$ref": "#/$defs/Turn"},
			},
			"required": []string{"reviewThreadId", "turn"},
		},
	}
	for name, want := range wants {
		if got := definitions[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestReviewStartContractsPreserveSerdeWireForms(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`"inline"`, `"inline"`},
		{`"detached"`, `"detached"`},
	} {
		var delivery ReviewDelivery
		if err := json.Unmarshal([]byte(tc.input), &delivery); err != nil {
			t.Errorf("unmarshal delivery %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(delivery)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("delivery round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}

	for _, tc := range []struct{ input, want string }{
		{`{"type":"uncommittedChanges"}`, `{"type":"uncommittedChanges"}`},
		{`{"future":true,"type":"baseBranch","branch":" main "}`, `{"type":"baseBranch","branch":" main "}`},
		{`{"type":"commit","sha":"deadbeef"}`, `{"type":"commit","sha":"deadbeef","title":null}`},
		{`{"type":"commit","sha":"deadbeef","title":" subject "}`, `{"type":"commit","sha":"deadbeef","title":" subject "}`},
		{`{"type":"custom","instructions":""}`, `{"type":"custom","instructions":""}`},
	} {
		var target ReviewTarget
		if err := json.Unmarshal([]byte(tc.input), &target); err != nil {
			t.Errorf("unmarshal target %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(target)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("target round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}

	for _, tc := range []struct{ input, want string }{
		{
			`{"threadId":"","target":{"type":"uncommittedChanges"}}`,
			`{"threadId":"","target":{"type":"uncommittedChanges"},"delivery":null}`,
		},
		{
			`{"future":true,"threadId":"thread","target":{"type":"commit","sha":"deadbeef"},"delivery":"detached"}`,
			`{"threadId":"thread","target":{"type":"commit","sha":"deadbeef","title":null},"delivery":"detached"}`,
		},
	} {
		var params ReviewStartParams
		if err := json.Unmarshal([]byte(tc.input), &params); err != nil {
			t.Errorf("unmarshal params %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(params)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("params round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}

	turn := `{"id":"turn","items":[],"itemsView":"full","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null}`
	var response ReviewStartResponse
	if err := json.Unmarshal([]byte(`{"future":true,"turn":`+turn+`,"reviewThreadId":"review-thread"}`), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	encoded, err := json.Marshal(response)
	want := `{"turn":` + turn + `,"reviewThreadId":"review-thread"}`
	if err != nil || string(encoded) != want {
		t.Fatalf("response round trip = %s, %v; want %s", encoded, err, want)
	}
}

func TestReviewStartContractsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `""`, `"other"`, `1`, `true`, `{}`, `"inline" {}`,
	} {
		assertJSONRejects[ReviewDelivery](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"type":null}`, `{"type":"other"}`, `{"type":"baseBranch"}`,
		`{"type":"baseBranch","branch":null}`, `{"type":"commit"}`, `{"type":"commit","sha":null}`,
		`{"type":"commit","sha":"a","title":1}`, `{"type":"custom"}`,
		`{"type":"custom","instructions":null}`, `{"type":"a","type":"custom","instructions":"x"}`,
		`{"type":"baseBranch","branch":"a","branch":"b"}`, `{"type":"custom","instructions":"x"} {}`,
	} {
		assertJSONRejects[ReviewTarget](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`, `{"threadId":"thread"}`,
		`{"target":{"type":"uncommittedChanges"}}`, `{"threadId":null,"target":{"type":"uncommittedChanges"}}`,
		`{"threadId":"thread","target":null}`, `{"threadId":"thread","target":{"type":"uncommittedChanges"},"delivery":"other"}`,
		`{"threadId":"a","threadId":"b","target":{"type":"uncommittedChanges"}}`,
		`{"threadId":"thread","target":{"type":"uncommittedChanges"},"delivery":null,"delivery":"inline"}`,
		`{"threadId":"thread","target":{"type":"uncommittedChanges"}} {}`,
	} {
		assertJSONRejects[ReviewStartParams](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`, `{"reviewThreadId":"thread"}`,
		`{"turn":{"id":"turn","items":[],"itemsView":"full","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null}}`,
		`{"turn":null,"reviewThreadId":"thread"}`, `{"turn":{"id":"turn","items":[],"itemsView":"full","status":"completed"},"reviewThreadId":"thread"}`,
		`{"turn":{"id":"turn","items":[],"itemsView":"full","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null},"reviewThreadId":null}`,
		`{"reviewThreadId":"a","reviewThreadId":"b","turn":{"id":"turn","items":[],"itemsView":"full","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null}}`,
	} {
		assertJSONRejects[ReviewStartResponse](t, input)
	}
}

func TestReviewStartContractsFailClosedAndRemainStandalone(t *testing.T) {
	var delivery *ReviewDelivery
	if err := delivery.UnmarshalJSON([]byte(`"inline"`)); err == nil {
		t.Fatal("nil delivery receiver succeeded")
	}
	var target *ReviewTarget
	if err := target.UnmarshalJSON([]byte(`{"type":"uncommittedChanges"}`)); err == nil {
		t.Fatal("nil target receiver succeeded")
	}
	var params *ReviewStartParams
	if err := params.UnmarshalJSON([]byte(`{"threadId":"thread","target":{"type":"uncommittedChanges"}}`)); err == nil {
		t.Fatal("nil params receiver succeeded")
	}
	var response *ReviewStartResponse
	if err := response.UnmarshalJSON([]byte(`{"turn":{"id":"turn","items":[],"itemsView":"full","status":"completed","error":null,"startedAt":null,"completedAt":null,"durationMs":null},"reviewThreadId":"thread"}`)); err == nil {
		t.Fatal("nil response receiver succeeded")
	}
	if _, err := json.Marshal(ReviewDelivery("other")); err == nil {
		t.Fatal("invalid delivery marshaled")
	}
	if _, err := json.Marshal(ReviewTarget{}); err == nil {
		t.Fatal("empty target marshaled")
	}

	names := []string{"ReviewDelivery", "ReviewTarget", "ReviewStartParams", "ReviewStartResponse"}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	request, ok := LookupMethod("review/start")
	if !ok || request.Surface != SurfaceClientRequest || request.State != MethodBlocked {
		t.Fatalf("review/start = %#v, %v; want blocked client request", request, ok)
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestReviewStartTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`export type ReviewDelivery = "inline" | "detached";`,
		"export type ReviewStartParams = {\n" +
			"  \"delivery\"?: ReviewDelivery | null;\n" +
			"  \"target\": ReviewTarget;\n" +
			"  \"threadId\": string;\n" +
			"};",
		"export type ReviewStartResponse = {\n" +
			"  \"reviewThreadId\": string;\n" +
			"  \"turn\": Turn;\n" +
			"};",
		"  \"title\": string | null;",
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}
