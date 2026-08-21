package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestThreadTurnLeafSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	for _, name := range []string{"ThreadId", "ThreadSource"} {
		if !reflect.DeepEqual(defs[name], Schema{"type": "string"}) {
			t.Fatalf("%s schema = %#v", name, defs[name])
		}
	}
	assertStringEnumSchema(t, defs, "NonSteerableTurnKind", []string{"review", "compact"})
	assertStringEnumSchema(t, defs, "TurnItemsView", []string{"notLoaded", "summary", "full"})
	assertStringEnumSchema(t, defs, "TurnStatus", []string{"completed", "interrupted", "failed", "inProgress"})
	assertStringEnumSchema(t, defs, "ThreadActiveFlag", []string{"waitingOnApproval", "waitingOnUserInput"})

	gitInfo, ok := defs["GitInfo"].(Schema)
	if !ok {
		t.Fatal("$defs missing GitInfo")
	}
	if gitInfo["additionalProperties"] != false {
		t.Fatalf("GitInfo allows extra fields: %#v", gitInfo)
	}
	if !slices.Equal(schemaRequiredNames(gitInfo), []string{"sha", "branch", "originUrl"}) {
		t.Fatalf("GitInfo required = %v", schemaRequiredNames(gitInfo))
	}
	properties := gitInfo["properties"].(Schema)
	for _, name := range []string{"sha", "branch", "originUrl"} {
		assertNullableStringSchema(t, properties[name])
	}
}

func TestThreadIdAndThreadSourceWireValidation(t *testing.T) {
	tests := []struct {
		name   string
		decode func([]byte) error
		encode func(string) ([]byte, error)
		nil    func([]byte) error
	}{
		{
			name: "thread id",
			decode: func(data []byte) error {
				var value ThreadId
				return json.Unmarshal(data, &value)
			},
			encode: func(value string) ([]byte, error) { return json.Marshal(ThreadId(value)) },
			nil: func(data []byte) error {
				var value *ThreadId
				return value.UnmarshalJSON(data)
			},
		},
		{
			name: "thread source",
			decode: func(data []byte) error {
				var value ThreadSource
				return json.Unmarshal(data, &value)
			},
			encode: func(value string) ([]byte, error) { return json.Marshal(ThreadSource(value)) },
			nil: func(data []byte) error {
				var value *ThreadSource
				return value.UnmarshalJSON(data)
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			for _, value := range []string{"", "thread-1", "not-a-uuid", "custom/source"} {
				input, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if err := testCase.decode(input); err != nil {
					t.Errorf("decode %q: %v", value, err)
				}
				encoded, err := testCase.encode(value)
				if err != nil || string(encoded) != string(input) {
					t.Errorf("encode %q = %s, %v", value, encoded, err)
				}
			}
			for _, input := range []string{`null`, `1`, `true`, `[]`, `{}`} {
				if err := testCase.decode([]byte(input)); err == nil {
					t.Errorf("decode %s succeeded", input)
				}
			}
			if err := testCase.nil([]byte(`"value"`)); err == nil {
				t.Fatal("nil receiver succeeded")
			}
		})
	}
}

