package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestMarketplaceRemoveParamsSchemaIsExact(t *testing.T) {
	got := JSONSchema()["$defs"].(Schema)["MarketplaceRemoveParams"]
	want := Schema{
		"properties": Schema{
			"marketplaceName": Schema{"type": "string"},
		},
		"required": []string{"marketplaceName"},
		"type":     "object",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MarketplaceRemoveParams = %#v, want %#v", got, want)
	}
}

func TestMarketplaceRemoveParamsPreserveSerdeWireForms(t *testing.T) {
	assertMarketplaceRemoveRoundTrip(
		t,
		`{"marketplaceName":" repo ","future":true}`,
		`{"marketplaceName":" repo "}`,
	)
}

func TestMarketplaceRemoveParamsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"marketplaceName":null}`, `{"marketplaceName":1}`,
		`{"marketplaceName":true}`, `{"marketplaceName":"repo","marketplaceName":"other"}`,
		`{"marketplaceName":"repo"} {}`,
	} {
		assertJSONRejects[MarketplaceRemoveParams](t, input)
	}
}

func TestMarketplaceRemoveParamsRemainStandalone(t *testing.T) {
	var nilParams *MarketplaceRemoveParams
	if err := nilParams.UnmarshalJSON([]byte(`{"marketplaceName":"repo"}`)); err == nil {
		t.Fatal("nil MarketplaceRemoveParams receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			if name == "MarketplaceRemoveParams" {
				t.Fatalf("MarketplaceRemoveParams unexpectedly bound to %s", binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestMarketplaceRemoveParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type MarketplaceRemoveParams = { marketplaceName: string, };`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

func assertMarketplaceRemoveRoundTrip(t *testing.T, input, want string) {
	t.Helper()
	var value MarketplaceRemoveParams
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %s = %s, %v; want %s", input, encoded, err, want)
	}
}
