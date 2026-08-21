package protocol

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestProcessContractsSchemasAreExact(t *testing.T) {
	wantTerminalSize := Schema{
		"description": "PTY size in character cells for `process/spawn` PTY sessions.",
		"properties": Schema{
			"cols": Schema{
				"description": "Terminal width in character cells.",
				"format":      "uint16", "minimum": float64(0), "type": "integer",
			},
			"rows": Schema{
				"description": "Terminal height in character cells.",
				"format":      "uint16", "minimum": float64(0), "type": "integer",
			},
		},
		"required": []string{"cols", "rows"},
		"type":     "object",
	}
	wantStream := Schema{
		"description": "Stream label for `process/outputDelta` notifications.",
		"oneOf": []any{
			Schema{
				"description": "stdout stream. PTY mode multiplexes terminal output here.",
				"enum":        []any{"stdout"}, "type": "string",
			},
			Schema{
				"description": "stderr stream.",
				"enum":        []any{"stderr"}, "type": "string",
			},
		},
	}
	wantOutputDelta := Schema{
		"description": "Base64-encoded output chunk emitted for a streaming `process/spawn` request.",
		"properties": Schema{
			"capReached": Schema{
				"description": "True on the final streamed chunk for this stream when output was truncated by `outputBytesCap`.",
				"type":        "boolean",
			},
			"deltaBase64": Schema{
				"description": "Base64-encoded output bytes.",
				"type":        "string",
			},
			"processHandle": Schema{
				"description": "Client-supplied, connection-scoped `processHandle` from `process/spawn`.",
				"type":        "string",
			},
			"stream": Schema{
				"allOf":       []any{Schema{"$ref": "#/$defs/ProcessOutputStream"}},
				"description": "Output stream this chunk belongs to.",
			},
		},
		"required": []string{"capReached", "deltaBase64", "processHandle", "stream"},
		"type":     "object",
	}
	wantExited := Schema{
		"description": "Final process exit notification for `process/spawn`.",
		"properties": Schema{
			"exitCode": Schema{
				"description": "Process exit code.",
				"format":      "int32", "type": "integer",
			},
			"processHandle": Schema{
				"description": "Client-supplied, connection-scoped `processHandle` from `process/spawn`.",
				"type":        "string",
			},
			"stderr": Schema{
				"description": "Buffered stderr capture.\n\nEmpty when stderr was streamed via `process/outputDelta`.",
				"type":        "string",
			},
			"stderrCapReached": Schema{
				"description": "Whether stderr reached `outputBytesCap`.\n\nIn streaming mode, stderr is empty and cap state is also reported on the final stderr `process/outputDelta` notification.",
				"type":        "boolean",
			},
			"stdout": Schema{
				"description": "Buffered stdout capture.\n\nEmpty when stdout was streamed via `process/outputDelta`.",
				"type":        "string",
			},
			"stdoutCapReached": Schema{
				"description": "Whether stdout reached `outputBytesCap`.\n\nIn streaming mode, stdout is empty and cap state is also reported on the final stdout `process/outputDelta` notification.",
				"type":        "boolean",
			},
		},
		"required": []string{
			"exitCode", "processHandle", "stderr", "stderrCapReached", "stdout", "stdoutCapReached",
		},
		"type": "object",
	}

	definitions := JSONSchema()["$defs"].(Schema)
	for name, want := range map[string]Schema{
		"ProcessTerminalSize":            wantTerminalSize,
		"ProcessOutputStream":            wantStream,
		"ProcessOutputDeltaNotification": wantOutputDelta,
		"ProcessExitedNotification":      wantExited,
	} {
		if got := definitions[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
}

func TestProcessTerminalSizeAcceptsSerdeWireForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{`{"rows":0,"cols":0}`, `{"rows":0,"cols":0}`},
		{`{"cols":65535,"rows":65535}`, `{"rows":65535,"cols":65535}`},
		{`{"future":true,"rows":24,"cols":80}`, `{"rows":24,"cols":80}`},
	} {
		var value ProcessTerminalSize
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

func TestProcessTerminalSizeRejectsMalformedWireForms(t *testing.T) {
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"rows":1}`, `{"cols":1}`, `{"rows":null,"cols":1}`, `{"rows":1,"cols":null}`,
		`{"rows":-1,"cols":1}`, `{"rows":65536,"cols":1}`, `{"rows":1.5,"cols":1}`,
		`{"rows":"1","cols":1}`, `{"rows":1,"cols":false}`,
		`{"rows":1,"rows":2,"cols":3}`, `{"rows":1,"cols":2,"cols":3}`,
		`{"rows":1,"cols":2} {}`, `{"rows":1,"cols":2} x`,
	} {
		assertJSONRejects[ProcessTerminalSize](t, input)
	}
}

func TestProcessOutputStreamAcceptsExactValues(t *testing.T) {
	for _, value := range []ProcessOutputStream{ProcessOutputStreamStdout, ProcessOutputStreamStderr} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Errorf("marshal %q: %v", value, err)
			continue
		}
		var decoded ProcessOutputStream
		if err := json.Unmarshal(encoded, &decoded); err != nil || decoded != value {
			t.Errorf("round trip %q = %q, %v", value, decoded, err)
		}
	}
}

func TestProcessOutputStreamRejectsInvalidValues(t *testing.T) {
	for _, input := range []string{
		``, `null`, `true`, `1`, `{}`, `[]`, `""`, `"STDOUT"`, `"stdout "`, `"other"`,
		`"stdout" "stderr"`, `"stdout" x`,
	} {
		assertJSONRejects[ProcessOutputStream](t, input)
	}
	if _, err := json.Marshal(ProcessOutputStream("other")); err == nil {
		t.Fatal("invalid ProcessOutputStream marshaled")
	}
}

func TestProcessOutputDeltaAcceptsSerdeWireForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{
			`{"processHandle":"","stream":"stdout","deltaBase64":"not base64","capReached":false}`,
			`{"processHandle":"","stream":"stdout","deltaBase64":"not base64","capReached":false}`,
		},
		{
			`{"future":1,"capReached":true,"deltaBase64":" AA== ","stream":"stderr","processHandle":" handle "}`,
			`{"processHandle":" handle ","stream":"stderr","deltaBase64":" AA== ","capReached":true}`,
		},
	} {
		var value ProcessOutputDeltaNotification
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

func TestProcessOutputDeltaRejectsMalformedWireForms(t *testing.T) {
	valid := `"processHandle":"p","stream":"stdout","deltaBase64":"","capReached":false`
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"stream":"stdout","deltaBase64":"","capReached":false}`,
		`{"processHandle":"p","deltaBase64":"","capReached":false}`,
		`{"processHandle":"p","stream":"stdout","capReached":false}`,
		`{"processHandle":"p","stream":"stdout","deltaBase64":""}`,
		`{"processHandle":null,"stream":"stdout","deltaBase64":"","capReached":false}`,
		`{"processHandle":"p","stream":"other","deltaBase64":"","capReached":false}`,
		`{"processHandle":"p","stream":"stdout","deltaBase64":1,"capReached":false}`,
		`{"processHandle":"p","stream":"stdout","deltaBase64":"","capReached":null}`,
		`{` + valid + `,"stream":"stderr"}`, `{` + valid + `,"future":1} {}`, `{` + valid + `} x`,
	} {
		assertJSONRejects[ProcessOutputDeltaNotification](t, input)
	}
	if _, err := json.Marshal(ProcessOutputDeltaNotification{Stream: ProcessOutputStream("other")}); err == nil {
		t.Fatal("output delta with invalid stream marshaled")
	}
}

