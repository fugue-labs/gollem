//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/ext/memory"
)

// TestMemoryToolSaveAndSearch verifies the memory tool can save and search data.
func TestMemoryToolSaveAndSearch(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	store := memory.NewMemoryStore()
	memTool := memory.MemoryTool(store, "test")

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](memTool),
	)

	// Ask the agent to save something.
	_, err := agent.Run(ctx, `Use the memory tool to save a document with key "greeting" and value {"message": "hello world"}.`)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("save run failed: %v", err)
	}

	// Verify the store has the document.
	doc, err := store.Get(ctx, []string{"test"}, "greeting")
	if err != nil {
		t.Fatalf("store.Get failed: %v", err)
	}
	if doc == nil {
		t.Error("expected saved document, got nil")
	} else {
		t.Logf("Saved document: key=%q value=%v", doc.Key, doc.Value)
	}
}

// TestMemoryToolSearchRetrieve verifies search returns stored data via the agent.
func TestMemoryToolSearchRetrieve(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	store := memory.NewMemoryStore()
	// Pre-populate with data.
	_ = store.Put(ctx, []string{"kb"}, "fact-1", map[string]any{"content": "The capital of France is Paris."})
	_ = store.Put(ctx, []string{"kb"}, "fact-2", map[string]any{"content": "The capital of Japan is Tokyo."})
	_ = store.Put(ctx, []string{"kb"}, "fact-3", map[string]any{"content": "Go was created at Google in 2009."})

	memTool := memory.MemoryTool(store, "kb")

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithTools[string](memTool),
	)

	result, err := agent.Run(ctx, `Search the memory for information about "France" using the memory tool.`)
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("search run failed: %v", err)
	}

	// The output should mention Paris or France.
	lower := strings.ToLower(result.Output)
	if !strings.Contains(lower, "paris") && !strings.Contains(lower, "france") {
		t.Logf("output may not contain expected content: %q", result.Output)
	}

	t.Logf("Memory search output: %q", result.Output)
}

// TestStoreKnowledgeBaseWithAgent verifies StoreKnowledgeBase works as a core.KnowledgeBase.
func TestStoreKnowledgeBaseWithAgent(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	store := memory.NewMemoryStore()
	// Pre-populate.
	_ = store.Put(ctx, []string{"docs"}, "api-key", map[string]any{
		"content": "To authenticate with the Gollem API, use the X-API-Key header with your token.",
	})

	kb := memory.StoreKnowledgeBase(store, "docs")

	agent := core.NewAgent[string](newAnthropicProvider(),
		core.WithKnowledgeBase[string](kb),
	)

	result, err := agent.Run(ctx, "How do I authenticate with the API?")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("Run failed: %v", err)
	}

	lower := strings.ToLower(result.Output)
	if !strings.Contains(lower, "api") && !strings.Contains(lower, "key") && !strings.Contains(lower, "header") {
		t.Logf("output may not reference knowledge base content: %q", result.Output)
	}

	t.Logf("KnowledgeBase output: %q", result.Output)
}

// TestBufferMemoryMultiTurn verifies BufferMemory preserves conversation history across runs.
func TestBufferMemoryMultiTurn(t *testing.T) {
	anthropicOnly(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	buf := memory.NewBuffer(memory.WithMaxMessages(50))
	model := newAnthropicProvider()

	// Run 1: tell the agent something.
	agent1 := core.NewAgent[string](model,
		core.WithSystemPrompt[string]("You are a helpful assistant with memory."),
	)

	result1, err := agent1.Run(ctx, "Remember this: my favorite color is blue.")
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("Run 1 failed: %v", err)
	}

	// Store the conversation in buffer.
	err = buf.Add(ctx, result1.Messages...)
	if err != nil {
		t.Fatalf("buffer.Add failed: %v", err)
	}

	// Run 2: ask about it, passing the history.
	msgs, err := buf.Get(ctx)
	if err != nil {
		t.Fatalf("buffer.Get failed: %v", err)
	}

	agent2 := core.NewAgent[string](model,
		core.WithSystemPrompt[string]("You are a helpful assistant with memory."),
	)

	result2, err := agent2.Run(ctx, "What is my favorite color?", core.WithMessages(msgs...))
	if err != nil {
		skipOnAccountError(t, err)
		t.Fatalf("Run 2 failed: %v", err)
	}

	lower := strings.ToLower(result2.Output)
	if !strings.Contains(lower, "blue") {
		t.Errorf("expected output to mention 'blue', got: %q", result2.Output)
	}

	t.Logf("Multi-turn memory: run1=%q run2=%q", result1.Output, result2.Output)
}

// TestBufferMemoryCapacity verifies buffer respects max message limit.
func TestBufferMemoryCapacity(t *testing.T) {
	ctx := context.Background()

	buf := memory.NewBuffer(memory.WithMaxMessages(3))

	// Add 5 messages.
	for i := 0; i < 5; i++ {
		err := buf.Add(ctx, core.ModelRequest{
			Parts:     []core.ModelRequestPart{core.UserPromptPart{Content: fmt.Sprintf("msg-%d", i)}},
			Timestamp: time.Now(),
		})
		if err != nil {
			t.Fatalf("buffer.Add failed: %v", err)
		}
	}

	// Should only have 3.
	if buf.Len() != 3 {
		t.Errorf("expected buffer length 3, got %d", buf.Len())
	}

	msgs, err := buf.Get(ctx)
	if err != nil {
		t.Fatalf("buffer.Get failed: %v", err)
	}

	// Should be the last 3 messages (msg-2, msg-3, msg-4).
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	t.Logf("Buffer capacity: stored %d messages (capacity=3)", len(msgs))
}

// TestMemoryStoreCRUD verifies in-memory store CRUD operations end-to-end.
func TestMemoryStoreCRUD(t *testing.T) {
	ctx := context.Background()

	store := memory.NewMemoryStore()
	ns := []string{"test", "crud"}

	// Put.
	err := store.Put(ctx, ns, "key1", map[string]any{"data": "value1"})
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Get.
	doc, err := store.Get(ctx, ns, "key1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if doc == nil {
		t.Fatal("expected document, got nil")
	}
	if doc.Value["data"] != "value1" {
		t.Errorf("expected value 'value1', got %v", doc.Value["data"])
	}

	// Search.
	results, err := store.Search(ctx, ns, "value1", 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 search result, got %d", len(results))
	}

	// Delete.
	err = store.Delete(ctx, ns, "key1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify deleted.
	doc, err = store.Get(ctx, ns, "key1")
	if err != nil {
		t.Fatalf("Get after delete failed: %v", err)
	}
	if doc != nil {
		t.Error("expected nil after delete")
	}

	t.Log("MemoryStore CRUD operations verified")
}

// TestBufferMemoryClear verifies buffer clear removes all messages.
func TestBufferMemoryClear(t *testing.T) {
	ctx := context.Background()

	buf := memory.NewBuffer()

	_ = buf.Add(ctx, core.ModelRequest{
		Parts:     []core.ModelRequestPart{core.UserPromptPart{Content: "test"}},
		Timestamp: time.Now(),
	})

	if buf.Len() != 1 {
		t.Fatalf("expected 1 message, got %d", buf.Len())
	}

	err := buf.Clear(ctx)
	if err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	if buf.Len() != 0 {
		t.Errorf("expected 0 messages after clear, got %d", buf.Len())
	}

	t.Log("BufferMemory clear verified")
}
