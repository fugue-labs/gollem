package protocol

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestFuzzyFileSearchSchemasAreExact(t *testing.T) {
	uint32Schema := Schema{"type": "integer", "minimum": 0, "maximum": 4294967295}
	resultSchema := closedThreadSessionParamSchema(Schema{
		"root":       Schema{"type": "string"},
		"path":       Schema{"type": "string"},
		"match_type": Schema{"$ref": "#/$defs/FuzzyFileSearchMatchType"},
		"file_name":  Schema{"type": "string"},
		"score":      uint32Schema,
		"indices": Schema{"anyOf": []any{
			Schema{"type": "array", "items": uint32Schema}, Schema{"type": "null"},
		}},
	}, []string{"root", "path", "match_type", "file_name", "score"})
	resultSchema["description"] = "Superset of [`codex_file_search::FileMatch`]"
	wants := map[string]Schema{
		"FuzzyFileSearchMatchType": {"type": "string", "enum": []any{"file", "directory"}},
		"FuzzyFileSearchParams": closedThreadSessionParamSchema(Schema{
			"query": Schema{"type": "string"},
			"roots": Schema{"type": "array", "items": Schema{"type": "string"}},
			"cancellationToken": Schema{"anyOf": []any{
				Schema{"type": "string"}, Schema{"type": "null"},
			}},
		}, []string{"query", "roots"}),
		"FuzzyFileSearchResult": resultSchema,
		"FuzzyFileSearchResponse": closedThreadSessionParamSchema(Schema{
			"files": Schema{"type": "array", "items": Schema{"$ref": "#/$defs/FuzzyFileSearchResult"}},
		}, []string{"files"}),
		"FuzzyFileSearchSessionUpdatedNotification": closedThreadSessionParamSchema(Schema{
			"sessionId": Schema{"type": "string"},
			"query":     Schema{"type": "string"},
			"files":     Schema{"type": "array", "items": Schema{"$ref": "#/$defs/FuzzyFileSearchResult"}},
		}, []string{"sessionId", "query", "files"}),
		"FuzzyFileSearchSessionCompletedNotification": closedThreadSessionParamSchema(Schema{
			"sessionId": Schema{"type": "string"},
		}, []string{"sessionId"}),
	}

	defs := JSONSchema()["$defs"].(Schema)
	for name, want := range wants {
		got, ok := defs[name].(Schema)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("%s schema = %#v, %v; want %#v", name, got, ok, want)
		}
	}
}

func TestFuzzyFileSearchMatchTypeIsClosed(t *testing.T) {
	for _, value := range []FuzzyFileSearchMatchType{
		FuzzyFileSearchMatchTypeFile,
		FuzzyFileSearchMatchTypeDirectory,
	} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("Marshal(%q): %v", value, err)
		}
		var roundTrip FuzzyFileSearchMatchType
		if err := json.Unmarshal(encoded, &roundTrip); err != nil || roundTrip != value {
			t.Fatalf("round trip = %q, %v; want %q", roundTrip, err, value)
		}
	}

	for _, input := range []string{`null`, `""`, `"File"`, `"folder"`, `1`, `{}`, `[]`, `"file" {}`} {
		var value FuzzyFileSearchMatchType
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}
	if _, err := json.Marshal(FuzzyFileSearchMatchType("other")); err == nil {
		t.Fatal("invalid match type marshaled")
	}
	var value *FuzzyFileSearchMatchType
	if err := value.UnmarshalJSON([]byte(`"file"`)); err == nil {
		t.Fatal("nil match-type receiver succeeded")
	}
}

