package core

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJSONFileExporter(t *testing.T) {
	dir := t.TempDir()
	exporter := NewJSONFileExporter(dir)

	model := NewTestModel(TextResponse("traced"))
	agent := NewAgent[string](model, WithTraceExporter[string](exporter))

	_, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	// Check that a JSON file was created.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 trace file, got %d", len(entries))
	}

	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}

	var trace RunTrace
	if err := json.Unmarshal(data, &trace); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if trace.Prompt != "test" {
		t.Errorf("expected prompt 'test', got %q", trace.Prompt)
	}
	if !trace.Success {
		t.Error("expected trace success=true")
	}
}

func TestConsoleExporter(t *testing.T) {
	var buf bytes.Buffer
	exporter := NewConsoleExporter(&buf)

	model := NewTestModel(TextResponse("console"))
	agent := NewAgent[string](model, WithTraceExporter[string](exporter))

	_, err := agent.Run(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	if !strings.Contains(output, "Trace") {
		t.Errorf("expected trace output, got %q", output)
	}
	if !strings.Contains(output, "Prompt: hello") {
		t.Errorf("expected prompt in output, got %q", output)
	}
	if !strings.Contains(output, "Success: true") {
		t.Errorf("expected success in output, got %q", output)
	}
}

func TestMultiExporter(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	exp1 := NewConsoleExporter(&buf1)
	exp2 := NewConsoleExporter(&buf2)
	multi := NewMultiExporter(exp1, exp2)

	model := NewTestModel(TextResponse("multi"))
	agent := NewAgent[string](model, WithTraceExporter[string](multi))

	_, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	if buf1.Len() == 0 {
		t.Error("exporter 1 received no output")
	}
	if buf2.Len() == 0 {
		t.Error("exporter 2 received no output")
	}
}

func TestWithTraceExporter_ImplicitTracing(t *testing.T) {
	var buf bytes.Buffer
	exporter := NewConsoleExporter(&buf)

	model := NewTestModel(TextResponse("implicit"))
	// Don't add WithTracing explicitly — it should be enabled by the exporter.
	agent := NewAgent[string](model, WithTraceExporter[string](exporter))

	result, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if result.Trace == nil {
		t.Error("expected trace to be set when exporter is configured")
	}
}

type failingExporter struct{}

func (e *failingExporter) Export(_ context.Context, _ *RunTrace) error {
	return context.DeadlineExceeded
}

func TestTraceExporter_ErrorHandling(t *testing.T) {
	model := NewTestModel(TextResponse("ok"))
	agent := NewAgent[string](model, WithTraceExporter[string](&failingExporter{}))

	// Run should succeed even if exporter fails.
	result, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal("expected run to succeed despite exporter error")
	}
	if result.Output != "ok" {
		t.Errorf("expected 'ok', got %q", result.Output)
	}
}
