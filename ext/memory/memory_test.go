package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/core"
)

func TestBufferMemoryAddGet(t *testing.T) {
	buf := NewBuffer()
	ctx := context.Background()

	msg1 := core.ModelRequest{
		Parts:     []core.ModelRequestPart{core.UserPromptPart{Content: "Hello"}},
		Timestamp: time.Now(),
	}
	msg2 := core.ModelResponse{
		Parts:     []core.ModelResponsePart{core.TextPart{Content: "Hi there"}},
		ModelName: "test",
	}

	if err := buf.Add(ctx, msg1, msg2); err != nil {
		t.Fatal(err)
	}

	messages, err := buf.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}

	// Verify first message.
	req, ok := messages[0].(core.ModelRequest)
	if !ok {
		t.Fatal("expected ModelRequest")
	}
	up, ok := req.Parts[0].(core.UserPromptPart)
	if !ok {
		t.Fatal("expected UserPromptPart")
	}
	if up.Content != "Hello" {
		t.Errorf("expected 'Hello', got '%s'", up.Content)
	}

	// Verify second message.
	resp, ok := messages[1].(core.ModelResponse)
	if !ok {
		t.Fatal("expected ModelResponse")
	}
	tp, ok := resp.Parts[0].(core.TextPart)
	if !ok {
		t.Fatal("expected TextPart")
	}
	if tp.Content != "Hi there" {
		t.Errorf("expected 'Hi there', got '%s'", tp.Content)
	}
}

func TestBufferMemoryClear(t *testing.T) {
	buf := NewBuffer()
	ctx := context.Background()

	buf.Add(ctx, core.ModelRequest{
		Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "test"}},
	})

	if buf.Len() != 1 {
		t.Fatalf("expected 1 message, got %d", buf.Len())
	}

	if err := buf.Clear(ctx); err != nil {
		t.Fatal(err)
	}

	if buf.Len() != 0 {
		t.Fatalf("expected 0 messages after clear, got %d", buf.Len())
	}

	messages, err := buf.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(messages))
	}
}

func TestBufferMemoryOverflow(t *testing.T) {
	buf := NewBuffer(WithMaxMessages(3))
	ctx := context.Background()

	// Add 5 messages.
	for i := range 5 {
		buf.Add(ctx, core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: string(rune('A' + i))},
			},
		})
	}

	if buf.Len() != 3 {
		t.Fatalf("expected 3 messages, got %d", buf.Len())
	}

	messages, err := buf.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Should have the last 3 messages (C, D, E).
	expected := []string{"C", "D", "E"}
	for i, msg := range messages {
		req := msg.(core.ModelRequest)
		up := req.Parts[0].(core.UserPromptPart)
		if up.Content != expected[i] {
			t.Errorf("position %d: expected '%s', got '%s'", i, expected[i], up.Content)
		}
	}
}

func TestBufferMemoryGetReturnsCopy(t *testing.T) {
	buf := NewBuffer()
	ctx := context.Background()

	buf.Add(ctx, core.ModelRequest{
		Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "original"}},
	})

	messages1, _ := buf.Get(ctx)
	messages2, _ := buf.Get(ctx)

	// Modifying one copy should not affect the other.
	messages1[0] = core.ModelRequest{
		Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "modified"}},
	}

	req := messages2[0].(core.ModelRequest)
	up := req.Parts[0].(core.UserPromptPart)
	if up.Content != "original" {
		t.Errorf("expected 'original', got '%s' — Get returned shared slice", up.Content)
	}
}

func TestBufferMemoryDefaultMaxSize(t *testing.T) {
	buf := NewBuffer()
	if buf.maxSize != 100 {
		t.Errorf("expected default maxSize 100, got %d", buf.maxSize)
	}
}

func TestBufferMemoryEmptyGet(t *testing.T) {
	buf := NewBuffer()
	ctx := context.Background()

	messages, err := buf.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(messages))
	}
}

func TestBufferMemoryConcurrent(t *testing.T) {
	buf := NewBuffer(WithMaxMessages(50))
	ctx := context.Background()

	var wg sync.WaitGroup
	// Concurrent writers.
	for i := range 10 {
		wg.Add(1)
		go func(_ int) {
			defer wg.Done()
			for range 10 {
				buf.Add(ctx, core.ModelRequest{
					Parts: []core.ModelRequestPart{
						core.UserPromptPart{Content: "msg"},
					},
				})
			}
		}(i)
	}

	// Concurrent readers.
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				buf.Get(ctx)
			}
		}()
	}

	wg.Wait()

	// Should not exceed max size.
	if buf.Len() > 50 {
		t.Errorf("expected at most 50 messages, got %d", buf.Len())
	}
}

func TestBufferMemoryBatchAdd(t *testing.T) {
	buf := NewBuffer(WithMaxMessages(5))
	ctx := context.Background()

	// Add a batch larger than maxSize.
	msgs := make([]core.ModelMessage, 8)
	for i := range msgs {
		msgs[i] = core.ModelRequest{
			Parts: []core.ModelRequestPart{
				core.UserPromptPart{Content: string(rune('A' + i))},
			},
		}
	}

	buf.Add(ctx, msgs...)

	if buf.Len() != 5 {
		t.Fatalf("expected 5 messages, got %d", buf.Len())
	}

	messages, _ := buf.Get(ctx)
	// Should have the last 5 (D, E, F, G, H).
	expected := []string{"D", "E", "F", "G", "H"}
	for i, msg := range messages {
		req := msg.(core.ModelRequest)
		up := req.Parts[0].(core.UserPromptPart)
		if up.Content != expected[i] {
			t.Errorf("position %d: expected '%s', got '%s'", i, expected[i], up.Content)
		}
	}
}