func TestFuzzyFileSearchParamsAcceptRustWireForms(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		want      FuzzyFileSearchParams
		canonical string
	}{
		{
			name:      "empty omitted cancellation and unknown",
			input:     `{"query":"","roots":[],"future":true}`,
			want:      FuzzyFileSearchParams{Query: "", Roots: []string{}},
			canonical: `{"query":"","roots":[],"cancellationToken":null}`,
		},
		{
			name:  "opaque ordered duplicate roots and token",
			input: `{"query":" ../needle ","roots":["","repo/../repo","/tmp","repo/../repo"],"cancellationToken":" token "}`,
			want: FuzzyFileSearchParams{
				Query: " ../needle ", Roots: []string{"", "repo/../repo", "/tmp", "repo/../repo"},
				CancellationToken: stringPointer(" token "),
			},
			canonical: `{"query":" ../needle ","roots":["","repo/../repo","/tmp","repo/../repo"],"cancellationToken":" token "}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got FuzzyFileSearchParams
			assertFuzzyFileSearchRoundTrip(t, tc.input, tc.canonical, &got)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("params = %#v, want %#v", got, tc.want)
			}
		})
	}

	encoded, err := json.Marshal(FuzzyFileSearchParams{})
	if err != nil || string(encoded) != `{"query":"","roots":[],"cancellationToken":null}` {
		t.Fatalf("zero params = %s, %v", encoded, err)
	}
}

func TestFuzzyFileSearchResultAcceptsRustWireForms(t *testing.T) {
	maximum := uint32(math.MaxUint32)
	indices := []uint32{maximum, 0, maximum}
	cases := []struct {
		name      string
		input     string
		want      FuzzyFileSearchResult
		canonical string
	}{
		{
			name:  "empty minimum omitted indices and unknown",
			input: `{"root":"","path":"","match_type":"file","file_name":"","score":0,"future":true}`,
			want: FuzzyFileSearchResult{
				Root: "", Path: "", MatchType: FuzzyFileSearchMatchTypeFile, FileName: "", Score: 0,
			},
			canonical: `{"root":"","path":"","match_type":"file","file_name":"","score":0,"indices":null}`,
		},
		{
			name:  "opaque paths maximum and duplicate indices",
			input: `{"root":" repo/../repo ","path":"./a/../b","match_type":"directory","file_name":" name ","score":4294967295,"indices":[4294967295,0,4294967295]}`,
			want: FuzzyFileSearchResult{
				Root: " repo/../repo ", Path: "./a/../b", MatchType: FuzzyFileSearchMatchTypeDirectory,
				FileName: " name ", Score: maximum, Indices: &indices,
			},
			canonical: `{"root":" repo/../repo ","path":"./a/../b","match_type":"directory","file_name":" name ","score":4294967295,"indices":[4294967295,0,4294967295]}`,
		},
		{
			name:  "empty indices distinct from null",
			input: `{"root":"r","path":"p","match_type":"file","file_name":"f","score":1,"indices":[]}`,
			want: FuzzyFileSearchResult{
				Root: "r", Path: "p", MatchType: FuzzyFileSearchMatchTypeFile,
				FileName: "f", Score: 1, Indices: &[]uint32{},
			},
			canonical: `{"root":"r","path":"p","match_type":"file","file_name":"f","score":1,"indices":[]}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got FuzzyFileSearchResult
			assertFuzzyFileSearchRoundTrip(t, tc.input, tc.canonical, &got)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("result = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestFuzzyFileSearchEnvelopesAcceptRustWireForms(t *testing.T) {
	file := FuzzyFileSearchResult{
		Root: "root", Path: "path", MatchType: FuzzyFileSearchMatchTypeFile,
		FileName: "file", Score: 7,
	}
	directory := FuzzyFileSearchResult{
		Root: "root", Path: "path", MatchType: FuzzyFileSearchMatchTypeDirectory,
		FileName: "file", Score: 7,
	}
	resultWire := `{"root":"root","path":"path","match_type":"file","file_name":"file","score":7,"indices":null}`
	directoryWire := `{"root":"root","path":"path","match_type":"directory","file_name":"file","score":7,"indices":null}`

	var response FuzzyFileSearchResponse
	assertFuzzyFileSearchRoundTrip(
		t,
		`{"files":[`+resultWire+`,`+directoryWire+`,`+resultWire+`],"future":true}`,
		`{"files":[`+resultWire+`,`+directoryWire+`,`+resultWire+`]}`,
		&response,
	)
	if !reflect.DeepEqual(response.Files, []FuzzyFileSearchResult{file, directory, file}) {
		t.Fatalf("response order/duplicates changed: %#v", response.Files)
	}

	var updated FuzzyFileSearchSessionUpdatedNotification
	assertFuzzyFileSearchRoundTrip(
		t,
		`{"sessionId":" session ","query":" query ","files":[`+resultWire+`,`+resultWire+`],"future":true}`,
		`{"sessionId":" session ","query":" query ","files":[`+resultWire+`,`+resultWire+`]}`,
		&updated,
	)
	if updated.SessionID != " session " || updated.Query != " query " ||
		!reflect.DeepEqual(updated.Files, []FuzzyFileSearchResult{file, file}) {
		t.Fatalf("updated notification = %#v", updated)
	}

	var completed FuzzyFileSearchSessionCompletedNotification
	assertFuzzyFileSearchRoundTrip(
		t,
		`{"sessionId":"","future":true}`,
		`{"sessionId":""}`,
		&completed,
	)
	if completed.SessionID != "" {
		t.Fatalf("completed notification = %#v", completed)
	}

	for _, tc := range []struct {
		value any
		want  string
	}{
		{FuzzyFileSearchResponse{}, `{"files":[]}`},
		{FuzzyFileSearchSessionUpdatedNotification{}, `{"sessionId":"","query":"","files":[]}`},
		{FuzzyFileSearchSessionCompletedNotification{}, `{"sessionId":""}`},
	} {
		encoded, err := json.Marshal(tc.value)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("zero %T = %s, %v; want %s", tc.value, encoded, err, tc.want)
		}
	}
}

func TestFuzzyFileSearchRejectsMalformedWireForms(t *testing.T) {
	validResult := `{"root":"r","path":"p","match_type":"file","file_name":"f","score":0,"indices":null}`
	paramsInvalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"roots":[]}`, `{"query":null,"roots":[]}`, `{"query":1,"roots":[]}`,
		`{"query":"q"}`, `{"query":"q","roots":null}`, `{"query":"q","roots":{}}`,
		`{"query":"q","roots":[null]}`, `{"query":"q","roots":[1]}`,
		`{"query":"q","roots":[],"cancellationToken":1}`,
		`{"query":"a","query":"b","roots":[]}`,
		`{"query":"q","roots":[],"roots":[]}`,
		`{"query":"q","roots":[],"cancellationToken":null,"cancellationToken":"x"}`,
		`{"query":"q","roots":[]`, `{"query":"q","roots":[]} {}`,
	}
	for _, input := range paramsInvalid {
		var value FuzzyFileSearchParams
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("params Unmarshal(%s) succeeded", input)
		}
	}

	resultInvalid := []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"path":"p","match_type":"file","file_name":"f","score":0}`,
		`{"root":null,"path":"p","match_type":"file","file_name":"f","score":0}`,
		`{"root":"r","match_type":"file","file_name":"f","score":0}`,
		`{"root":"r","path":"p","match_type":null,"file_name":"f","score":0}`,
		`{"root":"r","path":"p","match_type":"other","file_name":"f","score":0}`,
		`{"root":"r","path":"p","match_type":"file","score":0}`,
		`{"root":"r","path":"p","match_type":"file","file_name":null,"score":0}`,
		`{"root":"r","path":"p","match_type":"file","file_name":"f"}`,
		`{"root":"r","path":"p","match_type":"file","file_name":"f","score":null}`,
		`{"root":"r","path":"p","match_type":"file","file_name":"f","score":-1}`,
		`{"root":"r","path":"p","match_type":"file","file_name":"f","score":0.5}`,
		`{"root":"r","path":"p","match_type":"file","file_name":"f","score":1e3}`,
		`{"root":"r","path":"p","match_type":"file","file_name":"f","score":4294967296}`,
		`{"root":"r","path":"p","match_type":"file","file_name":"f","score":0,"indices":{}}`,
		`{"root":"r","path":"p","match_type":"file","file_name":"f","score":0,"indices":[null]}`,
		`{"root":"r","path":"p","match_type":"file","file_name":"f","score":0,"indices":[-1]}`,
		`{"root":"r","path":"p","match_type":"file","file_name":"f","score":0,"indices":[0.5]}`,
		`{"root":"r","path":"p","match_type":"file","file_name":"f","score":0,"indices":[1e3]}`,
		`{"root":"r","path":"p","match_type":"file","file_name":"f","score":0,"indices":[4294967296]}`,
		`{"root":"r","path":"p","matchType":"file","file_name":"f","score":0}`,
		`{"root":"r","path":"p","match_type":"file","fileName":"f","score":0}`,
		`{"root":"a","root":"b","path":"p","match_type":"file","file_name":"f","score":0}`,
		validResult[:len(validResult)-1], validResult + ` {}`,
	}
	for _, input := range resultInvalid {
		var value FuzzyFileSearchResult
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("result Unmarshal(%s) succeeded", input)
		}
	}

	envelopeInvalid := map[string][]string{
		"response": {
			``, `null`, `{}`, `{"files":null}`, `{"files":{}}`, `{"files":[null]}`,
			`{"files":[{}]}`, `{"files":[],"files":[]}`, `{"files":[]`, `{"files":[]} {}`,
		},
		"updated": {
			``, `null`, `{}`, `{"query":"q","files":[]}`, `{"sessionId":null,"query":"q","files":[]}`,
			`{"sessionId":"s","files":[]}`, `{"sessionId":"s","query":null,"files":[]}`,
			`{"sessionId":"s","query":"q"}`, `{"sessionId":"s","query":"q","files":null}`,
			`{"sessionId":"s","query":"q","files":[null]}`,
			`{"sessionId":"s","query":"q","files":[],"files":[]}`,
		},
		"completed": {
			``, `null`, `{}`, `{"sessionId":null}`, `{"sessionId":1}`, `{"session_id":"s"}`,
			`{"sessionId":"a","sessionId":"b"}`, `{"sessionId":"s"`, `{"sessionId":"s"} {}`,
		},
	}
	for _, input := range envelopeInvalid["response"] {
		var value FuzzyFileSearchResponse
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("response Unmarshal(%s) succeeded", input)
		}
	}
	for _, input := range envelopeInvalid["updated"] {
		var value FuzzyFileSearchSessionUpdatedNotification
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("updated Unmarshal(%s) succeeded", input)
		}
	}
	for _, input := range envelopeInvalid["completed"] {
		var value FuzzyFileSearchSessionCompletedNotification
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("completed Unmarshal(%s) succeeded", input)
		}
	}

	var params *FuzzyFileSearchParams
	if err := params.UnmarshalJSON([]byte(`{"query":"q","roots":[]}`)); err == nil {
		t.Fatal("nil params receiver succeeded")
	}
	var result *FuzzyFileSearchResult
	if err := result.UnmarshalJSON([]byte(validResult)); err == nil {
		t.Fatal("nil result receiver succeeded")
	}
	var response *FuzzyFileSearchResponse
	if err := response.UnmarshalJSON([]byte(`{"files":[]}`)); err == nil {
		t.Fatal("nil response receiver succeeded")
	}
	var updated *FuzzyFileSearchSessionUpdatedNotification
	if err := updated.UnmarshalJSON([]byte(`{"sessionId":"s","query":"q","files":[]}`)); err == nil {
		t.Fatal("nil updated receiver succeeded")
	}
	var completed *FuzzyFileSearchSessionCompletedNotification
	if err := completed.UnmarshalJSON([]byte(`{"sessionId":"s"}`)); err == nil {
		t.Fatal("nil completed receiver succeeded")
	}

	bad := FuzzyFileSearchResult{MatchType: FuzzyFileSearchMatchType("other")}
	for _, value := range []any{
		bad,
		FuzzyFileSearchResponse{Files: []FuzzyFileSearchResult{bad}},
		FuzzyFileSearchSessionUpdatedNotification{Files: []FuzzyFileSearchResult{bad}},
	} {
		if _, err := json.Marshal(value); err == nil {
			t.Errorf("invalid nested %T marshaled", value)
		}
	}
}

