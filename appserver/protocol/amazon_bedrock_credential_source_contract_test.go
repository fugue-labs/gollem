package protocol

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestAmazonBedrockCredentialSourceSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	assertStringEnum(t, defs["AmazonBedrockCredentialSource"], "codexManaged", "awsManaged")
}

func TestAmazonBedrockCredentialSourceAcceptsExactValues(t *testing.T) {
	for _, input := range []string{`"codexManaged"`, `"awsManaged"`} {
		var source AmazonBedrockCredentialSource
		if err := json.Unmarshal([]byte(input), &source); err != nil {
			t.Fatalf("unmarshal AmazonBedrockCredentialSource %s: %v", input, err)
		}
		encoded, err := json.Marshal(source)
		if err != nil {
			t.Fatalf("marshal AmazonBedrockCredentialSource %s: %v", input, err)
		}
		if got := string(encoded); got != input {
			t.Fatalf("AmazonBedrockCredentialSource round trip = %s, want %s", got, input)
		}
	}
}

func TestAmazonBedrockCredentialSourceRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `""`, `"codexmanaged"`, `"awsmanaged"`, `"other"`,
		`1`, `true`, `{}`, `[]`, `"codexManaged" {}`, `"awsManaged" x`,
	} {
		assertJSONRejects[AmazonBedrockCredentialSource](t, input)
	}
}

func TestAmazonBedrockCredentialSourceNilReceiverAndInvalidMarshalFailClosed(t *testing.T) {
	var source *AmazonBedrockCredentialSource
	if err := source.UnmarshalJSON([]byte(`"codexManaged"`)); err == nil {
		t.Fatal("nil AmazonBedrockCredentialSource receiver succeeded")
	}
	if _, err := json.Marshal(AmazonBedrockCredentialSource("other")); err == nil {
		t.Fatal("invalid AmazonBedrockCredentialSource marshaled")
	}
}

func TestAmazonBedrockCredentialSourceRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "AmazonBedrockCredentialSource") ||
			slices.Contains(binding.Result, "AmazonBedrockCredentialSource") {
			t.Fatalf("AmazonBedrockCredentialSource unexpectedly bound to %s", binding.Method)
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

func TestAmazonBedrockCredentialSourceTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type AmazonBedrockCredentialSource = "codexManaged" | "awsManaged";`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

var (
	_ json.Marshaler   = AmazonBedrockCredentialSource("")
	_ json.Unmarshaler = (*AmazonBedrockCredentialSource)(nil)
)
