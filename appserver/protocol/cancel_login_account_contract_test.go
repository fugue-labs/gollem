package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestCancelLoginAccountSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	assertStringEnum(t, defs["CancelLoginAccountStatus"], "canceled", "notFound")

	params := defs["CancelLoginAccountParams"].(Schema)
	if params["type"] != "object" || params["additionalProperties"] != false {
		t.Fatalf("CancelLoginAccountParams is not a closed object: %#v", params)
	}
	if got := schemaRequiredNames(params); !slices.Equal(got, []string{"loginId"}) {
		t.Fatalf("CancelLoginAccountParams required = %v", got)
	}
	wantParamsProperties := Schema{"loginId": Schema{"type": "string"}}
	if got, want := params["properties"], wantParamsProperties; !reflect.DeepEqual(got, want) {
		t.Fatalf("CancelLoginAccountParams properties = %#v, want %#v", got, want)
	}

	response := defs["CancelLoginAccountResponse"].(Schema)
	if response["type"] != "object" || response["additionalProperties"] != false {
		t.Fatalf("CancelLoginAccountResponse is not a closed object: %#v", response)
	}
	if got := schemaRequiredNames(response); !slices.Equal(got, []string{"status"}) {
		t.Fatalf("CancelLoginAccountResponse required = %v", got)
	}
	wantResponseProperties := Schema{
		"status": Schema{"$ref": "#/$defs/CancelLoginAccountStatus"},
	}
	if got := response["properties"]; !reflect.DeepEqual(got, wantResponseProperties) {
		t.Fatalf("CancelLoginAccountResponse properties = %#v, want %#v", got, wantResponseProperties)
	}
}

func TestCancelLoginAccountContractsAcceptExactWireForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{input: `{"loginId":""}`, want: `{"loginId":""}`},
		{input: `{"loginId":"login-1"}`, want: `{"loginId":"login-1"}`},
	} {
		var params CancelLoginAccountParams
		if err := json.Unmarshal([]byte(tc.input), &params); err != nil {
			t.Errorf("unmarshal params %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(params)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("params round trip = %s, %v; want %s", encoded, err, tc.want)
		}
	}

	for _, status := range []CancelLoginAccountStatus{
		CancelLoginAccountStatusCanceled,
		CancelLoginAccountStatusNotFound,
	} {
		encoded, err := json.Marshal(status)
		if err != nil {
			t.Errorf("marshal status %q: %v", status, err)
			continue
		}
		var roundTrip CancelLoginAccountStatus
		if err := json.Unmarshal(encoded, &roundTrip); err != nil || roundTrip != status {
			t.Errorf("status round trip = %q, %v; want %q", roundTrip, err, status)
		}
	}

	for _, status := range []string{"canceled", "notFound"} {
		input := `{"status":"` + status + `"}`
		var response CancelLoginAccountResponse
		if err := json.Unmarshal([]byte(input), &response); err != nil {
			t.Errorf("unmarshal response %s: %v", input, err)
			continue
		}
		encoded, err := json.Marshal(response)
		if err != nil || string(encoded) != input {
			t.Errorf("response round trip = %s, %v; want %s", encoded, err, input)
		}
	}
}

func TestCancelLoginAccountContractsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `{}`,
		`{"loginId":null}`, `{"loginId":1}`, `{"loginId":true}`,
		`{"loginId":{}}`, `{"loginId":[]}`, `{"login_id":"id"}`,
		`{"loginId":"id","extra":true}`, `{"loginId":"id"} {}`,
	} {
		assertJSONRejects[CancelLoginAccountParams](t, input)
	}

	for _, input := range []string{
		``, `null`, `""`, `"other"`, `"Canceled"`, `"not_found"`,
		`1`, `true`, `{}`, `[]`, `"canceled" {}`,
	} {
		assertJSONRejects[CancelLoginAccountStatus](t, input)
	}

	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `{}`,
		`{"status":null}`, `{"status":""}`, `{"status":"other"}`,
		`{"status":"Canceled"}`, `{"status":1}`, `{"Status":"canceled"}`,
		`{"status":"canceled","extra":true}`, `{"status":"canceled"} {}`,
	} {
		assertJSONRejects[CancelLoginAccountResponse](t, input)
	}
}

func TestCancelLoginAccountNilReceiversAndInvalidMarshalFailClosed(t *testing.T) {
	var params *CancelLoginAccountParams
	if err := params.UnmarshalJSON([]byte(`{"loginId":"id"}`)); err == nil {
		t.Fatal("nil CancelLoginAccountParams receiver succeeded")
	}
	var status *CancelLoginAccountStatus
	if err := status.UnmarshalJSON([]byte(`"canceled"`)); err == nil {
		t.Fatal("nil CancelLoginAccountStatus receiver succeeded")
	}
	var response *CancelLoginAccountResponse
	if err := response.UnmarshalJSON([]byte(`{"status":"canceled"}`)); err == nil {
		t.Fatal("nil CancelLoginAccountResponse receiver succeeded")
	}
	if _, err := json.Marshal(CancelLoginAccountStatus("other")); err == nil {
		t.Fatal("invalid CancelLoginAccountStatus marshaled")
	}
	if _, err := json.Marshal(CancelLoginAccountResponse{Status: CancelLoginAccountStatus("other")}); err == nil {
		t.Fatal("response with invalid status marshaled")
	}
}

func TestCancelLoginAccountContractsRemainStandalone(t *testing.T) {
	names := []string{
		"CancelLoginAccountParams",
		"CancelLoginAccountResponse",
		"CancelLoginAccountStatus",
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range names {
			if slices.Contains(binding.Params, name) || slices.Contains(binding.Result, name) {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
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

func TestCancelLoginAccountTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type CancelLoginAccountParams = {`,
		`"loginId": string;`,
		`export type CancelLoginAccountResponse = {`,
		`"status": CancelLoginAccountStatus;`,
		`export type CancelLoginAccountStatus = "canceled" | "notFound";`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

var (
	_ json.Unmarshaler = (*CancelLoginAccountParams)(nil)
	_ json.Marshaler   = CancelLoginAccountStatus("")
	_ json.Unmarshaler = (*CancelLoginAccountStatus)(nil)
	_ json.Unmarshaler = (*CancelLoginAccountResponse)(nil)
)
