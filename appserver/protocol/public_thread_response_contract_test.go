package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestPublicThreadResponseSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	tests := []struct {
		name       string
		required   []string
		properties Schema
	}{
		{
			name:     "ThreadListResponse",
			required: []string{"data", "nextCursor", "backwardsCursor"},
			properties: Schema{
				"data": Schema{
					"items": Schema{"$ref": "#/$defs/Thread"},
					"type":  "array",
				},
				"nextCursor": Schema{"anyOf": []any{
					Schema{"type": "string"}, Schema{"type": "null"},
				}},
				"backwardsCursor": Schema{"anyOf": []any{
					Schema{"type": "string"}, Schema{"type": "null"},
				}},
			},
		},
		{
			name:       "ThreadReadResponse",
			required:   []string{"thread"},
			properties: Schema{"thread": Schema{"$ref": "#/$defs/Thread"}},
		},
		{
			name:       "ThreadMetadataUpdateResponse",
			required:   []string{"thread"},
			properties: Schema{"thread": Schema{"$ref": "#/$defs/Thread"}},
		},
		{
			name:       "ThreadUnarchiveResponse",
			required:   []string{"thread"},
			properties: Schema{"thread": Schema{"$ref": "#/$defs/Thread"}},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			definition, ok := defs[testCase.name].(Schema)
			if !ok {
				t.Fatalf("$defs missing %s", testCase.name)
			}
			if definition["type"] != "object" || definition["additionalProperties"] != false {
				t.Fatalf("%s is not a closed object: %#v", testCase.name, definition)
			}
			if got := schemaRequiredNames(definition); !slices.Equal(got, testCase.required) {
				t.Fatalf("%s required = %v, want %v", testCase.name, got, testCase.required)
			}
			if got := definition["properties"].(Schema); !reflect.DeepEqual(got, testCase.properties) {
				t.Fatalf("%s properties = %#v, want %#v", testCase.name, got, testCase.properties)
			}
		})
	}
}

func TestPublicThreadResponseWireValidation(t *testing.T) {
	valid := []struct {
		input string
		value any
	}{
		{`{"data":[],"nextCursor":null,"backwardsCursor":null}`, new(ThreadListResponse)},
		{`{"data":[` + publicThreadWire + `],"nextCursor":"","backwardsCursor":"cursor"}`, new(ThreadListResponse)},
		{`{"thread":` + publicThreadWire + `}`, new(ThreadReadResponse)},
		{`{"thread":` + publicThreadWire + `}`, new(ThreadMetadataUpdateResponse)},
		{`{"thread":` + publicThreadWire + `}`, new(ThreadUnarchiveResponse)},
	}
	for _, testCase := range valid {
		if err := json.Unmarshal([]byte(testCase.input), testCase.value); err != nil {
			t.Errorf("Unmarshal(%s): %v", testCase.input, err)
			continue
		}
		encoded, err := json.Marshal(testCase.value)
		if err != nil || string(encoded) != testCase.input {
			t.Errorf("round trip %s = %s, %v", testCase.input, encoded, err)
		}
	}
}

func TestPublicThreadResponsesRejectMalformedWireValues(t *testing.T) {
	listInvalid := []string{
		`null`, `[]`, `{}`,
		`{"nextCursor":null,"backwardsCursor":null}`,
		`{"data":[],"backwardsCursor":null}`,
		`{"data":[],"nextCursor":null}`,
		`{"data":null,"nextCursor":null,"backwardsCursor":null}`,
		`{"data":[null],"nextCursor":null,"backwardsCursor":null}`,
		`{"data":[{}],"nextCursor":null,"backwardsCursor":null}`,
		`{"data":[],"nextCursor":1,"backwardsCursor":null}`,
		`{"data":[],"nextCursor":null,"backwardsCursor":false}`,
		`{"data":[],"nextCursor":null,"backwardsCursor":null,"threads":[]}`,
	}
	for _, input := range listInvalid {
		var value ThreadListResponse
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("ThreadListResponse Unmarshal(%s) succeeded", input)
		}
	}

	for _, newValue := range []func() any{
		func() any { return new(ThreadReadResponse) },
		func() any { return new(ThreadMetadataUpdateResponse) },
		func() any { return new(ThreadUnarchiveResponse) },
	} {
		for _, input := range []string{
			`null`, `[]`, `{}`, `{"thread":null}`, `{"thread":{}}`,
			`{"thread":` + publicThreadWire + `,"metadata":{}}`,
			`{"thread":` + publicThreadWire + `,"turns":[]}`,
		} {
			if err := json.Unmarshal([]byte(input), newValue()); err == nil {
				t.Errorf("%T Unmarshal(%s) succeeded", newValue(), input)
			}
		}
	}
}

