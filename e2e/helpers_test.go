//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/provider/anthropic"
	"github.com/fugue-labs/gollem/provider/openai"
	"github.com/fugue-labs/gollem/provider/vertexai"
	"github.com/fugue-labs/gollem/provider/vertexai_anthropic"
)

func init() {
	loadDotEnv()
}

// --- Global token usage tracker ---

// usageTracker accumulates token usage across all e2e tests.
var usageTracker = &globalUsageTracker{
	byProvider: make(map[string]*providerUsage),
}

type providerUsage struct {
	inputTokens  int
	outputTokens int
	requests     int
}

type globalUsageTracker struct {
	mu         sync.Mutex
	byProvider map[string]*providerUsage
}

func (t *globalUsageTracker) record(provider string, usage core.Usage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	pu, ok := t.byProvider[provider]
	if !ok {
		pu = &providerUsage{}
		t.byProvider[provider] = pu
	}
	pu.inputTokens += usage.InputTokens
	pu.outputTokens += usage.OutputTokens
	pu.requests++
}

func (t *globalUsageTracker) summary() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var sb strings.Builder
	sb.WriteString("\n╔══════════════════════════════════════════════════════════════════╗\n")
	sb.WriteString("║              E2E TEST SUITE — TOKEN USAGE REPORT               ║\n")
	sb.WriteString("╠═══════════════════════╦══════════╦═══════════╦═════════╦════════╣\n")
	sb.WriteString("║ Provider              ║ Requests ║ Input Tok ║ Out Tok ║  Total ║\n")
	sb.WriteString("╠═══════════════════════╬══════════╬═══════════╬═════════╬════════╣\n")
	var totalInput, totalOutput, totalRequests int
	for _, name := range []string{"Anthropic", "OpenAI", "XAI", "VertexAI", "VertexAIAnthropic"} {
		pu, ok := t.byProvider[name]
		if !ok {
			continue
		}
		total := pu.inputTokens + pu.outputTokens
		sb.WriteString(fmt.Sprintf("║ %-21s ║ %8d ║ %9d ║ %7d ║ %6d ║\n",
			name, pu.requests, pu.inputTokens, pu.outputTokens, total))
		totalInput += pu.inputTokens
		totalOutput += pu.outputTokens
		totalRequests += pu.requests
	}
	sb.WriteString("╠═══════════════════════╬══════════╬═══════════╬═════════╬════════╣\n")
	sb.WriteString(fmt.Sprintf("║ %-21s ║ %8d ║ %9d ║ %7d ║ %6d ║\n",
		"TOTAL", totalRequests, totalInput, totalOutput, totalInput+totalOutput))
	sb.WriteString("╚═══════════════════════╩══════════╩═══════════╩═════════╩════════╝\n")
	return sb.String()
}

// TestMain runs after all tests and prints usage summary.
func TestMain(m *testing.M) {
	code := m.Run()
	fmt.Print(usageTracker.summary())
	os.Exit(code)
}

// trackingModel wraps a core.Model and records usage to the global tracker.
type trackingModel struct {
	inner        core.Model
	providerName string
}

func (m *trackingModel) Request(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*core.ModelResponse, error) {
	resp, err := m.inner.Request(ctx, messages, settings, params)
	if err == nil && resp != nil {
		usageTracker.record(m.providerName, resp.Usage)
	}
	return resp, err
}

