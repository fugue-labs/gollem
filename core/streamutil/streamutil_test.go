package streamutil_test

import (
	"io"
	"testing"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/core/streamutil"
)

func TestStreamText_Delta(t *testing.T) {
	resp := &core.ModelResponse{
		Parts: []core.ModelResponsePart{core.TextPart{Content: "hello world"}},
	}
	stream := &testStreamedResponseWithDeltas{
		deltas: []string{"hel", "lo ", "wor", "ld"},
	}

	var chunks []string
	for text, err := range streamutil.StreamTextDelta(stream) {
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
	for text, err := range streamutil.StreamTextAccumulated(stream) {
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
	for text, err := range streamutil.StreamTextDebounced(stream, 0) {
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
	for _, err := range streamutil.StreamTextDelta(stream) {
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
	for text, err := range streamutil.StreamTextAccumulated(stream) {
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

func (s *testStreamedResponseWithDeltas) Next() (core.ModelResponseStreamEvent, error) {
	if s.idx >= len(s.deltas) {
		return nil, io.EOF
	}
	delta := s.deltas[s.idx]
	s.idx++
	return core.PartDeltaEvent{
		Index: 0,
		Delta: core.TextPartDelta{ContentDelta: delta},
	}, nil
}

func (s *testStreamedResponseWithDeltas) Response() *core.ModelResponse {
	var accumulated string
	for _, d := range s.deltas {
		accumulated += d
	}
	return &core.ModelResponse{Parts: []core.ModelResponsePart{core.TextPart{Content: accumulated}}}
}

func (s *testStreamedResponseWithDeltas) Usage() core.Usage { return core.Usage{} }
func (s *testStreamedResponseWithDeltas) Close() error      { return nil }
