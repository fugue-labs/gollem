package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

func TestConversationTextRoleContractIsExact(t *testing.T) {
	definitions := JSONSchema()["$defs"].(Schema)
	want := Schema{"type": "string", "enum": []any{"user", "developer", "assistant"}}
	if got := definitions["ConversationTextRole"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("ConversationTextRole = %#v, want %#v", got, want)
	}

	for _, role := range []ConversationTextRole{
		ConversationTextRoleUser,
		ConversationTextRoleDeveloper,
		ConversationTextRoleAssistant,
	} {
		encoded, err := json.Marshal(role)
		if err != nil || string(encoded) != `"`+string(role)+`"` {
			t.Fatalf("marshal %q = %s, %v", role, encoded, err)
		}
		assertJSONAccepts[ConversationTextRole](t, string(encoded))
	}

	for _, input := range []string{`null`, `"system"`, `"User"`, `1`, `{}`} {
		assertJSONRejects[ConversationTextRole](t, input)
	}
	if _, err := json.Marshal(ConversationTextRole("system")); err == nil {
		t.Fatal("unknown role serialized")
	}
	var nilRole *ConversationTextRole
	if err := nilRole.UnmarshalJSON([]byte(`"user"`)); err == nil {
		t.Fatal("nil role receiver succeeded")
	}

	for _, binding := range WireTypeBindings() {
		if slices.Contains(binding.Params, "ConversationTextRole") || slices.Contains(binding.Result, "ConversationTextRole") {
			t.Fatalf("standalone conversation role unexpectedly bound to %s", binding.Method)
		}
	}
	if got := len(definitions); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}
