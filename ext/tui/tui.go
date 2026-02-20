package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fugue-labs/gollem/core"
)

// Option configures the TUI.
type Option func(*config)

type config struct {
	theme Theme
}

// Theme defines styles for different message types in the TUI.
type Theme struct {
	System lipgloss.Style
	User   lipgloss.Style
	Model  lipgloss.Style
	Tool   lipgloss.Style
	Result lipgloss.Style
	Status lipgloss.Style
}

// DefaultTheme returns the default color theme for the TUI.
func DefaultTheme() Theme {
	return Theme{
		System: lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true),
		User:   lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true),
		Model:  lipgloss.NewStyle().Foreground(lipgloss.Color("7")),
		Tool:   lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		Result: lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
		Status: lipgloss.NewStyle().Background(lipgloss.Color("8")).Foreground(lipgloss.Color("15")).Padding(0, 1),
	}
}

// WithTheme sets a custom theme for the TUI.
func WithTheme(theme Theme) Option {
	return func(c *config) {
		c.theme = theme
	}
}

// step represents a single displayable event in the agent run.
type step struct {
	kind    string // "system", "user", "model", "tool-call", "tool-result", "error", "done"
	content string
}

// stepMsg is a bubbletea message carrying a new step.
type stepMsg struct {
	step step
}

// doneMsg signals the agent run has completed.
type doneMsg struct {
	err error
}

// model is the bubbletea model for the TUI.
type model struct {
	theme    Theme
	steps    []step
	usage    core.RunUsage
	stepMode bool
	scroll   int // scroll offset from bottom
	width    int
	height   int
	done     bool
	err      error
	quitting bool
}

// newModel creates a new TUI model.
func newModel(theme Theme) model {
	return model{
		theme:  theme,
		width:  80,
		height: 24,
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "s":
			m.stepMode = true
		case "a":
			m.stepMode = false
		case "up":
			if m.scroll < len(m.steps)-1 {
				m.scroll++
			}
		case "down":
			if m.scroll > 0 {
				m.scroll--
			}
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case stepMsg:
		m.steps = append(m.steps, msg.step)
		// Auto-scroll to bottom on new step.
		m.scroll = 0
	case doneMsg:
		m.done = true
		m.err = msg.err
		return m, tea.Quit
	}
	return m, nil
}

func (m model) View() string {
	if m.quitting {
		return ""
	}

	var sb strings.Builder

	// Calculate available lines for messages (reserve 2 for status bar).
	availableLines := m.height - 2
	if availableLines < 1 {
		availableLines = 1
	}

	// Render all steps.
	var renderedLines []string
	for _, s := range m.steps {
		rendered := renderStep(s, m.theme, m.width)
		lines := strings.Split(rendered, "\n")
		renderedLines = append(renderedLines, lines...)
	}

	// Apply scroll offset.
	endIdx := len(renderedLines) - m.scroll
	if endIdx < 0 {
		endIdx = 0
	}
	startIdx := endIdx - availableLines
	if startIdx < 0 {
		startIdx = 0
	}

	// Display visible lines.
	visible := renderedLines[startIdx:endIdx]
	for _, line := range visible {
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	// Pad remaining space.
	for i := len(visible); i < availableLines; i++ {
		sb.WriteString("\n")
	}

	// Status bar.
	sb.WriteString(m.renderStatusBar())

	return sb.String()
}

// renderStep formats a single step with the appropriate style.
func renderStep(s step, theme Theme, width int) string {
	maxWidth := width - 4
	if maxWidth < 20 {
		maxWidth = 20
	}

	switch s.kind {
	case "system":
		return theme.System.Width(maxWidth).Render("[system] " + s.content)
	case "user":
		return theme.User.Width(maxWidth).Render("[user] " + s.content)
	case "model":
		return theme.Model.Width(maxWidth).Render("[model] " + s.content)
	case "tool-call":
		return theme.Tool.Width(maxWidth).Render("[tool] " + s.content)
	case "tool-result":
		return theme.Result.Width(maxWidth).Render("[result] " + s.content)
	case "error":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true).Width(maxWidth).Render("[error] " + s.content)
	case "done":
		return theme.Result.Width(maxWidth).Bold(true).Render("[done] " + s.content)
	default:
		return s.content
	}
}

