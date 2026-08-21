package protocol

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestAccountTokenUsageSchemasAreExact(t *testing.T) {
	int64Schema := Schema{"type": "integer", "format": "int64"}
	nullableInt64Schema := Schema{"type": []any{"integer", "null"}, "format": "int64"}
	wants := map[string]Schema{
		"AccountTokenUsageDailyBucket": closedThreadSessionParamSchema(Schema{
			"startDate": Schema{"type": "string"},
			"tokens":    int64Schema,
		}, []string{"startDate", "tokens"}),
		"AccountTokenUsageSummary": closedThreadSessionParamSchema(Schema{
			"lifetimeTokens":        nullableInt64Schema,
			"peakDailyTokens":       nullableInt64Schema,
			"longestRunningTurnSec": nullableInt64Schema,
			"currentStreakDays":     nullableInt64Schema,
			"longestStreakDays":     nullableInt64Schema,
		}, nil),
	}
	defs := JSONSchema()["$defs"].(Schema)
	for name, want := range wants {
		if got := defs[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestAccountTokenUsageRecordsAcceptSerdeWireForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  AccountTokenUsageDailyBucket
		json  string
	}{
		{`{"startDate":"","tokens":0}`, AccountTokenUsageDailyBucket{}, `{"startDate":"","tokens":0}`},
		{`{"future":true,"startDate":" 2026-07-16 ","tokens":-9223372036854775808}`, AccountTokenUsageDailyBucket{StartDate: " 2026-07-16 ", Tokens: math.MinInt64}, `{"startDate":" 2026-07-16 ","tokens":-9223372036854775808}`},
		{`{"startDate":"arbitrary","tokens":9223372036854775807}`, AccountTokenUsageDailyBucket{StartDate: "arbitrary", Tokens: math.MaxInt64}, `{"startDate":"arbitrary","tokens":9223372036854775807}`},
	} {
		var bucket AccountTokenUsageDailyBucket
		if err := json.Unmarshal([]byte(tc.input), &bucket); err != nil {
			t.Errorf("unmarshal bucket %s: %v", tc.input, err)
			continue
		}
		if !reflect.DeepEqual(bucket, tc.want) {
			t.Errorf("bucket %s = %#v, want %#v", tc.input, bucket, tc.want)
		}
		encoded, err := json.Marshal(bucket)
		if err != nil || string(encoded) != tc.json {
			t.Errorf("marshal bucket %#v = %s, %v; want %s", bucket, encoded, err, tc.json)
		}
	}

	minimum := int64(math.MinInt64)
	maximum := int64(math.MaxInt64)
	for _, tc := range []struct {
		input string
		want  AccountTokenUsageSummary
		json  string
	}{
		{`{}`, AccountTokenUsageSummary{}, `{"lifetimeTokens":null,"peakDailyTokens":null,"longestRunningTurnSec":null,"currentStreakDays":null,"longestStreakDays":null}`},
		{`{"lifetimeTokens":null,"peakDailyTokens":null,"longestRunningTurnSec":null,"currentStreakDays":null,"longestStreakDays":null}`, AccountTokenUsageSummary{}, `{"lifetimeTokens":null,"peakDailyTokens":null,"longestRunningTurnSec":null,"currentStreakDays":null,"longestStreakDays":null}`},
		{
			`{"future":true,"lifetimeTokens":-9223372036854775808,"peakDailyTokens":9223372036854775807,"longestRunningTurnSec":0,"currentStreakDays":-1,"longestStreakDays":1}`,
			AccountTokenUsageSummary{LifetimeTokens: &minimum, PeakDailyTokens: &maximum, LongestRunningTurnSec: int64Pointer(0), CurrentStreakDays: int64Pointer(-1), LongestStreakDays: int64Pointer(1)},
			`{"lifetimeTokens":-9223372036854775808,"peakDailyTokens":9223372036854775807,"longestRunningTurnSec":0,"currentStreakDays":-1,"longestStreakDays":1}`,
		},
	} {
		var summary AccountTokenUsageSummary
		if err := json.Unmarshal([]byte(tc.input), &summary); err != nil {
			t.Errorf("unmarshal summary %s: %v", tc.input, err)
			continue
		}
		if !reflect.DeepEqual(summary, tc.want) {
			t.Errorf("summary %s = %#v, want %#v", tc.input, summary, tc.want)
		}
		encoded, err := json.Marshal(summary)
		if err != nil || string(encoded) != tc.json {
			t.Errorf("marshal summary %#v = %s, %v; want %s", summary, encoded, err, tc.json)
		}
	}
}

func TestAccountTokenUsageRecordsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"tokens":0}`, `{"startDate":null,"tokens":0}`, `{"startDate":1,"tokens":0}`,
		`{"startDate":"date"}`, `{"startDate":"date","tokens":null}`,
		`{"startDate":"date","tokens":"0"}`, `{"startDate":"date","tokens":true}`,
		`{"startDate":"date","tokens":0.5}`, `{"startDate":"date","tokens":1e3}`,
		`{"startDate":"date","tokens":9223372036854775808}`,
		`{"startDate":"date","tokens":-9223372036854775809}`,
		`{"startDate":"a","startDate":"b","tokens":0}`,
		`{"startDate":"date","tokens":0,"tokens":1}`,
		`{"startDate":"date","tokens":0} {}`, `{"startDate":"date","tokens":0} x`,
	} {
		assertJSONRejects[AccountTokenUsageDailyBucket](t, input)
	}

	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`,
		`{"lifetimeTokens":"0"}`, `{"peakDailyTokens":true}`,
		`{"longestRunningTurnSec":0.5}`, `{"currentStreakDays":1e3}`,
		`{"longestStreakDays":9223372036854775808}`,
		`{"lifetimeTokens":-9223372036854775809}`,
		`{"lifetimeTokens":null,"lifetimeTokens":0}`,
		`{"peakDailyTokens":0,"peakDailyTokens":null}`,
		`{"currentStreakDays":0} {}`, `{"currentStreakDays":0} x`,
	} {
		assertJSONRejects[AccountTokenUsageSummary](t, input)
	}
}

