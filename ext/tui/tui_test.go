package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fugue-labs/gollem/core"
)

func TestTUI_ModelCreation(t *testing.T) {
	theme := DefaultTheme()
	m := newModel(theme)

	// Verify initial state.
	if m.done {
		t.Error("expected done to be false initially")
	}
	if m.stepMode {
		t.Error("expected stepMode to be false initially")
	}
	if m.quitting {
		t.Error("expected quitting to be false initially")
	}
	if len(m.steps) != 0 {
		t.Errorf("expected 0 steps, got %d", len(m.steps))
	}
	if m.scroll != 0 {
		t.Errorf("expected scroll 0, got %d", m.scroll)
	}
	if m.width != 80 {
		t.Errorf("expected width 80, got %d", m.width)
	}
	if m.height != 24 {
		t.Errorf("expected height 24, got %d", m.height)
	}

	// Verify Init returns nil (no initial command).
	cmd := m.Init()
	if cmd != nil {
		t.Error("expected Init to return nil cmd")
	}
}

func TestTUI_StepRendering(t *testing.T) {
	// Use an unstyled theme to make testing easier (no ANSI codes to parse).
	theme := Theme{
		System: lipgloss.NewStyle(),
		User:   lipgloss.NewStyle(),
		Model:  lipgloss.NewStyle(),
		Tool:   lipgloss.NewStyle(),
		Result: lipgloss.NewStyle(),
		Status: lipgloss.NewStyle(),
	}

	tests := []struct {
		name     string
		step     step
		contains string
	}{
		{
			name:     "system prompt",
			step:     step{kind: "system", content: "You are helpful."},
			contains: "[system] You are helpful.",
		},
		{
			name:     "user prompt",
			step:     step{kind: "user", content: "Tell me about Go."},
			contains: "[user] Tell me about Go.",
		},
		{
			name:     "model response",
			step:     step{kind: "model", content: "Go is a programming language."},
			contains: "[model] Go is a programming language.",
		},
		{
			name:     "tool call",
			step:     step{kind: "tool-call", content: "search({\"query\": \"Go lang\"})"},
			contains: "[tool] search",
		},
		{
			name:     "tool result",
			step:     step{kind: "tool-result", content: "search: found 10 results"},
			contains: "[result] search: found 10 results",
		},
		{
			name:     "error",
			step:     step{kind: "error", content: "something went wrong"},
			contains: "[error] something went wrong",
		},
		{
			name:     "done",
			step:     step{kind: "done", content: "completed"},
			contains: "[done] completed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rendered := renderStep(tt.step, theme, 80)
			if !strings.Contains(rendered, tt.contains) {
				t.Errorf("rendered step %q does not contain %q\ngot: %q", tt.name, tt.contains, rendered)
			}
			if rendered == "" {
				t.Errorf("rendered step %q is empty", tt.name)
			}
		})
	}
}

func TestTUI_UsageDisplay(t *testing.T) {
	usage := core.RunUsage{
		Usage: core.Usage{
			InputTokens:  150,
			OutputTokens: 42,
		},
		Requests:  3,
		ToolCalls: 2,
	}

	formatted := FormatUsage(usage, "auto")

	if !strings.Contains(formatted, "150 in") {
		t.Errorf("expected input tokens in formatted output, got: %s", formatted)
	}
	if !strings.Contains(formatted, "42 out") {
		t.Errorf("expected output tokens in formatted output, got: %s", formatted)
	}
	if !strings.Contains(formatted, "requests: 3") {
		t.Errorf("expected request count in formatted output, got: %s", formatted)
	}
	if !strings.Contains(formatted, "tools: 2") {
		t.Errorf("expected tool call count in formatted output, got: %s", formatted)
	}
	if !strings.Contains(formatted, "mode: auto") {
		t.Errorf("expected mode in formatted output, got: %s", formatted)
	}

	// Test step mode.
	stepFormatted := FormatUsage(usage, "step")
	if !strings.Contains(stepFormatted, "mode: step") {
		t.Errorf("expected step mode in formatted output, got: %s", stepFormatted)
	}
}

