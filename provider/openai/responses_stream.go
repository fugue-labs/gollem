package openai

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// responsesStreamEvent represents a single SSE event from the Responses API.
type responsesStreamEvent struct {
	Type         string          `json:"type"`
	Response     json.RawMessage `json:"response,omitempty"`
	Item         json.RawMessage `json:"item,omitempty"`
	Delta        string          `json:"delta,omitempty"`
	ContentIndex *int            `json:"content_index,omitempty"`
	OutputIndex  *int            `json:"output_index,omitempty"`
}

// responsesStreamedResponse implements core.StreamedResponse for Responses API
// SSE streams. It parses events like response.output_text.delta,
// response.output_item.done, and terminal response events, yielding PartStartEvent
// and PartDeltaEvent as they arrive.
type responsesStreamedResponse struct {
	scanner      *bufio.Scanner
	body         io.ReadCloser
	model        string
	resolveModel func(string) (string, error)
	modelSeen    bool
	usage        core.Usage

	stopReason core.FinishReason
	done       bool
	streamErr  error // non-nil if server sent an error mid-stream

	// Text part tracking.
	textPartStarted bool
	textPartIndex   int
	textContent     strings.Builder

	// Parts by index for ordered finalization.
	partsByIndex  map[int]core.ModelResponsePart
	nextPartIndex int
	hasToolCalls  bool

	// Tool call argument streaming by output index.
	toolCallByOutputIdx map[int]int              // output_index → part index
	toolCallArgsBuffers map[int]*strings.Builder // output_index → accumulated args

	// Reasoning summary streaming by output index. o-series / GPT-5 models
	// emit reasoning items with a summary_text stream; gollem surfaces this
	// as ThinkingPart.
	reasoningByOutputIdx map[int]int              // output_index → part index
	reasoningBuffers     map[int]*strings.Builder // output_index → accumulated summary

	pendingEvents []core.ModelResponseStreamEvent
}

func newResponsesStreamedResponse(body io.ReadCloser, model string) *responsesStreamedResponse {
	return newBoundResponsesStreamedResponse(body, model, nil)
}

func newBoundResponsesStreamedResponse(
	body io.ReadCloser,
	model string,
	resolveModel func(string) (string, error),
) *responsesStreamedResponse {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return &responsesStreamedResponse{
		scanner:              scanner,
		body:                 body,
		model:                model,
		resolveModel:         resolveModel,
		stopReason:           core.FinishReasonStop,
		partsByIndex:         make(map[int]core.ModelResponsePart),
		toolCallByOutputIdx:  make(map[int]int),
		toolCallArgsBuffers:  make(map[int]*strings.Builder),
		reasoningByOutputIdx: make(map[int]int),
		reasoningBuffers:     make(map[int]*strings.Builder),
	}
}

// Next returns the next stream event. Returns io.EOF when complete.
func (s *responsesStreamedResponse) Next() (core.ModelResponseStreamEvent, error) {
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

		if !s.scanner.Scan() {
			if err := s.scanner.Err(); err != nil {
				// Some providers deliver a terminal response event and then abort the
				// underlying HTTP/2 stream before sending [DONE]. Once we have the
				// completed response payload, treat the stream as successfully done.
				if !s.done {
					return nil, fmt.Errorf("openai: SSE read error: %w", err)
				}
			}
			// Scanner exhausted without a terminal response event.
			s.done = true
			if err := s.finalizeModelIdentity(); err != nil {
				s.streamErr = err
				return nil, err
			}
			s.finalize()
			return nil, io.EOF
		}

		line := s.scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimPrefix(data, " ")
		if data == "[DONE]" {
			s.done = true
			if err := s.finalizeModelIdentity(); err != nil {
				s.streamErr = err
				return nil, err
			}
			s.finalize()
			return nil, io.EOF
		}

		var event responsesStreamEvent
		if json.Unmarshal([]byte(data), &event) != nil {
			continue
		}

		events := s.processEvent(&event)
		if len(events) > 0 {
			if len(events) > 1 {
				s.pendingEvents = append(s.pendingEvents, events[1:]...)
			}
			return events[0], nil
		}
	}
}

