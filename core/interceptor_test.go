package core

import (
	"context"
	"testing"
	"time"
)

func TestMessageInterceptor_Allow(t *testing.T) {
	interceptor := func(_ context.Context, _ []ModelMessage) InterceptResult {
		return InterceptResult{Action: MessageAllow}
	}

	messages := []ModelMessage{
		ModelRequest{Parts: []ModelRequestPart{UserPromptPart{Content: "hello"}}},
	}

	result, dropped := runMessageInterceptors(context.Background(), []MessageInterceptor{interceptor}, messages)
	if dropped {
		t.Fatal("expected message to be allowed")
	}
	if len(result) != 1 {
		t.Errorf("expected 1 message, got %d", len(result))
	}
}

func TestMessageInterceptor_Drop(t *testing.T) {
	interceptor := func(_ context.Context, _ []ModelMessage) InterceptResult {
		return InterceptResult{Action: MessageDrop, Reason: "blocked"}
	}

	messages := []ModelMessage{
		ModelRequest{Parts: []ModelRequestPart{UserPromptPart{Content: "bad content"}}},
	}

	_, dropped := runMessageInterceptors(context.Background(), []MessageInterceptor{interceptor}, messages)
	if !dropped {
		t.Fatal("expected message to be dropped")
	}
}

func TestMessageInterceptor_Modify(t *testing.T) {
	interceptor := func(_ context.Context, messages []ModelMessage) InterceptResult {
		modified := make([]ModelMessage, len(messages))
		for i, msg := range messages {
			if req, ok := msg.(ModelRequest); ok {
				newParts := make([]ModelRequestPart, len(req.Parts))
				for j, part := range req.Parts {
					if up, ok := part.(UserPromptPart); ok {
						newParts[j] = UserPromptPart{Content: up.Content + " [modified]", Timestamp: up.Timestamp}
					} else {
						newParts[j] = part
					}
				}
				modified[i] = ModelRequest{Parts: newParts, Timestamp: req.Timestamp}
			} else {
				modified[i] = msg
			}
		}
		return InterceptResult{Action: MessageModify, Messages: modified}
	}

	messages := []ModelMessage{
		ModelRequest{Parts: []ModelRequestPart{UserPromptPart{Content: "original"}}},
	}

	result, dropped := runMessageInterceptors(context.Background(), []MessageInterceptor{interceptor}, messages)
	if dropped {
		t.Fatal("expected message to be modified, not dropped")
	}

	req := result[0].(ModelRequest)
	up := req.Parts[0].(UserPromptPart)
	if up.Content != "original [modified]" {
		t.Errorf("expected 'original [modified]', got %q", up.Content)
	}
}

func TestResponseInterceptor(t *testing.T) {
	interceptor := func(_ context.Context, resp *ModelResponse) InterceptResult {
		if resp.TextContent() == "bad" {
			return InterceptResult{Action: MessageDrop, Reason: "filtered"}
		}
		return InterceptResult{Action: MessageAllow}
	}

	resp := &ModelResponse{Parts: []ModelResponsePart{TextPart{Content: "bad"}}, Timestamp: time.Now()}
	dropped := runResponseInterceptors(context.Background(), []ResponseInterceptor{interceptor}, resp)
	if !dropped {
		t.Fatal("expected response to be dropped")
	}

	resp2 := &ModelResponse{Parts: []ModelResponsePart{TextPart{Content: "good"}}, Timestamp: time.Now()}
	dropped2 := runResponseInterceptors(context.Background(), []ResponseInterceptor{interceptor}, resp2)
	if dropped2 {
		t.Fatal("expected response to be allowed")
	}
}

func TestRedactPII(t *testing.T) {
	// Redact email addresses.
	interceptor := RedactPII(`[\w.]+@[\w.]+\.\w+`, "[REDACTED]")

	messages := []ModelMessage{
		ModelRequest{Parts: []ModelRequestPart{
			UserPromptPart{Content: "Contact me at user@example.com please"},
		}},
	}

	result, dropped := runMessageInterceptors(context.Background(), []MessageInterceptor{interceptor}, messages)
	if dropped {
		t.Fatal("expected message to be modified, not dropped")
	}

	req := result[0].(ModelRequest)
	up := req.Parts[0].(UserPromptPart)
	if up.Content != "Contact me at [REDACTED] please" {
		t.Errorf("expected redacted content, got %q", up.Content)
	}
}

func TestAuditLog(t *testing.T) {
	var logged bool
	interceptor := AuditLog(func(direction string, messages []ModelMessage) {
		logged = true
		if direction != "request" {
			t.Errorf("expected direction 'request', got %q", direction)
		}
		if len(messages) != 1 {
			t.Errorf("expected 1 message, got %d", len(messages))
		}
	})

	messages := []ModelMessage{
		ModelRequest{Parts: []ModelRequestPart{UserPromptPart{Content: "audit this"}}},
	}

	result, dropped := runMessageInterceptors(context.Background(), []MessageInterceptor{interceptor}, messages)
	if dropped {
		t.Fatal("expected message to be allowed")
	}
	if !logged {
		t.Error("expected logger to be called")
	}
	if len(result) != 1 {
		t.Errorf("expected 1 message, got %d", len(result))
	}
}

func TestInterceptor_AgentIntegration(t *testing.T) {
	var intercepted bool
	interceptor := func(_ context.Context, messages []ModelMessage) InterceptResult {
		intercepted = true
		return InterceptResult{Action: MessageAllow}
	}

	model := NewTestModel(TextResponse("response"))
	agent := NewAgent[string](model,
		WithMessageInterceptor[string](interceptor),
	)

	_, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if !intercepted {
		t.Error("expected interceptor to be called during agent run")
	}
}
