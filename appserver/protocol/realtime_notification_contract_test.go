package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestRealtimeNotificationSchemasAreExact(t *testing.T) {
	object := func(description, title string, properties Schema, required ...string) Schema {
		return Schema{
			"$schema":     "http://json-schema.org/draft-07/schema#",
			"description": description,
			"properties":  properties,
			"required":    required,
			"title":       title,
			"type":        "object",
		}
	}
	want := map[string]Schema{
		"RealtimeConversationVersion": stringEnumSchema("v1", "v2", "v3"),
		"ThreadRealtimeAudioChunk": {
			"description": "EXPERIMENTAL - thread realtime audio chunk.",
			"properties": Schema{
				"data":              Schema{"type": "string"},
				"itemId":            Schema{"type": []any{"string", "null"}},
				"numChannels":       Schema{"format": "uint16", "minimum": float64(0), "type": "integer"},
				"sampleRate":        Schema{"format": "uint32", "minimum": float64(0), "type": "integer"},
				"samplesPerChannel": Schema{"format": "uint32", "minimum": float64(0), "type": []any{"integer", "null"}},
			},
			"required": []string{"data", "numChannels", "sampleRate"},
			"type":     "object",
		},
		"ThreadRealtimeClosedNotification": object(
			"EXPERIMENTAL - emitted when thread realtime transport closes.",
			"ThreadRealtimeClosedNotification",
			Schema{"reason": Schema{"type": []any{"string", "null"}}, "threadId": Schema{"type": "string"}},
			"threadId",
		),
		"ThreadRealtimeErrorNotification": object(
			"EXPERIMENTAL - emitted when thread realtime encounters an error.",
			"ThreadRealtimeErrorNotification",
			Schema{"message": Schema{"type": "string"}, "threadId": Schema{"type": "string"}},
			"message", "threadId",
		),
		"ThreadRealtimeItemAddedNotification": object(
			"EXPERIMENTAL - raw non-audio thread realtime item emitted by the backend.",
			"ThreadRealtimeItemAddedNotification",
			Schema{"item": true, "threadId": Schema{"type": "string"}},
			"item", "threadId",
		),
		"ThreadRealtimeOutputAudioDeltaNotification": object(
			"EXPERIMENTAL - streamed output audio emitted by thread realtime.",
			"ThreadRealtimeOutputAudioDeltaNotification",
			Schema{"audio": Schema{"$ref": "#/$defs/ThreadRealtimeAudioChunk"}, "threadId": Schema{"type": "string"}},
			"audio", "threadId",
		),
		"ThreadRealtimeSdpNotification": object(
			"EXPERIMENTAL - emitted with the remote SDP for a WebRTC realtime session.",
			"ThreadRealtimeSdpNotification",
			Schema{"sdp": Schema{"type": "string"}, "threadId": Schema{"type": "string"}},
			"sdp", "threadId",
		),
		"ThreadRealtimeStartedNotification": object(
			"EXPERIMENTAL - emitted when thread realtime startup is accepted.",
			"ThreadRealtimeStartedNotification",
			Schema{
				"realtimeSessionId": Schema{"type": []any{"string", "null"}},
				"threadId":          Schema{"type": "string"},
				"version":           Schema{"$ref": "#/$defs/RealtimeConversationVersion"},
			},
			"threadId", "version",
		),
		"ThreadRealtimeTranscriptDeltaNotification": object(
			"EXPERIMENTAL - flat transcript delta emitted whenever realtime transcript text changes.",
			"ThreadRealtimeTranscriptDeltaNotification",
			Schema{
				"delta":    Schema{"description": "Live transcript delta from the realtime event.", "type": "string"},
				"role":     Schema{"type": "string"},
				"threadId": Schema{"type": "string"},
			},
			"delta", "role", "threadId",
		),
		"ThreadRealtimeTranscriptDoneNotification": object(
			"EXPERIMENTAL - final transcript text emitted when realtime completes a transcript part.",
			"ThreadRealtimeTranscriptDoneNotification",
			Schema{
				"role":     Schema{"type": "string"},
				"text":     Schema{"description": "Final complete text for the transcript part.", "type": "string"},
				"threadId": Schema{"type": "string"},
			},
			"role", "text", "threadId",
		),
	}
	definitions := JSONSchema()["$defs"].(Schema)
	for name, expected := range want {
		if got := definitions[name]; !reflect.DeepEqual(got, expected) {
			t.Errorf("%s = %#v, want %#v", name, got, expected)
		}
	}
}

