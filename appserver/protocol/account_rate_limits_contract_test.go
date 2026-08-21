package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestAccountRateLimitSchemasAreExact(t *testing.T) {
	nullableString := Schema{"type": []any{"string", "null"}}
	nullableInt64 := Schema{"type": []any{"integer", "null"}, "format": "int64"}
	nullableRef := func(name string) Schema {
		return Schema{"anyOf": []any{Schema{"$ref": "#/$defs/" + name}, Schema{"type": "null"}}}
	}
	object := func(properties Schema, required ...string) Schema {
		out := Schema{"type": "object", "properties": properties}
		if len(required) != 0 {
			out["required"] = required
		}
		return out
	}

	wants := map[string]Schema{
		"PlanType": stringEnumSchema(
			"free", "go", "plus", "pro", "prolite", "team",
			"self_serve_business_usage_based", "business",
			"enterprise_cbp_usage_based", "enterprise", "edu", "unknown",
		),
		"RateLimitReachedType": stringEnumSchema(
			"rate_limit_reached", "workspace_owner_credits_depleted",
			"workspace_member_credits_depleted", "workspace_owner_usage_limit_reached",
			"workspace_member_usage_limit_reached",
		),
		"RateLimitResetType": stringEnumSchema("codexRateLimits", "unknown"),
		"RateLimitResetCreditStatus": stringEnumSchema(
			"available", "redeeming", "redeemed", "unknown",
		),
		"ConsumeAccountRateLimitResetCreditOutcome": Schema{"oneOf": []any{
			Schema{"description": "A reset credit was consumed and the eligible rate-limit windows were reset.", "enum": []any{"reset"}, "type": "string"},
			Schema{"description": "No current rate-limit window is eligible for a reset.", "enum": []any{"nothingToReset"}, "type": "string"},
			Schema{"description": "The account has no earned reset credits available.", "enum": []any{"noCredit"}, "type": "string"},
			Schema{"description": "The same idempotency key already completed a reset successfully.", "enum": []any{"alreadyRedeemed"}, "type": "string"},
		}},
		"RateLimitWindow": object(Schema{
			"usedPercent":        Schema{"type": "integer", "format": "int32"},
			"windowDurationMins": nullableInt64,
			"resetsAt":           nullableInt64,
		}, "usedPercent"),
		"CreditsSnapshot": object(Schema{
			"hasCredits": Schema{"type": "boolean"},
			"unlimited":  Schema{"type": "boolean"},
			"balance":    nullableString,
		}, "hasCredits", "unlimited"),
		"SpendControlLimitSnapshot": object(Schema{
			"limit":            Schema{"type": "string"},
			"used":             Schema{"type": "string"},
			"remainingPercent": Schema{"type": "integer", "format": "int32"},
			"resetsAt":         Schema{"type": "integer", "format": "int64"},
		}, "limit", "remainingPercent", "resetsAt", "used"),
		"RateLimitSnapshot": object(Schema{
			"limitId":              nullableString,
			"limitName":            nullableString,
			"primary":              nullableRef("RateLimitWindow"),
			"secondary":            nullableRef("RateLimitWindow"),
			"credits":              nullableRef("CreditsSnapshot"),
			"individualLimit":      nullableRef("SpendControlLimitSnapshot"),
			"planType":             nullableRef("PlanType"),
			"rateLimitReachedType": nullableRef("RateLimitReachedType"),
		}),
		"RateLimitResetCredit": object(Schema{
			"id":          Schema{"description": "Opaque backend identifier for this reset credit.", "type": "string"},
			"resetType":   Schema{"$ref": "#/$defs/RateLimitResetType"},
			"status":      Schema{"$ref": "#/$defs/RateLimitResetCreditStatus"},
			"grantedAt":   Schema{"description": "Unix timestamp in seconds when the credit was granted.", "format": "int64", "type": "integer"},
			"expiresAt":   Schema{"description": "Unix timestamp in seconds when the credit expires, or `null` if it does not expire.", "format": "int64", "type": []any{"integer", "null"}},
			"title":       Schema{"description": "Backend-provided display title for this credit, or `null` when unavailable.", "type": []any{"string", "null"}},
			"description": Schema{"description": "Backend-provided display description for this credit, or `null` when unavailable.", "type": []any{"string", "null"}},
		}, "grantedAt", "id", "resetType", "status"),
		"RateLimitResetCreditsSummary": object(Schema{
			"availableCount": Schema{"format": "int64", "type": "integer"},
			"credits": Schema{
				"description": "Detail rows for available reset credits, when the backend provides them.\n\n`null` means only `availableCount` is known, while an empty array means details were fetched and no available credits were returned. The backend may cap this list, so its length can be less than `availableCount`.",
				"items":       Schema{"$ref": "#/$defs/RateLimitResetCredit"}, "type": []any{"array", "null"},
			},
		}, "availableCount"),
		"GetAccountRateLimitsResponse": object(Schema{
			"rateLimits": Schema{
				"allOf":       []any{Schema{"$ref": "#/$defs/RateLimitSnapshot"}},
				"description": "Backward-compatible single-bucket view; mirrors the historical payload.",
			},
			"rateLimitsByLimitId": Schema{
				"additionalProperties": Schema{"$ref": "#/$defs/RateLimitSnapshot"},
				"description":          "Multi-bucket view keyed by metered `limit_id` (for example, `codex`).",
				"type":                 []any{"object", "null"},
			},
			"rateLimitResetCredits": nullableRef("RateLimitResetCreditsSummary"),
		}, "rateLimits"),
		"ConsumeAccountRateLimitResetCreditParams": object(Schema{
			"idempotencyKey": Schema{"description": "Identifies one logical reset attempt. A UUID is recommended; reuse the same value when retrying that attempt.", "type": "string"},
			"creditId":       Schema{"description": "Opaque reset-credit identifier to redeem. When omitted, the backend selects the next available credit.", "type": []any{"string", "null"}},
		}, "idempotencyKey"),
		"ConsumeAccountRateLimitResetCreditResponse": object(Schema{
			"outcome": Schema{"$ref": "#/$defs/ConsumeAccountRateLimitResetCreditOutcome"},
		}, "outcome"),
		"AccountRateLimitsUpdatedNotification": Schema{
			"description": "Sparse rolling rate-limit update.\n\nClients should merge available values into the most recent `account/rateLimits/read` response or refetch that snapshot. Nullable account metadata may be unavailable in a rolling update and does not clear a previously observed value.",
			"properties":  Schema{"rateLimits": Schema{"$ref": "#/$defs/RateLimitSnapshot"}},
			"required":    []string{"rateLimits"}, "type": "object",
		},
	}

	defs := JSONSchema()["$defs"].(Schema)
	for name, want := range wants {
		if got := defs[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestAccountRateLimitEnumsPreserveExactAndFallbackSemantics(t *testing.T) {
	closed := map[string]any{
		`"rate_limit_reached"`:                   RateLimitReachedTypeRateLimitReached,
		`"workspace_owner_credits_depleted"`:     RateLimitReachedTypeWorkspaceOwnerCreditsDepleted,
		`"workspace_member_credits_depleted"`:    RateLimitReachedTypeWorkspaceMemberCreditsDepleted,
		`"workspace_owner_usage_limit_reached"`:  RateLimitReachedTypeWorkspaceOwnerUsageLimitReached,
		`"workspace_member_usage_limit_reached"`: RateLimitReachedTypeWorkspaceMemberUsageLimitReached,
		`"reset"`:                                ConsumeAccountRateLimitResetCreditOutcomeReset,
		`"nothingToReset"`:                       ConsumeAccountRateLimitResetCreditOutcomeNothingToReset,
		`"noCredit"`:                             ConsumeAccountRateLimitResetCreditOutcomeNoCredit,
		`"alreadyRedeemed"`:                      ConsumeAccountRateLimitResetCreditOutcomeAlreadyRedeemed,
	}
	for input, want := range closed {
		var got any
		switch want.(type) {
		case RateLimitReachedType:
			var value RateLimitReachedType
			if err := json.Unmarshal([]byte(input), &value); err != nil {
				t.Fatalf("unmarshal %s: %v", input, err)
			}
			got = value
		case ConsumeAccountRateLimitResetCreditOutcome:
			var value ConsumeAccountRateLimitResetCreditOutcome
			if err := json.Unmarshal([]byte(input), &value); err != nil {
				t.Fatalf("unmarshal %s: %v", input, err)
			}
			got = value
		}
		if got != want {
			t.Errorf("unmarshal %s = %#v, want %#v", input, got, want)
		}
	}

	for _, input := range []string{`""`, `"other"`, `"unknown"`, `"RateLimitReached"`, `null`, `1`, `true`, `{}`, `[]`, `"rate_limit_reached" {}`, `"rate_limit_reached" x`} {
		assertJSONRejects[RateLimitReachedType](t, input)
	}
	for _, input := range []string{`""`, `"other"`, `"unknown"`, `"nothing_to_reset"`, `null`, `1`, `true`, `{}`, `[]`, `"reset" {}`, `"reset" x`} {
		assertJSONRejects[ConsumeAccountRateLimitResetCreditOutcome](t, input)
	}

	for _, tc := range []struct {
		input  string
		plan   PlanType
		reset  RateLimitResetType
		status RateLimitResetCreditStatus
	}{
		{input: `"free"`, plan: PlanTypeFree},
		{input: `"self_serve_business_usage_based"`, plan: PlanTypeSelfServeBusinessUsageBased},
		{input: `"enterprise_cbp_usage_based"`, plan: PlanTypeEnterpriseCBPUsageBased},
		{input: `"edu"`, plan: PlanTypeEdu},
		{input: `"codexRateLimits"`, reset: RateLimitResetTypeCodexRateLimits},
		{input: `"available"`, status: RateLimitResetCreditStatusAvailable},
		{input: `"redeeming"`, status: RateLimitResetCreditStatusRedeeming},
		{input: `"redeemed"`, status: RateLimitResetCreditStatusRedeemed},
	} {
		switch {
		case tc.plan != "":
			var value PlanType
			if err := json.Unmarshal([]byte(tc.input), &value); err != nil || value != tc.plan {
				t.Errorf("plan %s = %q, %v; want %q", tc.input, value, err, tc.plan)
			}
		case tc.reset != "":
			var value RateLimitResetType
			if err := json.Unmarshal([]byte(tc.input), &value); err != nil || value != tc.reset {
				t.Errorf("reset %s = %q, %v; want %q", tc.input, value, err, tc.reset)
			}
		case tc.status != "":
			var value RateLimitResetCreditStatus
			if err := json.Unmarshal([]byte(tc.input), &value); err != nil || value != tc.status {
				t.Errorf("status %s = %q, %v; want %q", tc.input, value, err, tc.status)
			}
		}
	}

	for _, input := range []string{`"unknown"`, `"future-plan"`, `""`, `" FREE "`} {
		var value PlanType
		if err := json.Unmarshal([]byte(input), &value); err != nil || value != PlanTypeUnknown {
			t.Errorf("fallback plan %s = %q, %v", input, value, err)
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != `"unknown"` {
			t.Errorf("fallback plan marshal = %s, %v", encoded, err)
		}
	}
	for _, input := range []string{`"unknown"`, `"future-reset"`, `""`} {
		var value RateLimitResetType
		if err := json.Unmarshal([]byte(input), &value); err != nil || value != RateLimitResetTypeUnknown {
			t.Errorf("fallback reset %s = %q, %v", input, value, err)
		}
	}
	for _, input := range []string{`"unknown"`, `"future-status"`, `""`} {
		var value RateLimitResetCreditStatus
		if err := json.Unmarshal([]byte(input), &value); err != nil || value != RateLimitResetCreditStatusUnknown {
			t.Errorf("fallback status %s = %q, %v", input, value, err)
		}
	}
	for _, input := range []string{``, `null`, `1`, `true`, `{}`, `[]`, `"free" {}`, `"free" x`} {
		assertJSONRejects[PlanType](t, input)
		assertJSONRejects[RateLimitResetType](t, input)
		assertJSONRejects[RateLimitResetCreditStatus](t, input)
	}
}

func TestAccountRateLimitLeafRecordsAcceptSerdeFormsAndBounds(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`{"usedPercent":-2147483648}`, `{"usedPercent":-2147483648,"windowDurationMins":null,"resetsAt":null}`},
		{`{"future":true,"resetsAt":9223372036854775807,"windowDurationMins":-9223372036854775808,"usedPercent":2147483647}`, `{"usedPercent":2147483647,"windowDurationMins":-9223372036854775808,"resetsAt":9223372036854775807}`},
	} {
		assertAccountRateLimitRoundTrip[RateLimitWindow](t, tc.input, tc.want)
	}
	for _, tc := range []struct{ input, want string }{
		{`{"hasCredits":false,"unlimited":false}`, `{"hasCredits":false,"unlimited":false,"balance":null}`},
		{`{"future":1,"balance":" arbitrary ","unlimited":true,"hasCredits":true}`, `{"hasCredits":true,"unlimited":true,"balance":" arbitrary "}`},
	} {
		assertAccountRateLimitRoundTrip[CreditsSnapshot](t, tc.input, tc.want)
	}
	for _, tc := range []struct{ input, want string }{
		{`{"limit":"","used":"","remainingPercent":-2147483648,"resetsAt":-9223372036854775808}`, `{"limit":"","used":"","remainingPercent":-2147483648,"resetsAt":-9223372036854775808}`},
		{`{"future":true,"resetsAt":9223372036854775807,"remainingPercent":2147483647,"used":" used ","limit":" limit "}`, `{"limit":" limit ","used":" used ","remainingPercent":2147483647,"resetsAt":9223372036854775807}`},
	} {
		assertAccountRateLimitRoundTrip[SpendControlLimitSnapshot](t, tc.input, tc.want)
	}
}

func TestAccountRateLimitLeafRecordsRejectMalformedForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"windowDurationMins":1}`, `{"usedPercent":null}`, `{"usedPercent":"1"}`,
		`{"usedPercent":1.5}`, `{"usedPercent":1e3}`, `{"usedPercent":2147483648}`,
		`{"usedPercent":-2147483649}`, `{"usedPercent":1,"windowDurationMins":1.5}`,
		`{"usedPercent":1,"resetsAt":9223372036854775808}`,
		`{"usedPercent":1,"usedPercent":2}`, `{"usedPercent":1} {}`, `{"usedPercent":1} x`,
	} {
		assertJSONRejects[RateLimitWindow](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"hasCredits":true}`, `{"unlimited":false}`, `{"hasCredits":null,"unlimited":false}`,
		`{"hasCredits":true,"unlimited":0}`, `{"hasCredits":true,"unlimited":false,"balance":1}`,
		`{"hasCredits":true,"hasCredits":false,"unlimited":false}`,
		`{"hasCredits":true,"unlimited":false} {}`, `{"hasCredits":true,"unlimited":false} x`,
	} {
		assertJSONRejects[CreditsSnapshot](t, input)
	}
	valid := `"limit":"l","used":"u","remainingPercent":0,"resetsAt":0`
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"used":"u","remainingPercent":0,"resetsAt":0}`,
		`{"limit":"l","remainingPercent":0,"resetsAt":0}`,
		`{"limit":"l","used":"u","resetsAt":0}`,
		`{"limit":"l","used":"u","remainingPercent":0}`,
		`{"limit":null,"used":"u","remainingPercent":0,"resetsAt":0}`,
		`{"limit":"l","used":"u","remainingPercent":2147483648,"resetsAt":0}`,
		`{"limit":"l","used":"u","remainingPercent":0,"resetsAt":9223372036854775808}`,
		`{` + valid + `,"limit":"x"}`, `{` + valid + `} {}`, `{` + valid + `} x`,
	} {
		assertJSONRejects[SpendControlLimitSnapshot](t, input)
	}
}

func TestRateLimitSnapshotCanonicalizesSparseNestedData(t *testing.T) {
	allNull := `{"limitId":null,"limitName":null,"primary":null,"secondary":null,"credits":null,"individualLimit":null,"planType":null,"rateLimitReachedType":null}`
	assertAccountRateLimitRoundTrip[RateLimitSnapshot](t, `{}`, allNull)
	assertAccountRateLimitRoundTrip[RateLimitSnapshot](t, allNull, allNull)
	assertAccountRateLimitRoundTrip[RateLimitSnapshot](t,
		`{"future":true,"rateLimitReachedType":"workspace_member_usage_limit_reached","planType":"future-plan","individualLimit":{"limit":"10","used":"2","remainingPercent":80,"resetsAt":9},"credits":{"hasCredits":true,"unlimited":false,"balance":"8"},"secondary":{"usedPercent":2,"windowDurationMins":3,"resetsAt":4},"primary":{"usedPercent":1},"limitName":" name ","limitId":" id "}`,
		`{"limitId":" id ","limitName":" name ","primary":{"usedPercent":1,"windowDurationMins":null,"resetsAt":null},"secondary":{"usedPercent":2,"windowDurationMins":3,"resetsAt":4},"credits":{"hasCredits":true,"unlimited":false,"balance":"8"},"individualLimit":{"limit":"10","used":"2","remainingPercent":80,"resetsAt":9},"planType":"unknown","rateLimitReachedType":"workspace_member_usage_limit_reached"}`,
	)
}

func TestRateLimitSnapshotRejectsMalformedNestedData(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`,
		`{"limitId":1}`, `{"limitName":true}`, `{"primary":{}}`,
		`{"secondary":{"usedPercent":2147483648}}`, `{"credits":{"hasCredits":true}}`,
		`{"individualLimit":{"limit":"l","used":"u","remainingPercent":0}}`,
		`{"planType":1}`, `{"rateLimitReachedType":"other"}`,
		`{"limitId":"a","limitId":"b"}`, `{} {}`, `{} x`,
	} {
		assertJSONRejects[RateLimitSnapshot](t, input)
	}
}