func TestTUI_ToolCallDisplay(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		argsJSON string
		contains string
	}{
		{
			name:     "simple tool call",
			toolName: "search",
			argsJSON: `{"query": "hello"}`,
			contains: `search({"query": "hello"})`,
		},
		{
			name:     "empty args",
			toolName: "get_time",
			argsJSON: "{}",
			contains: "get_time({})",
		},
		{
			name:     "long args truncated",
			toolName: "analyze",
			argsJSON: strings.Repeat("x", 300),
			contains: "analyze(" + strings.Repeat("x", 200) + "...)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formatted := FormatToolCall(tt.toolName, tt.argsJSON)
			if !strings.Contains(formatted, tt.contains) {
				t.Errorf("FormatToolCall(%q, %q) = %q, expected to contain %q",
					tt.toolName, tt.argsJSON, formatted, tt.contains)
			}
		})
	}
}

func TestTUI_ExtractSteps(t *testing.T) {
	messages := []core.ModelMessage{
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.SystemPromptPart{Content: "Be helpful."},
				core.UserPromptPart{Content: "Hello"},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.TextPart{Content: "Hi there!"},
			},
		},
		core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.ToolReturnPart{ToolName: "search", Content: "results"},
			},
		},
		core.ModelResponse{
			Parts: []core.ModelResponsePart{
				core.ToolCallPart{ToolName: "search", ArgsJSON: `{"q":"test"}`},
			},
		},
	}

	steps := ExtractSteps(messages)

	if len(steps) != 5 {
		t.Fatalf("expected 5 steps, got %d", len(steps))
	}

	expected := []struct {
		kind    string
		content string
	}{
		{"system", "Be helpful."},
		{"user", "Hello"},
		{"model", "Hi there!"},
		{"tool-result", "search: results"},
		{"tool-call", "search"},
	}

	for i, exp := range expected {
		if steps[i].kind != exp.kind {
			t.Errorf("step %d: expected kind %q, got %q", i, exp.kind, steps[i].kind)
		}
		if !strings.Contains(steps[i].content, exp.content) {
			t.Errorf("step %d: expected content containing %q, got %q", i, exp.content, steps[i].content)
		}
	}
}

func TestTUI_UpdateStepMsg(t *testing.T) {
	m := newModel(DefaultTheme())

	s := step{kind: "model", content: "Hello!"}
	updated, _ := m.Update(stepMsg{step: s})
	um := updated.(model)

	if len(um.steps) != 1 {
		t.Fatalf("expected 1 step after update, got %d", len(um.steps))
	}
	if um.steps[0].kind != "model" {
		t.Errorf("expected step kind 'model', got %q", um.steps[0].kind)
	}
	if um.steps[0].content != "Hello!" {
		t.Errorf("expected step content 'Hello!', got %q", um.steps[0].content)
	}
	// Scroll should reset to 0 on new step.
	if um.scroll != 0 {
		t.Errorf("expected scroll 0 after new step, got %d", um.scroll)
	}
}

func TestTUI_UpdateDoneMsg(t *testing.T) {
	m := newModel(DefaultTheme())

	updated, cmd := m.Update(doneMsg{err: nil})
	um := updated.(model)

	if !um.done {
		t.Error("expected done to be true after doneMsg")
	}
	if um.err != nil {
		t.Errorf("expected nil error, got %v", um.err)
	}
	// doneMsg should cause a Quit command.
	if cmd == nil {
		t.Error("expected non-nil cmd (tea.Quit) after doneMsg")
	}
}