func TestRealtimeNotificationsPreserveSerdeWireForms(t *testing.T) {
	fixtures := []struct {
		name  string
		input string
		want  string
		value func() any
	}{
		{"audio absent options", `{"future":1,"data":"AA==","sampleRate":4294967295,"numChannels":65535}`, `{"data":"AA==","sampleRate":4294967295,"numChannels":65535,"samplesPerChannel":null,"itemId":null}`, func() any { return new(ThreadRealtimeAudioChunk) }},
		{"audio options", `{"data":"","sampleRate":0,"numChannels":0,"samplesPerChannel":0,"itemId":"item"}`, `{"data":"","sampleRate":0,"numChannels":0,"samplesPerChannel":0,"itemId":"item"}`, func() any { return new(ThreadRealtimeAudioChunk) }},
		{"started absent session", `{"future":1,"threadId":"thread","version":"v2"}`, `{"threadId":"thread","realtimeSessionId":null,"version":"v2"}`, func() any { return new(ThreadRealtimeStartedNotification) }},
		{"started session", `{"threadId":"thread","realtimeSessionId":"session","version":"v3"}`, `{"threadId":"thread","realtimeSessionId":"session","version":"v3"}`, func() any { return new(ThreadRealtimeStartedNotification) }},
		{"item null", `{"future":1,"threadId":"thread","item":null}`, `{"threadId":"thread","item":null}`, func() any { return new(ThreadRealtimeItemAddedNotification) }},
		{"transcript delta", `{"future":1,"threadId":"thread","role":"assistant","delta":"delta"}`, `{"threadId":"thread","role":"assistant","delta":"delta"}`, func() any { return new(ThreadRealtimeTranscriptDeltaNotification) }},
		{"transcript done", `{"future":1,"threadId":"thread","role":"user","text":"text"}`, `{"threadId":"thread","role":"user","text":"text"}`, func() any { return new(ThreadRealtimeTranscriptDoneNotification) }},
		{"output audio", `{"future":1,"threadId":"thread","audio":{"data":"AA==","sampleRate":16000,"numChannels":1}}`, `{"threadId":"thread","audio":{"data":"AA==","sampleRate":16000,"numChannels":1,"samplesPerChannel":null,"itemId":null}}`, func() any { return new(ThreadRealtimeOutputAudioDeltaNotification) }},
		{"sdp", `{"future":1,"threadId":"thread","sdp":"v=0"}`, `{"threadId":"thread","sdp":"v=0"}`, func() any { return new(ThreadRealtimeSdpNotification) }},
		{"error", `{"future":1,"threadId":"thread","message":"message"}`, `{"threadId":"thread","message":"message"}`, func() any { return new(ThreadRealtimeErrorNotification) }},
		{"closed absent reason", `{"future":1,"threadId":"thread"}`, `{"threadId":"thread","reason":null}`, func() any { return new(ThreadRealtimeClosedNotification) }},
		{"closed reason", `{"threadId":"thread","reason":"closed"}`, `{"threadId":"thread","reason":"closed"}`, func() any { return new(ThreadRealtimeClosedNotification) }},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			value := fixture.value()
			if err := json.Unmarshal([]byte(fixture.input), value); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			encoded, err := json.Marshal(value)
			if err != nil || string(encoded) != fixture.want {
				t.Fatalf("round trip = %s, %v; want %s", encoded, err, fixture.want)
			}
		})
	}

	for _, value := range []RealtimeConversationVersion{
		RealtimeConversationVersionV1, RealtimeConversationVersionV2, RealtimeConversationVersionV3,
	} {
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != `"`+string(value)+`"` {
			t.Errorf("version %q = %s, %v", value, encoded, err)
		}
	}
}

