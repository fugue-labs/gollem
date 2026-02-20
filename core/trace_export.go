package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// TraceExporter exports a completed RunTrace to an external system.
type TraceExporter interface {
	Export(ctx context.Context, trace *RunTrace) error
}

// JSONFileExporter writes traces as JSON files to a directory.
type JSONFileExporter struct {
	dir string
}

// NewJSONFileExporter creates a trace exporter that writes JSON files.
func NewJSONFileExporter(dir string) *JSONFileExporter {
	return &JSONFileExporter{dir: dir}
}

func (e *JSONFileExporter) Export(_ context.Context, trace *RunTrace) error {
	if err := os.MkdirAll(e.dir, 0o755); err != nil {
		return fmt.Errorf("json exporter: create dir: %w", err)
	}

	filename := fmt.Sprintf("trace_%s_%s.json", trace.RunID, trace.StartTime.Format("20060102T150405"))
	path := filepath.Join(e.dir, filename)

	data, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		return fmt.Errorf("json exporter: marshal: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("json exporter: write: %w", err)
	}
	return nil
}

// ConsoleExporter prints a human-readable trace summary to an io.Writer.
type ConsoleExporter struct {
	w io.Writer
}

// NewConsoleExporter creates a trace exporter that writes to an io.Writer.
func NewConsoleExporter(w io.Writer) *ConsoleExporter {
	return &ConsoleExporter{w: w}
}

func (e *ConsoleExporter) Export(_ context.Context, trace *RunTrace) error {
	fmt.Fprintf(e.w, "=== Trace %s ===\n", trace.RunID)
	fmt.Fprintf(e.w, "Prompt: %s\n", trace.Prompt)
	fmt.Fprintf(e.w, "Duration: %s\n", trace.Duration)
	fmt.Fprintf(e.w, "Success: %v\n", trace.Success)
	if trace.Error != "" {
		fmt.Fprintf(e.w, "Error: %s\n", trace.Error)
	}
	fmt.Fprintf(e.w, "Steps: %d\n", len(trace.Steps))
	fmt.Fprintf(e.w, "Requests: %d, Tool Calls: %d\n", trace.Usage.Requests, trace.Usage.ToolCalls)
	fmt.Fprintf(e.w, "Tokens: in=%d out=%d\n", trace.Usage.InputTokens, trace.Usage.OutputTokens)

	for i, step := range trace.Steps {
		fmt.Fprintf(e.w, "  [%d] %s (%.0fms)\n", i, step.Kind, float64(step.Duration)/float64(time.Millisecond))
	}
	return nil
}

// MultiExporter fans out to multiple exporters.
type MultiExporter struct {
	exporters []TraceExporter
}

// NewMultiExporter creates an exporter that fans out to multiple exporters.
func NewMultiExporter(exporters ...TraceExporter) *MultiExporter {
	return &MultiExporter{exporters: exporters}
}

func (e *MultiExporter) Export(ctx context.Context, trace *RunTrace) error {
	var firstErr error
	for _, exp := range e.exporters {
		if err := exp.Export(ctx, trace); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// WithTraceExporter adds a trace exporter to the agent.
// Tracing is automatically enabled when an exporter is set.
func WithTraceExporter[T any](exporter TraceExporter) AgentOption[T] {
	return func(a *Agent[T]) {
		a.traceExporters = append(a.traceExporters, exporter)
		a.tracingEnabled = true // implicitly enable tracing
	}
}
