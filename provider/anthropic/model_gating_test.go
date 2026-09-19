package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fugue-labs/gollem/core"
)

// gatingCase pins the per-model request surface across every generation the
// provider distinguishes, including the version-segment collisions
// ("opus-5" vs "opus-4-5") that substring matching would get wrong.
type gatingCase struct {
	model    string
	claude5  bool
	adaptive bool
	manual   bool
	effort   bool
	xhigh    bool
	max      bool
	sampling bool
}

var gatingCases = []gatingCase{
	{model: "claude-opus-5", claude5: true, adaptive: true, manual: false, effort: true, xhigh: true, max: true, sampling: false},
	{model: "claude-opus-5-20260401", claude5: true, adaptive: true, manual: false, effort: true, xhigh: true, max: true, sampling: false},
	{model: "claude-opus-5@20260401", claude5: true, adaptive: true, manual: false, effort: true, xhigh: true, max: true, sampling: false},
	{model: "claude-sonnet-5", claude5: true, adaptive: true, manual: false, effort: true, xhigh: true, max: true, sampling: false},
	{model: "Claude-Sonnet-5 ", claude5: true, adaptive: true, manual: false, effort: true, xhigh: true, max: true, sampling: false},
	{model: "claude-fable-5-1", claude5: false, adaptive: true, manual: false, effort: true, xhigh: true, max: true, sampling: false},
	{model: "claude-fable-5", claude5: false, adaptive: true, manual: false, effort: true, xhigh: true, max: true, sampling: false},
	{model: "claude-mythos-5-1", claude5: false, adaptive: true, manual: false, effort: true, xhigh: true, max: true, sampling: false},
	{model: "claude-opus-4-8", claude5: false, adaptive: true, manual: false, effort: true, xhigh: true, max: true, sampling: false},
	{model: "claude-opus-4-7", claude5: false, adaptive: true, manual: false, effort: true, xhigh: true, max: true, sampling: false},
	{model: "claude-opus-4-6", claude5: false, adaptive: true, manual: true, effort: true, xhigh: false, max: true, sampling: true},
	{model: "claude-sonnet-4-6", claude5: false, adaptive: true, manual: true, effort: true, xhigh: false, max: true, sampling: true},
	{model: "claude-opus-4-5-20251101", claude5: false, adaptive: false, manual: true, effort: true, xhigh: false, max: false, sampling: true},
	{model: "claude-opus-4-5", claude5: false, adaptive: false, manual: true, effort: true, xhigh: false, max: false, sampling: true},
	{model: "claude-sonnet-4-5-20250929", claude5: false, adaptive: false, manual: true, effort: false, xhigh: false, max: false, sampling: true},
	{model: "claude-sonnet-4-5", claude5: false, adaptive: false, manual: true, effort: false, xhigh: false, max: false, sampling: true},
	{model: "claude-haiku-4-5-20251001", claude5: false, adaptive: false, manual: true, effort: false, xhigh: false, max: false, sampling: true},
	{model: "claude-haiku-4-5", claude5: false, adaptive: false, manual: true, effort: false, xhigh: false, max: false, sampling: true},
	{model: "claude-opus-50", claude5: false, adaptive: false, manual: true, effort: false, xhigh: false, max: false, sampling: true},
	{model: "claude-3-5-sonnet-20241022", claude5: false, adaptive: false, manual: true, effort: false, xhigh: false, max: false, sampling: true},
}

func TestModelGatingPredicates(t *testing.T) {
	for _, tc := range gatingCases {
		t.Run(tc.model, func(t *testing.T) {
			check := func(name string, got, want bool) {
				t.Helper()
				if got != want {
					t.Errorf("%s(%q) = %v, want %v", name, tc.model, got, want)
				}
			}
			check("isClaude5Family", isClaude5Family(tc.model), tc.claude5)
			check("supportsAdaptiveThinking", supportsAdaptiveThinking(tc.model), tc.adaptive)
			check("supportsManualThinking", supportsManualThinking(tc.model), tc.manual)
			check("supportsEffort", supportsEffort(tc.model), tc.effort)
			for _, effort := range []string{"low", "medium", "high"} {
				check("supportsEffortValue["+effort+"]", supportsEffortValue(tc.model, effort), tc.effort)
			}
			check("supportsEffortValue[xhigh]", supportsEffortValue(tc.model, "xhigh"), tc.xhigh)
			check("supportsEffortValue[max]", supportsEffortValue(tc.model, "max"), tc.max)
			check("supportsEffortValue[bogus]", supportsEffortValue(tc.model, "bogus"), false)
			check("supportsSampling", supportsSampling(tc.model), tc.sampling)

			// Exported wrappers must be the same rules.
			check("SupportsAdaptiveThinking", SupportsAdaptiveThinking(tc.model), tc.adaptive)
			check("SupportsManualThinking", SupportsManualThinking(tc.model), tc.manual)
			check("SupportsEffort", SupportsEffort(tc.model), tc.effort)
			check("SupportsEffortValue[xhigh]", SupportsEffortValue(tc.model, "xhigh"), tc.xhigh)
			check("SupportsEffortValue[max]", SupportsEffortValue(tc.model, "max"), tc.max)
			check("SupportsSampling", SupportsSampling(tc.model), tc.sampling)
			check("SupportsToolSearch", SupportsToolSearch(tc.model), supportsToolSearch(tc.model))
		})
	}
}

func TestSupportsToolSearchClaude5(t *testing.T) {
	for _, model := range []string{ClaudeOpus5, ClaudeSonnet5, ClaudeMythos51, "claude-opus-5-20260401"} {
		if !supportsToolSearch(model) {
			t.Errorf("supportsToolSearch(%q) = false, want true", model)
		}
	}
	if supportsToolSearch(ClaudeHaiku45) {
		t.Errorf("supportsToolSearch(%q) = true, want false", ClaudeHaiku45)
	}
}

