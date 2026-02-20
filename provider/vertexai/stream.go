package vertexai

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// streamedResponse implements core.StreamedResponse for Vertex AI Gemini SSE streams.
type streamedResponse struct {
	reader     *bufio.Reader
	body       io.ReadCloser
	model      string
	usage      core.Usage
	parts      []core.ModelResponsePart
	stopReason core.FinishReason
	done       bool

	// State for tracking current parts being built.
	currentParts  map[int]core.ModelResponsePart
	nextPartIndex int
}

func newStreamedResponse(body io.ReadCloser, model string) *streamedResponse {
	return &streamedResponse{
		reader:       bufio.NewReader(body),
		body:         body,
		model:        model,
		currentParts: make(map[int]core.ModelResponsePart),
		stopReason:   core.FinishReasonStop,
	}
}

// Next returns the next stream event.
func (s *streamedResponse) Next() (core.ModelResponseStreamEvent, error) {
	for {
		if s.done {
			return nil, io.EOF
		}

		line, err := s.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				s.done = true
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

		var resp geminiResponse
		if err := json.Unmarshal([]byte(data), &resp); err != nil {
			continue
		}

		// Update usage.
		if resp.UsageMetadata.PromptTokenCount > 0 || resp.UsageMetadata.CandidatesTokenCount > 0 {
			s.usage = mapUsage(resp.UsageMetadata)
		}

		if len(resp.Candidates) == 0 {
			continue
		}

		candidate := resp.Candidates[0]

		// Check finish reason.
		if candidate.FinishReason != "" {
			s.stopReason = mapFinishReasonStr(candidate.FinishReason)
		}

		// Process parts in this chunk.
		for _, p := range candidate.Content.Parts {
			if p.Text != "" {
				event := s.handleTextDelta(p.Text)
				if event != nil {
					return event, nil
				}
			}
			if p.FunctionCall != nil {
				event := s.handleFunctionCall(p.FunctionCall)
				if event != nil {
					return event, nil
				}
			}
		}
	}
}

// handleTextDelta processes a text delta from a streaming chunk.
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
		textIdx = s.nextPartIndex
		s.nextPartIndex++
		s.currentParts[textIdx] = core.TextPart{Content: content}
		return core.PartStartEvent{
			Index: textIdx,
			Part:  core.TextPart{Content: content},
		}
	}

	if tp, ok := s.currentParts[textIdx].(core.TextPart); ok {
		tp.Content += content
		s.currentParts[textIdx] = tp
	}
	return core.PartDeltaEvent{
		Index: textIdx,
		Delta: core.TextPartDelta{ContentDelta: content},
	}
}

// handleFunctionCall processes a function call from a streaming chunk.
func (s *streamedResponse) handleFunctionCall(fc *geminiFunctionCall) core.ModelResponseStreamEvent {
	idx := s.nextPartIndex
	s.nextPartIndex++

	argsJSON := "{}"
	if fc.Args != nil {
		b, _ := json.Marshal(fc.Args)
		argsJSON = string(b)
	}

	part := core.ToolCallPart{
		ToolName:   fc.Name,
		ArgsJSON:   argsJSON,
		ToolCallID: fc.Name,
	}
	s.currentParts[idx] = part

	return core.PartStartEvent{
		Index: idx,
		Part:  part,
	}
}

// finalizeAll moves current parts into the finalized parts list.
func (s *streamedResponse) finalizeAll() {
	for _, part := range s.currentParts {
		s.parts = append(s.parts, part)
	}
	s.currentParts = make(map[int]core.ModelResponsePart)
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

// mapFinishReasonStr maps a Gemini finish reason string to gollem FinishReason.
func mapFinishReasonStr(reason string) core.FinishReason {
	switch reason {
	case "STOP":
		return core.FinishReasonStop
	case "MAX_TOKENS":
		return core.FinishReasonLength
	case "SAFETY", "RECITATION":
		return core.FinishReasonContentFilter
	default:
		return core.FinishReasonStop
	}
}

// Verify streamedResponse implements core.StreamedResponse.
var _ core.StreamedResponse = (*streamedResponse)(nil)

// Ensure fmt is used.
var _ = fmt.Sprintf