func TestProcessExitedAcceptsSerdeWireFormsAndInt32Bounds(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{
			`{"processHandle":"","exitCode":0,"stdout":"","stdoutCapReached":false,"stderr":"","stderrCapReached":false}`,
			`{"processHandle":"","exitCode":0,"stdout":"","stdoutCapReached":false,"stderr":"","stderrCapReached":false}`,
		},
		{
			`{"future":true,"stderrCapReached":true,"stderr":" e ","stdoutCapReached":true,"stdout":" o ","exitCode":-2147483648,"processHandle":" p "}`,
			`{"processHandle":" p ","exitCode":-2147483648,"stdout":" o ","stdoutCapReached":true,"stderr":" e ","stderrCapReached":true}`,
		},
		{
			`{"processHandle":"p","exitCode":2147483647,"stdout":"o","stdoutCapReached":false,"stderr":"e","stderrCapReached":false}`,
			`{"processHandle":"p","exitCode":2147483647,"stdout":"o","stdoutCapReached":false,"stderr":"e","stderrCapReached":false}`,
		},
	} {
		var value ProcessExitedNotification
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

func TestProcessExitedRejectsMalformedWireForms(t *testing.T) {
	valid := `"processHandle":"p","exitCode":0,"stdout":"","stdoutCapReached":false,"stderr":"","stderrCapReached":false`
	for _, input := range []string{
		``, `null`, `[]`, `"value"`, `1`, `true`, `{`, `{}`,
		`{"exitCode":0,"stdout":"","stdoutCapReached":false,"stderr":"","stderrCapReached":false}`,
		`{"processHandle":"p","stdout":"","stdoutCapReached":false,"stderr":"","stderrCapReached":false}`,
		`{"processHandle":"p","exitCode":0,"stdoutCapReached":false,"stderr":"","stderrCapReached":false}`,
		`{"processHandle":"p","exitCode":0,"stdout":"","stderr":"","stderrCapReached":false}`,
		`{"processHandle":"p","exitCode":0,"stdout":"","stdoutCapReached":false,"stderrCapReached":false}`,
		`{"processHandle":"p","exitCode":0,"stdout":"","stdoutCapReached":false,"stderr":""}`,
		`{"processHandle":null,"exitCode":0,"stdout":"","stdoutCapReached":false,"stderr":"","stderrCapReached":false}`,
		`{"processHandle":"p","exitCode":2147483648,"stdout":"","stdoutCapReached":false,"stderr":"","stderrCapReached":false}`,
		`{"processHandle":"p","exitCode":-2147483649,"stdout":"","stdoutCapReached":false,"stderr":"","stderrCapReached":false}`,
		`{"processHandle":"p","exitCode":1.5,"stdout":"","stdoutCapReached":false,"stderr":"","stderrCapReached":false}`,
		`{"processHandle":"p","exitCode":0,"stdout":null,"stdoutCapReached":false,"stderr":"","stderrCapReached":false}`,
		`{"processHandle":"p","exitCode":0,"stdout":"","stdoutCapReached":null,"stderr":"","stderrCapReached":false}`,
		`{` + valid + `,"exitCode":1}`, `{` + valid + `} {}`, `{` + valid + `} x`,
	} {
		assertJSONRejects[ProcessExitedNotification](t, input)
	}
}

func TestProcessContractsNilReceiversFailClosed(t *testing.T) {
	var terminal *ProcessTerminalSize
	if err := terminal.UnmarshalJSON([]byte(`{"rows":1,"cols":1}`)); err == nil {
		t.Fatal("nil ProcessTerminalSize receiver succeeded")
	}
	var stream *ProcessOutputStream
	if err := stream.UnmarshalJSON([]byte(`"stdout"`)); err == nil {
		t.Fatal("nil ProcessOutputStream receiver succeeded")
	}
	var output *ProcessOutputDeltaNotification
	if err := output.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ProcessOutputDeltaNotification receiver succeeded")
	}
	var exited *ProcessExitedNotification
	if err := exited.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("nil ProcessExitedNotification receiver succeeded")
	}
}