func (m *trackingModel) RequestStream(ctx context.Context, messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (core.StreamedResponse, error) {
	stream, err := m.inner.RequestStream(ctx, messages, settings, params)
	if err != nil {
		return nil, err
	}
	return &trackingStream{inner: stream, providerName: m.providerName}, nil
}

func (m *trackingModel) ModelName() string {
	return m.inner.ModelName()
}

// trackingStream wraps a StreamedResponse to capture usage when the stream completes.
type trackingStream struct {
	inner        core.StreamedResponse
	providerName string
	recorded     bool
}

func (s *trackingStream) Next() (core.ModelResponseStreamEvent, error) {
	event, err := s.inner.Next()
	if err != nil && !s.recorded {
		// Stream ended — record final usage.
		s.recorded = true
		usageTracker.record(s.providerName, s.inner.Usage())
	}
	return event, err
}

func (s *trackingStream) Response() *core.ModelResponse { return s.inner.Response() }
func (s *trackingStream) Usage() core.Usage             { return s.inner.Usage() }
func (s *trackingStream) Close() error                  { return s.inner.Close() }

// loadDotEnv loads key=value pairs from .env at the repo root.
// Existing env vars take precedence (are not overwritten).
func loadDotEnv() {
	// Find the repo root relative to this test file.
	_, filename, _, _ := runtime.Caller(0)
	repoRoot := filepath.Dir(filepath.Dir(filename))
	envFile := filepath.Join(repoRoot, ".env")

	f, err := os.Open(envFile)
	if err != nil {
		return // no .env file, that's fine
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		// Always set from .env - it's the authoritative source for e2e tests.
		os.Setenv(key, value)
	}
}

// providerEntry describes a provider for cross-provider test iteration.
type providerEntry struct {
	name       string
	newFn      func() core.Model
	credEnvVar string // the env var checked to determine if credentials exist
}

func allProviders() []providerEntry {
	return []providerEntry{
		{
			name:       "Anthropic",
			newFn:      func() core.Model { return newAnthropicProvider() },
			credEnvVar: "ANTHROPIC_API_KEY",
		},
		{
			name:       "OpenAI",
			newFn:      func() core.Model { return newOpenAIProvider() },
			credEnvVar: "OPENAI_API_KEY",
		},
		{
			name:       "XAI",
			newFn:      func() core.Model { return newXAIProvider() },
			credEnvVar: "XAI_API_KEY",
		},
		{
			name:       "VertexAI",
			newFn:      func() core.Model { return newVertexAIProvider() },
			credEnvVar: "GOOGLE_CLOUD_PROJECT",
		},
		{
			name:       "VertexAIAnthropic",
			newFn:      func() core.Model { return newVertexAIAnthropicProvider() },
			credEnvVar: "GOOGLE_CLOUD_PROJECT",
		},
	}
}

// tracked wraps a model with usage tracking.
func tracked(name string, model core.Model) core.Model {
	return &trackingModel{inner: model, providerName: name}
}

func skipIfNoCredentials(t *testing.T, envVar string) {
	t.Helper()
	if os.Getenv(envVar) == "" {
		t.Skipf("skipping: %s not set", envVar)
	}
}

// skipOnAccountError skips a test for errors that indicate account/project configuration
// issues rather than code bugs (quota exceeded, billing not set up, model not enabled).
func skipOnAccountError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	errStr := err.Error()
	skipPatterns := []string{
		"insufficient_quota",
		"billing",
		"exceeded your current quota",
		"was not found or your project does not have access",
		"does not have access to it",
		"Permission denied",
		"PERMISSION_DENIED",
		"Quota exceeded",
	}
	for _, pattern := range skipPatterns {
		if strings.Contains(errStr, pattern) {
			t.Skipf("Skipping: account/project issue (not a code bug): %v", err)
		}
	}
}

// newAnthropicProvider returns a tracked Anthropic provider.
func newAnthropicProvider() core.Model {
	return tracked("Anthropic", anthropic.New(
		anthropic.WithModel(anthropic.Claude4Haiku),
		anthropic.WithMaxTokens(200),
	))
}

// newOpenAIProvider returns a tracked OpenAI provider.
func newOpenAIProvider() core.Model {
	return tracked("OpenAI", openai.New(
		openai.WithModel("gpt-4o-mini"),
		openai.WithMaxTokens(200),
	))
}

// newXAIProvider returns a tracked xAI/Grok provider.
func newXAIProvider() core.Model {
	return tracked("XAI", openai.New(
		openai.WithAPIKey(os.Getenv("XAI_API_KEY")),
		openai.WithBaseURL(os.Getenv("XAI_BASE_URL")),
		openai.WithModel(os.Getenv("XAI_MODEL")),
		openai.WithMaxTokens(200),
	))
}

// newVertexAIProvider returns a tracked VertexAI Gemini provider.
func newVertexAIProvider() core.Model {
	loc := os.Getenv("VERTEX_AI_LOCATION")
	if loc == "" {
		loc = "us-central1"
	}
	return tracked("VertexAI", vertexai.New(
		vertexai.WithProject(os.Getenv("GOOGLE_CLOUD_PROJECT")),
		vertexai.WithLocation(loc),
		vertexai.WithModel(vertexai.Gemini25Flash),
	))
}

// newVertexAIAnthropicProvider returns a tracked VertexAI Anthropic provider.
func newVertexAIAnthropicProvider() core.Model {
	loc := os.Getenv("VERTEX_AI_ANTHROPIC_LOCATION")
	if loc == "" {
		loc = "us-east5"
	}
	return tracked("VertexAIAnthropic", vertexai_anthropic.New(
		vertexai_anthropic.WithProject(os.Getenv("GOOGLE_CLOUD_PROJECT")),
		vertexai_anthropic.WithLocation(loc),
		vertexai_anthropic.WithModel(vertexai_anthropic.Claude4Haiku),
		vertexai_anthropic.WithMaxTokens(200),
	))
}
