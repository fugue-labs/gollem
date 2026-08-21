package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestTerminalInteractionNotificationContractIsExact(t *testing.T) {
	definitions := JSONSchema()["$defs"].(Schema)
	if got, want := definitions["TerminalInteractionNotification"], terminalInteractionNotificationSchema(); !reflect.DeepEqual(got, want) {
		t.Fatalf("TerminalInteractionNotification = %#v, want %#v", got, want)
	}
}

func TestTerminalInteractionNotificationPreservesSerdeWireForms(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{
			`{"threadId":"thread-1","turnId":"turn-1","itemId":"item-1","processId":"process-1","stdin":"printf ok\\n"}`,
			`{"threadId":"thread-1","turnId":"turn-1","itemId":"item-1","processId":"process-1","stdin":"printf ok\\n"}`,
		},
		{
			`{"future":true,"stdin":"","processId":"process-2","itemId":"item-2","turnId":"turn-2","threadId":"thread-2"}`,
			`{"threadId":"thread-2","turnId":"turn-2","itemId":"item-2","processId":"process-2","stdin":""}`,
		},
	} {
		var value TerminalInteractionNotification
		if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
			t.Errorf("unmarshal %s: %v", tc.input, err)
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("round trip %s = %s, %v; want %s", tc.input, encoded, err, tc.want)
		}
	}
}

func TestTerminalInteractionNotificationRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"turnId":"turn","itemId":"item","processId":"process","stdin":""}`,
		`{"threadId":"thread","itemId":"item","processId":"process","stdin":""}`,
		`{"threadId":"thread","turnId":"turn","processId":"process","stdin":""}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","stdin":""}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","processId":"process"}`,
		`{"threadId":null,"turnId":"turn","itemId":"item","processId":"process","stdin":""}`,
		`{"threadId":"thread","turnId":1,"itemId":"item","processId":"process","stdin":""}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","processId":"process","stdin":false}`,
		`{"threadId":"thread","threadId":"other","turnId":"turn","itemId":"item","processId":"process","stdin":""}`,
		`{"threadId":"thread","turnId":"turn","itemId":"item","processId":"process","stdin":""} {}`,
	} {
		assertJSONRejects[TerminalInteractionNotification](t, input)
	}
	var value *TerminalInteractionNotification
	if err := value.UnmarshalJSON([]byte(`{"threadId":"thread","turnId":"turn","itemId":"item","processId":"process","stdin":""}`)); err == nil {
		t.Fatal("nil notification receiver succeeded")
	}
}

func TestTerminalInteractionNotificationRemainsStandalone(t *testing.T) {
	for _, binding := range WireTypeBindings() {
		if binding.Method == "item/commandExecution/terminalInteraction" ||
			slices.Contains(binding.Params, "TerminalInteractionNotification") ||
			slices.Contains(binding.Result, "TerminalInteractionNotification") {
			t.Fatalf("TerminalInteractionNotification unexpectedly bound to %s", binding.Method)
		}
	}
	info, ok := LookupMethod("item/commandExecution/terminalInteraction")
	if !ok || info.Surface != SurfaceServerNotification || info.State != MethodBlocked {
		t.Fatalf("terminal interaction method = %#v, %v; want blocked server notification", info, ok)
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	want := `export type TerminalInteractionNotification = {
  "itemId": string;
  "processId": string;
  "stdin": string;
  "threadId": string;
  "turnId": string;
};`
	if !strings.Contains(string(generated), want) {
		t.Fatalf("generated TypeScript missing %q", want)
	}
}
