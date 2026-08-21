package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestNetworkApprovalContextSchemaIsExact(t *testing.T) {
	definition, ok := JSONSchema()["$defs"].(Schema)["NetworkApprovalContext"].(Schema)
	if !ok {
		t.Fatal("$defs missing NetworkApprovalContext")
	}
	want := Schema{
		"type": "object",
		"properties": Schema{
			"host":     Schema{"type": "string"},
			"protocol": Schema{"$ref": "#/$defs/NetworkApprovalProtocol"},
		},
		"required":             []string{"host", "protocol"},
		"additionalProperties": false,
	}
	if !reflect.DeepEqual(definition, want) {
		t.Fatalf("NetworkApprovalContext = %#v, want %#v", definition, want)
	}
}

func TestNetworkApprovalContextAcceptsSerdeWireForms(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: `{"host":"","protocol":"http"}`, want: `{"host":"","protocol":"http"}`},
		{input: `{"host":"example.com:443","protocol":"https"}`, want: `{"host":"example.com:443","protocol":"https"}`},
		{input: `{"host":"[::1]","protocol":"socks5Tcp"}`, want: `{"host":"[::1]","protocol":"socks5Tcp"}`},
		{input: `{"future":true,"host":"arbitrary host","protocol":"socks5Udp"}`, want: `{"host":"arbitrary host","protocol":"socks5Udp"}`},
		{input: `{"host":"host","protocol":"http","future":1,"future":2}`, want: `{"host":"host","protocol":"http"}`},
	} {
		var context NetworkApprovalContext
		if err := json.Unmarshal([]byte(test.input), &context); err != nil {
			t.Errorf("Unmarshal(%s): %v", test.input, err)
			continue
		}
		encoded, err := json.Marshal(context)
		if err != nil || string(encoded) != test.want {
			t.Errorf("round trip %s = %s, %v; want %s", test.input, encoded, err, test.want)
		}
	}
}

func TestNetworkApprovalContextRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"protocol":"http"}`,
		`{"host":"host"}`,
		`{"host":null,"protocol":"http"}`,
		`{"host":1,"protocol":"http"}`,
		`{"host":"host","protocol":null}`,
		`{"host":"host","protocol":true}`,
		`{"host":"host","protocol":"ftp"}`,
		`{"host":"one","host":"two","protocol":"http"}`,
		`{"host":"host","protocol":"http","protocol":"https"}`,
		`{"host":"host","protocol":"http"} {}`,
		`{"host":"host","protocol":"http"} x`,
	} {
		assertJSONRejects[NetworkApprovalContext](t, input)
	}

	var context *NetworkApprovalContext
	if err := context.UnmarshalJSON([]byte(`{"host":"host","protocol":"http"}`)); err == nil {
		t.Fatal("nil NetworkApprovalContext receiver succeeded")
	}
}

func TestNetworkApprovalContextRemainsStandalone(t *testing.T) {
	if _, ok := reflect.TypeFor[CommandExecutionApprovalRequestParams]().FieldByName("NetworkApprovalContext"); ok {
		t.Fatal("live command approval request unexpectedly embeds NetworkApprovalContext")
	}
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "NetworkApprovalContext") ||
			slices.Contains(binding.Result, "NetworkApprovalContext") {
			t.Fatalf("NetworkApprovalContext unexpectedly bound to %s", binding.Method)
		}
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

func TestNetworkApprovalContextTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := "export type NetworkApprovalContext = {\n" +
		"  \"host\": string;\n" +
		"  \"protocol\": NetworkApprovalProtocol;\n" +
		"};"
	if !strings.Contains(string(generated), want) {
		t.Fatalf("generated TypeScript missing %q", want)
	}
}
