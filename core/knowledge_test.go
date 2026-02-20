package core

import (
	"context"
	"errors"
	"testing"
)

// mockKnowledgeBase is a test KnowledgeBase that allows controlling behavior.
type mockKnowledgeBase struct {
	retrieveContent string
	retrieveErr     error
	storeErr        error
	stored          []string
	retrieveCalls   int
	storeCalls      int
}

func (kb *mockKnowledgeBase) Retrieve(_ context.Context, _ string) (string, error) {
	kb.retrieveCalls++
	return kb.retrieveContent, kb.retrieveErr
}

func (kb *mockKnowledgeBase) Store(_ context.Context, content string) error {
	kb.storeCalls++
	kb.stored = append(kb.stored, content)
	return kb.storeErr
}

// TestKnowledgeBase_RetrieveInjectsContext verifies that retrieved context
// appears as a system prompt in the model request.
func TestKnowledgeBase_RetrieveInjectsContext(t *testing.T) {
	kb := &mockKnowledgeBase{
		retrieveContent: "The capital of France is Paris.",
	}

	model := NewTestModel(TextResponse("Paris is the capital."))
	agent := NewAgent[string](model,
		WithKnowledgeBase[string](kb),
	)

	result, err := agent.Run(context.Background(), "What is the capital of France?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "Paris is the capital." {
		t.Errorf("output = %q, want 'Paris is the capital.'", result.Output)
	}

	// Verify Retrieve was called.
	if kb.retrieveCalls != 1 {
		t.Errorf("retrieve calls = %d, want 1", kb.retrieveCalls)
	}

	// Verify the model received the knowledge context as a system prompt.
	calls := model.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	req, ok := calls[0].Messages[0].(ModelRequest)
	if !ok {
		t.Fatal("expected ModelRequest")
	}
	foundKB := false
	for _, p := range req.Parts {
		if sp, ok := p.(SystemPromptPart); ok {
			if sp.Content == "[Knowledge Context] The capital of France is Paris." {
				foundKB = true
			}
		}
	}
	if !foundKB {
		t.Error("expected knowledge context system prompt in request")
	}
}

// TestKnowledgeBase_EmptyRetrieve verifies that an empty retrieve result
// does not add any system prompt.
func TestKnowledgeBase_EmptyRetrieve(t *testing.T) {
	kb := &mockKnowledgeBase{
		retrieveContent: "",
	}

	model := NewTestModel(TextResponse("Hello!"))
	agent := NewAgent[string](model,
		WithKnowledgeBase[string](kb),
	)

	result, err := agent.Run(context.Background(), "Hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "Hello!" {
		t.Errorf("output = %q, want 'Hello!'", result.Output)
	}

	// Verify no KB system prompt was added.
	calls := model.Calls()
	req, ok := calls[0].Messages[0].(ModelRequest)
	if !ok {
		t.Fatal("expected ModelRequest")
	}
	for _, p := range req.Parts {
		if sp, ok := p.(SystemPromptPart); ok {
			if sp.Content != "" {
				t.Errorf("unexpected system prompt: %q", sp.Content)
			}
		}
	}
}

// TestKnowledgeBase_AutoStore verifies that a successful run stores the
// response text when auto-store is enabled.
func TestKnowledgeBase_AutoStore(t *testing.T) {
	kb := &mockKnowledgeBase{
		retrieveContent: "some context",
	}

	model := NewTestModel(TextResponse("The answer is 42."))
	agent := NewAgent[string](model,
		WithKnowledgeBase[string](kb),
		WithKnowledgeBaseAutoStore[string](),
	)

	result, err := agent.Run(context.Background(), "What is the answer?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "The answer is 42." {
		t.Errorf("output = %q, want 'The answer is 42.'", result.Output)
	}

	// Verify Store was called with the response text.
	if kb.storeCalls != 1 {
		t.Fatalf("store calls = %d, want 1", kb.storeCalls)
	}
	if len(kb.stored) != 1 || kb.stored[0] != "The answer is 42." {
		t.Errorf("stored = %v, want [\"The answer is 42.\"]", kb.stored)
	}
}

// TestKnowledgeBase_AutoStoreWithoutKB verifies that enabling auto-store
// without a knowledge base does not cause errors.
func TestKnowledgeBase_AutoStoreWithoutKB(t *testing.T) {
	model := NewTestModel(TextResponse("Hello!"))
	agent := NewAgent[string](model,
		WithKnowledgeBaseAutoStore[string](),
	)

	result, err := agent.Run(context.Background(), "Hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "Hello!" {
		t.Errorf("output = %q, want 'Hello!'", result.Output)
	}
}

