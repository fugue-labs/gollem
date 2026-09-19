package main

import "testing"

// TestAnthropicModelPrefersAdaptive pins the CLI's default thinking-mode
// selection: Claude 4.6+ (and the empty string, i.e. the provider's
// Sonnet 4.6 default) get adaptive thinking; older models keep the
// manual-budget default, which they still accept.
func TestAnthropicModelPrefersAdaptive(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"", true}, // provider default model (Sonnet 4.6)
		{"claude-sonnet-4-6", true},
		{"claude-opus-4-6", true},
		{"claude-opus-4-7", true},
		{"claude-opus-4-8", true},
		{"claude-fable-5", true},
		{"claude-fable-5-1", true},
		{"claude-opus-5", true},
		{"claude-sonnet-5", true},
		{"claude-mythos-5-1", true},
		{"claude-sonnet-4-5", false},
		{"claude-opus-4-5", false},
		{"claude-haiku-4-5-20251001", false},
		{"claude-3-5-sonnet-20241022", false},
	}
	for _, tc := range cases {
		if got := anthropicModelPrefersAdaptive(tc.model); got != tc.want {
			t.Errorf("anthropicModelPrefersAdaptive(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}
