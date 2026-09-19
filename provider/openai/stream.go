package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// streamedResponse implements core.StreamedResponse for OpenAI SSE streams.
type streamedResponse struct {
	reader       *bufio.Reader
	body         io.ReadCloser
	model        string
	resolveModel func(string) (string, error)
	modelSeen    bool
	usage        core.Usage
	parts        []core.ModelResponsePart
	stopReason   core.FinishReason
	done         bool
	terminal     bool
	streamErr    error // non-nil if server sent an error mid-stream

	// instrumentation receives per-request latency updates; nil = no-op. It is
	// finalized exactly once (on Close/EOF). The Chat Completions path does not
	// capture streaming phase granularity; it only finalizes the trace so the
	// observer fires for every request.
	instrumentation *requestInstrumentation

	// State for tracking tool calls being built across deltas.
	currentParts map[int]core.ModelResponsePart
	argsBuffers  map[int]*strings.Builder
	// Map tool call index to the next part index.
	toolCallPartIndex map[int]int
	nextPartIndex     int
	pendingEvents     []core.ModelResponseStreamEvent

	// interleaveParts keeps reasoning, text, and tool calls in arrival order
	// (a new part whenever the kind changes). When false, all text extends
	// the first text part, the long-standing Chat Completions behavior.
	interleaveParts bool
}

func newStreamedResponse(body io.ReadCloser, model string) *streamedResponse {
	return newBoundStreamedResponse(body, model, nil, nil)
}

func newBoundStreamedResponse(
	body io.ReadCloser,
	model string,
	resolveModel func(string) (string, error),
	ri *requestInstrumentation,
) *streamedResponse {
	return &streamedResponse{
		reader:            bufio.NewReader(body),
		body:              body,
		model:             model,
		resolveModel:      resolveModel,
		instrumentation:   ri,
		currentParts:      make(map[int]core.ModelResponsePart),
		argsBuffers:       make(map[int]*strings.Builder),
		toolCallPartIndex: make(map[int]int),
		stopReason:        core.FinishReasonStop,
	}
}

// apiChunk represents an OpenAI streaming chunk.
type apiChunk struct {
	ID      string           `json:"id"`
	Object  string           `json:"object"`
	Model   string           `json:"model,omitempty"`
	Choices []apiChunkChoice `json:"choices"`
	Usage   *apiUsage        `json:"usage,omitempty"`
	Error   *apiStreamError  `json:"error,omitempty"`
}

// apiStreamError represents an error returned in a streaming chunk.
// OpenAI-compatible APIs (xAI, Together, etc.) may send error objects
// mid-stream when rate limits or server errors occur.
type apiStreamError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

type apiChunkChoice struct {
	Index        int           `json:"index"`
	Delta        apiChunkDelta `json:"delta"`
	FinishReason *string       `json:"finish_reason"`
}

type apiChunkDelta struct {
	Role             string             `json:"role,omitempty"`
	Content          string             `json:"content,omitempty"`
	ReasoningContent string             `json:"reasoning_content,omitempty"`
	ToolCalls        []apiChunkToolCall `json:"tool_calls,omitempty"`
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
			if s.streamErr != nil {
				return nil, s.streamErr
			}
			return nil, io.EOF
		}

		line, err := s.reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				if strings.TrimSpace(line) == "" {
					if !s.terminal {
						return s.failIncompleteStream()
					}
					s.done = true
					if modelErr := s.finalizeModelIdentity(); modelErr != nil {
						s.streamErr = modelErr
						s.instrumentation.recordError(modelErr)
						s.instrumentation.finish()
						return nil, modelErr
					}
					s.finalizeAll()
					s.instrumentation.finish()
					return nil, io.EOF
				}
				// Data received with EOF; process this line, finalize on next read.
			} else {
				return s.failStream(normalizeStreamReadError(err))
			}
		}

		line = strings.TrimSpace(line)

		// Skip empty lines and non-data lines.
		// Per SSE spec, the space after the colon is optional.
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimPrefix(data, " ")

		// Check for stream termination.
		if data == "[DONE]" {
			s.terminal = true
			s.done = true
			if err := s.finalizeModelIdentity(); err != nil {
				s.streamErr = err
				s.instrumentation.recordError(err)
				s.instrumentation.finish()
				return nil, err
			}
			s.finalizeAll()
			s.instrumentation.finish()
			return nil, io.EOF
		}

		var chunk apiChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			if strings.HasPrefix(strings.TrimSpace(data), "{") {
				return s.failStream(&core.StreamProtocolError{Provider: "openai"})
			}
			continue
		}
		if chunk.Model != "" {
			resolved := chunk.Model
			if s.resolveModel != nil {
				resolved, err = s.resolveModel(chunk.Model)
				if err != nil {
					s.done = true
					s.streamErr = err
					s.instrumentation.recordError(err)
					s.instrumentation.finish()
					return nil, err
				}
			}
			s.model = resolved
			s.modelSeen = true
		}

		// Check for error in stream data (sent by some OpenAI-compatible APIs).
		if chunk.Error != nil {
			s.done = true
			s.finalizeAll()
			classification := classifyProviderError(chunk.Error.Type, chunk.Error.Message)
			s.streamErr = fmt.Errorf("openai stream error (%s)", classification)
			s.instrumentation.classifyHTTPResponse(0, classification)
			s.instrumentation.recordError(s.streamErr)
			s.instrumentation.finish()
			return nil, s.streamErr
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
			s.terminal = true
		}

		var events []core.ModelResponseStreamEvent

		// Handle reasoning delta (DeepSeek reasoning_content). Thinking
		// precedes visible text within a turn; an empty string is the
		// API's block opener and carries nothing.
		if choice.Delta.ReasoningContent != "" {
			events = append(events, s.handleThinkingDelta(choice.Delta.ReasoningContent))
		}

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

