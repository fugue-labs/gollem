package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestMarketplaceAddParamsSchemaIsExact(t *testing.T) {
	got := JSONSchema()["$defs"].(Schema)["MarketplaceAddParams"]
	want := Schema{
		"properties": Schema{
			"refName": Schema{"type": []any{"string", "null"}},
			"source":  Schema{"type": "string"},
			"sparsePaths": Schema{
				"items": Schema{"type": "string"},
				"type":  []any{"array", "null"},
			},
		},
		"required": []string{"source"},
		"type":     "object",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MarketplaceAddParams = %#v, want %#v", got, want)
	}
}

func TestMarketplaceAddParamsPreserveSerdeWireForms(t *testing.T) {
	assertMarketplaceAddRoundTrip(
		t,
		`{"source":" https://example.com/repo ","future":true}`,
		`{"source":" https://example.com/repo ","refName":null,"sparsePaths":null}`,
	)
	assertMarketplaceAddRoundTrip(
		t,
		`{"source":"repo","refName":"main","sparsePaths":["plugins","skills"],"future":true}`,
		`{"source":"repo","refName":"main","sparsePaths":["plugins","skills"]}`,
	)
	assertMarketplaceAddRoundTrip(
		t,
		`{"source":"repo","refName":null,"sparsePaths":null}`,
		`{"source":"repo","refName":null,"sparsePaths":null}`,
	)
}

func TestMarketplaceAddParamsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"source":null}`, `{"source":1}`, `{"refName":"main"}`,
		`{"source":"repo","refName":1}`, `{"source":"repo","sparsePaths":{}}`,
		`{"source":"repo","sparsePaths":[null]}`, `{"source":"repo","sparsePaths":[1]}`,
		`{"source":"repo","source":"other"}`, `{"source":"repo","refName":"main","refName":"other"}`,
		`{"source":"repo","sparsePaths":[],"sparsePaths":[]}`, `{"source":"repo"} {}`,
	} {
		assertJSONRejects[MarketplaceAddParams](t, input)
	}
}

func TestMarketplaceAddParamsRemainStandalone(t *testing.T) {
	var nilParams *MarketplaceAddParams
	if err := nilParams.UnmarshalJSON([]byte(`{"source":"repo"}`)); err == nil {
		t.Fatal("nil MarketplaceAddParams receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			if name == "MarketplaceAddParams" {
				t.Fatalf("MarketplaceAddParams unexpectedly bound to %s", binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestMarketplaceAddParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type MarketplaceAddParams = { source: string, refName?: string | null, sparsePaths?: Array<string> | null, };`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

func assertMarketplaceAddRoundTrip(t *testing.T, input, want string) {
	t.Helper()
	var value MarketplaceAddParams
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %s = %s, %v; want %s", input, encoded, err, want)
	}
}
