package protocol

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestHookMetadataListSchemasAreExact(t *testing.T) {
	nullableString := Schema{"type": []any{"string", "null"}}
	want := map[string]Schema{
		"HookErrorInfo": closedThreadSessionParamSchema(Schema{
			"path": Schema{"type": "string"}, "message": Schema{"type": "string"},
		}, []string{"message", "path"}),
		"HookMetadata": closedThreadSessionParamSchema(Schema{
			"key":           Schema{"type": "string"},
			"eventName":     Schema{"$ref": "#/$defs/HookEventName"},
			"handlerType":   Schema{"$ref": "#/$defs/HookHandlerType"},
			"matcher":       nullableString,
			"command":       nullableString,
			"timeoutSec":    Schema{"type": "integer", "format": "uint64", "minimum": float64(0)},
			"statusMessage": nullableString,
			"sourcePath":    Schema{"$ref": "#/$defs/AbsolutePathBuf"},
			"source":        Schema{"$ref": "#/$defs/HookSource"},
			"pluginId":      nullableString,
			"displayOrder":  Schema{"type": "integer", "format": "int64"},
			"enabled":       Schema{"type": "boolean"},
			"isManaged":     Schema{"type": "boolean"},
			"currentHash":   Schema{"type": "string"},
			"trustStatus":   Schema{"$ref": "#/$defs/HookTrustStatus"},
		}, []string{
			"currentHash", "displayOrder", "enabled", "eventName", "handlerType",
			"isManaged", "key", "source", "sourcePath", "timeoutSec", "trustStatus",
		}),
		"HooksListEntry": closedThreadSessionParamSchema(Schema{
			"cwd":      Schema{"type": "string"},
			"hooks":    Schema{"type": "array", "items": Schema{"$ref": "#/$defs/HookMetadata"}},
			"warnings": Schema{"type": "array", "items": Schema{"type": "string"}},
			"errors":   Schema{"type": "array", "items": Schema{"$ref": "#/$defs/HookErrorInfo"}},
		}, []string{"cwd", "errors", "hooks", "warnings"}),
		"HooksListParams": closedThreadSessionParamSchema(Schema{
			"cwds": Schema{
				"type": "array", "items": Schema{"type": "string"},
				"description": "When empty, defaults to the current session working directory.",
			},
		}, nil),
		"HooksListResponse": closedThreadSessionParamSchema(Schema{
			"data": Schema{"type": "array", "items": Schema{"$ref": "#/$defs/HooksListEntry"}},
		}, []string{"data"}),
	}

	defs := JSONSchema()["$defs"].(Schema)
	for name, expected := range want {
		got, ok := defs[name].(Schema)
		if !ok || !reflect.DeepEqual(got, expected) {
			t.Errorf("%s schema = %#v, %v; want %#v", name, got, ok, expected)
		}
	}
}

