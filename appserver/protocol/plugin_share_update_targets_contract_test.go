package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPluginShareUpdateTargetsSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	for name, want := range map[string]Schema{
		"PluginShareUpdateDiscoverability": pluginShareUpdateDiscoverabilitySchema(),
		"PluginShareUpdateTargetsParams":   pluginShareUpdateTargetsParamsSchema(),
	} {
		if got := defs[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestPluginShareUpdateDiscoverabilityIsClosed(t *testing.T) {
	for _, value := range []PluginShareUpdateDiscoverability{
		PluginShareUpdateDiscoverabilityUnlisted,
		PluginShareUpdateDiscoverabilityPrivate,
		PluginShareUpdateDiscoverabilityListed,
	} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("Marshal(%q): %v", value, err)
		}
		var decoded PluginShareUpdateDiscoverability
		if err := json.Unmarshal(encoded, &decoded); err != nil || decoded != value {
			t.Fatalf("round trip = %q, %v; want %q", decoded, err, value)
		}
	}
	for _, input := range []string{`null`, `"unlisted"`, `"OTHER"`, `1`, `{}`, `[]`, `"LISTED" {}`} {
		var value PluginShareUpdateDiscoverability
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("Unmarshal(%s) succeeded", input)
		}
	}
	if _, err := json.Marshal(PluginShareUpdateDiscoverability("OTHER")); err == nil {
		t.Fatal("invalid update discoverability marshaled")
	}
	var value *PluginShareUpdateDiscoverability
	if err := value.UnmarshalJSON([]byte(`"LISTED"`)); err == nil {
		t.Fatal("nil update-discoverability receiver succeeded")
	}
}

func TestPluginShareUpdateTargetsPreserveSerdeWireForms(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`{"remotePluginId":" remote ","discoverability":"UNLISTED","shareTargets":[],"future":true}`, `{"remotePluginId":" remote ","discoverability":"UNLISTED","shareTargets":[]}`},
		{`{"remotePluginId":"plugin","discoverability":"PRIVATE","shareTargets":[{"principalType":"user","principalId":" user ","role":"reader"}]}`, `{"remotePluginId":"plugin","discoverability":"PRIVATE","shareTargets":[{"principalType":"user","principalId":" user ","role":"reader"}]}`},
	} {
		var value PluginShareUpdateTargetsParams
		if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Fatalf("Unmarshal(%s): %v", tc.input, err)
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != tc.want {
			t.Fatalf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
	if encoded, err := json.Marshal(PluginShareUpdateTargetsParams{RemotePluginID: "plugin", Discoverability: PluginShareUpdateDiscoverabilityListed}); err != nil || string(encoded) != `{"remotePluginId":"plugin","discoverability":"LISTED","shareTargets":[]}` {
		t.Fatalf("nil targets = %s, %v", encoded, err)
	}
}

func TestPluginShareUpdateTargetsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{}`,
		`{"discoverability":"LISTED","shareTargets":[]}`, `{"remotePluginId":"plugin","shareTargets":[]}`,
		`{"remotePluginId":"plugin","discoverability":null,"shareTargets":[]}`,
		`{"remotePluginId":"plugin","discoverability":"listed","shareTargets":[]}`,
		`{"remotePluginId":"plugin","discoverability":"LISTED","shareTargets":null}`,
		`{"remotePluginId":"plugin","discoverability":"LISTED","shareTargets":[null]}`,
		`{"remotePluginId":"plugin","discoverability":"LISTED","shareTargets":[{"principalType":"user","principalId":"id","role":"owner"}]}`,
		`{"remotePluginId":"plugin","remotePluginId":"other","discoverability":"LISTED","shareTargets":[]}`,
		`{"remotePluginId":"plugin","discoverability":"LISTED","shareTargets":[]} {}`,
	} {
		assertJSONRejects[PluginShareUpdateTargetsParams](t, input)
	}
}

func TestPluginShareUpdateTargetsRemainStandalone(t *testing.T) {
	var params *PluginShareUpdateTargetsParams
	if err := params.UnmarshalJSON([]byte(`{"remotePluginId":"plugin","discoverability":"LISTED","shareTargets":[]}`)); err == nil {
		t.Fatal("nil params receiver succeeded")
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			if name == "PluginShareUpdateDiscoverability" || name == "PluginShareUpdateTargetsParams" {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestPluginShareUpdateTargetsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`export type PluginShareUpdateDiscoverability = "UNLISTED" | "PRIVATE" | "LISTED";`,
		`export type PluginShareUpdateTargetsParams = { remotePluginId: string, discoverability: PluginShareUpdateDiscoverability, shareTargets: Array<PluginShareTarget>, };`,
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}