func TestRateLimitResetCreditRecordsPreserveOrderDuplicatesAndBounds(t *testing.T) {
	credit := `{"id":" c ","resetType":"codexRateLimits","status":"available","grantedAt":-9223372036854775808}`
	wantCredit := `{"id":" c ","resetType":"codexRateLimits","status":"available","grantedAt":-9223372036854775808,"expiresAt":null,"title":null,"description":null}`
	assertAccountRateLimitRoundTrip[RateLimitResetCredit](t, credit, wantCredit)
	assertAccountRateLimitRoundTrip[RateLimitResetCredit](t,
		`{"future":true,"description":" d ","title":" t ","expiresAt":9223372036854775807,"grantedAt":9223372036854775807,"status":"future-status","resetType":"future-reset","id":""}`,
		`{"id":"","resetType":"unknown","status":"unknown","grantedAt":9223372036854775807,"expiresAt":9223372036854775807,"title":" t ","description":" d "}`,
	)
	assertAccountRateLimitRoundTrip[RateLimitResetCreditsSummary](t,
		`{"availableCount":-9223372036854775808}`,
		`{"availableCount":-9223372036854775808,"credits":null}`,
	)
	assertAccountRateLimitRoundTrip[RateLimitResetCreditsSummary](t,
		`{"future":true,"credits":[],"availableCount":9223372036854775807}`,
		`{"availableCount":9223372036854775807,"credits":[]}`,
	)
	assertAccountRateLimitRoundTrip[RateLimitResetCreditsSummary](t,
		`{"availableCount":2,"credits":[`+credit+`,`+credit+`]}`,
		`{"availableCount":2,"credits":[`+wantCredit+`,`+wantCredit+`]}`,
	)
}

