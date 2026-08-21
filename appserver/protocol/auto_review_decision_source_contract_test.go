package protocol

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestAutoReviewDecisionSourceSchemaIsExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	assertStringEnum(t, defs["AutoReviewDecisionSource"], "agent")
}

func TestAutoReviewDecisionSourceAcceptsExactValue(t *testing.T) {
	var source AutoReviewDecisionSource
	if err := json.Unmarshal([]byte(`"agent"`), &source); err != nil {
		t.Fatalf("unmarshal AutoReviewDecisionSource: %v", err)
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatalf("marshal AutoReviewDecisionSource: %v", err)
	}
	if got, want := string(encoded), `"agent"`; got != want {
		t.Fatalf("AutoReviewDecisionSource round trip = %s, want %s", got, want)
	}
}

func TestAutoReviewDecisionSourceRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `""`, `"other"`, `"Agent"`, `1`, `true`, `{}`, `[]`,
		`"agent" {}`, `"agent" x`,
	} {
		assertJSONRejects[AutoReviewDecisionSource](t, input)
	}
}

func TestAutoReviewDecisionSourceNilReceiverAndInvalidMarshalFailClosed(t *testing.T) {
	var source *AutoReviewDecisionSource
	if err := source.UnmarshalJSON([]byte(`"agent"`)); err == nil {
		t.Fatal("nil AutoReviewDecisionSource receiver succeeded")
	}
	if _, err := json.Marshal(AutoReviewDecisionSource("other")); err == nil {
		t.Fatal("invalid AutoReviewDecisionSource marshaled")
	}
}

func TestAutoReviewDecisionSourceRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "AutoReviewDecisionSource") ||
			slices.Contains(binding.Result, "AutoReviewDecisionSource") {
			t.Fatalf("AutoReviewDecisionSource unexpectedly bound to %s", binding.Method)
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

func TestAutoReviewDecisionSourceTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type AutoReviewDecisionSource = "agent";`
	if !strings.Contains(string(generated), want) {
		t.Errorf("generated TypeScript missing %q", want)
	}
}

var (
	_ json.Marshaler   = AutoReviewDecisionSource("")
	_ json.Unmarshaler = (*AutoReviewDecisionSource)(nil)
)