// renderStatusBar renders the bottom status bar with usage statistics.
func (m model) renderStatusBar() string {
	modeStr := "auto"
	if m.stepMode {
		modeStr = "step"
	}
	status := FormatUsage(m.usage, modeStr)
	return m.theme.Status.Width(m.width).Render(status)
}

// FormatUsage formats usage statistics for display.
func FormatUsage(usage core.RunUsage, mode string) string {
	return fmt.Sprintf(
		"tokens: %d in / %d out | requests: %d | tools: %d | mode: %s | q:quit s:step a:auto",
		usage.InputTokens, usage.OutputTokens,
		usage.Requests, usage.ToolCalls,
		mode,
	)
}

// FormatToolCall formats a tool call for display.
func FormatToolCall(name string, argsJSON string) string {
	// Truncate very long args for display.
	args := argsJSON
	if len(args) > 200 {
		args = args[:200] + "..."
	}
	return fmt.Sprintf("%s(%s)", name, args)
}

// ExtractSteps extracts displayable steps from a list of model messages.
func ExtractSteps(messages []core.ModelMessage) []step {
	var steps []step
	for _, msg := range messages {
		switch m := msg.(type) {
		case core.ModelRequest:
			for _, part := range m.Parts {
				switch p := part.(type) {
				case core.SystemPromptPart:
					steps = append(steps, step{kind: "system", content: p.Content})
				case core.UserPromptPart:
					steps = append(steps, step{kind: "user", content: p.Content})
				case core.ToolReturnPart:
					content := fmt.Sprintf("%v", p.Content)
					steps = append(steps, step{kind: "tool-result", content: fmt.Sprintf("%s: %s", p.ToolName, content)})
				case core.RetryPromptPart:
					steps = append(steps, step{kind: "error", content: p.Content})
				}
			}
		case core.ModelResponse:
			for _, part := range m.Parts {
				switch p := part.(type) {
				case core.TextPart:
					steps = append(steps, step{kind: "model", content: p.Content})
				case core.ToolCallPart:
					steps = append(steps, step{kind: "tool-call", content: FormatToolCall(p.ToolName, p.ArgsJSON)})
				}
			}
		}
	}
	return steps
}

// DebugUI creates and runs a TUI that wraps an agent run, displaying messages
// as they happen with color-coded formatting and a status bar.
func DebugUI[T any](agent *core.Agent[T], prompt string, opts ...Option) (*core.RunResult[T], error) {
	cfg := &config{
		theme: DefaultTheme(),
	}
	for _, opt := range opts {
		opt(cfg)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create the bubbletea model.
	m := newModel(cfg.theme)

	// Create and run the program.
	p := tea.NewProgram(m, tea.WithAltScreen())

	// Run the agent in a goroutine.
	var agentResult *core.RunResult[T]
	var agentErr error

	go func() {
		iter := agent.Iter(ctx, prompt)

		// Send initial steps.
		for _, s := range ExtractSteps(iter.Messages()) {
			p.Send(stepMsg{step: s})
		}

		for !iter.Done() {
			resp, err := iter.Next()
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				p.Send(doneMsg{err: err})
				return
			}

			if resp != nil {
				for _, part := range resp.Parts {
					switch rp := part.(type) {
					case core.TextPart:
						p.Send(stepMsg{step: step{kind: "model", content: rp.Content}})
					case core.ToolCallPart:
						p.Send(stepMsg{step: step{kind: "tool-call", content: FormatToolCall(rp.ToolName, rp.ArgsJSON)}})
					}
				}

				// Update usage.
				p.Send(stepMsg{step: step{kind: "model", content: ""}})
			}
		}

		result, err := iter.Result()
		if err != nil {
			agentErr = err
			p.Send(doneMsg{err: err})
			return
		}

		agentResult = result
		p.Send(stepMsg{step: step{kind: "done", content: fmt.Sprintf("completed (requests: %d, tokens: %d)", result.Usage.Requests, result.Usage.TotalTokens())}})
		p.Send(doneMsg{err: nil})
	}()

	_, err := p.Run()
	if err != nil {
		return nil, fmt.Errorf("TUI error: %w", err)
	}

	if agentErr != nil {
		return nil, agentErr
	}

	return agentResult, nil
}