func TestRateLimitResetCreditRecordsRejectMalformedForms(t *testing.T) {
	valid := `"id":"c","resetType":"codexRateLimits","status":"available","grantedAt":0`
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"resetType":"codexRateLimits","status":"available","grantedAt":0}`,
		`{"id":"c","status":"available","grantedAt":0}`,
		`{"id":"c","resetType":"codexRateLimits","grantedAt":0}`,
		`{"id":"c","resetType":"codexRateLimits","status":"available"}`,
		`{"id":null,"resetType":"codexRateLimits","status":"available","grantedAt":0}`,
		`{"id":"c","resetType":1,"status":"available","grantedAt":0}`,
		`{"id":"c","resetType":"codexRateLimits","status":null,"grantedAt":0}`,
		`{"id":"c","resetType":"codexRateLimits","status":"available","grantedAt":1.5}`,
		`{"id":"c","resetType":"codexRateLimits","status":"available","grantedAt":9223372036854775808}`,
		`{` + valid + `,"expiresAt":"1"}`, `{` + valid + `,"title":1}`, `{` + valid + `,"description":true}`,
		`{` + valid + `,"id":"x"}`, `{` + valid + `} {}`, `{` + valid + `} x`,
	} {
		assertJSONRejects[RateLimitResetCredit](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"availableCount":null}`, `{"availableCount":"1"}`, `{"availableCount":1.5}`,
		`{"availableCount":9223372036854775808}`, `{"availableCount":0,"credits":{}}`,
		`{"availableCount":0,"credits":[{}]}`, `{"availableCount":0,"availableCount":1}`,
		`{"availableCount":0} {}`, `{"availableCount":0} x`,
	} {
		assertJSONRejects[RateLimitResetCreditsSummary](t, input)
	}
}

