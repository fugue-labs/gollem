package protocol

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestRuntimeClientBindingsAreExact(t *testing.T) {
	want := map[string]WireTypeBinding{
		"item/agentMessage/delta": {
			Method: "item/agentMessage/delta", Surface: SurfaceServerNotification,
			Params: []string{"RuntimeDeltaNotification"},
		},
		"item/reasoning/textDelta": {
			Method: "item/reasoning/textDelta", Surface: SurfaceServerNotification,
			Params: []string{"RuntimeDeltaNotification"},
		},
		"model/list": {
			Method: "model/list", Surface: SurfaceClientRequest,
			Params: []string{"ModelCatalogListParams"}, Result: []string{"ModelCatalogListResponse"},
		},
		"provider/list": {
			Method: "provider/list", Surface: SurfaceGollemExtension,
			Params: []string{"ProviderListParams"}, Result: []string{"ProviderListResponse"},
		},
		"provider/health/probe": {
			Method: "provider/health/probe", Surface: SurfaceGollemExtension,
			Params: []string{"ProviderHealthProbeParams"}, Result: []string{"ProviderHealthProbeResponse"},
		},
		"thread/start": {
			Method: "thread/start", Surface: SurfaceClientRequest,
			Params: []string{"ThreadRunStartParams"}, Result: []string{"ThreadRunStartResult"},
		},
		"thread/started": {
			Method: "thread/started", Surface: SurfaceServerNotification,
			Params: []string{"RuntimeThreadNotification"},
		},
		"thread/rollback": {
			Method: "thread/rollback", Surface: SurfaceClientRequest,
			Params: []string{"ThreadHistoryRollbackParams"}, Result: []string{"ThreadHistoryRollbackResult"},
		},
		"turn/completed": {
			Method: "turn/completed", Surface: SurfaceServerNotification,
			Params: []string{"RuntimeTurnNotification"},
		},
		"turn/interrupt": {
			Method: "turn/interrupt", Surface: SurfaceClientRequest,
			Params: []string{"TurnRunInterruptParams"}, Result: []string{"TurnRunInterruptResult"},
		},
		"turn/start": {
			Method: "turn/start", Surface: SurfaceClientRequest,
			Params: []string{"TurnRunStartParams"}, Result: []string{"TurnRunStartResult"},
		},
		"turn/steer": {
			Method: "turn/steer", Surface: SurfaceClientRequest,
			Params: []string{"TurnSteerParams"}, Result: []string{"TurnSteerResponse"},
		},
		"turn/started": {
			Method: "turn/started", Surface: SurfaceServerNotification,
			Params: []string{"RuntimeTurnNotification"},
		},
	}

	got := make(map[string]WireTypeBinding)
	for _, binding := range WireTypeBindings() {
		if _, ok := want[binding.Method]; ok {
			got[binding.Method] = binding
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runtime client bindings = %#v, want %#v", got, want)
	}
}

func TestRuntimeModelParamsExposePromptCacheAndReasoningSummarySettings(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	properties := defs["RuntimeModelParams"].(Schema)["properties"].(Schema)
	for _, name := range []string{"promptCacheEnabled", "reasoningSummary"} {
		if _, ok := properties[name]; !ok {
			t.Fatalf("RuntimeModelParams schema omitted %s", name)
		}
	}
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`"promptCacheEnabled"?: boolean | null;`,
		`"reasoningSummary"?: string | null;`,
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("RuntimeModelParams TypeScript omitted %q", want)
		}
	}
}

func TestModelCatalogCapabilitiesExposeRuntimeControls(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	properties := defs["ModelCatalogCapabilities"].(Schema)["properties"].(Schema)
	for _, name := range []string{"adaptiveThinking", "manualThinking", "reasoningSummaries", "sampling", "stopSequences"} {
		if _, ok := properties[name]; !ok {
			t.Fatalf("ModelCatalogCapabilities schema omitted %s", name)
		}
	}
	providerProperties := defs["ProviderCatalogCapabilities"].(Schema)["properties"].(Schema)
	for _, name := range []string{"sampling", "stopSequences"} {
		if _, ok := providerProperties[name]; !ok {
			t.Fatalf("ProviderCatalogCapabilities schema omitted %s", name)
		}
	}
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`"adaptiveThinking": boolean;`,
		`"manualThinking": boolean;`,
		`"reasoningSummaries": boolean;`,
		`"sampling": boolean;`,
		`"stopSequences": boolean;`,
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("ModelCatalogCapabilities TypeScript missing %q", want)
		}
	}
}