func (s *responsesStreamedResponse) processEvent(event *responsesStreamEvent) []core.ModelResponseStreamEvent {
	switch event.Type {
	case "response.output_text.delta":
		return s.handleTextDelta(event.Delta)

	case "response.output_item.added":
		return s.handleOutputItemAdded(event.Item, event.OutputIndex)

	case "response.function_call_arguments.delta":
		return s.handleFunctionCallArgsDelta(event.OutputIndex, event.Delta)

	case "response.reasoning_summary_text.delta":
		return s.handleReasoningSummaryDelta(event.OutputIndex, event.Delta)

	case "response.output_item.done":
		return s.handleOutputItemDone(event.Item, event.OutputIndex)

	case "response.completed", "response.done":
		s.handleCompleted(event.Response)
		return nil

	case "response.failed":
		s.done = true
		s.finalize()
		// Extract error message from the response object.
		if event.Response != nil {
			var failedResp struct {
				Error struct {
					Message string `json:"message"`
					Code    string `json:"code"`
				} `json:"error"`
			}
			if json.Unmarshal(event.Response, &failedResp) == nil && failedResp.Error.Message != "" {
				s.streamErr = fmt.Errorf("openai: response failed (%s): %s", failedResp.Error.Code, failedResp.Error.Message)
			} else {
				s.streamErr = errors.New("openai: response failed")
			}
		} else {
			s.streamErr = errors.New("openai: response failed")
		}
		return nil

	case "response.incomplete":
		s.stopReason = core.FinishReasonLength
		s.done = true
		// Extract usage from the response payload (incomplete responses
		// still report token counts) and finalize accumulated parts.
		if event.Response != nil {
			var resp responsesAPIResponse
			if json.Unmarshal(event.Response, &resp) == nil {
				s.usage = mapResponsesUsage(resp.Usage)
				if err := s.bindResponseModel(resp.Model); err != nil {
					s.streamErr = err
				}
			}
		}
		if err := s.finalizeModelIdentity(); err != nil {
			s.streamErr = err
		}
		s.finalize()
		return nil
	}
	return nil
}

func (s *responsesStreamedResponse) handleTextDelta(delta string) []core.ModelResponseStreamEvent {
	if delta == "" {
		return nil
	}
	if !s.textPartStarted {
		s.textPartStarted = true
		s.textPartIndex = s.nextPartIndex
		s.nextPartIndex++
		s.textContent.WriteString(delta)
		return []core.ModelResponseStreamEvent{
			core.PartStartEvent{
				Index: s.textPartIndex,
				Part:  core.TextPart{Content: delta},
			},
		}
	}
	s.textContent.WriteString(delta)
	return []core.ModelResponseStreamEvent{
		core.PartDeltaEvent{
			Index: s.textPartIndex,
			Delta: core.TextPartDelta{ContentDelta: delta},
		},
	}
}

func (s *responsesStreamedResponse) handleOutputItemAdded(itemJSON json.RawMessage, outputIndex *int) []core.ModelResponseStreamEvent {
	if itemJSON == nil {
		return nil
	}
	var item responsesOutputItem
	if json.Unmarshal(itemJSON, &item) != nil {
		return nil
	}

	switch item.Type {
	case "function_call":
		s.hasToolCalls = true
		callID := item.CallID
		if callID == "" {
			callID = fmt.Sprintf("call_%d", s.nextPartIndex)
		}
		idx := s.nextPartIndex
		s.nextPartIndex++
		part := core.ToolCallPart{
			ToolName:   item.Name,
			ToolCallID: callID,
		}
		s.partsByIndex[idx] = part

		if outputIndex != nil {
			s.toolCallByOutputIdx[*outputIndex] = idx
			s.toolCallArgsBuffers[*outputIndex] = &strings.Builder{}
		}

		return []core.ModelResponseStreamEvent{
			core.PartStartEvent{Index: idx, Part: part},
		}

	case "reasoning":
		// Reasoning items are pre-registered here so summary deltas can append
		// into the same part. Emit PartStartEvent on the first delta (not here)
		// since the summary text is what users care about.
		if outputIndex != nil {
			if _, exists := s.reasoningByOutputIdx[*outputIndex]; !exists {
				s.reasoningBuffers[*outputIndex] = &strings.Builder{}
			}
		}
	}

	return nil
}

