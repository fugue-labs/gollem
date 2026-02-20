package core

import (
	"errors"
	"io"
	"iter"
	"sync"
)

// StreamResult wraps a streaming model response and provides methods to
// consume the stream as text, events, or structured output.
type StreamResult[T any] struct {
	stream       StreamedResponse
	outputSchema *OutputSchema
	validators   []OutputValidatorFunc[T]
	messages     []ModelMessage

	mu sync.Mutex
}

// newStreamResult creates a new StreamResult.
func newStreamResult[T any](stream StreamedResponse, schema *OutputSchema, validators []OutputValidatorFunc[T], messages []ModelMessage) *StreamResult[T] {
	return &StreamResult[T]{
		stream:       stream,
		outputSchema: schema,
		validators:   validators,
		messages:     messages,
	}
}

// StreamText returns an iterator that yields text content from the stream.
// If delta is true, yields incremental text chunks.
// If delta is false, yields cumulative text.
func (s *StreamResult[T]) StreamText(delta bool) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		var cumulative string
		for {
			event, err := s.stream.Next()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				yield("", err)
				return
			}

			switch e := event.(type) {
			case PartDeltaEvent:
				if td, ok := e.Delta.(TextPartDelta); ok {
					if delta {
						if !yield(td.ContentDelta, nil) {
							return
						}
					} else {
						cumulative += td.ContentDelta
						if !yield(cumulative, nil) {
							return
						}
					}
				}
			case PartStartEvent:
				if tp, ok := e.Part.(TextPart); ok && !delta {
					cumulative += tp.Content
					if cumulative != "" {
						if !yield(cumulative, nil) {
							return
						}
					}
				}
			}
		}
	}
}

// StreamEvents returns an iterator over raw stream events.
func (s *StreamResult[T]) StreamEvents() iter.Seq2[ModelResponseStreamEvent, error] {
	return func(yield func(ModelResponseStreamEvent, error) bool) {
		for {
			event, err := s.stream.Next()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(event, nil) {
				return
			}
		}
	}
}

// GetOutput consumes the entire stream and returns the final response.
func (s *StreamResult[T]) GetOutput() (*ModelResponse, error) {
	// Drain the stream.
	for {
		_, err := s.stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return s.stream.Response(), nil
}

// Response returns the ModelResponse built from data received so far.
func (s *StreamResult[T]) Response() *ModelResponse {
	return s.stream.Response()
}

// Messages returns the message history at the start of this stream.
func (s *StreamResult[T]) Messages() []ModelMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.messages
}

// Close releases streaming resources.
func (s *StreamResult[T]) Close() error {
	return s.stream.Close()
}
