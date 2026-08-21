package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestThreadSectionParamSchemasAreExact(t *testing.T) {
	defs := JSONSchema()["$defs"].(Schema)
	appearanceOrNull := []any{
		Schema{"$ref": "#/$defs/ThreadSectionAppearance"},
		Schema{"type": "null"},
	}
	want := map[string]Schema{
		"ThreadSectionAppearance": {
			"description": "Extensible visual presentation for a custom thread section.",
			"properties": Schema{
				"color": Schema{"type": []any{"string", "null"}},
				"icon":  Schema{"type": []any{"string", "null"}},
			},
			"type": "object",
		},
		"ThreadSectionCreateParams": {
			"description": "Parameters for creating an independently persisted thread section.",
			"properties": Schema{
				"appearance": Schema{"anyOf": appearanceOrNull, "default": nil},
				"name": Schema{
					"description": "The user-visible name of the section.",
					"type":        "string",
				},
			},
			"required": []string{"name"},
			"type":     "object",
		},
		"ThreadSectionDeleteParams": {
			"description": "Parameters for deleting an independently persisted thread section.",
			"properties": Schema{
				"sectionId": Schema{
					"description": "The stable, server-generated identity of the section to delete.",
					"type":        "string",
				},
			},
			"required": []string{"sectionId"},
			"type":     "object",
		},
		"ThreadSectionListParams": {
			"description": "Parameters for listing independently persisted thread sections.",
			"properties": Schema{
				"cursor": Schema{
					"description": "Opaque pagination cursor returned by a previous call.",
					"type":        []any{"string", "null"},
				},
				"limit": Schema{
					"description": "Maximum number of sections to return.",
					"format":      "uint32",
					"minimum":     json.Number("0.0"),
					"type":        []any{"integer", "null"},
				},
			},
			"type": "object",
		},
		"ThreadSectionMoveParams": {
			"description": "Parameters for moving a thread within a server-owned section ordering.",
			"properties": Schema{
				"beforeThreadId": Schema{
					"description": "Existing thread to insert before; omission or null appends to the section.",
					"type":        []any{"string", "null"},
				},
				"sectionId": Schema{
					"description": "Destination section, or `null` to remove the thread from its section.",
					"type":        []any{"string", "null"},
				},
				"threadId": Schema{
					"description": "Thread to move into, within, or out of a section.",
					"type":        "string",
				},
			},
			"required": []string{"sectionId", "threadId"},
			"type":     "object",
		},
		"ThreadSectionUpdateParams": {
			"description": "Parameters for updating an independently persisted thread section.",
			"properties": Schema{
				"appearance": Schema{
					"anyOf":       appearanceOrNull,
					"description": "Omit to preserve appearance, use `null` to clear it, or provide a replacement.",
				},
				"name": Schema{
					"description": "The updated user-visible name of the section.",
					"type":        "string",
				},
				"sectionId": Schema{
					"description": "The stable, server-generated identity of the section to update.",
					"type":        "string",
				},
			},
			"required": []string{"name", "sectionId"},
			"type":     "object",
		},
	}
	for name, expected := range want {
		if got := defs[name]; !reflect.DeepEqual(got, expected) {
			t.Errorf("%s = %#v, want %#v", name, got, expected)
		}
	}
}

func TestThreadSectionParamsPreserveSerdeWireForms(t *testing.T) {
	assertThreadSectionRoundTrip[ThreadSectionAppearance](t,
		`{"future":true}`, `{"icon":null,"color":null}`)
	assertThreadSectionRoundTrip[ThreadSectionAppearance](t,
		`{"icon":"bookmark","color":null,"future":true}`, `{"icon":"bookmark","color":null}`)
	assertThreadSectionRoundTrip[ThreadSectionCreateParams](t,
		`{"name":"Pinned","future":true}`, `{"name":"Pinned","appearance":null}`)
	assertThreadSectionRoundTrip[ThreadSectionCreateParams](t,
		`{"name":"Pinned","appearance":{"color":"blue","future":true}}`, `{"name":"Pinned","appearance":{"icon":null,"color":"blue"}}`)
	assertThreadSectionRoundTrip[ThreadSectionDeleteParams](t,
		`{"sectionId":"section","future":true}`, `{"sectionId":"section"}`)
	assertThreadSectionRoundTrip[ThreadSectionListParams](t,
		`{"future":true}`, `{"cursor":null,"limit":null}`)
	assertThreadSectionRoundTrip[ThreadSectionListParams](t,
		`{"cursor":"next","limit":4294967295,"future":true}`, `{"cursor":"next","limit":4294967295}`)
	assertThreadSectionRoundTrip[ThreadSectionMoveParams](t,
		`{"threadId":"thread","sectionId":null,"future":true}`, `{"threadId":"thread","sectionId":null,"beforeThreadId":null}`)
	assertThreadSectionRoundTrip[ThreadSectionMoveParams](t,
		`{"threadId":"thread","sectionId":"section","beforeThreadId":"before","future":true}`, `{"threadId":"thread","sectionId":"section","beforeThreadId":"before"}`)
	assertThreadSectionRoundTrip[ThreadSectionUpdateParams](t,
		`{"sectionId":"section","name":"Pinned","future":true}`, `{"sectionId":"section","name":"Pinned"}`)
	assertThreadSectionRoundTrip[ThreadSectionUpdateParams](t,
		`{"sectionId":"section","name":"Pinned","appearance":null,"future":true}`, `{"sectionId":"section","name":"Pinned","appearance":null}`)
	assertThreadSectionRoundTrip[ThreadSectionUpdateParams](t,
		`{"sectionId":"section","name":"Pinned","appearance":{"icon":"bookmark","color":"blue","future":true}}`, `{"sectionId":"section","name":"Pinned","appearance":{"icon":"bookmark","color":"blue"}}`)

	update := ThreadSectionUpdateParams{SectionID: "section", Name: "Pinned"}
	update.SetAppearance(nil)
	encoded, err := json.Marshal(update)
	if err != nil || string(encoded) != `{"sectionId":"section","name":"Pinned","appearance":null}` {
		t.Fatalf("explicit-null update = %s, %v", encoded, err)
	}
	appearance := &ThreadSectionAppearance{}
	update = ThreadSectionUpdateParams{SectionID: "section", Name: "Pinned", Appearance: appearance}
	encoded, err = json.Marshal(update)
	if err != nil || string(encoded) != `{"sectionId":"section","name":"Pinned","appearance":{"icon":null,"color":null}}` {
		t.Fatalf("replacement update = %s, %v", encoded, err)
	}
}

func TestThreadSectionParamsRejectMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`,
		`{"icon":1}`, `{"icon":"one","icon":"two"}`, `{"icon":"one"} {}`,
	} {
		assertJSONRejects[ThreadSectionAppearance](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `{}`, `{"name":null}`, `{"name":1}`,
		`{"name":"Pinned","appearance":1}`, `{"name":"Pinned","appearance":{"icon":"one","icon":"two"}}`,
		`{"name":"Pinned","name":"duplicate"}`, `{"name":"Pinned"} {}`,
	} {
		assertJSONRejects[ThreadSectionCreateParams](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `{}`, `{"sectionId":null}`, `{"sectionId":1}`,
		`{"sectionId":"section","sectionId":"duplicate"}`, `{"sectionId":"section"} {}`,
	} {
		assertJSONRejects[ThreadSectionDeleteParams](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `{"cursor":1}`, `{"limit":-1}`, `{"limit":4294967296}`,
		`{"limit":1.5}`, `{"cursor":"next","cursor":"duplicate"}`, `{} {}`,
	} {
		assertJSONRejects[ThreadSectionListParams](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `{}`, `{"threadId":"thread"}`, `{"sectionId":null}`,
		`{"threadId":null,"sectionId":null}`, `{"threadId":"thread","sectionId":1}`,
		`{"threadId":"thread","sectionId":null,"beforeThreadId":1}`,
		`{"threadId":"thread","sectionId":null,"sectionId":"duplicate"}`, `{"threadId":"thread","sectionId":null} {}`,
	} {
		assertJSONRejects[ThreadSectionMoveParams](t, input)
	}
	for _, input := range []string{
		``, `null`, `[]`, `{}`, `{"sectionId":"section"}`, `{"name":"Pinned"}`,
		`{"sectionId":null,"name":"Pinned"}`, `{"sectionId":"section","name":null}`,
		`{"sectionId":"section","name":"Pinned","appearance":1}`,
		`{"sectionId":"section","name":"Pinned","appearance":{"color":"one","color":"two"}}`,
		`{"sectionId":"section","name":"Pinned","appearance":null,"appearance":{}}`,
		`{"sectionId":"section","name":"Pinned"} {}`,
	} {
		assertJSONRejects[ThreadSectionUpdateParams](t, input)
	}
}

func TestThreadSectionParamsRemainStandalone(t *testing.T) {
	for _, value := range []json.Unmarshaler{
		(*ThreadSectionAppearance)(nil),
		(*ThreadSectionCreateParams)(nil),
		(*ThreadSectionDeleteParams)(nil),
		(*ThreadSectionListParams)(nil),
		(*ThreadSectionMoveParams)(nil),
		(*ThreadSectionUpdateParams)(nil),
	} {
		if err := value.UnmarshalJSON([]byte(`{}`)); err == nil {
			t.Errorf("nil %T receiver succeeded", value)
		}
	}
	for _, binding := range WireTypeBindings() {
		for _, name := range append(append([]string(nil), binding.Params...), binding.Result...) {
			if strings.HasPrefix(name, "ThreadSection") {
				t.Fatalf("%s unexpectedly bound to %s", name, binding.Method)
			}
		}
	}
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
}

func TestThreadSectionParamsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	for _, want := range []string{
		`export type ThreadSectionAppearance = { icon: string | null, color: string | null, };`,
		`export type ThreadSectionCreateParams = { name: string, appearance?: ThreadSectionAppearance | null, };`,
		`export type ThreadSectionDeleteParams = { sectionId: string, };`,
		`export type ThreadSectionListParams = { cursor?: string | null, limit?: number | null, };`,
		`export type ThreadSectionMoveParams = { threadId: string, sectionId: string | null, beforeThreadId?: string | null, };`,
		`export type ThreadSectionUpdateParams = { sectionId: string, name: string, appearance?: ThreadSectionAppearance | null, };`,
	} {
		if !strings.Contains(string(generated), want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func assertThreadSectionRoundTrip[T any](t *testing.T, input, want string) {
	t.Helper()
	var value T
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		t.Fatalf("Unmarshal(%s): %v", input, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != want {
		t.Fatalf("round trip %s = %s, %v; want %s", input, encoded, err, want)
	}
}
