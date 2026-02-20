package vertexai_anthropic

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// streamedResponse implements core.StreamedResponse for Anthropic SSE streams via Vertex AI.
type streamedResponse struct {
	reader     *bufio.Reader
	body       io.ReadCloser
	model      string
	usage      core.Usage
	parts      []core.ModelResponsePart
	stopReason core.FinishReason
	done       bool

	currentParts map[int]core.ModelResponsePart
	argsBuffers  map[int]*strings.Builder
}

func newStreamedResponse(body io.ReadCloser, model string) *streamedResponse {
	return &streamedResponse{
		reader:       bufio.NewReader(body),
		body:         body,
		model:        model,
		currentParts: make(map[int]core.ModelResponsePart),
		argsBuffers:  make(map[int]*strings.Builder),
		stopReason:   core.FinishReasonStop,
	}
}

// sseEvent represents a parsed Server-Sent Event.
type sseEvent struct {
	Event string
	Data  string
}

// Next returns the next stream event.
func (s *streamedResponse) Next() (core.ModelResponseStreamEvent, error) {
	for {
		if s.done {
			return nil, io.EOF
		}

		event, err := s.readSSEEvent()
		if err != nil {
			return nil, err
		}

		gollemEvent, ok := s.processSSEEvent(event)
		if ok {
			return gollemEvent, nil
		}
	}
}

// readSSEEvent reads one SSE event from the stream.
func (s *streamedResponse) readSSEEvent() (*sseEvent, error) {
	var eventType, data string

	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF && (eventType != "" || data != "") {
				break
			}
			return nil, err
		}

		line = strings.TrimRight(line, "\r\n")

		if line == "" {
			if eventType != "" || data != "" {
				break
			}
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			data = strings.TrimPrefix(line, "data: ")
		}
	}

	return &sseEvent{Event: eventType, Data: data}, nil
}

// processSSEEvent converts an SSE event into a gollem stream event.
func (s *streamedResponse) processSSEEvent(event *sseEvent) (core.ModelResponseStreamEvent, bool) {
	switch event.Event {
	case "message_start":
		var msg struct {
			Message struct {
				Usage apiUsage `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(event.Data), &msg); err == nil {
			s.usage = mapUsage(msg.Message.Usage)
		}
		return nil, false

	case "content_block_start":
		var block struct {
			Index        int             `json:"index"`
			ContentBlock json.RawMessage `json:"content_block"`
		}
		if err := json.Unmarshal([]byte(event.Data), &block); err != nil {
			return nil, false
		}

		var blockType struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
			ID   string `json:"id,omitempty"`
			Name string `json:"name,omitempty"`
		}
		if err := json.Unmarshal(block.ContentBlock, &blockType); err != nil {
			return nil, false
		}

		var part core.ModelResponsePart
		switch blockType.Type {
		case "text":
			part = core.TextPart{Content: blockType.Text}
		case "tool_use":
			part = core.ToolCallPart{
				ToolName:   blockType.Name,
				ToolCallID: blockType.ID,
			}
			s.argsBuffers[block.Index] = &strings.Builder{}
		default:
			return nil, false
		}

		s.currentParts[block.Index] = part
		return core.PartStartEvent{Index: block.Index, Part: part}, true

	case "content_block_delta":
		var delta struct {
			Index int `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text,omitempty"`
				PartialJSON string `json:"partial_json,omitempty"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(event.Data), &delta); err != nil {
			return nil, false
		}

		switch delta.Delta.Type {
		case "text_delta":
			if tp, ok := s.currentParts[delta.Index].(core.TextPart); ok {
				tp.Content += delta.Delta.Text
				s.currentParts[delta.Index] = tp
			}
			return core.PartDeltaEvent{
				Index: delta.Index,
				Delta: core.TextPartDelta{ContentDelta: delta.Delta.Text},
			}, true

		case "input_json_delta":
			if buf, ok := s.argsBuffers[delta.Index]; ok {
				buf.WriteString(delta.Delta.PartialJSON)
			}
			return core.PartDeltaEvent{
				Index: delta.Index,
				Delta: core.ToolCallPartDelta{ArgsJSONDelta: delta.Delta.PartialJSON},
			}, true
		}
		return nil, false

	case "content_block_stop":
		var block struct {
			Index int `json:"index"`
		}
		if err := json.Unmarshal([]byte(event.Data), &block); err != nil {
			return nil, false
		}

		if part, ok := s.currentParts[block.Index]; ok {
			if tc, ok := part.(core.ToolCallPart); ok {
				if buf, ok := s.argsBuffers[block.Index]; ok {
					tc.ArgsJSON = buf.String()
					if tc.ArgsJSON == "" {
						tc.ArgsJSON = "{}"
					}
					part = tc
					delete(s.argsBuffers, block.Index)
				}
			}
			s.parts = append(s.parts, part)
			delete(s.currentParts, block.Index)
		}

		return core.PartEndEvent{Index: block.Index}, true

	case "message_delta":
		var md struct {
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage apiUsage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(event.Data), &md); err == nil {
			s.stopReason = mapStopReason(md.Delta.StopReason)
			if md.Usage.OutputTokens > 0 {
				s.usage.OutputTokens = md.Usage.OutputTokens
			}
		}
		return nil, false

	case "message_stop":
		s.done = true
		return nil, false

	case "error":
		s.done = true
		return nil, false

	default:
		return nil, false
	}
}

// Response returns the complete ModelResponse built from the stream.
func (s *streamedResponse) Response() *core.ModelResponse {
	return &core.ModelResponse{
		Parts:        s.parts,
		Usage:        s.usage,
		ModelName:    s.model,
		FinishReason: s.stopReason,
		Timestamp:    time.Now(),
	}
}

// Usage returns the current usage information.
func (s *streamedResponse) Usage() core.Usage {
	return s.usage
}

// Close releases resources.
func (s *streamedResponse) Close() error {
	return s.body.Close()
}

// Verify streamedResponse implements core.StreamedResponse.
var _ core.StreamedResponse = (*streamedResponse)(nil)

// Ensure fmt is used.
var _ = fmt.Sprintf