func (s *responsesStreamedResponse) handleReasoningSummaryDelta(outputIndex *int, delta string) []core.ModelResponseStreamEvent {
	if delta == "" || outputIndex == nil {
		return nil
	}
	if partIdx, ok := s.reasoningByOutputIdx[*outputIndex]; ok {
		if buf, ok := s.reasoningBuffers[*outputIndex]; ok {
			buf.WriteString(delta)
		}
		if tp, ok := s.partsByIndex[partIdx].(core.ThinkingPart); ok {
			tp.Content += delta
			s.partsByIndex[partIdx] = tp
		}
		return []core.ModelResponseStreamEvent{
			core.PartDeltaEvent{
				Index: partIdx,
				Delta: core.ThinkingPartDelta{ContentDelta: delta},
			},
		}
	}

	// First delta for this output_index — emit PartStartEvent.
	idx := s.nextPartIndex
	s.nextPartIndex++
	s.reasoningByOutputIdx[*outputIndex] = idx
	buf := s.reasoningBuffers[*outputIndex]
	if buf == nil {
		buf = &strings.Builder{}
		s.reasoningBuffers[*outputIndex] = buf
	}
	buf.WriteString(delta)
	s.partsByIndex[idx] = core.ThinkingPart{Content: delta}
	return []core.ModelResponseStreamEvent{
		core.PartStartEvent{
			Index: idx,
			Part:  core.ThinkingPart{Content: delta},
		},
	}
}

func (s *responsesStreamedResponse) handleFunctionCallArgsDelta(outputIndex *int, delta string) []core.ModelResponseStreamEvent {
	if delta == "" || outputIndex == nil {
		return nil
	}
	partIdx, ok := s.toolCallByOutputIdx[*outputIndex]
	if !ok {
		return nil
	}
	if buf, ok := s.toolCallArgsBuffers[*outputIndex]; ok {
		buf.WriteString(delta)
	}
	return []core.ModelResponseStreamEvent{
		core.PartDeltaEvent{
			Index: partIdx,
			Delta: core.ToolCallPartDelta{ArgsJSONDelta: delta},
		},
	}
}

func (s *responsesStreamedResponse) handleOutputItemDone(itemJSON json.RawMessage, outputIndex *int) []core.ModelResponseStreamEvent {
	if itemJSON == nil {
		return nil
	}

	var item responsesOutputItem
	if json.Unmarshal(itemJSON, &item) != nil {
		return nil
	}

	switch item.Type {
	case "function_call":
		s.hasToolCalls = true
		argsJSON := item.Arguments
		if argsJSON == "" {
			argsJSON = "{}"
		}
		callID := item.CallID
		if callID == "" {
			callID = fmt.Sprintf("call_%d", s.nextPartIndex)
		}

		// If tracked via output_item.added, finalize without re-emitting PartStartEvent.
		if outputIndex != nil {
			if partIdx, ok := s.toolCallByOutputIdx[*outputIndex]; ok {
				s.partsByIndex[partIdx] = core.ToolCallPart{
					ToolName:   item.Name,
					ArgsJSON:   argsJSON,
					ToolCallID: callID,
				}
				delete(s.toolCallByOutputIdx, *outputIndex)
				delete(s.toolCallArgsBuffers, *outputIndex)
				return nil
			}
		}

		// Not tracked — emit PartStartEvent (no output_item.added was received).
		idx := s.nextPartIndex
		s.nextPartIndex++
		part := core.ToolCallPart{
			ToolName:   item.Name,
			ArgsJSON:   argsJSON,
			ToolCallID: callID,
		}
		s.partsByIndex[idx] = part
		return []core.ModelResponseStreamEvent{
			core.PartStartEvent{Index: idx, Part: part},
		}

	case "message":
		// Message items are already handled via text deltas. If we missed
		// deltas (e.g. non-streaming fallback), capture the full text here.
		text := parseResponsesMessageText(item)
		if text != "" && !s.textPartStarted {
			s.textPartStarted = true
			s.textPartIndex = s.nextPartIndex
			s.nextPartIndex++
			s.textContent.WriteString(text)
			return []core.ModelResponseStreamEvent{
				core.PartStartEvent{
					Index: s.textPartIndex,
					Part:  core.TextPart{Content: text},
				},
			}
		}

	case "reasoning":
		summary := parseResponsesReasoningSummary(item)
		if summary == "" {
			return nil
		}
		// If we already surfaced this reasoning item via summary deltas, the
		// final payload only confirms what we've built — nothing to emit.
		if outputIndex != nil {
			if _, tracked := s.reasoningByOutputIdx[*outputIndex]; tracked {
				return nil
			}
		}
		// No deltas arrived (e.g. non-streaming fallback). Emit the summary
		// as a single ThinkingPart.
		idx := s.nextPartIndex
		s.nextPartIndex++
		if outputIndex != nil {
			s.reasoningByOutputIdx[*outputIndex] = idx
		}
		s.partsByIndex[idx] = core.ThinkingPart{Content: summary}
		return []core.ModelResponseStreamEvent{
			core.PartStartEvent{
				Index: idx,
				Part:  core.ThinkingPart{Content: summary},
			},
		}
	}

	return nil
}