func normalizeStreamReadError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &core.StreamTransportError{Provider: "openai"}
}

func (s *streamedResponse) failStream(err error) (core.ModelResponseStreamEvent, error) {
	s.done = true
	s.finalizeAll()
	s.streamErr = err
	s.instrumentation.recordError(err)
	s.instrumentation.finish()
	return nil, err
}

func (s *streamedResponse) failIncompleteStream() (core.ModelResponseStreamEvent, error) {
	if err := s.finalizeModelIdentity(); err != nil {
		return s.failStream(err)
	}
	return s.failStream(&core.StreamIncompleteError{Provider: "openai"})
}

func (s *streamedResponse) finalizeModelIdentity() error {
	if s.resolveModel == nil || s.modelSeen {
		return nil
	}
	resolved, err := s.resolveModel("")
	if err != nil {
		return err
	}
	s.model = resolved
	return nil
}

// openTextPartIndex returns the index of the text part deltas should extend:
// the most recently started part, if it is a text part. Text that arrives
// after thinking or a tool call started begins a new part so interleaved
// output keeps its order.
func (s *streamedResponse) openTextPartIndex() int {
	if s.interleaveParts {
		if _, ok := s.currentParts[s.nextPartIndex-1].(core.TextPart); ok {
			return s.nextPartIndex - 1
		}
		return -1
	}
	for idx, part := range s.currentParts {
		if _, ok := part.(core.TextPart); ok {
			return idx
		}
	}
	return -1
}

// handleTextDelta processes a text content delta.
func (s *streamedResponse) handleTextDelta(content string) core.ModelResponseStreamEvent {
	textIdx := s.openTextPartIndex()

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

// handleThinkingDelta processes a reasoning_content delta.
func (s *streamedResponse) handleThinkingDelta(content string) core.ModelResponseStreamEvent {
	// Extend the most recently started part only when it is a thinking part;
	// reasoning that resumes after text or a tool call is a new part.
	thinkingIdx := -1
	if s.interleaveParts {
		if _, ok := s.currentParts[s.nextPartIndex-1].(core.ThinkingPart); ok {
			thinkingIdx = s.nextPartIndex - 1
		}
	} else {
		for idx, part := range s.currentParts {
			if _, ok := part.(core.ThinkingPart); ok {
				thinkingIdx = idx
				break
			}
		}
	}

	if thinkingIdx == -1 {
		thinkingIdx = s.nextPartIndex
		s.nextPartIndex++
		s.currentParts[thinkingIdx] = core.ThinkingPart{Content: content}
		return core.PartStartEvent{
			Index: thinkingIdx,
			Part:  core.ThinkingPart{Content: content},
		}
	}

	if tp, ok := s.currentParts[thinkingIdx].(core.ThinkingPart); ok {
		tp.Content += content
		s.currentParts[thinkingIdx] = tp
	}
	return core.PartDeltaEvent{
		Index: thinkingIdx,
		Delta: core.ThinkingPartDelta{ContentDelta: content},
	}
}

// handleToolCallDeltas processes tool call deltas from a chunk.
func (s *streamedResponse) handleToolCallDeltas(toolCalls []apiChunkToolCall) []core.ModelResponseStreamEvent {
	var events []core.ModelResponseStreamEvent

	for _, tc := range toolCalls {
		partIdx, isNew := s.getToolCallPartIndex(tc.Index)

		if isNew {
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

	syntheticIdx := 0
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
			// OpenAI-compatible APIs (Ollama, LiteLLM) may return empty tool
			// call IDs. Generate synthetic IDs so tool results can be matched.
			if tc, ok := part.(core.ToolCallPart); ok && tc.ToolCallID == "" {
				tc.ToolCallID = fmt.Sprintf("call_%d", syntheticIdx)
				part = tc
			}
			syntheticIdx++
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
	s.instrumentation.finish()
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
