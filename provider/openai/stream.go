package openai

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// streamedResponse implements core.StreamedResponse for OpenAI SSE streams.
type streamedResponse struct {
	reader     *bufio.Reader
	body       io.ReadCloser
	model      string
	usage      core.Usage
	parts      []core.ModelResponsePart
	stopReason core.FinishReason
	done       bool

	// State for tracking tool calls being built across deltas.
	currentParts map[int]core.ModelResponsePart
	argsBuffers  map[int]*strings.Builder
	// Map tool call index to the next part index.
	toolCallPartIndex map[int]int
	nextPartIndex     int
	pendingEvents     []core.ModelResponseStreamEvent
}

func newStreamedResponse(body io.ReadCloser, model string) *streamedResponse {
	return &streamedResponse{
		reader:            bufio.NewReader(body),
		body:              body,
		model:             model,
		currentParts:      make(map[int]core.ModelResponsePart),
		argsBuffers:       make(map[int]*strings.Builder),
		toolCallPartIndex: make(map[int]int),
		stopReason:        core.FinishReasonStop,
	}
}

// apiChunk represents an OpenAI streaming chunk.
type apiChunk struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Choices []apiChunkChoice `json:"choices"`
	Usage   *apiUsage      `json:"usage,omitempty"`
}

type apiChunkChoice struct {
	Index        int            `json:"index"`
	Delta        apiChunkDelta  `json:"delta"`
	FinishReason *string        `json:"finish_reason"`
}

type apiChunkDelta struct {
	Role      string            `json:"role,omitempty"`
	Content   string            `json:"content,omitempty"`
	ToolCalls []apiChunkToolCall `json:"tool_calls,omitempty"`
}

type apiChunkToolCall struct {
	Index    int              `json:"index"`
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function apiChunkFunction `json:"function"`
}

type apiChunkFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// Next returns the next stream event.
func (s *streamedResponse) Next() (core.ModelResponseStreamEvent, error) {
	for {
		if len(s.pendingEvents) > 0 {
			event := s.pendingEvents[0]
			s.pendingEvents = s.pendingEvents[1:]
			return event, nil
		}

		if s.done {
			return nil, io.EOF
		}

		line, err := s.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				s.done = true
				// Finalize any remaining parts.
				s.finalizeAll()
				return nil, io.EOF
			}
			return nil, err
		}

		line = strings.TrimSpace(line)

		// Skip empty lines and non-data lines.
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		// Check for stream termination.
		if data == "[DONE]" {
			s.done = true
			s.finalizeAll()
			return nil, io.EOF
		}

		var chunk apiChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		// Update usage if present.
		if chunk.Usage != nil {
			s.usage = mapUsage(*chunk.Usage)
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]

		// Check finish reason.
		if choice.FinishReason != nil {
			s.stopReason = mapFinishReasonStr(*choice.FinishReason)
		}

		var events []core.ModelResponseStreamEvent

		// Handle text content delta.
		if choice.Delta.Content != "" {
			event := s.handleTextDelta(choice.Delta.Content)
			if event != nil {
				events = append(events, event)
			}
		}

		// Handle tool call deltas.
		if len(choice.Delta.ToolCalls) > 0 {
			events = append(events, s.handleToolCallDeltas(choice.Delta.ToolCalls)...)
		}

		if len(events) > 0 {
			if len(events) > 1 {
				s.pendingEvents = append(s.pendingEvents, events[1:]...)
			}
			return events[0], nil
		}
	}
}