func TestAccountRateLimitEnvelopeRecordsCanonicalizeAndMapLastWins(t *testing.T) {
	nullSnapshot := `{"limitId":null,"limitName":null,"primary":null,"secondary":null,"credits":null,"individualLimit":null,"planType":null,"rateLimitReachedType":null}`
	assertAccountRateLimitRoundTrip[GetAccountRateLimitsResponse](t,
		`{"rateLimits":{}}`,
		`{"rateLimits":`+nullSnapshot+`,"rateLimitsByLimitId":null,"rateLimitResetCredits":null}`,
	)
	assertAccountRateLimitRoundTrip[GetAccountRateLimitsResponse](t,
		`{"future":true,"rateLimitResetCredits":{"availableCount":0,"credits":[]},"rateLimitsByLimitId":{"":{"limitId":"first"},"dup":{"limitId":"first"},"dup":{"limitId":"last"}},"rateLimits":{"limitId":"root"}}`,
		`{"rateLimits":{"limitId":"root","limitName":null,"primary":null,"secondary":null,"credits":null,"individualLimit":null,"planType":null,"rateLimitReachedType":null},"rateLimitsByLimitId":{"":{"limitId":"first","limitName":null,"primary":null,"secondary":null,"credits":null,"individualLimit":null,"planType":null,"rateLimitReachedType":null},"dup":{"limitId":"last","limitName":null,"primary":null,"secondary":null,"credits":null,"individualLimit":null,"planType":null,"rateLimitReachedType":null}},"rateLimitResetCredits":{"availableCount":0,"credits":[]}}`,
	)
	assertAccountRateLimitRoundTrip[ConsumeAccountRateLimitResetCreditParams](t,
		`{"idempotencyKey":""}`, `{"idempotencyKey":"","creditId":null}`,
	)
	assertAccountRateLimitRoundTrip[ConsumeAccountRateLimitResetCreditParams](t,
		`{"future":1,"creditId":" credit ","idempotencyKey":" key "}`,
		`{"idempotencyKey":" key ","creditId":" credit "}`,
	)
	assertAccountRateLimitRoundTrip[ConsumeAccountRateLimitResetCreditResponse](t,
		`{"future":true,"outcome":"alreadyRedeemed"}`, `{"outcome":"alreadyRedeemed"}`,
	)
	assertAccountRateLimitRoundTrip[AccountRateLimitsUpdatedNotification](t,
		`{"future":true,"rateLimits":{}}`, `{"rateLimits":`+nullSnapshot+`}`,
	)
}

