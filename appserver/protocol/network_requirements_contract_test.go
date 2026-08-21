package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestNetworkRequirementSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	for _, name := range []string{"NetworkDomainPermission", "NetworkUnixSocketPermission"} {
		got := defs[name].(Schema)
		if got["type"] != "string" || !reflect.DeepEqual(got["enum"], []any{"allow", "deny"}) {
			t.Errorf("%s schema = %#v, want allow/deny string enum", name, got)
		}
	}

	fields := []string{
		"enabled", "httpPort", "socksPort", "allowUpstreamProxy",
		"dangerouslyAllowNonLoopbackProxy", "dangerouslyAllowAllUnixSockets",
		"domains", "managedAllowedDomainsOnly", "allowedDomains", "deniedDomains",
		"unixSockets", "allowUnixSockets", "allowLocalBinding",
	}
	requirements := defs["NetworkRequirements"].(Schema)
	assertClosedObjectSchema(t, requirements, fields...)
	properties := requirements["properties"].(Schema)
	for _, name := range []string{
		"enabled", "allowUpstreamProxy", "dangerouslyAllowNonLoopbackProxy",
		"dangerouslyAllowAllUnixSockets", "managedAllowedDomainsOnly", "allowLocalBinding",
	} {
		assertConfigNullableSchema(t, properties[name], Schema{"type": "boolean"})
	}
	for _, name := range []string{"httpPort", "socksPort"} {
		assertConfigNullableSchema(t, properties[name], Schema{
			"type": "integer", "minimum": 0, "maximum": 65535,
		})
	}
	assertConfigNullableSchema(
		t,
		properties["domains"],
		networkRequirementsMapSchema("NetworkDomainPermission"),
	)
	assertConfigNullableSchema(
		t,
		properties["unixSockets"],
		networkRequirementsMapSchema("NetworkUnixSocketPermission"),
	)
	for _, name := range []string{"allowedDomains", "deniedDomains", "allowUnixSockets"} {
		assertConfigNullableSchema(t, properties[name], Schema{
			"type": "array", "items": Schema{"type": "string"},
		})
	}
}

func TestNetworkRequirementLeafValuesAreExact(t *testing.T) {
	for _, value := range []NetworkDomainPermission{
		NetworkDomainPermissionAllow, NetworkDomainPermissionDeny,
	} {
		assertConfigRequirementEnumRoundTrip(t, value)
	}
	for _, value := range []NetworkUnixSocketPermission{
		NetworkUnixSocketPermissionAllow, NetworkUnixSocketPermissionDeny,
	} {
		assertConfigRequirementEnumRoundTrip(t, value)
	}

	for _, input := range []string{`null`, `"permit"`, `""`, `1`, `true`, `[]`, `"allow" {}`} {
		assertJSONRejects[NetworkDomainPermission](t, input)
		assertJSONRejects[NetworkUnixSocketPermission](t, input)
	}
}

func TestNetworkRequirementsAcceptExactWireForms(t *testing.T) {
	nullCanonical := `{"enabled":null,"httpPort":null,"socksPort":null,"allowUpstreamProxy":null,"dangerouslyAllowNonLoopbackProxy":null,"dangerouslyAllowAllUnixSockets":null,"domains":null,"managedAllowedDomainsOnly":null,"allowedDomains":null,"deniedDomains":null,"unixSockets":null,"allowUnixSockets":null,"allowLocalBinding":null}`
	var omitted NetworkRequirements
	assertConfigRequirementsRoundTrip(t, `{}`, nullCanonical, &omitted)
	var explicitNull NetworkRequirements
	assertConfigRequirementsRoundTrip(t, nullCanonical, nullCanonical, &explicitNull)
	emptyCollections := `{"domains":{},"allowedDomains":[],"deniedDomains":[],"unixSockets":{},"allowUnixSockets":[]}`
	emptyCollectionsCanonical := `{"enabled":null,"httpPort":null,"socksPort":null,"allowUpstreamProxy":null,"dangerouslyAllowNonLoopbackProxy":null,"dangerouslyAllowAllUnixSockets":null,"domains":{},"managedAllowedDomainsOnly":null,"allowedDomains":[],"deniedDomains":[],"unixSockets":{},"allowUnixSockets":[],"allowLocalBinding":null}`
	var emptyValues NetworkRequirements
	assertConfigRequirementsRoundTrip(t, emptyCollections, emptyCollectionsCanonical, &emptyValues)

	full := `{"enabled":false,"httpPort":0,"socksPort":65535,"allowUpstreamProxy":true,"dangerouslyAllowNonLoopbackProxy":false,"dangerouslyAllowAllUnixSockets":true,"domains":{"":"allow","example.com":"deny"},"managedAllowedDomainsOnly":false,"allowedDomains":["","example.com","example.com"],"deniedDomains":[],"unixSockets":{"":"deny","/tmp/codex.sock":"allow"},"allowUnixSockets":["","/tmp/codex.sock","/tmp/codex.sock"],"allowLocalBinding":true}`
	var fullValue NetworkRequirements
	assertConfigRequirementsRoundTrip(t, full, full, &fullValue)
}

func TestNetworkRequirementsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		`null`, `[]`, `"value"`, `1`, `true`,
		`{"enabled":0}`,
		`{"httpPort":"80"}`,
		`{"httpPort":-1}`,
		`{"httpPort":1.5}`,
		`{"httpPort":65536}`,
		`{"socksPort":-1}`,
		`{"socksPort":1.5}`,
		`{"socksPort":65536}`,
		`{"allowUpstreamProxy":"true"}`,
		`{"dangerouslyAllowNonLoopbackProxy":0}`,
		`{"dangerouslyAllowAllUnixSockets":[]}`,
		`{"domains":[]}`,
		`{"domains":{"example.com":null}}`,
		`{"domains":{"example.com":"permit"}}`,
		`{"managedAllowedDomainsOnly":1}`,
		`{"allowedDomains":{}}`,
		`{"allowedDomains":[null]}`,
		`{"deniedDomains":[1]}`,
		`{"unixSockets":[]}`,
		`{"unixSockets":{"/tmp/codex.sock":null}}`,
		`{"unixSockets":{"/tmp/codex.sock":"permit"}}`,
		`{"allowUnixSockets":[null]}`,
		`{"allowLocalBinding":"false"}`,
		`{"extra":true}`,
		`{} {}`,
	} {
		assertJSONRejects[NetworkRequirements](t, input)
	}
}

func TestNetworkRequirementsNilReceiversAndInvalidMarshal(t *testing.T) {
	var domain *NetworkDomainPermission
	if err := domain.UnmarshalJSON([]byte(`"allow"`)); err == nil {
		t.Fatal("nil NetworkDomainPermission receiver succeeded")
	}
	var socket *NetworkUnixSocketPermission
	if err := socket.UnmarshalJSON([]byte(`"allow"`)); err == nil {
		t.Fatal("nil NetworkUnixSocketPermission receiver succeeded")
	}
	var requirements *NetworkRequirements
	if err := requirements.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil NetworkRequirements receiver succeeded")
	}
	if _, err := json.Marshal(NetworkDomainPermission("permit")); err == nil {
		t.Fatal("invalid NetworkDomainPermission marshal succeeded")
	}
	if _, err := json.Marshal(NetworkUnixSocketPermission("permit")); err == nil {
		t.Fatal("invalid NetworkUnixSocketPermission marshal succeeded")
	}
	invalidDomains := map[string]NetworkDomainPermission{"example.com": "permit"}
	if _, err := json.Marshal(NetworkRequirements{Domains: &invalidDomains}); err == nil {
		t.Fatal("invalid nested domain permission marshal succeeded")
	}
	invalidSockets := map[string]NetworkUnixSocketPermission{"/tmp/codex.sock": "permit"}
	if _, err := json.Marshal(NetworkRequirements{UnixSockets: &invalidSockets}); err == nil {
		t.Fatal("invalid nested Unix-socket permission marshal succeeded")
	}
}

func TestNetworkRequirementContractsRemainStandalone(t *testing.T) {
	names := []string{"NetworkDomainPermission", "NetworkUnixSocketPermission", "NetworkRequirements"}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	configProperties := JSONSchema()["$defs"].(Schema)["ConfigRequirements"].(Schema)["properties"].(Schema)
	if _, ok := configProperties["network"]; ok {
		t.Fatal("standalone NetworkRequirements widened ConfigRequirements")
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

func TestNetworkRequirementsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type NetworkDomainPermission = "allow" | "deny";`,
		`export type NetworkUnixSocketPermission = "allow" | "deny";`,
		`export type NetworkRequirements = {`,
		`"enabled": boolean | null;`,
		`"httpPort": number | null;`,
		`"socksPort": number | null;`,
		`"domains": { [key in string]?: NetworkDomainPermission } | null;`,
		`"allowedDomains": Array<string> | null;`,
		`"unixSockets": { [key in string]?: NetworkUnixSocketPermission } | null;`,
		`"allowUnixSockets": Array<string> | null;`,
		`"allowLocalBinding": boolean | null;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func networkRequirementsMapSchema(permission string) Schema {
	return Schema{
		"type": "object", "additionalProperties": Schema{"$ref": "#/$defs/" + permission},
		"x-gollem-typescript-optional-map": true,
	}
}