func TestHookMetadataAcceptsRustWireForms(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		want      HookMetadata
		canonical string
	}{
		{
			name: "omitted options empty values and unsigned maximum",
			input: `{"key":"","eventName":"preToolUse","handlerType":"command",` +
				`"timeoutSec":18446744073709551615,"sourcePath":"/hooks.json",` +
				`"source":"unknown","displayOrder":-9223372036854775808,` +
				`"enabled":false,"isManaged":false,"currentHash":"",` +
				`"trustStatus":"untrusted","future":true}`,
			want: HookMetadata{
				Key: "", EventName: HookEventNamePreToolUse, HandlerType: HookHandlerTypeCommand,
				TimeoutSec: math.MaxUint64, SourcePath: AbsolutePathBuf("/hooks.json"),
				Source: HookSourceUnknown, DisplayOrder: math.MinInt64,
				CurrentHash: "", TrustStatus: HookTrustStatusUntrusted,
			},
			canonical: `{"key":"","eventName":"preToolUse","handlerType":"command",` +
				`"matcher":null,"command":null,"timeoutSec":18446744073709551615,` +
				`"statusMessage":null,"sourcePath":"/hooks.json","source":"unknown",` +
				`"pluginId":null,"displayOrder":-9223372036854775808,"enabled":false,` +
				`"isManaged":false,"currentHash":"","trustStatus":"untrusted"}`,
		},
		{
			name: "explicit null and present options",
			input: `{"key":" key ","eventName":"stop","handlerType":"agent",` +
				`"matcher":" matcher ","command":"","timeoutSec":0,"statusMessage":null,` +
				`"sourcePath":"/repo/../repo/hooks.json","source":"project","pluginId":" plugin ",` +
				`"displayOrder":9223372036854775807,"enabled":true,"isManaged":true,` +
				`"currentHash":" hash ","trustStatus":"managed"}`,
			want: HookMetadata{
				Key: " key ", EventName: HookEventNameStop, HandlerType: HookHandlerTypeAgent,
				Matcher: stringPointer(" matcher "), Command: stringPointer(""), TimeoutSec: 0,
				SourcePath: AbsolutePathBuf("/repo/hooks.json"), Source: HookSourceProject,
				PluginID: stringPointer(" plugin "), DisplayOrder: math.MaxInt64,
				Enabled: true, IsManaged: true, CurrentHash: " hash ", TrustStatus: HookTrustStatusManaged,
			},
			canonical: `{"key":" key ","eventName":"stop","handlerType":"agent",` +
				`"matcher":" matcher ","command":"","timeoutSec":0,"statusMessage":null,` +
				`"sourcePath":"/repo/hooks.json","source":"project","pluginId":" plugin ",` +
				`"displayOrder":9223372036854775807,"enabled":true,"isManaged":true,` +
				`"currentHash":" hash ","trustStatus":"managed"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got HookMetadata
			if err := json.Unmarshal([]byte(tc.input), &got); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("metadata = %#v, want %#v", got, tc.want)
			}
			encoded, err := json.Marshal(got)
			if err != nil || string(encoded) != tc.canonical {
				t.Fatalf("canonical = %s, %v; want %s", encoded, err, tc.canonical)
			}
		})
	}
}

func TestHookListGraphAcceptsRustWireForms(t *testing.T) {
	var errorInfo HookErrorInfo
	if err := json.Unmarshal([]byte(`{"path":"relative/../hook.json","message":"","future":true}`), &errorInfo); err != nil {
		t.Fatalf("Unmarshal error info: %v", err)
	}
	encoded, err := json.Marshal(errorInfo)
	if err != nil || string(encoded) != `{"path":"relative/../hook.json","message":""}` {
		t.Fatalf("error info canonical = %s, %v", encoded, err)
	}

	metadata := sampleHookMetadataForContract()
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	input := `{"cwd":"repo/../repo","hooks":[` + string(metadataJSON) + `,` + string(metadataJSON) + `],` +
		`"warnings":[""," warning ",""],"errors":[{"path":"relative/../hook.json","message":""},` +
		`{"path":"relative/../hook.json","message":""}],"future":true}`
	var entry HooksListEntry
	if err := json.Unmarshal([]byte(input), &entry); err != nil {
		t.Fatalf("Unmarshal entry: %v", err)
	}
	if entry.CWD != "repo/../repo" || len(entry.Hooks) != 2 || len(entry.Warnings) != 3 ||
		len(entry.Errors) != 2 || !reflect.DeepEqual(entry.Hooks[0], entry.Hooks[1]) ||
		!reflect.DeepEqual(entry.Errors[0], entry.Errors[1]) {
		t.Fatalf("entry order/duplicates changed: %#v", entry)
	}

	encoded, err = json.Marshal(HooksListEntry{CWD: ""})
	if err != nil || string(encoded) != `{"cwd":"","hooks":[],"warnings":[],"errors":[]}` {
		t.Fatalf("nil entry slices = %s, %v", encoded, err)
	}
	encoded, err = json.Marshal(HooksListResponse{})
	if err != nil || string(encoded) != `{"data":[]}` {
		t.Fatalf("nil response data = %s, %v", encoded, err)
	}

	for _, tc := range []struct {
		input     string
		want      []string
		canonical string
	}{
		{`{}`, []string{}, `{}`},
		{`{"cwds":[],"future":true}`, []string{}, `{}`},
		{`{"cwds":["","repo/../repo",""]}`, []string{"", "repo/../repo", ""}, `{"cwds":["","repo/../repo",""]}`},
	} {
		var params HooksListParams
		if err := json.Unmarshal([]byte(tc.input), &params); err != nil {
			t.Fatalf("Unmarshal params %s: %v", tc.input, err)
		}
		if !slices.Equal(params.CWDs, tc.want) {
			t.Fatalf("params %s = %#v, want %#v", tc.input, params.CWDs, tc.want)
		}
		encoded, err := json.Marshal(params)
		if err != nil || string(encoded) != tc.canonical {
			t.Fatalf("params canonical = %s, %v; want %s", encoded, err, tc.canonical)
		}
	}

	responseInput := `{"data":[` + input + `,` + input + `],"future":true}`
	var response HooksListResponse
	if err := json.Unmarshal([]byte(responseInput), &response); err != nil {
		t.Fatalf("Unmarshal response: %v", err)
	}
	if len(response.Data) != 2 || !reflect.DeepEqual(response.Data[0], response.Data[1]) {
		t.Fatalf("response order/duplicates changed: %#v", response)
	}
}

func TestHookMetadataRejectsMalformedWireForms(t *testing.T) {
	validBytes, err := json.Marshal(sampleHookMetadataForContract())
	if err != nil {
		t.Fatal(err)
	}
	valid := string(validBytes)
	invalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		strings.Replace(valid, `"key":"key",`, ``, 1),
		strings.Replace(valid, `"eventName":"preToolUse",`, ``, 1),
		strings.Replace(valid, `"handlerType":"command",`, ``, 1),
		strings.Replace(valid, `"timeoutSec":1,`, ``, 1),
		strings.Replace(valid, `"sourcePath":"/hooks.json",`, ``, 1),
		strings.Replace(valid, `"source":"project",`, ``, 1),
		strings.Replace(valid, `"displayOrder":-1,`, ``, 1),
		strings.Replace(valid, `"enabled":true,`, ``, 1),
		strings.Replace(valid, `"isManaged":false,`, ``, 1),
		strings.Replace(valid, `"currentHash":"hash",`, ``, 1),
		strings.Replace(valid, `"trustStatus":"trusted"`, ``, 1),
		strings.Replace(valid, `"key":"key"`, `"key":null`, 1),
		strings.Replace(valid, `"eventName":"preToolUse"`, `"eventName":"other"`, 1),
		strings.Replace(valid, `"handlerType":"command"`, `"handlerType":"other"`, 1),
		strings.Replace(valid, `"matcher":null`, `"matcher":1`, 1),
		strings.Replace(valid, `"command":null`, `"command":false`, 1),
		strings.Replace(valid, `"timeoutSec":1`, `"timeoutSec":-1`, 1),
		strings.Replace(valid, `"timeoutSec":1`, `"timeoutSec":0.5`, 1),
		strings.Replace(valid, `"timeoutSec":1`, `"timeoutSec":1e3`, 1),
		strings.Replace(valid, `"timeoutSec":1`, `"timeoutSec":18446744073709551616`, 1),
		strings.Replace(valid, `"statusMessage":null`, `"statusMessage":[]`, 1),
		strings.Replace(valid, `"sourcePath":"/hooks.json"`, `"sourcePath":"relative"`, 1),
		strings.Replace(valid, `"source":"project"`, `"source":null`, 1),
		strings.Replace(valid, `"source":"project"`, `"source":"other"`, 1),
		strings.Replace(valid, `"pluginId":null`, `"pluginId":1`, 1),
		strings.Replace(valid, `"displayOrder":-1`, `"displayOrder":0.5`, 1),
		strings.Replace(valid, `"displayOrder":-1`, `"displayOrder":9223372036854775808`, 1),
		strings.Replace(valid, `"enabled":true`, `"enabled":null`, 1),
		strings.Replace(valid, `"isManaged":false`, `"isManaged":"false"`, 1),
		strings.Replace(valid, `"currentHash":"hash"`, `"currentHash":1`, 1),
		strings.Replace(valid, `"trustStatus":"trusted"`, `"trustStatus":"other"`, 1),
		strings.Replace(valid, `"key":"key"`, `"key":"key","key":"other"`, 1),
		strings.TrimSuffix(valid, `}`), valid + ` {}`,
	}
	for _, input := range invalid {
		assertJSONRejects[HookMetadata](t, input)
	}

	var value *HookMetadata
	if err := value.UnmarshalJSON(validBytes); err == nil {
		t.Fatal("nil HookMetadata receiver succeeded")
	}
	bad := sampleHookMetadataForContract()
	bad.SourcePath = AbsolutePathBuf("relative")
	if _, err := json.Marshal(bad); err == nil {
		t.Fatal("metadata with relative source path marshaled")
	}
	for name, mutate := range map[string]func(*HookMetadata){
		"event": func(value *HookMetadata) { value.EventName = HookEventName("other") },
		"handler": func(value *HookMetadata) {
			value.HandlerType = HookHandlerType("other")
		},
		"source": func(value *HookMetadata) { value.Source = HookSource("other") },
		"trust":  func(value *HookMetadata) { value.TrustStatus = HookTrustStatus("other") },
	} {
		t.Run("marshal invalid "+name, func(t *testing.T) {
			value := sampleHookMetadataForContract()
			mutate(&value)
			if _, err := json.Marshal(value); err == nil {
				t.Fatalf("invalid metadata %#v marshaled", value)
			}
		})
	}
}

func TestHookListGraphRejectsMalformedWireForms(t *testing.T) {
	metadataJSON, err := json.Marshal(sampleHookMetadataForContract())
	if err != nil {
		t.Fatal(err)
	}
	validEntry := `{"cwd":"/repo","hooks":[` + string(metadataJSON) + `],"warnings":[],"errors":[]}`
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"message":"x"}`, `{"path":null,"message":"x"}`, `{"path":"p","message":null}`,
		`{"path":"p","message":"x","path":"q"}`, `{"path":"p","message":"x"} {}`,
	} {
		assertJSONRejects[HookErrorInfo](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		strings.Replace(validEntry, `"cwd":"/repo",`, ``, 1),
		strings.Replace(validEntry, `"hooks":[`+string(metadataJSON)+`],`, ``, 1),
		strings.Replace(validEntry, `"warnings":[],`, ``, 1),
		strings.Replace(validEntry, `,"errors":[]`, ``, 1),
		strings.Replace(validEntry, `"cwd":"/repo"`, `"cwd":null`, 1),
		strings.Replace(validEntry, `"hooks":[`+string(metadataJSON)+`]`, `"hooks":null`, 1),
		strings.Replace(validEntry, `"hooks":[`+string(metadataJSON)+`]`, `"hooks":[null]`, 1),
		strings.Replace(validEntry, `"warnings":[]`, `"warnings":[null]`, 1),
		strings.Replace(validEntry, `"errors":[]`, `"errors":[null]`, 1),
		strings.Replace(validEntry, `"cwd":"/repo"`, `"cwd":"/repo","cwd":"other"`, 1),
		strings.TrimSuffix(validEntry, `}`), validEntry + ` {}`,
	} {
		assertJSONRejects[HooksListEntry](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{"cwds":null}`, `{"cwds":{}}`,
		`{"cwds":[null]}`, `{"cwds":[1]}`, `{"cwds":[],"cwds":[]}`, `{} {}`,
	} {
		assertJSONRejects[HooksListParams](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`, `{"data":null}`, `{"data":{}}`,
		`{"data":[null]}`, `{"data":[{}]}`, `{"data":[],"data":[]}`, `{"data":[]} {}`,
	} {
		assertJSONRejects[HooksListResponse](t, input)
	}

	var errorInfo *HookErrorInfo
	if err := errorInfo.UnmarshalJSON([]byte(`{"path":"p","message":"m"}`)); err == nil {
		t.Fatal("nil HookErrorInfo receiver succeeded")
	}
	var entry *HooksListEntry
	if err := entry.UnmarshalJSON([]byte(validEntry)); err == nil {
		t.Fatal("nil HooksListEntry receiver succeeded")
	}
	var params *HooksListParams
	if err := params.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil HooksListParams receiver succeeded")
	}
	var response *HooksListResponse
	if err := response.UnmarshalJSON([]byte(`{"data":[]}`)); err == nil {
		t.Fatal("nil HooksListResponse receiver succeeded")
	}
	invalidNested := sampleHookMetadataForContract()
	invalidNested.TrustStatus = HookTrustStatus("other")
	if _, err := json.Marshal(HooksListEntry{Hooks: []HookMetadata{invalidNested}}); err == nil {
		t.Fatal("entry with invalid nested metadata marshaled")
	}
}