// handleTextDelta processes a text content delta.
func (s *streamedResponse) handleTextDelta(content string) core.ModelResponseStreamEvent {
	// Check if text part already started.
	textIdx := -1
	for idx, part := range s.currentParts {
		if _, ok := part.(core.TextPart); ok {
			textIdx = idx
			break
		}
	}

	if textIdx == -1 {
		// Start a new text part.
		textIdx = s.nextPartIndex
		s.nextPartIndex++
		s.currentParts[textIdx] = core.TextPart{Content: content}
		return core.PartStartEvent{
			Index: textIdx,
			Part:  core.TextPart{Content: content},
		}
	}

	// Append to existing text part.
	if tp, ok := s.currentParts[textIdx].(core.TextPart); ok {
		tp.Content += content
		s.currentParts[textIdx] = tp
	}
	return core.PartDeltaEvent{
		Index: textIdx,
		Delta: core.TextPartDelta{ContentDelta: content},
	}
}

// handleToolCallDeltas processes tool call deltas from a chunk.
func (s *streamedResponse) handleToolCallDeltas(toolCalls []apiChunkToolCall) []core.ModelResponseStreamEvent {
	var events []core.ModelResponseStreamEvent

	for _, tc := range toolCalls {
		partIdx, isNew := s.getToolCallPartIndex(tc.Index)

		if isNew || tc.ID != "" {
			// Start a new tool call part.
			part := core.ToolCallPart{
				ToolName:   tc.Function.Name,
				ToolCallID: tc.ID,
			}
			s.currentParts[partIdx] = part
			s.argsBuffers[tc.Index] = &strings.Builder{}
			if tc.Function.Arguments != "" {
				s.argsBuffers[tc.Index].WriteString(tc.Function.Arguments)
			}
			events = append(events, core.PartStartEvent{
				Index: partIdx,
				Part:  part,
			})
		} else if tc.Function.Arguments != "" {
			// Append arguments delta.
			if buf, ok := s.argsBuffers[tc.Index]; ok {
				buf.WriteString(tc.Function.Arguments)
			}
			events = append(events, core.PartDeltaEvent{
				Index: partIdx,
				Delta: core.ToolCallPartDelta{ArgsJSONDelta: tc.Function.Arguments},
			})
		}
	}

	return events
}

// getToolCallPartIndex maps a tool call index to a part index.
func (s *streamedResponse) getToolCallPartIndex(tcIndex int) (int, bool) {
	if idx, ok := s.toolCallPartIndex[tcIndex]; ok {
		return idx, false
	}
	idx := s.nextPartIndex
	s.nextPartIndex++
	s.toolCallPartIndex[tcIndex] = idx
	return idx, true
}

// finalizeAll finalizes all remaining parts.
func (s *streamedResponse) finalizeAll() {
	keys := make([]int, 0, len(s.currentParts))
	for idx := range s.currentParts {
		keys = append(keys, idx)
	}
	sort.Ints(keys)

	for _, idx := range keys {
		part := s.currentParts[idx]
		if tc, ok := part.(core.ToolCallPart); ok {
			// Find the tool call index for this part.
			for tcIdx, partIdx := range s.toolCallPartIndex {
				if partIdx == idx {
					if buf, ok := s.argsBuffers[tcIdx]; ok {
						tc.ArgsJSON = buf.String()
						if tc.ArgsJSON == "" {
							tc.ArgsJSON = "{}"
						}
						part = tc
					}
					break
				}
			}
		}
		s.parts = append(s.parts, part)
	}
	s.currentParts = make(map[int]core.ModelResponsePart)
	s.argsBuffers = make(map[int]*strings.Builder)
	s.toolCallPartIndex = make(map[int]int)
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

// mapFinishReasonStr maps a finish reason string to gollem FinishReason.
func mapFinishReasonStr(reason string) core.FinishReason {
	switch reason {
	case "stop":
		return core.FinishReasonStop
	case "length":
		return core.FinishReasonLength
	case "tool_calls":
		return core.FinishReasonToolCall
	case "content_filter":
		return core.FinishReasonContentFilter
	default:
		return core.FinishReasonStop
	}
}

// Verify streamedResponse implements core.StreamedResponse.
var _ core.StreamedResponse = (*streamedResponse)(nil)

// Ensure fmt is used.
var _ = fmt.Sprintf