func TestModelCatalogDefaultReasoningEffortIsNullable(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	properties := defs["ModelCatalogEntry"].(Schema)["properties"].(Schema)
	if got := properties["defaultReasoningEffort"]; !reflect.DeepEqual(got, nullableStringSchema()) {
		t.Fatalf("ModelCatalogEntry.defaultReasoningEffort = %#v, want nullable string", got)
	}
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	if want := `"defaultReasoningEffort": string | null;`; !strings.Contains(string(generated), want) {
		t.Errorf("ModelCatalogEntry TypeScript missing %q", want)
	}
}

func TestRuntimeClientSchemasAreClosedAndCredentialFree(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	names := []string{
		"ModelCatalogAvailabilityNux",
		"ModelCatalogCapabilities",
		"ModelCatalogEntry",
		"ModelCatalogListParams",
		"ModelCatalogListResponse",
		"ModelCatalogReasoningEffortOption",
		"ModelCatalogServiceTier",
		"ModelCatalogUpgradeInfo",
		"ProviderCatalogCapabilities",
		"ProviderCatalogEntry",
		"ProviderHealthProbeParams",
		"ProviderHealthProbeResponse",
		"ProviderListParams",
		"ProviderListResponse",
		"RuntimeDeltaNotification",
		"RuntimeModelParams",
		"RuntimeThreadNotification",
		"RuntimeTurnNotification",
		"ThreadHistoryRollbackParams",
		"ThreadHistoryRollbackRecord",
		"ThreadHistoryRollbackResult",
		"ThreadRunStartParams",
		"ThreadRunStartResult",
		"TurnRunInterruptParams",
		"TurnRunInterruptResult",
		"TurnRunStartParams",
		"TurnRunStartResult",
	}
	for _, name := range names {
		definition, ok := defs[name].(Schema)
		if !ok {
			t.Errorf("schema missing %s", name)
			continue
		}
		if definition["type"] != "object" || definition["additionalProperties"] != false {
			t.Errorf("%s is not a closed object: %#v", name, definition)
		}
	}

	required := map[string][]string{
		"ModelCatalogListResponse":    {"data", "nextCursor"},
		"ProviderListResponse":        {"data", "providers"},
		"ProviderHealthProbeParams":   {"providerId"},
		"ProviderHealthProbeResponse": {"providerId", "status"},
		"ThreadRunStartResult":        {"thread", "turn"},
		"ThreadHistoryRollbackResult": {
			"thread", "removedTurnIds", "marker", "workspaceEffectsReverted",
		},
		"TurnRunStartResult":        {"thread", "turn"},
		"TurnRunInterruptResult":    {"ok", "turnId"},
		"RuntimeThreadNotification": {"threadId", "at"},
		"RuntimeTurnNotification":   {"threadId", "turnId", "at"},
		"RuntimeDeltaNotification":  {"threadId", "turnId", "delta", "at"},
	}
	for name, fields := range required {
		definition := defs[name].(Schema)
		got := schemaRequiredNames(definition)
		if !slices.Equal(got, fields) {
			t.Errorf("%s required = %v, want %v", name, got, fields)
		}
	}
	assertSchemaEnum(t, defs, "ProviderHealthProbeStatus", []any{
		"available", "unavailable", "not-configured", "unsupported",
	})

	for _, name := range []string{
		"ModelCatalogListParams",
		"ProviderListParams",
		"ProviderHealthProbeParams",
		"RuntimeModelParams",
		"ThreadHistoryRollbackParams",
		"ThreadRunStartParams",
		"TurnRunInterruptParams",
		"TurnRunStartParams",
	} {
		properties := defs[name].(Schema)["properties"].(Schema)
		for _, forbidden := range []string{
			"apiKey", "accessToken", "refreshToken", "authorization",
			"credential", "credentials", "password", "secret",
		} {
			if _, ok := properties[forbidden]; ok {
				t.Errorf("%s exposes credential field %s", name, forbidden)
			}
		}
	}
}
