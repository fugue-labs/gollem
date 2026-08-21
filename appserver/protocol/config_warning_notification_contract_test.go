package protocol

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestConfigWarningNotificationSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)

	position := defs["TextPosition"].(Schema)
	assertClosedObjectSchema(t, position, "line", "column")
	for _, field := range []string{"line", "column"} {
		property := position["properties"].(Schema)[field].(Schema)
		if property["type"] != "integer" || property["minimum"] != 0 {
			t.Errorf("TextPosition.%s = %#v", field, property)
		}
	}

	textRange := defs["TextRange"].(Schema)
	assertClosedObjectSchema(t, textRange, "start", "end")
	for _, field := range []string{"start", "end"} {
		property := textRange["properties"].(Schema)[field].(Schema)
		if !reflect.DeepEqual(property, Schema{"$ref": "#/$defs/TextPosition"}) {
			t.Errorf("TextRange.%s = %#v", field, property)
		}
	}

	warning := defs["ConfigWarningNotification"].(Schema)
	assertClosedObjectSchema(t, warning, "summary", "details")
	warningProperties := warning["properties"].(Schema)
	if !reflect.DeepEqual(warningProperties["summary"], Schema{"type": "string"}) {
		t.Fatalf("ConfigWarningNotification.summary = %#v", warningProperties["summary"])
	}
	assertNullableStringSchema(t, warningProperties["details"])
	if !reflect.DeepEqual(warningProperties["path"], Schema{"type": "string"}) {
		t.Fatalf("ConfigWarningNotification.path = %#v", warningProperties["path"])
	}
	if !reflect.DeepEqual(warningProperties["range"], Schema{"$ref": "#/$defs/TextRange"}) {
		t.Fatalf("ConfigWarningNotification.range = %#v", warningProperties["range"])
	}
}

func TestConfigWarningNotificationAcceptsExactWireValues(t *testing.T) {
	maximum := uint64(math.MaxUint64)
	position := TextPosition{Line: maximum, Column: maximum}
	encoded, err := json.Marshal(position)
	if err != nil || string(encoded) != `{"line":18446744073709551615,"column":18446744073709551615}` {
		t.Fatalf("maximum TextPosition = %s, %v", encoded, err)
	}
	var decodedPosition TextPosition
	if err := json.Unmarshal(encoded, &decodedPosition); err != nil || decodedPosition != position {
		t.Fatalf("decode maximum TextPosition = %#v, %v", decodedPosition, err)
	}

	cases := []struct {
		name  string
		input string
		want  string
		value func() any
	}{
		{"zero position", `{"line":0,"column":0}`, `{"line":0,"column":0}`, func() any { return new(TextPosition) }},
		{"range", `{"start":{"line":1,"column":2},"end":{"line":3,"column":4}}`, `{"start":{"line":1,"column":2},"end":{"line":3,"column":4}}`, func() any { return new(TextRange) }},
		{"omitted details", `{"summary":"warning"}`, `{"summary":"warning","details":null}`, func() any { return new(ConfigWarningNotification) }},
		{"null details", `{"summary":"","details":null}`, `{"summary":"","details":null}`, func() any { return new(ConfigWarningNotification) }},
		{"empty strings", `{"summary":"","details":"","path":""}`, `{"summary":"","details":"","path":""}`, func() any { return new(ConfigWarningNotification) }},
		{"all fields", `{"summary":"warning","details":"fix it","path":"/tmp/config.toml","range":{"start":{"line":1,"column":1},"end":{"line":2,"column":3}}}`, `{"summary":"warning","details":"fix it","path":"/tmp/config.toml","range":{"start":{"line":1,"column":1},"end":{"line":2,"column":3}}}`, func() any { return new(ConfigWarningNotification) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := tc.value()
			if err := json.Unmarshal([]byte(tc.input), value); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			encoded, err := json.Marshal(value)
			if err != nil || string(encoded) != tc.want {
				t.Fatalf("round trip = %s, %v; want %s", encoded, err, tc.want)
			}
		})
	}
}