func TestProcessContractsRemainStandaloneFromLegacyRuntime(t *testing.T) {
	names := []string{
		"ProcessTerminalSize", "ProcessOutputStream",
		"ProcessOutputDeltaNotification", "ProcessExitedNotification",
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
	for _, method := range []string{"process/outputDelta", "process/exited"} {
		info, ok := LookupMethod(method)
		if !ok || info.Surface != SurfaceServerNotification || info.State != MethodImplemented {
			t.Fatalf("%s = %#v, %v; want unchanged implemented notification", method, info, ok)
		}
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

func TestProcessContractsTypeScriptIsExact(t *testing.T) {
	generated, err := MarshalTypeScript()
	if err != nil {
		t.Fatalf("MarshalTypeScript: %v", err)
	}
	source := string(generated)
	for _, want := range []string{
		`export type ProcessOutputStream = "stdout" | "stderr";`,
		"export type ProcessTerminalSize = {\n",
		`  "cols": number;`, `  "rows": number;`,
		"export type ProcessOutputDeltaNotification = {\n",
		`  "capReached": boolean;`, `  "deltaBase64": string;`,
		`  "processHandle": string;`, `  "stream": ProcessOutputStream;`,
		"export type ProcessExitedNotification = {\n",
		`  "exitCode": number;`, `  "stderr": string;`, `  "stderrCapReached": boolean;`,
		`  "stdout": string;`, `  "stdoutCapReached": boolean;`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("generated TypeScript missing %q", want)
		}
	}
}

var (
	_ json.Marshaler   = ProcessTerminalSize{}
	_ json.Unmarshaler = (*ProcessTerminalSize)(nil)
	_ json.Marshaler   = ProcessOutputStream("")
	_ json.Unmarshaler = (*ProcessOutputStream)(nil)
	_ json.Marshaler   = ProcessOutputDeltaNotification{}
	_ json.Unmarshaler = (*ProcessOutputDeltaNotification)(nil)
	_ json.Marshaler   = ProcessExitedNotification{}
	_ json.Unmarshaler = (*ProcessExitedNotification)(nil)
)