func (s *responsesStreamedResponse) handleCompleted(respJSON json.RawMessage) {
	if respJSON != nil {
		var resp responsesAPIResponse
		if json.Unmarshal(respJSON, &resp) == nil {
			if err := s.bindResponseModel(resp.Model); err != nil {
				s.streamErr = err
			}
			s.usage = mapResponsesUsage(resp.Usage)
			if resp.IncompleteDetails != nil && strings.Contains(resp.IncompleteDetails.Reason, "max_output_tokens") {
				s.stopReason = core.FinishReasonLength
			}
		}
	}
	if err := s.finalizeModelIdentity(); err != nil {
		s.streamErr = err
	}
	s.done = true
	s.finalize()
}

func (s *responsesStreamedResponse) bindResponseModel(actual string) error {
	resolved := strings.TrimSpace(actual)
	if s.resolveModel != nil {
		var err error
		resolved, err = s.resolveModel(actual)
		if err != nil {
			return err
		}
	}
	if resolved != "" {
		s.model = resolved
	}
	s.modelSeen = strings.TrimSpace(actual) != ""
	return nil
}

func (s *responsesStreamedResponse) finalizeModelIdentity() error {
	if s.resolveModel == nil || s.modelSeen {
		return nil
	}
	return s.bindResponseModel("")
}

func (s *responsesStreamedResponse) finalize() {
	// Store the accumulated text part.
	if s.textPartStarted {
		s.partsByIndex[s.textPartIndex] = core.TextPart{Content: s.textContent.String()}
	}
	// Finalize any in-progress tool call args from incremental deltas
	// (handles streams that ended before output_item.done).
	for outputIdx, partIdx := range s.toolCallByOutputIdx {
		if tc, ok := s.partsByIndex[partIdx].(core.ToolCallPart); ok {
			if buf, ok := s.toolCallArgsBuffers[outputIdx]; ok && buf.Len() > 0 {
				tc.ArgsJSON = buf.String()
			}
			if tc.ArgsJSON == "" {
				tc.ArgsJSON = "{}"
			}
			s.partsByIndex[partIdx] = tc
		}
	}
	if s.hasToolCalls {
		s.stopReason = core.FinishReasonToolCall
	}
}

// Response returns the complete ModelResponse built from the stream.
func (s *responsesStreamedResponse) Response() *core.ModelResponse {
	// Collect parts in index order.
	indices := make([]int, 0, len(s.partsByIndex))
	for idx := range s.partsByIndex {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	parts := make([]core.ModelResponsePart, 0, len(indices))
	for _, idx := range indices {
		parts = append(parts, s.partsByIndex[idx])
	}

	return &core.ModelResponse{
		Parts:        parts,
		Usage:        s.usage,
		ModelName:    s.model,
		FinishReason: s.stopReason,
		Timestamp:    time.Now(),
	}
}

// Usage returns the current usage information.
func (s *responsesStreamedResponse) Usage() core.Usage {
	return s.usage
}

// Close releases resources.
func (s *responsesStreamedResponse) Close() error {
	return s.body.Close()
}

// Verify responsesStreamedResponse implements core.StreamedResponse.
var _ core.StreamedResponse = (*responsesStreamedResponse)(nil)

// prebuiltResponsesStream wraps a complete ModelResponse as a StreamedResponse.
// Used when the server returns JSON instead of SSE (e.g., streaming unsupported),
// so callers always get a valid StreamedResponse regardless of server behavior.
type prebuiltResponsesStream struct {
	response *core.ModelResponse
	idx      int
	done     bool
}

func newPrebuiltResponsesStream(resp *core.ModelResponse) *prebuiltResponsesStream {
	return &prebuiltResponsesStream{response: resp}
}

func (s *prebuiltResponsesStream) Next() (core.ModelResponseStreamEvent, error) {
	if s.done || s.idx >= len(s.response.Parts) {
		s.done = true
		return nil, io.EOF
	}
	event := core.PartStartEvent{
		Index: s.idx,
		Part:  s.response.Parts[s.idx],
	}
	s.idx++
	return event, nil
}

func (s *prebuiltResponsesStream) Response() *core.ModelResponse { return s.response }
func (s *prebuiltResponsesStream) Usage() core.Usage             { return s.response.Usage }
func (s *prebuiltResponsesStream) Close() error                  { return nil }

var _ core.StreamedResponse = (*prebuiltResponsesStream)(nil)
