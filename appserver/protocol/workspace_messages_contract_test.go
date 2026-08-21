package protocol

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestWorkspaceMessageSchemasAreExact(t *testing.T) {
	want := map[string]Schema{
		"WorkspaceMessageType": stringEnumSchema("headline", "announcement", "unknown"),
		"WorkspaceMessage": {
			"type": "object",
			"properties": Schema{
				"archivedAt": Schema{
					"description": "Unix timestamp (in seconds) when the message was archived.",
					"format":      "int64", "type": []any{"integer", "null"},
				},
				"createdAt": Schema{
					"description": "Unix timestamp (in seconds) when the message was created.",
					"format":      "int64", "type": []any{"integer", "null"},
				},
				"messageBody": Schema{"type": "string"},
				"messageId":   Schema{"type": "string"},
				"messageType": Schema{"$ref": "#/$defs/WorkspaceMessageType"},
			},
			"required": []string{"messageBody", "messageId", "messageType"},
		},
		"GetWorkspaceMessagesResponse": {
			"type": "object",
			"properties": Schema{
				"featureEnabled": Schema{
					"description": "Whether the workspace-message backend route is available for this client.",
					"type":        "boolean",
				},
				"messages": Schema{
					"description": "Active workspace messages returned by the backend.",
					"items":       Schema{"$ref": "#/$defs/WorkspaceMessage"}, "type": "array",
				},
			},
			"required": []string{"featureEnabled", "messages"},
		},
	}
	definitions := JSONSchema()["$defs"].(Schema)
	for name, expected := range want {
		if got := definitions[name]; !reflect.DeepEqual(got, expected) {
			t.Errorf("%s = %#v, want %#v", name, got, expected)
		}
	}
}

func TestWorkspaceMessageTypePreservesSerdeFallback(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`"headline"`, `"headline"`},
		{`"announcement"`, `"announcement"`},
		{`"unknown"`, `"unknown"`},
		{`"future"`, `"unknown"`},
		{`""`, `"unknown"`},
	} {
		var value WorkspaceMessageType
		if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
	for _, input := range []string{``, `null`, `1`, `true`, `[]`, `{}`, `"headline" "announcement"`} {
		assertJSONRejects[WorkspaceMessageType](t, input)
	}
	if _, err := json.Marshal(WorkspaceMessageType("future")); err == nil {
		t.Fatal("invalid workspace message type marshaled")
	}
}

func TestWorkspaceMessageAcceptsExactSerdeWireForms(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{
			`{"messageId":"","messageType":"headline","messageBody":""}`,
			`{"messageId":"","messageType":"headline","messageBody":"","createdAt":null,"archivedAt":null}`,
		},
		{
			`{"future":1,"future":2,"messageId":" id ","messageType":"future","messageBody":" body ","createdAt":null,"archivedAt":null}`,
			`{"messageId":" id ","messageType":"unknown","messageBody":" body ","createdAt":null,"archivedAt":null}`,
		},
		{
			`{"messageId":"id","messageType":"announcement","messageBody":"body","createdAt":-9223372036854775808,"archivedAt":9223372036854775807}`,
			`{"messageId":"id","messageType":"announcement","messageBody":"body","createdAt":-9223372036854775808,"archivedAt":9223372036854775807}`,
		},
	} {
		var value WorkspaceMessage
		if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
	min, max := int64(math.MinInt64), int64(math.MaxInt64)
	encoded, err := json.Marshal(WorkspaceMessage{
		MessageID: "id", MessageType: WorkspaceMessageTypeHeadline, MessageBody: "body",
		CreatedAt: &min, ArchivedAt: &max,
	})
	if err != nil || !strings.Contains(string(encoded), `"createdAt":-9223372036854775808`) ||
		!strings.Contains(string(encoded), `"archivedAt":9223372036854775807`) {
		t.Fatalf("boundary message = %s, %v", encoded, err)
	}
}

func TestWorkspaceMessageRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"messageType":"headline","messageBody":"body"}`,
		`{"messageId":"id","messageBody":"body"}`,
		`{"messageId":"id","messageType":"headline"}`,
		`{"messageId":null,"messageType":"headline","messageBody":"body"}`,
		`{"messageId":"id","messageType":null,"messageBody":"body"}`,
		`{"messageId":"id","messageType":"headline","messageBody":null}`,
		`{"messageId":"id","messageType":"headline","messageBody":"body","createdAt":"0"}`,
		`{"messageId":"id","messageType":"headline","messageBody":"body","createdAt":1.5}`,
		`{"messageId":"id","messageType":"headline","messageBody":"body","createdAt":9223372036854775808}`,
		`{"messageId":"id","messageType":"headline","messageBody":"body","archivedAt":-9223372036854775809}`,
		`{"messageId":"a","messageId":"b","messageType":"headline","messageBody":"body"}`,
		`{"messageId":"id","messageType":"headline","messageType":"announcement","messageBody":"body"}`,
		`{"messageId":"id","messageType":"headline","messageBody":"a","messageBody":"b"}`,
		`{"messageId":"id","messageType":"headline","messageBody":"body","createdAt":null,"createdAt":0}`,
		`{"messageId":"id","messageType":"headline","messageBody":"body"} {}`,
		`{"messageId":"id","messageType":"headline","messageBody":"body"} x`,
	} {
		assertJSONRejects[WorkspaceMessage](t, input)
	}
}

func TestGetWorkspaceMessagesResponsePreservesOrderedStrictMessages(t *testing.T) {
	message := `{"messageId":"id","messageType":"headline","messageBody":"body"}`
	canonical := `{"messageId":"id","messageType":"headline","messageBody":"body","createdAt":null,"archivedAt":null}`
	for _, tc := range []struct{ input, want string }{
		{`{"featureEnabled":false,"messages":[]}`, `{"featureEnabled":false,"messages":[]}`},
		{`{"future":1,"future":2,"featureEnabled":true,"messages":[` + message + `]}`, `{"featureEnabled":true,"messages":[` + canonical + `]}`},
		{`{"featureEnabled":true,"messages":[` + message + `,` + message + `]}`, `{"featureEnabled":true,"messages":[` + canonical + `,` + canonical + `]}`},
	} {
		var value GetWorkspaceMessagesResponse
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

func TestGetWorkspaceMessagesResponseRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"messages":[]}`, `{"featureEnabled":false}`,
		`{"featureEnabled":null,"messages":[]}`, `{"featureEnabled":0,"messages":[]}`,
		`{"featureEnabled":false,"messages":null}`, `{"featureEnabled":false,"messages":{}}`,
		`{"featureEnabled":false,"messages":[null]}`, `{"featureEnabled":false,"messages":[{}]}`,
		`{"featureEnabled":false,"featureEnabled":true,"messages":[]}`,
		`{"featureEnabled":false,"messages":[],"messages":[]}`,
		`{"featureEnabled":false,"messages":[]} {}`, `{"featureEnabled":false,"messages":[]} x`,
	} {
		assertJSONRejects[GetWorkspaceMessagesResponse](t, input)
	}
}

func TestWorkspaceMessageNilReceiversAndInvalidMarshal(t *testing.T) {
	var messageType *WorkspaceMessageType
	if err := messageType.UnmarshalJSON([]byte(`"headline"`)); err == nil {
		t.Fatal("nil WorkspaceMessageType receiver succeeded")
	}
	var message *WorkspaceMessage
	if err := message.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil WorkspaceMessage receiver succeeded")
	}
	var response *GetWorkspaceMessagesResponse
	if err := response.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil GetWorkspaceMessagesResponse receiver succeeded")
	}
	if _, err := json.Marshal(GetWorkspaceMessagesResponse{}); err == nil {
		t.Fatal("nil message array marshaled")
	}
}

func TestWorkspaceMessageContractsRemainStandaloneAndDeferred(t *testing.T) {
	names := []string{"WorkspaceMessageType", "WorkspaceMessage", "GetWorkspaceMessagesResponse"}
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
	method, ok := LookupMethod("account/workspaceMessages/read")
	if !ok || method.Surface != SurfaceClientRequest || method.State != MethodDeferredStub {
		t.Fatalf("account/workspaceMessages/read = %#v, %v; want deferred client request", method, ok)
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if len(Methods()) != 229 || len(WireTypeBindings()) != 86 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("counts = %d methods/%d method bindings/%d item bindings; want 229/86/5", len(Methods()), len(WireTypeBindings()), len(ItemPayloadBindings()))
	}
}

func TestWorkspaceMessageTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type WorkspaceMessageType = "headline" | "announcement" | "unknown";`,
		"export type WorkspaceMessage = {\n  \"archivedAt\": number | null;\n  \"createdAt\": number | null;\n  \"messageBody\": string;\n  \"messageId\": string;\n  \"messageType\": WorkspaceMessageType;\n};",
		"export type GetWorkspaceMessagesResponse = {\n  \"featureEnabled\": boolean;\n  \"messages\": Array<WorkspaceMessage>;\n};",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}