func TestTUI_ScrollBehavior(t *testing.T) {
	m := newModel(DefaultTheme())

	// Add some steps.
	for range 5 {
		updated, _ := m.Update(stepMsg{step: step{kind: "model", content: "msg"}})
		m = updated.(model)
	}

	if m.scroll != 0 {
		t.Errorf("expected scroll 0, got %d", m.scroll)
	}

	// Scroll up.
	updated, _ := m.Update(keyMsg("up"))
	m = updated.(model)
	if m.scroll != 1 {
		t.Errorf("expected scroll 1 after up, got %d", m.scroll)
	}

	// Scroll down.
	updated, _ = m.Update(keyMsg("down"))
	m = updated.(model)
	if m.scroll != 0 {
		t.Errorf("expected scroll 0 after down, got %d", m.scroll)
	}

	// Scroll down at bottom should not go below 0.
	updated, _ = m.Update(keyMsg("down"))
	m = updated.(model)
	if m.scroll != 0 {
		t.Errorf("expected scroll to stay at 0, got %d", m.scroll)
	}
}

func TestTUI_ModeToggle(t *testing.T) {
	m := newModel(DefaultTheme())

	if m.stepMode {
		t.Error("expected initial mode to be auto (stepMode=false)")
	}

	// Switch to step mode.
	updated, _ := m.Update(keyMsg("s"))
	m = updated.(model)
	if !m.stepMode {
		t.Error("expected stepMode=true after pressing 's'")
	}

	// Switch back to auto mode.
	updated, _ = m.Update(keyMsg("a"))
	m = updated.(model)
	if m.stepMode {
		t.Error("expected stepMode=false after pressing 'a'")
	}
}

func TestTUI_ViewOutput(t *testing.T) {
	m := newModel(Theme{
		System: lipgloss.NewStyle(),
		User:   lipgloss.NewStyle(),
		Model:  lipgloss.NewStyle(),
		Tool:   lipgloss.NewStyle(),
		Result: lipgloss.NewStyle(),
		Status: lipgloss.NewStyle(),
	})
	m.width = 80
	m.height = 24

	// Add a step.
	updated, _ := m.Update(stepMsg{step: step{kind: "user", content: "Hello world"}})
	m = updated.(model)

	view := m.View()
	if view == "" {
		t.Error("expected non-empty view")
	}
	if !strings.Contains(view, "[user] Hello world") {
		t.Errorf("view should contain user message, got: %q", view)
	}
	if !strings.Contains(view, "tokens:") {
		t.Errorf("view should contain status bar with tokens, got: %q", view)
	}
}

func TestTUI_DefaultTheme(t *testing.T) {
	theme := DefaultTheme()

	// Just verify the theme fields are non-zero (styles were created).
	// We use renderStep to verify styles are functional.
	rendered := renderStep(step{kind: "system", content: "test"}, theme, 80)
	if rendered == "" {
		t.Error("DefaultTheme produced empty render for system step")
	}
	rendered = renderStep(step{kind: "user", content: "test"}, theme, 80)
	if rendered == "" {
		t.Error("DefaultTheme produced empty render for user step")
	}
	rendered = renderStep(step{kind: "model", content: "test"}, theme, 80)
	if rendered == "" {
		t.Error("DefaultTheme produced empty render for model step")
	}
	rendered = renderStep(step{kind: "tool-call", content: "test"}, theme, 80)
	if rendered == "" {
		t.Error("DefaultTheme produced empty render for tool step")
	}
	rendered = renderStep(step{kind: "tool-result", content: "test"}, theme, 80)
	if rendered == "" {
		t.Error("DefaultTheme produced empty render for result step")
	}
}

func TestTUI_WithTheme(t *testing.T) {
	customTheme := Theme{
		System: lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		User:   lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		Model:  lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		Tool:   lipgloss.NewStyle().Foreground(lipgloss.Color("4")),
		Result: lipgloss.NewStyle().Foreground(lipgloss.Color("5")),
		Status: lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
	}

	cfg := &config{theme: DefaultTheme()}
	opt := WithTheme(customTheme)
	opt(cfg)

	// Verify the theme was applied (by rendering with it).
	rendered := renderStep(step{kind: "system", content: "test"}, cfg.theme, 80)
	if rendered == "" {
		t.Error("WithTheme produced empty render")
	}
}

// keyMsg is a helper to create tea.KeyMsg for testing.
func keyMsg(key string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
}