func TestConfigWarningNotificationRejectsMalformedWireValues(t *testing.T) {
	positionInvalid := []string{
		`null`, `[]`, `{}`, `{"line":0}`, `{"column":0}`,
		`{"line":-1,"column":0}`, `{"line":0,"column":-1}`,
		`{"line":1.5,"column":0}`, `{"line":0,"column":"1"}`,
		`{"line":18446744073709551616,"column":0}`,
		`{"line":0,"column":0,"extra":true}`, `{"line":0,"column":0} {}`,
	}
	for _, input := range positionInvalid {
		var value TextPosition
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("TextPosition accepted %s", input)
		}
	}

	rangeInvalid := []string{
		`null`, `{}`, `{"start":{"line":0,"column":0}}`, `{"end":{"line":0,"column":0}}`,
		`{"start":null,"end":{"line":0,"column":0}}`,
		`{"start":{"line":0,"column":0},"end":{"line":-1,"column":0}}`,
		`{"start":{"line":0,"column":0},"end":{"line":0,"column":0},"extra":true}`,
		`{"start":{"line":0,"column":0},"end":{"line":0,"column":0}} {}`,
	}
	for _, input := range rangeInvalid {
		var value TextRange
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("TextRange accepted %s", input)
		}
	}

	warningInvalid := []string{
		`null`, `[]`, `{}`, `{"details":null}`, `{"summary":null,"details":null}`,
		`{"summary":"warning","details":false}`, `{"summary":"warning","details":null,"path":null}`,
		`{"summary":"warning","details":null,"range":null}`,
		`{"summary":"warning","details":null,"range":{"start":{"line":0,"column":0},"end":{"line":0}}}`,
		`{"summary":"warning","details":null,"extra":true}`,
		`{"summary":"warning","details":null} {}`,
	}
	for _, input := range warningInvalid {
		var value ConfigWarningNotification
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("ConfigWarningNotification accepted %s", input)
		}
	}
}

func TestConfigWarningNotificationNilReceiversAndDistinctTypes(t *testing.T) {
	var position *TextPosition
	var textRange *TextRange
	var warning *ConfigWarningNotification
	for name, decode := range map[string]func() error{
		"position": func() error { return position.UnmarshalJSON([]byte(`{}`)) },
		"range":    func() error { return textRange.UnmarshalJSON([]byte(`{}`)) },
		"warning":  func() error { return warning.UnmarshalJSON([]byte(`{}`)) },
	} {
		if err := decode(); err == nil {
			t.Errorf("nil %s receiver succeeded", name)
		}
	}
	types := []reflect.Type{
		reflect.TypeFor[TextPosition](),
		reflect.TypeFor[TextRange](),
		reflect.TypeFor[ConfigWarningNotification](),
	}
	for i := range types {
		for j := i + 1; j < len(types); j++ {
			if types[i] == types[j] {
				t.Fatalf("types %d and %d unexpectedly alias", i, j)
			}
		}
	}
}

func TestConfigWarningNotificationContractRemainsStandalone(t *testing.T) {
	names := []string{"TextPosition", "TextRange", "ConfigWarningNotification"}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	info, ok := LookupMethod("configWarning")
	if !ok || info.State != MethodBlocked {
		t.Fatalf("configWarning = %#v, %v; want blocked", info, ok)
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(WireTypeBindings()); got != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 85/5", got, len(ItemPayloadBindings()))
	}
}

func TestConfigWarningNotificationTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type TextPosition = {`,
		`"line": number;`,
		`"column": number;`,
		`export type TextRange = {`,
		`"start": TextPosition;`,
		`"end": TextPosition;`,
		`export type ConfigWarningNotification = {`,
		`"summary": string;`,
		`"details": string | null;`,
		`"path"?: string;`,
		`"range"?: TextRange;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}