func TestHookMetadataListContractsStayStandalone(t *testing.T) {
	names := []string{
		"HookErrorInfo", "HookMetadata", "HooksListEntry", "HooksListParams", "HooksListResponse",
	}
	for _, binding := range WireTypeBindings() {
		if binding.Method == "hooks/list" {
			t.Fatalf("hooks/list unexpectedly bound: %#v", binding)
		}
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Errorf("standalone definition %s unexpectedly bound by %s", name, binding.Method)
			}
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if slices.Contains(names, binding.Type) {
			t.Errorf("standalone definition %s unexpectedly bound to item %s", binding.Type, binding.Kind)
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Errorf("definition count = %d, want 675", got)
	}
	if got := len(WireTypeBindings()); got != 85 {
		t.Errorf("wire binding count = %d, want 85", got)
	}
	if got := len(ItemPayloadBindings()); got != 5 {
		t.Errorf("item binding count = %d, want 5", got)
	}
}

func sampleHookMetadataForContract() HookMetadata {
	return HookMetadata{
		Key: "key", EventName: HookEventNamePreToolUse, HandlerType: HookHandlerTypeCommand,
		TimeoutSec: 1, SourcePath: AbsolutePathBuf("/hooks.json"), Source: HookSourceProject,
		DisplayOrder: -1, Enabled: true, IsManaged: false, CurrentHash: "hash",
		TrustStatus: HookTrustStatusTrusted,
	}
}
