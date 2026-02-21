//go:build e2e

package e2e

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func newAnthropicProvider() *anthropic.Provider {
	return anthropic.New(
		anthropic.WithModel(anthropic.Claude4Haiku),
		anthropic.WithMaxTokens(200),
	)
}

func newOpenAIProvider() *openai.Provider {
	return openai.New(
		openai.WithModel("gpt-4o-mini"),
		openai.WithMaxTokens(200),
	)
}

func newXAIProvider() *openai.Provider {
	return openai.New(
		openai.WithAPIKey(os.Getenv("XAI_API_KEY")),
		openai.WithBaseURL(os.Getenv("XAI_BASE_URL")),
		openai.WithModel(os.Getenv("XAI_MODEL")),
		openai.WithMaxTokens(200),
	)
}

func newVertexAIProvider() *vertexai.Provider {
	loc := os.Getenv("VERTEX_AI_LOCATION")
	if loc == "" {
		loc = "us-central1"
	}
	return vertexai.New(
		vertexai.WithProject(os.Getenv("GOOGLE_CLOUD_PROJECT")),
		vertexai.WithLocation(loc),
		vertexai.WithModel(vertexai.Gemini25Flash),
	)
}

func newVertexAIAnthropicProvider() *vertexai_anthropic.Provider {
	loc := os.Getenv("VERTEX_AI_ANTHROPIC_LOCATION")
	if loc == "" {
		loc = "us-east5"
	}
	return vertexai_anthropic.New(
		vertexai_anthropic.WithProject(os.Getenv("GOOGLE_CLOUD_PROJECT")),
		vertexai_anthropic.WithLocation(loc),
		vertexai_anthropic.WithModel(vertexai_anthropic.Claude4Haiku),
		vertexai_anthropic.WithMaxTokens(200),
	)
}