func TestThreadTurnLeafEnumsRejectUnknownValues(t *testing.T) {
	tests := []struct {
		name    string
		values  []string
		decode  func([]byte) error
		marshal func(string) error
		nil     func([]byte) error
	}{
		{
			name:    "non-steerable turn kind",
			values:  []string{"review", "compact"},
			decode:  func(data []byte) error { var value NonSteerableTurnKind; return json.Unmarshal(data, &value) },
			marshal: func(value string) error { _, err := json.Marshal(NonSteerableTurnKind(value)); return err },
			nil:     func(data []byte) error { var value *NonSteerableTurnKind; return value.UnmarshalJSON(data) },
		},
		{
			name:    "turn items view",
			values:  []string{"notLoaded", "summary", "full"},
			decode:  func(data []byte) error { var value TurnItemsView; return json.Unmarshal(data, &value) },
			marshal: func(value string) error { _, err := json.Marshal(TurnItemsView(value)); return err },
			nil:     func(data []byte) error { var value *TurnItemsView; return value.UnmarshalJSON(data) },
		},
		{
			name:    "turn status",
			values:  []string{"completed", "interrupted", "failed", "inProgress"},
			decode:  func(data []byte) error { var value TurnStatus; return json.Unmarshal(data, &value) },
			marshal: func(value string) error { _, err := json.Marshal(TurnStatus(value)); return err },
			nil:     func(data []byte) error { var value *TurnStatus; return value.UnmarshalJSON(data) },
		},
		{
			name:    "thread active flag",
			values:  []string{"waitingOnApproval", "waitingOnUserInput"},
			decode:  func(data []byte) error { var value ThreadActiveFlag; return json.Unmarshal(data, &value) },
			marshal: func(value string) error { _, err := json.Marshal(ThreadActiveFlag(value)); return err },
			nil:     func(data []byte) error { var value *ThreadActiveFlag; return value.UnmarshalJSON(data) },
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			for _, value := range testCase.values {
				if err := testCase.decode([]byte(`"` + value + `"`)); err != nil {
					t.Errorf("decode %q: %v", value, err)
				}
				if err := testCase.marshal(value); err != nil {
					t.Errorf("marshal %q: %v", value, err)
				}
			}
			for _, input := range []string{`null`, `""`, `"unknown"`, `1`, `{}`} {
				if err := testCase.decode([]byte(input)); err == nil {
					t.Errorf("decode %s succeeded", input)
				}
			}
			for _, value := range []string{"", "unknown"} {
				if err := testCase.marshal(value); err == nil {
					t.Errorf("marshal %q succeeded", value)
				}
			}
			if err := testCase.nil([]byte(`"unknown"`)); err == nil {
				t.Fatal("nil receiver succeeded")
			}
		})
	}
}

func TestGitInfoWireValidation(t *testing.T) {
	valid := []string{
		`{"sha":null,"branch":null,"originUrl":null}`,
		`{"sha":"abc","branch":"main","originUrl":"git@example.test:repo.git"}`,
		`{"sha":"","branch":"","originUrl":""}`,
	}
	for _, input := range valid {
		var info GitInfo
		if err := json.Unmarshal([]byte(input), &info); err != nil {
			t.Errorf("Unmarshal(%s): %v", input, err)
			continue
		}
		encoded, err := json.Marshal(info)
		if err != nil || string(encoded) != input {
			t.Errorf("round trip %s = %s, %v", input, encoded, err)
		}
	}
	for _, input := range []string{
		`null`, `[]`, `{}`,
		`{"branch":null,"originUrl":null}`,
		`{"sha":null,"originUrl":null}`,
		`{"sha":null,"branch":null}`,
		`{"sha":1,"branch":null,"originUrl":null}`,
		`{"sha":null,"branch":false,"originUrl":null}`,
		`{"sha":null,"branch":null,"originUrl":[]}`,
		`{"sha":null,"branch":null,"originUrl":null,"extra":true}`,
	} {
		var info GitInfo
		if err := json.Unmarshal([]byte(input), &info); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}
	var info *GitInfo
	if err := info.UnmarshalJSON([]byte(valid[0])); err == nil {
		t.Fatal("nil GitInfo receiver succeeded")
	}
}

func TestThreadTurnLeafTypesRemainStandaloneAndDistinct(t *testing.T) {
	if reflect.TypeFor[TurnStatus]() == reflect.TypeFor[TurnLifecycleStatus]() ||
		reflect.TypeFor[ThreadSource]() == reflect.TypeFor[ThreadSourceKind]() ||
		reflect.TypeFor[GitInfo]() == reflect.TypeFor[ThreadMetadataGitInfoUpdateParams]() {
		t.Fatal("public leaf type aliases an incompatible Gollem type")
	}

	names := []string{
		"ThreadId", "NonSteerableTurnKind", "TurnItemsView", "TurnStatus",
		"ThreadActiveFlag", "ThreadSource", "GitInfo",
	}
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
	if len(JSONSchema()["$defs"].(Schema)) != 675 {
		t.Fatalf("definition count = %d, want 675", len(JSONSchema()["$defs"].(Schema)))
	}
	if len(WireTypeBindings()) != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 85/5", len(WireTypeBindings()), len(ItemPayloadBindings()))
	}

	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		"export type ThreadId = string;",
		`export type NonSteerableTurnKind = "review" | "compact";`,
		`export type TurnItemsView = "notLoaded" | "summary" | "full";`,
		`export type TurnStatus = "completed" | "interrupted" | "failed" | "inProgress";`,
		`export type ThreadActiveFlag = "waitingOnApproval" | "waitingOnUserInput";`,
		"export type ThreadSource = string;",
		"export type GitInfo = {",
		`"sha": string | null;`,
		`"branch": string | null;`,
		`"originUrl": string | null;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}