func TestAccountRateLimitEnvelopeRecordsRejectMalformedForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"rateLimits":null}`, `{"rateLimits":[]}`, `{"rateLimits":{"primary":{}}}`,
		`{"rateLimits":{},"rateLimitsByLimitId":[]}`,
		`{"rateLimits":{},"rateLimitsByLimitId":{"x":null}}`,
		`{"rateLimits":{},"rateLimitResetCredits":{}}`,
		`{"rateLimits":{},"rateLimits":{}}`, `{"rateLimits":{}} {}`, `{"rateLimits":{}} x`,
	} {
		assertJSONRejects[GetAccountRateLimitsResponse](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"idempotencyKey":null}`, `{"idempotencyKey":1}`, `{"idempotencyKey":"k","creditId":1}`,
		`{"idempotencyKey":"k","idempotencyKey":"x"}`, `{"idempotencyKey":"k"} {}`, `{"idempotencyKey":"k"} x`,
	} {
		assertJSONRejects[ConsumeAccountRateLimitResetCreditParams](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"outcome":null}`, `{"outcome":"other"}`, `{"outcome":"reset","outcome":"noCredit"}`,
		`{"outcome":"reset"} {}`, `{"outcome":"reset"} x`,
	} {
		assertJSONRejects[ConsumeAccountRateLimitResetCreditResponse](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"rateLimits":null}`, `{"rateLimits":{"planType":1}}`,
		`{"rateLimits":{},"rateLimits":{}}`, `{"rateLimits":{}} {}`, `{"rateLimits":{}} x`,
	} {
		assertJSONRejects[AccountRateLimitsUpdatedNotification](t, input)
	}
}

