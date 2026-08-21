package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestServerRequestSchemaAndBindingAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	definition, ok := defs["ServerRequest"].(Schema)
	if !ok {
		t.Fatal("$defs missing ServerRequest")
	}
	if definition["title"] != "ServerRequest" {
		t.Fatalf("ServerRequest title = %#v", definition["title"])
	}
	if _, present := definition["type"]; present {
		t.Fatalf("ServerRequest unexpectedly has a top-level type: %#v", definition)
	}
	variants, ok := definition["oneOf"].([]any)
	if !ok || len(variants) != len(serverRequestVariants) {
		t.Fatalf("ServerRequest variants = %#v", definition["oneOf"])
	}
	for index, want := range serverRequestVariants {
		variant := variants[index].(Schema)
		if variant["title"] != want.Title {
			t.Fatalf("variant %d = %#v, want %s/%q", index, variant, want.Title, want.Description)
		}
		if want.Description == "" {
			if _, present := variant["description"]; present {
				t.Fatalf("variant %s unexpectedly has a description: %#v", want.Method, variant)
			}
		} else if variant["description"] != want.Description {
			t.Fatalf("variant %d description = %#v, want %q", index, variant["description"], want.Description)
		}
		if _, closed := variant["additionalProperties"]; closed {
			t.Fatalf("variant %s unexpectedly closes the serde-open record", want.Method)
		}
		assertSchemaRequired(t, variant, "id", "method", "params")
		properties := variant["properties"].(Schema)
		if properties["id"].(Schema)["$ref"] != "#/$defs/RequestId" ||
			properties["method"].(Schema)["title"] != want.Title+"Method" ||
			properties["method"].(Schema)["enum"].([]any)[0] != want.Method ||
			properties["params"].(Schema)["$ref"] != "#/$defs/"+want.ParamType {
			t.Fatalf("variant %s properties = %#v", want.Method, properties)
		}
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			if name == "ServerRequest" {
				t.Fatalf("ServerRequest unexpectedly bound to %s", binding.Method)
			}
		}
	}
	if got := len(defs); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	typescript, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`export type ServerRequest = { "method": "item/commandExecution/requestApproval"; "id": RequestId; "params": CommandExecutionRequestApprovalParams; }`,
		`{ "method": "mcpServer/elicitation/request"; "id": RequestId; "params": McpServerElicitationRequestParams; }`,
		`{ "method": "execCommandApproval"; "id": RequestId; "params": ExecCommandApprovalParams; };`,
	} {
		if !strings.Contains(string(typescript), want) {
			t.Fatalf("generated TypeScript missing %q", want)
		}
	}
}

func TestServerRequestUsesSourceSerdeSemantics(t *testing.T) {
	for _, test := range []struct {
		method string
		params string
	}{
		{"item/commandExecution/requestApproval", `{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1}`},
		{"item/fileChange/requestApproval", `{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1}`},
		{"item/tool/requestUserInput", `{"threadId":"thread","turnId":"turn","itemId":"item","questions":[{"id":"question","header":"Header","question":"Question","isOther":false,"isSecret":false,"options":[]}],"isBlocking":true}`},
		{"mcpServer/elicitation/request", `{"threadId":"thread","serverName":"server","mode":"form","_meta":null,"message":"Choose","requestedSchema":{"type":"object","properties":{}}}`},
		{"item/permissions/requestApproval", `{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":1,"cwd":"/workspace","permissions":{}}`},
		{"item/tool/call", `{"threadId":"thread","turnId":"turn","callId":"call","tool":"client.search","arguments":null}`},
		{"account/chatgptAuthTokens/refresh", `{"reason":"unauthorized"}`},
		{"attestation/generate", `{}`},
		{"applyPatchApproval", `{"conversationId":"thread","callId":"call","fileChanges":{}}`},
		{"execCommandApproval", `{"conversationId":"thread","callId":"call","command":[],"cwd":".","parsedCmd":[]}`},
	} {
		t.Run(test.method, func(t *testing.T) {
			input := `{"method":` + quoteServerRequestString(test.method) + `,"id":42,"params":` + test.params + `,"unknown":true}`
			var request ServerRequest
			if err := json.Unmarshal([]byte(input), &request); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if request.Method != test.method || request.ID.Value() == nil || !json.Valid(request.Params) {
				t.Fatalf("decoded request = %#v", request)
			}
			encoded, err := json.Marshal(request)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var canonical map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &canonical); err != nil {
				t.Fatalf("decode canonical request: %v", err)
			}
			if len(canonical) != 3 || canonical["method"] == nil || canonical["id"] == nil || canonical["params"] == nil {
				t.Fatalf("canonical request = %s", encoded)
			}
		})
	}

	var request ServerRequest
	if err := json.Unmarshal([]byte(`{"method":"attestation/generate","id":"request","params":{"unknown":true},"unknown":true}`), &request); err != nil {
		t.Fatalf("Unmarshal source-open request: %v", err)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("Marshal source-open request: %v", err)
	}
	if string(encoded) != `{"method":"attestation/generate","id":"request","params":{}}` {
		t.Fatalf("canonical request = %s", encoded)
	}

	for _, input := range []string{
		`{}`,
		`{"method":"unknown","id":"request","params":{}}`,
		`{"method":"attestation/generate","id":null,"params":{}}`,
		`{"method":"attestation/generate","id":"request","params":null}`,
		`{"method":"attestation/generate","method":"item/tool/call","id":"request","params":{}}`,
		`{"method":"attestation/generate","id":"request","params":{}} null`,
	} {
		if err := json.Unmarshal([]byte(input), &request); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}
	var nilRequest *ServerRequest
	if err := nilRequest.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ServerRequest receiver succeeded")
	}
}

func quoteServerRequestString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
