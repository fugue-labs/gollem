package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestMarketplaceUpgradeParamsSchemaIsExact(t *testing.T) {
	got := JSONSchema()["$defs"].(Schema)["MarketplaceUpgradeParams"]
	want := Schema{
		"properties": Schema{
			"marketplaceName": Schema{"type": []any{"string", "null"}},
		},
		"type": "object",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MarketplaceUpgradeParams = %#v, want %#v", got, want)
	}
}

func TestMarketplaceUpgradeParamsPreserveSerdeWireForms(t *testing.T) {
	assertMarketplaceUpgradeRoundTrip(t, `{}`, `{"marketplaceName":null}`)
	assertMarketplaceUpgradeRoundTrip(t, `{"future":true,"future":false}`, `{"marketplaceName":null}`)
	assertMarketplaceUpgradeRoundTrip(t, `{"marketplaceName":null}`, `{"marketplaceName":null}`)
	assertMarketplaceUpgradeRoundTrip(t, `{"marketplaceName":" official ","future":true}`, `{"marketplaceName":" official "}`)
}

func TestMarketplaceUpgradeParamsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`,
		`{"marketplaceName":1}`, `{"marketplaceName":true}`,
		`{"marketplaceName":"one","marketplaceName":"two"}`, `{} {}`,
	} {
		assertJSONRejects[MarketplaceUpgradeParams](t, input)
	}
}

func TestMarketplaceUpgradeParamsRemainStandalone(t *testing.T) {
	var nilParams *MarketplaceUpgradeParams
	if err := nilParams.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil MarketplaceUpgradeParams receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			if name == "MarketplaceUpgradeParams" {
				t.Fatalf("MarketplaceUpgradeParams unexpectedly bound to %s", binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestMarketplaceUpgradeParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type MarketplaceUpgradeParams = { marketplaceName?: string | null, };`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

func assertMarketplaceUpgradeRoundTrip(t *testing.T, input, want string) {
	t.Helper()
	var value MarketplaceUpgradeParams
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %s = %s, %v; want %s", input, encoded, err, want)
	}
}