func TestAccountRateLimitContractsNilReceiversAndInvalidMarshalFailClosed(t *testing.T) {
	for name, unmarshal := range map[string]func() error{
		"PlanType":                   func() error { var v *PlanType; return v.UnmarshalJSON([]byte(`"free"`)) },
		"RateLimitReachedType":       func() error { var v *RateLimitReachedType; return v.UnmarshalJSON([]byte(`"rate_limit_reached"`)) },
		"RateLimitResetType":         func() error { var v *RateLimitResetType; return v.UnmarshalJSON([]byte(`"codexRateLimits"`)) },
		"RateLimitResetCreditStatus": func() error { var v *RateLimitResetCreditStatus; return v.UnmarshalJSON([]byte(`"available"`)) },
		"ConsumeOutcome": func() error {
			var v *ConsumeAccountRateLimitResetCreditOutcome
			return v.UnmarshalJSON([]byte(`"reset"`))
		},
		"RateLimitWindow": func() error { var v *RateLimitWindow; return v.UnmarshalJSON([]byte(`{"usedPercent":0}`)) },
		"CreditsSnapshot": func() error {
			var v *CreditsSnapshot
			return v.UnmarshalJSON([]byte(`{"hasCredits":false,"unlimited":false}`))
		},
		"SpendControlLimitSnapshot":    func() error { var v *SpendControlLimitSnapshot; return v.UnmarshalJSON([]byte(`{}`)) },
		"RateLimitSnapshot":            func() error { var v *RateLimitSnapshot; return v.UnmarshalJSON([]byte(`{}`)) },
		"RateLimitResetCredit":         func() error { var v *RateLimitResetCredit; return v.UnmarshalJSON([]byte(`{}`)) },
		"RateLimitResetCreditsSummary": func() error { var v *RateLimitResetCreditsSummary; return v.UnmarshalJSON([]byte(`{}`)) },
		"GetAccountRateLimitsResponse": func() error { var v *GetAccountRateLimitsResponse; return v.UnmarshalJSON([]byte(`{}`)) },
		"ConsumeParams":                func() error { var v *ConsumeAccountRateLimitResetCreditParams; return v.UnmarshalJSON([]byte(`{}`)) },
		"ConsumeResponse":              func() error { var v *ConsumeAccountRateLimitResetCreditResponse; return v.UnmarshalJSON([]byte(`{}`)) },
		"UpdatedNotification":          func() error { var v *AccountRateLimitsUpdatedNotification; return v.UnmarshalJSON([]byte(`{}`)) },
	} {
		if err := unmarshal(); err == nil {
			t.Errorf("nil %s receiver succeeded", name)
		}
	}
	for name, marshal := range map[string]func() error{
		"PlanType":                   func() error { _, err := json.Marshal(PlanType("invalid")); return err },
		"RateLimitReachedType":       func() error { _, err := json.Marshal(RateLimitReachedType("invalid")); return err },
		"RateLimitResetType":         func() error { _, err := json.Marshal(RateLimitResetType("invalid")); return err },
		"RateLimitResetCreditStatus": func() error { _, err := json.Marshal(RateLimitResetCreditStatus("invalid")); return err },
		"ConsumeOutcome":             func() error { _, err := json.Marshal(ConsumeAccountRateLimitResetCreditOutcome("invalid")); return err },
	} {
		if err := marshal(); err == nil {
			t.Errorf("invalid %s marshaled", name)
		}
	}
}

