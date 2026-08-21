package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestAdditionalContextSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	assertStringEnum(t, defs["AdditionalContextKind"], "untrusted", "application")

	wantEntry := Schema{
		"type": "object",
		"properties": Schema{
			"value": Schema{"type": "string"},
			"kind":  Schema{"$ref": "#/$defs/AdditionalContextKind"},
		},
		"required":             []string{"kind", "value"},
		"additionalProperties": true,
		"x-gollem-typescript-ignore-additional-properties": true,
	}
	if got := defs["AdditionalContextEntry"]; !reflect.DeepEqual(got, wantEntry) {
		t.Fatalf("AdditionalContextEntry schema = %#v, want %#v", got, wantEntry)
	}
}

func TestAdditionalContextKindAcceptsExactValues(t *testing.T) {
	for _, input := range []string{`"untrusted"`, `"application"`} {
		var kind AdditionalContextKind
		if err := json.Unmarshal([]byte(input), &kind); err != nil {
			t.Fatalf("unmarshal AdditionalContextKind %s: %v", input, err)
		}
		encoded, err := json.Marshal(kind)
		if err != nil {
			t.Fatalf("marshal AdditionalContextKind %s: %v", input, err)
		}
		if got := string(encoded); got != input {
			t.Fatalf("AdditionalContextKind round trip = %s, want %s", got, input)
		}
	}
}

func TestAdditionalContextEntryCanonicalizesOpenInput(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{`{"value":"","kind":"untrusted"}`, `{"value":"","kind":"untrusted"}`},
		{`{"kind":"application","value":"app context"}`, `{"value":"app context","kind":"application"}`},
		{`{"future":{"ignored":true},"value":"source","kind":"untrusted"}`, `{"value":"source","kind":"untrusted"}`},
		{`{"future":1,"future":2,"value":"source","kind":"application"}`, `{"value":"source","kind":"application"}`},
	}
	for _, tc := range cases {
		var entry AdditionalContextEntry
		if err := json.Unmarshal([]byte(tc.input), &entry); err != nil {
			t.Fatalf("unmarshal AdditionalContextEntry %s: %v", tc.input, err)
		}
		encoded, err := json.Marshal(entry)
		if err != nil {
			t.Fatalf("marshal AdditionalContextEntry %s: %v", tc.input, err)
		}
		if got := string(encoded); got != tc.want {
			t.Fatalf("AdditionalContextEntry %s = %s, want %s", tc.input, got, tc.want)
		}

		var roundTripped AdditionalContextEntry
		if err := json.Unmarshal(encoded, &roundTripped); err != nil {
			t.Fatalf("unmarshal canonical AdditionalContextEntry %s: %v", encoded, err)
		}
		if !reflect.DeepEqual(roundTripped, entry) {
			t.Fatalf("AdditionalContextEntry round trip = %#v, want %#v", roundTripped, entry)
		}
	}
}

func TestAdditionalContextContractsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `""`, `"other"`, `1`, `true`, `{}`, `[]`, `"untrusted" {}`,
	} {
		assertJSONRejects[AdditionalContextKind](t, input)
	}

	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`, `{} {}`, `{} x`,
		`{"kind":"untrusted"}`,
		`{"value":"context"}`,
		`{"value":null,"kind":"untrusted"}`,
		`{"value":1,"kind":"untrusted"}`,
		`{"value":[],"kind":"untrusted"}`,
		`{"value":"context","kind":null}`,
		`{"value":"context","kind":""}`,
		`{"value":"context","kind":"other"}`,
		`{"value":"context","kind":1}`,
		`{"value":"a","value":"b","kind":"untrusted"}`,
		`{"value":"context","kind":"untrusted","kind":"application"}`,
	} {
		assertJSONRejects[AdditionalContextEntry](t, input)
	}
}

func TestAdditionalContextEntryDirectDecoderRejectsMalformedTokenStreams(t *testing.T) {
	for _, input := range []string{
		``, `{@`, `{"value"`, `{"value":`, `{"future":`, `{"future":1`, `{} {}`, `{} x`,
	} {
		var entry AdditionalContextEntry
		if err := entry.UnmarshalJSON([]byte(input)); err == nil {
			t.Errorf("direct AdditionalContextEntry decoder accepted %q", input)
		}
	}
}

func TestAdditionalContextNilReceiversAndInvalidMarshalFailClosed(t *testing.T) {
	var kind *AdditionalContextKind
	if err := kind.UnmarshalJSON([]byte(`"untrusted"`)); err == nil {
		t.Fatal("nil AdditionalContextKind receiver succeeded")
	}
	var entry *AdditionalContextEntry
	if err := entry.UnmarshalJSON([]byte(`{"value":"context","kind":"untrusted"}`)); err == nil {
		t.Fatal("nil AdditionalContextEntry receiver succeeded")
	}

	for name, value := range map[string]any{
		"kind":  AdditionalContextKind("other"),
		"entry": AdditionalContextEntry{Value: "context", Kind: AdditionalContextKind("other")},
	} {
		if _, err := json.Marshal(value); err == nil {
			t.Errorf("invalid AdditionalContext %s marshaled", name)
		}
	}
}

func TestAdditionalContextContractsRemainStandalone(t *testing.T) {
	names := []string{"AdditionalContextKind", "AdditionalContextEntry"}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	defs := JSONSchema()["$defs"].(Schema)
	for _, paramsName := range []string{"TurnStartParams", "TurnSteerParams"} {
		properties := defs[paramsName].(Schema)["properties"].(Schema)
		if _, exists := properties["additionalContext"]; exists {
			t.Fatalf("%s unexpectedly exports additionalContext", paramsName)
		}
	}
	if got := len(defs); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(Methods()); got != 229 {
		t.Fatalf("methods = %d, want 229", got)
	}
	if got := len(WireTypeBindings()); got != 86 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 86/5", got, len(ItemPayloadBindings()))
	}
}

func TestAdditionalContextTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`export type AdditionalContextKind = "untrusted" | "application";`,
		`export type AdditionalContextEntry = {`,
		`"kind": AdditionalContextKind;`,
		`"value": string;`,
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

var (
	_ json.Marshaler   = AdditionalContextKind("")
	_ json.Unmarshaler = (*AdditionalContextKind)(nil)
	_ json.Unmarshaler = (*AdditionalContextEntry)(nil)
)