func TestRealtimeNotificationsRejectMalformedWireForms(t *testing.T) {
	assertRealtimeNotificationRejects[RealtimeConversationVersion](t, ``, `null`, `[]`, `{}`, `"future"`, `"v1" {}`)
	assertRealtimeNotificationRejects[ThreadRealtimeAudioChunk](t,
		``, `null`, `[]`, `{}`, `{"sampleRate":1,"numChannels":1}`, `{"data":"data","sampleRate":null,"numChannels":1}`,
		`{"data":"data","sampleRate":4294967296,"numChannels":1}`, `{"data":"data","sampleRate":1,"numChannels":65536}`,
		`{"data":"data","sampleRate":1,"numChannels":1,"samplesPerChannel":-1}`, `{"data":"data","sampleRate":1,"numChannels":1,"itemId":false}`,
		`{"data":"one","data":"two","sampleRate":1,"numChannels":1}`, `{} {}`)
	assertRealtimeNotificationRejects[ThreadRealtimeStartedNotification](t,
		``, `null`, `[]`, `{}`, `{"threadId":"thread"}`, `{"threadId":null,"version":"v1"}`,
		`{"threadId":"thread","version":"future"}`, `{"threadId":"thread","realtimeSessionId":false,"version":"v1"}`,
		`{"threadId":"one","threadId":"two","version":"v1"}`, `{} {}`)
	assertRealtimeNotificationRejects[ThreadRealtimeItemAddedNotification](t,
		``, `null`, `[]`, `{}`, `{"threadId":"thread"}`, `{"threadId":null,"item":null}`,
		`{"threadId":"one","threadId":"two","item":null}`, `{} {}`)
	assertRealtimeNotificationRejects[ThreadRealtimeTranscriptDeltaNotification](t,
		``, `null`, `[]`, `{}`, `{"threadId":"thread","role":"assistant"}`, `{"threadId":"thread","role":null,"delta":"delta"}`,
		`{"threadId":"thread","role":"assistant","delta":false}`, `{"threadId":"one","threadId":"two","role":"assistant","delta":"delta"}`, `{} {}`)
	assertRealtimeNotificationRejects[ThreadRealtimeTranscriptDoneNotification](t,
		``, `null`, `[]`, `{}`, `{"threadId":"thread","role":"assistant"}`, `{"threadId":"thread","role":null,"text":"text"}`,
		`{"threadId":"thread","role":"assistant","text":false}`, `{"threadId":"one","threadId":"two","role":"assistant","text":"text"}`, `{} {}`)
	assertRealtimeNotificationRejects[ThreadRealtimeOutputAudioDeltaNotification](t,
		``, `null`, `[]`, `{}`, `{"threadId":"thread"}`, `{"threadId":"thread","audio":null}`,
		`{"threadId":"thread","audio":{"data":"data","sampleRate":1}}`, `{"threadId":"one","threadId":"two","audio":{"data":"data","sampleRate":1,"numChannels":1}}`, `{} {}`)
	assertRealtimeNotificationRejects[ThreadRealtimeSdpNotification](t,
		``, `null`, `[]`, `{}`, `{"threadId":"thread"}`, `{"threadId":null,"sdp":"v=0"}`,
		`{"threadId":"thread","sdp":false}`, `{"threadId":"one","threadId":"two","sdp":"v=0"}`, `{} {}`)
	assertRealtimeNotificationRejects[ThreadRealtimeErrorNotification](t,
		``, `null`, `[]`, `{}`, `{"threadId":"thread"}`, `{"threadId":null,"message":"message"}`,
		`{"threadId":"thread","message":false}`, `{"threadId":"one","threadId":"two","message":"message"}`, `{} {}`)
	assertRealtimeNotificationRejects[ThreadRealtimeClosedNotification](t,
		``, `null`, `[]`, `{}`, `{"reason":"closed"}`, `{"threadId":null}`,
		`{"threadId":"thread","reason":false}`, `{"threadId":"one","threadId":"two"}`, `{} {}`)
}