// TestKnowledgeBase_RetrieveError verifies that a retrieve error propagates.
func TestKnowledgeBase_RetrieveError(t *testing.T) {
	kb := &mockKnowledgeBase{
		retrieveErr: errors.New("database connection failed"),
	}

	model := NewTestModel(TextResponse("Should not reach here"))
	agent := NewAgent[string](model,
		WithKnowledgeBase[string](kb),
	)

	_, err := agent.Run(context.Background(), "Hello")
	if err == nil {
		t.Fatal("expected error from knowledge base retrieve")
	}
	if err.Error() != "failed to build initial request: knowledge base retrieve failed: database connection failed" {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestKnowledgeBase_StoreError verifies that a store error propagates.
func TestKnowledgeBase_StoreError(t *testing.T) {
	kb := &mockKnowledgeBase{
		retrieveContent: "context",
		storeErr:        errors.New("storage full"),
	}

	model := NewTestModel(TextResponse("A response"))
	agent := NewAgent[string](model,
		WithKnowledgeBase[string](kb),
		WithKnowledgeBaseAutoStore[string](),
	)

	_, err := agent.Run(context.Background(), "Hello")
	if err == nil {
		t.Fatal("expected error from knowledge base store")
	}
	if err.Error() != "knowledge base store failed: storage full" {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestKnowledgeBase_NoKB verifies that an agent works normally without a knowledge base.
func TestKnowledgeBase_NoKB(t *testing.T) {
	model := NewTestModel(TextResponse("Normal response"))
	agent := NewAgent[string](model)

	result, err := agent.Run(context.Background(), "Hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "Normal response" {
		t.Errorf("output = %q, want 'Normal response'", result.Output)
	}

	// Verify no extra system prompts from KB.
	calls := model.Calls()
	req, ok := calls[0].Messages[0].(ModelRequest)
	if !ok {
		t.Fatal("expected ModelRequest")
	}
	for _, p := range req.Parts {
		if _, ok := p.(SystemPromptPart); ok {
			t.Error("unexpected system prompt in request without knowledge base")
		}
	}
}

// TestStaticKnowledgeBase verifies that StaticKnowledgeBase returns fixed context.
func TestStaticKnowledgeBase(t *testing.T) {
	kb := NewStaticKnowledgeBase("Go is a statically typed language.")

	// Retrieve should always return the same context.
	ctx := context.Background()
	content, err := kb.Retrieve(ctx, "anything")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "Go is a statically typed language." {
		t.Errorf("content = %q, want 'Go is a statically typed language.'", content)
	}

	// Retrieve with a different query should still return the same context.
	content2, err := kb.Retrieve(ctx, "something else")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content2 != content {
		t.Errorf("expected same content for different queries, got %q and %q", content, content2)
	}

	// Store should record content.
	if err := kb.Store(ctx, "first"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := kb.Store(ctx, "second"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stored := kb.Stored()
	if len(stored) != 2 {
		t.Fatalf("stored count = %d, want 2", len(stored))
	}
	if stored[0] != "first" || stored[1] != "second" {
		t.Errorf("stored = %v, want [first, second]", stored)
	}
}

// TestKnowledgeBase_WithStaticKB tests the full integration using StaticKnowledgeBase.
func TestKnowledgeBase_WithStaticKB(t *testing.T) {
	kb := NewStaticKnowledgeBase("User prefers concise answers.")

	model := NewTestModel(TextResponse("42"))
	agent := NewAgent[string](model,
		WithKnowledgeBase[string](kb),
		WithKnowledgeBaseAutoStore[string](),
	)

	result, err := agent.Run(context.Background(), "What is the meaning of life?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "42" {
		t.Errorf("output = %q, want '42'", result.Output)
	}

	// Verify KB context was injected.
	calls := model.Calls()
	req, ok := calls[0].Messages[0].(ModelRequest)
	if !ok {
		t.Fatal("expected ModelRequest")
	}
	foundKB := false
	for _, p := range req.Parts {
		if sp, ok := p.(SystemPromptPart); ok && sp.Content == "[Knowledge Context] User prefers concise answers." {
			foundKB = true
		}
	}
	if !foundKB {
		t.Error("expected knowledge context system prompt in request")
	}

	// Verify auto-store recorded the response.
	stored := kb.Stored()
	if len(stored) != 1 || stored[0] != "42" {
		t.Errorf("stored = %v, want [\"42\"]", stored)
	}
}

// TestKnowledgeBase_WithSystemPrompt verifies KB context works alongside regular system prompts.
func TestKnowledgeBase_WithSystemPrompt(t *testing.T) {
	kb := &mockKnowledgeBase{
		retrieveContent: "Relevant facts here.",
	}

	model := NewTestModel(TextResponse("Combined response"))
	agent := NewAgent[string](model,
		WithSystemPrompt[string]("You are helpful."),
		WithKnowledgeBase[string](kb),
	)

	result, err := agent.Run(context.Background(), "Tell me something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "Combined response" {
		t.Errorf("output = %q", result.Output)
	}

	// Verify both system prompt and KB context are present.
	calls := model.Calls()
	req, ok := calls[0].Messages[0].(ModelRequest)
	if !ok {
		t.Fatal("expected ModelRequest")
	}
	foundSystem := false
	foundKB := false
	for _, p := range req.Parts {
		if sp, ok := p.(SystemPromptPart); ok {
			if sp.Content == "You are helpful." {
				foundSystem = true
			}
			if sp.Content == "[Knowledge Context] Relevant facts here." {
				foundKB = true
			}
		}
	}
	if !foundSystem {
		t.Error("expected static system prompt")
	}
	if !foundKB {
		t.Error("expected knowledge context system prompt")
	}
}