func TestAccountRateLimitContractsRemainStandaloneAndDeferred(t *testing.T) {
	names := []string{
		"PlanType", "RateLimitReachedType", "RateLimitResetType", "RateLimitResetCreditStatus",
		"ConsumeAccountRateLimitResetCreditOutcome", "RateLimitWindow", "CreditsSnapshot",
		"SpendControlLimitSnapshot", "RateLimitSnapshot", "RateLimitResetCredit",
		"RateLimitResetCreditsSummary", "GetAccountRateLimitsResponse",
		"ConsumeAccountRateLimitResetCreditParams", "ConsumeAccountRateLimitResetCreditResponse",
		"AccountRateLimitsUpdatedNotification",
	}
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
	for methodName, surface := range map[string]Surface{
		"account/rateLimits/read":              SurfaceClientRequest,
		"account/rateLimitResetCredit/consume": SurfaceClientRequest,
		"account/rateLimits/updated":           SurfaceServerNotification,
	} {
		method, ok := LookupMethod(methodName)
		if !ok || method.Surface != surface || method.State != MethodDeferredStub {
			t.Errorf("%s = %#v, %v; want deferred %s", methodName, method, ok, surface)
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

func TestAccountRateLimitTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	wants := []string{
		`export type PlanType = "free" | "go" | "plus" | "pro" | "prolite" | "team" | "self_serve_business_usage_based" | "business" | "enterprise_cbp_usage_based" | "enterprise" | "edu" | "unknown";`,
		`export type RateLimitReachedType = "rate_limit_reached" | "workspace_owner_credits_depleted" | "workspace_member_credits_depleted" | "workspace_owner_usage_limit_reached" | "workspace_member_usage_limit_reached";`,
		`export type RateLimitResetType = "codexRateLimits" | "unknown";`,
		`export type RateLimitResetCreditStatus = "available" | "redeeming" | "redeemed" | "unknown";`,
		`export type ConsumeAccountRateLimitResetCreditOutcome = "reset" | "nothingToReset" | "noCredit" | "alreadyRedeemed";`,
		"export type RateLimitWindow = {\n  \"resetsAt\": number | null;\n  \"usedPercent\": number;\n  \"windowDurationMins\": number | null;\n};",
		"export type CreditsSnapshot = {\n  \"balance\": string | null;\n  \"hasCredits\": boolean;\n  \"unlimited\": boolean;\n};",
		"export type SpendControlLimitSnapshot = {\n  \"limit\": string;\n  \"remainingPercent\": number;\n  \"resetsAt\": number;\n  \"used\": string;\n};",
		"export type RateLimitSnapshot = {\n  \"credits\": CreditsSnapshot | null;\n  \"individualLimit\": SpendControlLimitSnapshot | null;\n  \"limitId\": string | null;\n  \"limitName\": string | null;\n  \"planType\": PlanType | null;\n  \"primary\": RateLimitWindow | null;\n  \"rateLimitReachedType\": RateLimitReachedType | null;\n  \"secondary\": RateLimitWindow | null;\n};",
		"export type RateLimitResetCreditsSummary = {\n  \"availableCount\": bigint;\n  \"credits\": Array<RateLimitResetCredit> | null;\n};",
		"export type GetAccountRateLimitsResponse = {\n  \"rateLimitResetCredits\": RateLimitResetCreditsSummary | null;\n  \"rateLimits\": RateLimitSnapshot;\n  \"rateLimitsByLimitId\": { [key in string]?: RateLimitSnapshot } | null;\n};",
		"export type ConsumeAccountRateLimitResetCreditParams = {\n  \"creditId\"?: string | null;\n  \"idempotencyKey\": string;\n};",
		"export type ConsumeAccountRateLimitResetCreditResponse = {\n  \"outcome\": ConsumeAccountRateLimitResetCreditOutcome;\n};",
		"export type AccountRateLimitsUpdatedNotification = {\n  \"rateLimits\": RateLimitSnapshot;\n};",
	}
	for _, want := range wants {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
	for _, name := range []string{
		"RateLimitWindow", "CreditsSnapshot", "SpendControlLimitSnapshot", "RateLimitSnapshot",
		"RateLimitResetCredit", "RateLimitResetCreditsSummary", "GetAccountRateLimitsResponse",
		"ConsumeAccountRateLimitResetCreditResponse", "AccountRateLimitsUpdatedNotification",
	} {
		declaration := accountRateLimitTypeScriptDeclaration(t, generated, name)
		if strings.Contains(declaration, "Record<string, unknown>") {
			t.Errorf("%s TypeScript remains structurally open: %s", name, declaration)
		}
	}
	credit := accountRateLimitTypeScriptDeclaration(t, generated, "RateLimitResetCredit")
	for _, field := range []string{`"grantedAt": number;`, `"expiresAt": number | null;`} {
		if !strings.Contains(credit, field) {
			t.Errorf("RateLimitResetCredit missing %q", field)
		}
	}
}

func assertAccountRateLimitRoundTrip[T any](t *testing.T, input, want string) {
	t.Helper()
	var value T
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("unmarshal %T %s: %v", value, input, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %T %s = %s, %v; want %s", value, input, encoded, err, want)
	}
}

func accountRateLimitTypeScriptDeclaration(t *testing.T, generated []byte, name string) string {
	t.Helper()
	prefix := "export type " + name + " = "
	start := strings.Index(string(generated), prefix)
	if start < 0 {
		t.Fatalf("generated TypeScript missing declaration %s", name)
	}
	rest := string(generated[start:])
	end := strings.Index(rest, ";\n\n")
	if end < 0 {
		t.Fatalf("generated TypeScript declaration %s has no terminator", name)
	}
	return rest[:end+1]
}