func TestRealtimeNotificationsFailClosedAndRemainStandalone(t *testing.T) {
	checks := map[string]func() error{
		"version": func() error { var value *RealtimeConversationVersion; return value.UnmarshalJSON([]byte(`"v1"`)) },
		"audio":   func() error { var value *ThreadRealtimeAudioChunk; return value.UnmarshalJSON([]byte(`{}`)) },
		"started": func() error { var value *ThreadRealtimeStartedNotification; return value.UnmarshalJSON([]byte(`{}`)) },
		"item":    func() error { var value *ThreadRealtimeItemAddedNotification; return value.UnmarshalJSON([]byte(`{}`)) },
		"delta": func() error {
			var value *ThreadRealtimeTranscriptDeltaNotification
			return value.UnmarshalJSON([]byte(`{}`))
		},
		"done": func() error {
			var value *ThreadRealtimeTranscriptDoneNotification
			return value.UnmarshalJSON([]byte(`{}`))
		},
		"audio delta": func() error {
			var value *ThreadRealtimeOutputAudioDeltaNotification
			return value.UnmarshalJSON([]byte(`{}`))
		},
		"sdp":    func() error { var value *ThreadRealtimeSdpNotification; return value.UnmarshalJSON([]byte(`{}`)) },
		"error":  func() error { var value *ThreadRealtimeErrorNotification; return value.UnmarshalJSON([]byte(`{}`)) },
		"closed": func() error { var value *ThreadRealtimeClosedNotification; return value.UnmarshalJSON([]byte(`{}`)) },
	}
	for name, check := range checks {
		if err := check(); err == nil {
			t.Errorf("nil %s receiver succeeded", name)
		}
	}

	names := []string{
		"RealtimeConversationVersion", "ThreadRealtimeAudioChunk", "ThreadRealtimeClosedNotification", "ThreadRealtimeErrorNotification",
		"ThreadRealtimeItemAddedNotification", "ThreadRealtimeOutputAudioDeltaNotification", "ThreadRealtimeSdpNotification",
		"ThreadRealtimeStartedNotification", "ThreadRealtimeTranscriptDeltaNotification", "ThreadRealtimeTranscriptDoneNotification",
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
	if got := len(JSONSchema()["$defs"].(Schema)); got != 675 {
		t.Fatalf("definition count = %d, want 675", got)
	}
	if len(Methods()) != 229 || len(WireTypeBindings()) != 85 || len(ItemPayloadBindings()) != 5 {
		t.Fatalf("counts = %d methods/%d method bindings/%d item bindings; want 229/85/5", len(Methods()), len(WireTypeBindings()), len(ItemPayloadBindings()))
	}
}

func TestRealtimeNotificationsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type RealtimeConversationVersion = "v1" | "v2" | "v3";`,
		"export type ThreadRealtimeAudioChunk = {\n  \"data\": string;\n  \"itemId\": string | null;\n  \"numChannels\": number;\n  \"sampleRate\": number;\n  \"samplesPerChannel\": number | null;\n};",
		"export type ThreadRealtimeClosedNotification = {\n  \"reason\": string | null;\n  \"threadId\": string;\n};",
		"export type ThreadRealtimeErrorNotification = {\n  \"message\": string;\n  \"threadId\": string;\n};",
		"export type ThreadRealtimeItemAddedNotification = {\n  \"item\": JsonValue;\n  \"threadId\": string;\n};",
		"export type ThreadRealtimeOutputAudioDeltaNotification = {\n  \"audio\": ThreadRealtimeAudioChunk;\n  \"threadId\": string;\n};",
		"export type ThreadRealtimeSdpNotification = {\n  \"sdp\": string;\n  \"threadId\": string;\n};",
		"export type ThreadRealtimeStartedNotification = {\n  \"realtimeSessionId\": string | null;\n  \"threadId\": string;\n  \"version\": RealtimeConversationVersion;\n};",
		"export type ThreadRealtimeTranscriptDeltaNotification = {\n  \"delta\": string;\n  \"role\": string;\n  \"threadId\": string;\n};",
		"export type ThreadRealtimeTranscriptDoneNotification = {\n  \"role\": string;\n  \"text\": string;\n  \"threadId\": string;\n};",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

func assertRealtimeNotificationRejects[T any](t *testing.T, inputs ...string) {
	t.Helper()
	for _, input := range inputs {
		assertJSONRejects[T](t, input)
	}
}