func TestPublicThreadResponseNilReceiversAndMarshalValidation(t *testing.T) {
	validThread := mustPublicThread(t)
	for index, value := range []any{
		ThreadListResponse{Data: []Thread{}, NextCursor: nil, BackwardsCursor: nil},
		ThreadReadResponse{Thread: validThread},
		ThreadMetadataUpdateResponse{Thread: validThread},
		ThreadUnarchiveResponse{Thread: validThread},
	} {
		if _, err := json.Marshal(value); err != nil {
			t.Errorf("valid response %d: %v", index, err)
		}
	}
	for index, value := range []any{
		ThreadListResponse{},
		ThreadReadResponse{},
		ThreadMetadataUpdateResponse{},
		ThreadUnarchiveResponse{},
	} {
		if _, err := json.Marshal(value); err == nil {
			t.Errorf("invalid response %d marshaled", index)
		}
	}
	var list *ThreadListResponse
	if err := list.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ThreadListResponse receiver succeeded")
	}
	var read *ThreadReadResponse
	if err := read.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ThreadReadResponse receiver succeeded")
	}
	var metadata *ThreadMetadataUpdateResponse
	if err := metadata.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ThreadMetadataUpdateResponse receiver succeeded")
	}
	var unarchive *ThreadUnarchiveResponse
	if err := unarchive.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ThreadUnarchiveResponse receiver succeeded")
	}
}

func TestPublicThreadResponsesRemainSeparateFromLiveResults(t *testing.T) {
	if reflect.TypeFor[ThreadListResponse]() == reflect.TypeFor[ThreadListResult]() ||
		reflect.TypeFor[ThreadReadResponse]() == reflect.TypeFor[ThreadReadResult]() ||
		reflect.TypeFor[ThreadMetadataUpdateResponse]() == reflect.TypeFor[ThreadMetadataUpdateResult]() ||
		reflect.TypeFor[ThreadUnarchiveResponse]() == reflect.TypeFor[ThreadUnarchiveResult]() {
		t.Fatal("public response aliases an incompatible live result")
	}
	bindings := WireTypeBindings()
	want := map[string]string{
		"thread/list":            "ThreadListResult",
		"thread/read":            "ThreadReadResult",
		"thread/metadata/update": "ThreadMetadataUpdateResult",
		"thread/unarchive":       "ThreadUnarchiveResult",
	}
	for _, binding := range bindings {
		result, ok := want[binding.Method]
		if !ok {
			continue
		}
		if !slices.Equal(binding.Result, []string{result}) {
			t.Errorf("%s results = %v, want [%s]", binding.Method, binding.Result, result)
		}
		for _, publicName := range []string{
			"ThreadListResponse", "ThreadReadResponse",
			"ThreadMetadataUpdateResponse", "ThreadUnarchiveResponse",
		} {
			if slices.Contains(binding.Result, publicName) {
				t.Errorf("%s unexpectedly binds standalone %s", binding.Method, publicName)
			}
		}
	}
	if len(JSONSchema()["$defs"].(Schema)) != 675 {
		t.Fatalf("definition count = %d, want 675", len(JSONSchema()["$defs"].(Schema)))
	}
	if len(bindings) != 86 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 86/5", len(bindings), len(ItemPayloadBindings()))
	}
}

func TestPublicThreadResponseTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		"export type ThreadListResponse = {\n  \"backwardsCursor\": string | null;\n  \"data\": Array<Thread>;\n  \"nextCursor\": string | null;\n};",
		"export type ThreadReadResponse = {\n  \"thread\": Thread;\n};",
		"export type ThreadMetadataUpdateResponse = {\n  \"thread\": Thread;\n};",
		"export type ThreadUnarchiveResponse = {\n  \"thread\": Thread;\n};",
		"export type ThreadListResult = {",
		"export type ThreadReadResult = {",
		"export type ThreadMetadataUpdateResult = {",
		"export type ThreadUnarchiveResult = {",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}
