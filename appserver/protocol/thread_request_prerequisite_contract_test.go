package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestThreadRequestPrerequisiteSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	for name, values := range map[string][]string{
		"Personality":       {"none", "friendly", "pragmatic"},
		"SandboxMode":       {"read-only", "workspace-write", "danger-full-access"},
		"ThreadStartSource": {"startup", "clear"},
	} {
		if got, want := defs[name], stringEnumSchema(values...); !reflect.DeepEqual(got, want) {
			t.Errorf("%s schema = %#v, want %#v", name, got, want)
		}
	}
}

func TestThreadRequestPrerequisiteWireValidation(t *testing.T) {
	for _, testCase := range threadRequestPrerequisiteCases() {
		t.Run(testCase.name, func(t *testing.T) {
			for _, value := range testCase.values {
				input, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if err := testCase.decode(input); err != nil {
					t.Errorf("decode %q: %v", value, err)
				}
				if err := testCase.marshal(value); err != nil {
					t.Errorf("marshal %q: %v", value, err)
				}
			}

			for _, input := range []string{
				`null`, `""`, `"unknown"`, `"ReadOnly"`, `"read_only"`,
				`1`, `true`, `[]`, `{}`,
			} {
				if err := testCase.decode([]byte(input)); err == nil {
					t.Errorf("decode %s succeeded", input)
				}
			}
			for _, value := range []string{"", "unknown", "ReadOnly", "read_only"} {
				if err := testCase.marshal(value); err == nil {
					t.Errorf("marshal %q succeeded", value)
				}
			}
			for _, value := range testCase.crossed {
				input, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if err := testCase.decode(input); err == nil {
					t.Errorf("decode crossed value %q succeeded", value)
				}
				if err := testCase.marshal(value); err == nil {
					t.Errorf("marshal crossed value %q succeeded", value)
				}
			}
			if err := testCase.nil([]byte(`"unknown"`)); err == nil {
				t.Fatal("nil receiver succeeded")
			}
		})
	}
}

func TestThreadRequestPrerequisitesRemainDistinctAndStandalone(t *testing.T) {
	if reflect.TypeFor[Personality]() == reflect.TypeFor[SandboxMode]() ||
		reflect.TypeFor[Personality]() == reflect.TypeFor[ThreadStartSource]() ||
		reflect.TypeFor[SandboxMode]() == reflect.TypeFor[SandboxPolicy]() ||
		reflect.TypeFor[SandboxMode]() == reflect.TypeFor[ThreadSource]() ||
		reflect.TypeFor[SandboxMode]() == reflect.TypeFor[ThreadSourceKind]() ||
		reflect.TypeFor[ThreadStartSource]() == reflect.TypeFor[ThreadSource]() ||
		reflect.TypeFor[ThreadStartSource]() == reflect.TypeFor[ThreadSourceKind]() ||
		reflect.TypeFor[ThreadStartSource]() == reflect.TypeFor[SessionSource]() {
		t.Fatal("request prerequisite aliases an incompatible protocol type")
	}

	names := []string{"Personality", "SandboxMode", "ThreadStartSource"}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound by %#v", name, binding)
			}
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if slices.Contains(names, binding.Type) {
			t.Fatalf("%s unexpectedly bound by %#v", binding.Type, binding)
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(WireTypeBindings()); got != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 85/5", got, len(ItemPayloadBindings()))
	}
}

func TestThreadRequestPrerequisiteTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type Personality = "none" | "friendly" | "pragmatic";`,
		`export type SandboxMode = "read-only" | "workspace-write" | "danger-full-access";`,
		`export type ThreadStartSource = "startup" | "clear";`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

type threadRequestPrerequisiteCase struct {
	name    string
	values  []string
	crossed []string
	decode  func([]byte) error
	marshal func(string) error
	nil     func([]byte) error
}

func threadRequestPrerequisiteCases() []threadRequestPrerequisiteCase {
	return []threadRequestPrerequisiteCase{
		{
			name:    "personality",
			values:  []string{"none", "friendly", "pragmatic"},
			crossed: []string{"read-only", "workspace-write", "startup", "clear"},
			decode: func(data []byte) error {
				var value Personality
				return json.Unmarshal(data, &value)
			},
			marshal: func(value string) error {
				_, err := json.Marshal(Personality(value))
				return err
			},
			nil: func(data []byte) error {
				var value *Personality
				return value.UnmarshalJSON(data)
			},
		},
		{
			name:    "sandbox mode",
			values:  []string{"read-only", "workspace-write", "danger-full-access"},
			crossed: []string{"none", "friendly", "startup", "clear"},
			decode: func(data []byte) error {
				var value SandboxMode
				return json.Unmarshal(data, &value)
			},
			marshal: func(value string) error {
				_, err := json.Marshal(SandboxMode(value))
				return err
			},
			nil: func(data []byte) error {
				var value *SandboxMode
				return value.UnmarshalJSON(data)
			},
		},
		{
			name:    "thread start source",
			values:  []string{"startup", "clear"},
			crossed: []string{"none", "pragmatic", "read-only", "danger-full-access"},
			decode: func(data []byte) error {
				var value ThreadStartSource
				return json.Unmarshal(data, &value)
			},
			marshal: func(value string) error {
				_, err := json.Marshal(ThreadStartSource(value))
				return err
			},
			nil: func(data []byte) error {
				var value *ThreadStartSource
				return value.UnmarshalJSON(data)
			},
		},
	}
}