func TestFuzzyFileSearchContractsRemainStandalone(t *testing.T) {
	names := []string{
		"FuzzyFileSearchMatchType",
		"FuzzyFileSearchParams",
		"FuzzyFileSearchResult",
		"FuzzyFileSearchResponse",
		"FuzzyFileSearchSessionUpdatedNotification",
		"FuzzyFileSearchSessionCompletedNotification",
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound: %#v", name, binding)
			}
		}
	}
	for _, method := range []string{
		"fuzzyFileSearch",
		"fuzzyFileSearch/sessionUpdated",
		"fuzzyFileSearch/sessionCompleted",
	} {
		info, ok := LookupMethod(method)
		if !ok || info.State != MethodDeferredStub {
			t.Fatalf("%s = %#v, %v; want deferred stub", method, info, ok)
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if got := len(Methods()); got != 229 {
		t.Fatalf("methods = %d, want 229", got)
	}
	if got := len(WireTypeBindings()); got != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("bindings = %d methods/%d items, want 85/5", got, len(ItemPayloadBindings()))
	}
}

func TestFuzzyFileSearchTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`export type FuzzyFileSearchMatchType = "file" | "directory";`,
		"export type FuzzyFileSearchParams = {\n" +
			"  \"cancellationToken\": string | null;\n" +
			"  \"query\": string;\n" +
			"  \"roots\": Array<string>;\n" +
			"};",
		"export type FuzzyFileSearchResult = {\n" +
			"  \"file_name\": string;\n" +
			"  \"indices\": Array<number> | null;\n" +
			"  \"match_type\": FuzzyFileSearchMatchType;\n" +
			"  \"path\": string;\n" +
			"  \"root\": string;\n" +
			"  \"score\": number;\n" +
			"};",
		"export type FuzzyFileSearchResponse = {\n" +
			"  \"files\": Array<FuzzyFileSearchResult>;\n" +
			"};",
		"export type FuzzyFileSearchSessionUpdatedNotification = {\n" +
			"  \"files\": Array<FuzzyFileSearchResult>;\n" +
			"  \"query\": string;\n" +
			"  \"sessionId\": string;\n" +
			"};",
		"export type FuzzyFileSearchSessionCompletedNotification = {\n" +
			"  \"sessionId\": string;\n" +
			"};",
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func assertFuzzyFileSearchRoundTrip(t *testing.T, input, canonical string, target any) {
	t.Helper()
	if err := json.Unmarshal([]byte(input), target); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	encoded, err := json.Marshal(target)
	if err != nil || string(encoded) != canonical {
		t.Fatalf("canonical = %s, %v; want %s", encoded, err, canonical)
	}
	copy := reflect.New(reflect.TypeOf(target).Elem()).Interface()
	if err := json.Unmarshal(encoded, copy); err != nil || !reflect.DeepEqual(copy, target) {
		t.Fatalf("round trip = %#v, %v; want %#v", copy, err, target)
	}
}

var (
	_ json.Marshaler   = FuzzyFileSearchMatchType("")
	_ json.Unmarshaler = (*FuzzyFileSearchMatchType)(nil)
	_ json.Marshaler   = FuzzyFileSearchParams{}
	_ json.Unmarshaler = (*FuzzyFileSearchParams)(nil)
	_ json.Marshaler   = FuzzyFileSearchResult{}
	_ json.Unmarshaler = (*FuzzyFileSearchResult)(nil)
	_ json.Marshaler   = FuzzyFileSearchResponse{}
	_ json.Unmarshaler = (*FuzzyFileSearchResponse)(nil)
	_ json.Marshaler   = FuzzyFileSearchSessionUpdatedNotification{}
	_ json.Unmarshaler = (*FuzzyFileSearchSessionUpdatedNotification)(nil)
	_ json.Marshaler   = FuzzyFileSearchSessionCompletedNotification{}
	_ json.Unmarshaler = (*FuzzyFileSearchSessionCompletedNotification)(nil)
)
