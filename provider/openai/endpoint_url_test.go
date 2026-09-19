package openai

import (
	"strings"
	"testing"
)

func TestEndpointURLRespectsVersionedBaseURLs(t *testing.T) {
	cases := []struct {
		base string
		want string
	}{
		{"https://api.openai.com", "https://api.openai.com/v1/chat/completions"},
		{"https://api.openai.com/v1", "https://api.openai.com/v1/chat/completions"},
		{"https://api.deepseek.com/v1", "https://api.deepseek.com/v1/chat/completions"},
		{"https://api.z.ai/api/coding/paas/v4", "https://api.z.ai/api/coding/paas/v4/chat/completions"},
		{"https://open.bigmodel.cn/api/paas/v4/", "https://open.bigmodel.cn/api/paas/v4/chat/completions"},
		{"https://example.test/v1beta", "https://example.test/v1beta/chat/completions"},
		{"https://example.test/vendor", "https://example.test/vendor/v1/chat/completions"},
		{"https://example.test/v", "https://example.test/v/v1/chat/completions"},
	}
	for _, tc := range cases {
		p := New(WithAPIKey("k"), WithBaseURL(tc.base))
		if got := p.endpointURL(chatCompletionsEndpoint); got != tc.want {
			t.Errorf("base %q -> %q, want %q", tc.base, got, tc.want)
		}
	}
}

func TestWebSocketAndHTTPShareVersionAwarePaths(t *testing.T) {
	for _, base := range []string{"https://api.openai.com", "https://gw.test/api/v4", "https://gw.test/v1beta"} {
		p := New(WithAPIKey("k"), WithBaseURL(base))
		httpURL := p.endpointURL(responsesEndpoint)
		wsURL, err := responsesWebSocketURL(p.baseURL, p.endpointPath(responsesEndpoint))
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimPrefix(httpURL, "https://") != strings.TrimPrefix(wsURL, "wss://") {
			t.Errorf("base %q: http %q and websocket %q disagree", base, httpURL, wsURL)
		}
	}
}
