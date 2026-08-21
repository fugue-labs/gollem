package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestTopLevelEnvelopeSchemasArePinnedToSource(t *testing.T) {
	definitions := JSONSchema()["$defs"].(Schema)
	for _, fixture := range []struct {
		name   string
		source string
		digest string
		count  int
	}{
		{"ClientRequest", clientRequestSourceSchema, clientRequestSourceSchemaSHA256, 95},
		{"ServerNotification", serverNotificationSourceSchema, serverNotificationSourceSchemaSHA256, 72},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			digest := sha256.Sum256([]byte(fixture.source))
			if got := hex.EncodeToString(digest[:]); got != fixture.digest {
				t.Fatalf("source schema digest = %s, want %s", got, fixture.digest)
			}
			want, err := topLevelEnvelopeSourceSchema(fixture.source)
			if err != nil {
				t.Fatalf("decode source schema: %v", err)
			}
			if got := definitions[fixture.name]; !reflect.DeepEqual(got, want) {
				t.Fatalf("schema = %#v, want source %#v", got, want)
			}
			variants := want["oneOf"].([]any)
			if len(variants) != fixture.count {
				t.Fatalf("variant count = %d, want %d", len(variants), fixture.count)
			}
			assertSchemaRefsResolve(t, want, definitions)
		})
	}
	if got := len(definitions); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestTopLevelEnvelopesPreserveSourceSerdeForms(t *testing.T) {
	clientFixtures := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "null params",
			input: `{"method":"config/mcpServer/reload","id":7,"params":null,"future":true}`,
			want:  `{"method":"config/mcpServer/reload","id":7,"params":null}`,
		},
		{
			name:  "nullable usage params",
			input: `{"method":"account/usage/read","id":"usage","params":{"threadId":"thread","future":true}}`,
			want:  `{"method":"account/usage/read","id":"usage","params":{"threadId":"thread"}}`,
		},
		{
			name:  "initialize",
			input: `{"method":"initialize","id":"initialize","params":{"clientInfo":{"name":"client","version":"1"},"future":true}}`,
			want:  `{"method":"initialize","id":"initialize","params":{"clientInfo":{"name":"client","version":"1"}}}`,
		},
	}
	for _, fixture := range clientFixtures {
		t.Run("client "+fixture.name, func(t *testing.T) {
			var value ClientRequest
			if err := json.Unmarshal([]byte(fixture.input), &value); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			encoded, err := json.Marshal(value)
			if err != nil || string(encoded) != fixture.want {
				t.Fatalf("round trip = %s, %v; want %s", encoded, err, fixture.want)
			}
		})
	}

	serverFixtures := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty leaf and omitted timestamp",
			input: `{"method":"skills/changed","params":{"future":true},"future":true}`,
			want:  `{"method":"skills/changed","params":{}}`,
		},
		{
			name:  "realtime notification and timestamp",
			input: `{"method":"thread/realtime/started","params":{"threadId":"thread","version":"\u00761","future":true},"emittedAtMs":42,"future":true}`,
			want:  `{"method":"thread/realtime/started","params":{"threadId":"thread","realtimeSessionId":null,"version":"v1"},"emittedAtMs":42}`,
		},
		{
			name:  "null timestamp",
			input: `{"method":"windowsSandbox/setupCompleted","params":{"mode":"elevated","success":true,"future":true},"emittedAtMs":null}`,
			want:  `{"method":"windowsSandbox/setupCompleted","params":{"mode":"elevated","success":true,"error":null}}`,
		},
	}
	for _, fixture := range serverFixtures {
		t.Run("server "+fixture.name, func(t *testing.T) {
			var value ServerNotification
			if err := json.Unmarshal([]byte(fixture.input), &value); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			encoded, err := json.Marshal(value)
			if err != nil || string(encoded) != fixture.want {
				t.Fatalf("round trip = %s, %v; want %s", encoded, err, fixture.want)
			}
		})
	}
}

func TestTopLevelEnvelopesRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `{}`, `{"method":null,"id":"request","params":{}}`,
		`{"method":"unknown","id":"request","params":{}}`,
		`{"method":"initialize","id":null,"params":{}}`,
		`{"method":"initialize","id":"request"}`,
		`{"method":"initialize","id":"request","params":null}`,
		`{"method":"config/mcpServer/reload","id":"request","params":{}}`,
		`{"method":"initialize","id":"request","params":{"clientInfo":{}}}`,
		`{"method":"initialize","id":"request","params":{"clientInfo":null}}`,
		`{"method":"initialize","id":"request","params":{"clientInfo":{"name":"client","name":"other","version":"1"}}}`,
		`{"method":"initialize","id":"request","params":{"clientInfo":{"name":"client","version":1}}}`,
		`{"method":"initialize","method":"initialize","id":"request","params":{}}`,
		`{"method":"initialize","id":"request","id":"other","params":{}}`,
		`{"method":"initialize","id":"request","params":{},"params":{}}`,
		`{"method":"initialize","id":"request","params":{}} {}`,
	} {
		assertJSONRejects[ClientRequest](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `{}`, `{"method":null,"params":{}}`,
		`{"method":"unknown","params":{}}`,
		`{"method":"skills/changed"}`,
		`{"method":"skills/changed","params":null}`,
		`{"method":"thread/realtime/started","params":{"threadId":"thread","version":"v9"}}`,
		`{"method":"thread/realtime/started","params":{"threadId":"thread","threadId":"other","version":"v1"}}`,
		`{"method":"skills/changed","params":{},"emittedAtMs":1.5}`,
		`{"method":"skills/changed","params":{},"emittedAtMs":9223372036854775808}`,
		`{"method":"skills/changed","method":"skills/changed","params":{}}`,
		`{"method":"skills/changed","params":{},"params":{}}`,
		`{"method":"skills/changed","params":{},"emittedAtMs":1,"emittedAtMs":2}`,
		`{"method":"skills/changed","params":{}} {}`,
	} {
		assertJSONRejects[ServerNotification](t, input)
	}
}

func TestTopLevelEnvelopesRemainStandalone(t *testing.T) {
	var clientRequest *ClientRequest
	if err := clientRequest.UnmarshalJSON([]byte(`{"method":"config/mcpServer/reload","id":1,"params":null}`)); err == nil {
		t.Fatal("nil ClientRequest receiver succeeded")
	}
	var serverNotification *ServerNotification
	if err := serverNotification.UnmarshalJSON([]byte(`{"method":"skills/changed","params":{}}`)); err == nil {
		t.Fatal("nil ServerNotification receiver succeeded")
	}
	if _, err := json.Marshal(ClientRequest{Method: "config/mcpServer/reload", Params: json.RawMessage("null")}); err == nil {
		t.Fatal("ClientRequest without id marshaled")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			if name == "ClientRequest" || name == "ServerNotification" {
				t.Fatalf("standalone envelope unexpectedly bound to %s", binding.Method)
			}
		}
	}
	for _, method := range Methods() {
		if method.Method == "ClientRequest" || method.Method == "ServerNotification" {
			t.Fatalf("standalone envelope unexpectedly added method %s", method.Method)
		}
	}
}

func TestTopLevelEnvelopeTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`export type ClientRequest = { "method": "initialize"; "id": RequestId; "params": InitializeParams; }`,
		`{ "method": "config/mcpServer/reload"; "id": RequestId; "params": null; }`,
		`export type ServerNotification = { "emittedAtMs"?: number; } & ({ "method": "error"; "params": ErrorNotification; }`,
		`{ "method": "thread/realtime/started"; "params": ThreadRealtimeStartedNotification; }`,
	} {
		if !strings.Contains(string(generated), want) {
			t.Fatalf("generated TypeScript missing %q", want)
		}
	}
}