func TestAccountTokenUsageNilReceiversFailClosed(t *testing.T) {
	var bucket *AccountTokenUsageDailyBucket
	if err := bucket.UnmarshalJSON([]byte(`{"startDate":"date","tokens":0}`)); err == nil {
		t.Fatal("nil daily bucket receiver succeeded")
	}
	var summary *AccountTokenUsageSummary
	if err := summary.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil summary receiver succeeded")
	}
}

func TestAccountTokenUsageRecordsRemainStandaloneAndDeferred(t *testing.T) {
	names := []string{"AccountTokenUsageDailyBucket", "AccountTokenUsageSummary"}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	for _, binding := range ItemPayloadBindings() {
		if slices.Contains(names, binding.Type) {
			t.Fatalf("%s unexpectedly bound to item %s", binding.Type, binding.Kind)
		}
	}
	method, ok := LookupMethod("account/usage/read")
	if !ok || method.Surface != SurfaceClientRequest || method.State != MethodDeferredStub {
		t.Fatalf("account/usage/read = %#v, %v; want deferred client request", method, ok)
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

func TestAccountTokenUsageTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		"export type AccountTokenUsageDailyBucket = {\n" +
			"  \"startDate\": string;\n" +
			"  \"tokens\": bigint;\n" +
			"};",
		"export type AccountTokenUsageSummary = {\n" +
			"  \"currentStreakDays\": bigint | null;\n" +
			"  \"lifetimeTokens\": bigint | null;\n" +
			"  \"longestRunningTurnSec\": bigint | null;\n" +
			"  \"longestStreakDays\": bigint | null;\n" +
			"  \"peakDailyTokens\": bigint | null;\n" +
			"};",
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
	plainInt64, err := typeScriptType(Schema{"type": "integer", "format": "int64"}, 0)
	if err != nil || plainInt64 != "number" {
		t.Fatalf("plain int64 TypeScript = %q, %v; want number", plainInt64, err)
	}
}