func TestHasMajorVersion(t *testing.T) {
	cases := []struct {
		m      string
		family string
		major  byte
		want   bool
	}{
		{"claude-opus-5", "opus-", '5', true},
		{"claude-opus-5-20260401", "opus-", '5', true},
		{"claude-opus-5@20260401", "opus-", '5', true},
		{"claude-opus-5.1", "opus-", '5', true},
		{"claude-opus-4-5", "opus-", '5', false},
		{"claude-opus-4-5-20251101", "opus-", '5', false},
		{"claude-opus-50", "opus-", '5', false},
		{"claude-opus-", "opus-", '5', false},
		{"claude-sonnet-5", "opus-", '5', false},
		{"opus-4-5-opus-5", "opus-", '5', true},
		{"opus-4-opus-4-5", "opus-", '5', false},
		{"", "opus-", '5', false},
	}
	for _, tc := range cases {
		if got := hasMajorVersion(tc.m, tc.family, tc.major); got != tc.want {
			t.Errorf("hasMajorVersion(%q, %q, %q) = %v, want %v", tc.m, tc.family, tc.major, got, tc.want)
		}
	}
}

// TestClaude5RejectsThinkingBudget mirrors TestOpus47RejectsThinkingBudget:
// a manual budget on Opus 5 must fail at build time with a pointer to the
// adaptive/effort controls instead of surfacing as an API 400.
func TestClaude5RejectsThinkingBudget(t *testing.T) {
	budget := 2048
	for _, model := range []string{ClaudeOpus5, ClaudeSonnet5, ClaudeFable51} {
		t.Run(model, func(t *testing.T) {
			settings := &core.ModelSettings{ThinkingBudget: &budget}
			_, err := buildRequest(nil, settings, nil, model, 4096, false, false, false)
			if err == nil {
				t.Fatalf("expected error when ThinkingBudget is set on %s", model)
			}
			msg := err.Error()
			if !strings.Contains(msg, model) {
				t.Errorf("error should mention the model name, got: %s", msg)
			}
			if !strings.Contains(msg, "WithReasoningEffort") {
				t.Errorf("error should point to WithReasoningEffort, got: %s", msg)
			}
		})
	}
}

// TestClaude5AdaptiveThinkingWithXHighEffort verifies the wire shape for
// Sonnet 5: {thinking:{type:"adaptive"}} plus output_config.effort "xhigh",
// and no budget_tokens.
func TestClaude5AdaptiveThinkingWithXHighEffort(t *testing.T) {
	on := true
	effort := "xhigh"
	for _, model := range []string{ClaudeSonnet5, ClaudeOpus5, ClaudeFable51} {
		t.Run(model, func(t *testing.T) {
			settings := &core.ModelSettings{AdaptiveThinking: &on, ReasoningEffort: &effort}
			req, err := buildRequest(nil, settings, nil, model, 4096, false, false, false)
			if err != nil {
				t.Fatalf("buildRequest: %v", err)
			}
			if req.Thinking == nil || req.Thinking.Type != "adaptive" {
				t.Fatalf("thinking = %+v, want adaptive", req.Thinking)
			}
			if req.OutputConfig == nil || req.OutputConfig.Effort != "xhigh" {
				t.Fatalf("output_config = %+v, want effort xhigh", req.OutputConfig)
			}
			wire, err := json.Marshal(req)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !strings.Contains(string(wire), `"thinking":{"type":"adaptive"}`) {
				t.Errorf("wire thinking shape wrong: %s", wire)
			}
			if !strings.Contains(string(wire), `"output_config":{"effort":"xhigh"}`) {
				t.Errorf("wire output_config shape wrong: %s", wire)
			}
			if strings.Contains(string(wire), "budget_tokens") {
				t.Errorf("budget_tokens must be absent: %s", wire)
			}
		})
	}
}

// TestClaude5StripsSamplingWithoutThinking verifies temperature and top_p
// are dropped on Opus 5 even when no thinking mode is requested, matching
// the Opus 4.7/4.8 treatment. Sonnet 4.6 keeps them as a control.
func TestClaude5StripsSamplingWithoutThinking(t *testing.T) {
	temp := 0.7
	topP := 0.9
	for _, model := range []string{ClaudeOpus5, ClaudeSonnet5, ClaudeOpus47} {
		t.Run(model, func(t *testing.T) {
			settings := &core.ModelSettings{Temperature: &temp, TopP: &topP}
			req, err := buildRequest(nil, settings, nil, model, 4096, false, false, false)
			if err != nil {
				t.Fatalf("buildRequest: %v", err)
			}
			if req.Temperature != nil {
				t.Errorf("temperature = %v, want nil", *req.Temperature)
			}
			if req.TopP != nil {
				t.Errorf("top_p = %v, want nil", *req.TopP)
			}
			wire, _ := json.Marshal(req)
			if strings.Contains(string(wire), "temperature") || strings.Contains(string(wire), "top_p") {
				t.Errorf("sampling controls must be absent on the wire: %s", wire)
			}
		})
	}
	settings := &core.ModelSettings{Temperature: &temp, TopP: &topP}
	req, err := buildRequest(nil, settings, nil, ClaudeSonnet46, 4096, false, false, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if req.Temperature == nil || *req.Temperature != temp || req.TopP == nil || *req.TopP != topP {
		t.Errorf("Sonnet 4.6 should keep sampling controls, got temperature=%v top_p=%v", req.Temperature, req.TopP)
	}
}
