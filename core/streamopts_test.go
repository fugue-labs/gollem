package core

import (
	"io"
	"testing"
)

func TestStreamText_Delta(t *testing.T) {
	resp := &ModelResponse{
		Parts: []ModelResponsePart{TextPart{Content: "hello world"}},
	}
	stream := &testStreamedResponseWithDeltas{
		deltas: []string{"hel", "lo ", "wor", "ld"},
	}

	var chunks []string
	for text, err := range StreamTextDelta(stream) {
		if err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, text)
	}

	if len(chunks) != 4 {
		t.Errorf("expected 4 delta chunks, got %d", len(chunks))
	}

	_ = resp // used for setup context
}

func TestStreamText_Accumulated(t *testing.T) {
	stream := &testStreamedResponseWithDeltas{
		deltas: []string{"hel", "lo ", "wor", "ld"},
	}

	var chunks []string
	for text, err := range StreamTextAccumulated(stream) {
		if err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, text)
	}

	if len(chunks) != 4 {
		t.Errorf("expected 4 accumulated chunks, got %d", len(chunks))
	}

	// Last chunk should be the full text.
	if chunks[len(chunks)-1] != "hello world" {
		t.Errorf("expected 'hello world', got %q", chunks[len(chunks)-1])
	}
}

func TestStreamText_Debounce(t *testing.T) {
	stream := &testStreamedResponseWithDeltas{
		deltas: []string{"a", "b", "c"},
	}

	// With zero debounce, all chunks pass through.
	var chunks []string
	for text, err := range StreamTextDebounced(stream, 0) {
		if err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, text)
	}

	// Should get all 3 chunks (no debounce grouping).
	if len(chunks) != 3 {
		t.Errorf("expected 3 chunks, got %d", len(chunks))
	}
}

func TestStreamTextDelta_Convenience(t *testing.T) {
	stream := &testStreamedResponseWithDeltas{
		deltas: []string{"x", "y"},
	}

	count := 0
	for _, err := range StreamTextDelta(stream) {
		if err != nil {
			t.Fatal(err)
		}
		count++
	}

	if count != 2 {
		t.Errorf("expected 2 chunks, got %d", count)
	}
}

func TestStreamTextAccumulated_Convenience(t *testing.T) {
	stream := &testStreamedResponseWithDeltas{
		deltas: []string{"a", "b", "c"},
	}

	var last string
	for text, err := range StreamTextAccumulated(stream) {
		if err != nil {
			t.Fatal(err)
		}
		last = text
	}

	if last != "abc" {
		t.Errorf("expected 'abc', got %q", last)
	}
}

// testStreamedResponseWithDeltas emits text deltas for testing stream options.
type testStreamedResponseWithDeltas struct {
	deltas []string
	idx    int
}

func (s *testStreamedResponseWithDeltas) Next() (ModelResponseStreamEvent, error) {
	if s.idx >= len(s.deltas) {
		return nil, errEOF
	}
	delta := s.deltas[s.idx]
	s.idx++
	return PartDeltaEvent{
		Index: 0,
		Delta: TextPartDelta{ContentDelta: delta},
	}, nil
}

func (s *testStreamedResponseWithDeltas) Response() *ModelResponse {
	var accumulated string
	for _, d := range s.deltas {
		accumulated += d
	}
	return &ModelResponse{Parts: []ModelResponsePart{TextPart{Content: accumulated}}}
}

func (s *testStreamedResponseWithDeltas) Usage() Usage { return Usage{} }
func (s *testStreamedResponseWithDeltas) Close() error { return nil }

// errEOF is io.EOF for test usage.
var errEOF = io.EOF
