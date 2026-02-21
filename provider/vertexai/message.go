package vertexai

import (
	"encoding/json"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// --- Gemini API request types ---

type geminiRequest struct {
	Contents         []geminiContent         `json:"contents"`
	Tools            []geminiToolDecl        `json:"tools,omitempty"`
	SystemInstruction *geminiContent         `json:"systemInstruction,omitempty"`
	GenerationConfig *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	// Text part.
	Text string `json:"text,omitempty"`
	// Function call from model.
	FunctionCall *geminiFunctionCall `json:"functionCall,omitempty"`
	// Function response from user.
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
	// ThoughtSignature is required by Gemini 3.x models. The model returns it
	// with function calls, and it must be sent back with the conversation history.
	ThoughtSignature string `json:"thoughtSignature,omitempty"`
}

type geminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiToolDecl struct {
	FunctionDeclarations []geminiFunction `json:"functionDeclarations"`
}

type geminiFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type geminiGenerationConfig struct {
	MaxOutputTokens  int      `json:"maxOutputTokens,omitempty"`
	Temperature      *float64 `json:"temperature,omitempty"`
	TopP             *float64 `json:"topP,omitempty"`
	ResponseMimeType string   `json:"responseMimeType,omitempty"`
	ResponseSchema   any      `json:"responseSchema,omitempty"`
}

// --- Gemini API response types ---

type geminiResponse struct {
	Candidates    []geminiCandidate `json:"candidates"`
	UsageMetadata geminiUsage       `json:"usageMetadata"`
	ModelVersion  string            `json:"modelVersion"`
}

type geminiCandidate struct {
	Content       geminiContent `json:"content"`
	FinishReason  string        `json:"finishReason"`
	SafetyRatings []any         `json:"safetyRatings,omitempty"`
}

type geminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// buildRequest converts gollem messages into a Gemini API request.
func buildRequest(messages []core.ModelMessage, settings *core.ModelSettings, params *core.ModelRequestParameters) (*geminiRequest, error) {
	req := &geminiRequest{}

	// Generation config.
	genConfig := &geminiGenerationConfig{}
	hasGenConfig := false

	if settings != nil {
		if settings.MaxTokens != nil {
			genConfig.MaxOutputTokens = *settings.MaxTokens
			hasGenConfig = true
		}
		if settings.Temperature != nil {
			genConfig.Temperature = settings.Temperature
			hasGenConfig = true
		}
		if settings.TopP != nil {
			genConfig.TopP = settings.TopP
			hasGenConfig = true
		}
	}

	// Handle native structured output.
	if params != nil && params.OutputMode == core.OutputModeNative && params.OutputObject != nil {
		genConfig.ResponseMimeType = "application/json"
		genConfig.ResponseSchema = params.OutputObject.JSONSchema
		hasGenConfig = true
	}

	if hasGenConfig {
		req.GenerationConfig = genConfig
	}

	// Convert tool definitions.
	if params != nil {
		allTools := params.AllToolDefs()
		if len(allTools) > 0 {
			var funcs []geminiFunction
			for _, td := range allTools {
				schemaJSON, err := json.Marshal(td.ParametersSchema)
				if err != nil {
					return nil, err
				}
				funcs = append(funcs, geminiFunction{
					Name:        td.Name,
					Description: td.Description,
					Parameters:  schemaJSON,
				})
			}
			req.Tools = []geminiToolDecl{{FunctionDeclarations: funcs}}
		}
	}

	// Convert messages.
	for _, msg := range messages {
		switch m := msg.(type) {
		case core.ModelRequest:
			var userParts []geminiPart

			for _, part := range m.Parts {
				switch p := part.(type) {
				case core.SystemPromptPart:
					req.SystemInstruction = &geminiContent{
						Role:  "user",
						Parts: []geminiPart{{Text: p.Content}},
					}
				case core.UserPromptPart:
					userParts = append(userParts, geminiPart{Text: p.Content})
				case core.ToolReturnPart:
					content := make(map[string]any)
					switch v := p.Content.(type) {
					case string:
						content["result"] = v
					default:
						b, _ := json.Marshal(v)
						_ = json.Unmarshal(b, &content)
					}
					userParts = append(userParts, geminiPart{
						FunctionResponse: &geminiFunctionResponse{
							Name:     p.ToolName,
							Response: content,
						},
					})
				case core.RetryPromptPart:
					if p.ToolName != "" {
						userParts = append(userParts, geminiPart{
							FunctionResponse: &geminiFunctionResponse{
								Name:     p.ToolName,
								Response: map[string]any{"error": p.Content},
							},
						})
					} else {
						userParts = append(userParts, geminiPart{Text: p.Content})
					}
				}
			}

			if len(userParts) > 0 {
				req.Contents = append(req.Contents, geminiContent{
					Role:  "user",
					Parts: userParts,
				})
			}

		case core.ModelResponse:
			var modelParts []geminiPart
			for _, part := range m.Parts {
				switch p := part.(type) {
				case core.TextPart:
					modelParts = append(modelParts, geminiPart{Text: p.Content})
				case core.ToolCallPart:
					args := make(map[string]any)
					if p.ArgsJSON != "" && p.ArgsJSON != "{}" {
						_ = json.Unmarshal([]byte(p.ArgsJSON), &args)
					}
					gp := geminiPart{
						FunctionCall: &geminiFunctionCall{
							Name: p.ToolName,
							Args: args,
						},
					}
					// Round-trip thought signature for Gemini 3.x.
					if p.Metadata != nil {
						if sig, ok := p.Metadata["thoughtSignature"]; ok {
							gp.ThoughtSignature = sig
						}
					}
					modelParts = append(modelParts, gp)
				}
			}
			if len(modelParts) > 0 {
				req.Contents = append(req.Contents, geminiContent{
					Role:  "model",
					Parts: modelParts,
				})
			}
		}
	}

	return req, nil
}

// parseResponse converts a Gemini API response to a core.ModelResponse.
func parseResponse(resp *geminiResponse, modelName string) *core.ModelResponse {
	var parts []core.ModelResponsePart

	if len(resp.Candidates) > 0 {
		candidate := resp.Candidates[0]
		for _, p := range candidate.Content.Parts {
			if p.Text != "" {
				parts = append(parts, core.TextPart{Content: p.Text})
			}
			if p.FunctionCall != nil {
				argsJSON := "{}"
				if p.FunctionCall.Args != nil {
					b, _ := json.Marshal(p.FunctionCall.Args)
					argsJSON = string(b)
				}
				tc := core.ToolCallPart{
					ToolName:   p.FunctionCall.Name,
					ArgsJSON:   argsJSON,
					ToolCallID: p.FunctionCall.Name, // Gemini doesn't use tool call IDs
				}
				// Preserve thought signature for Gemini 3.x round-tripping.
				if p.ThoughtSignature != "" {
					tc.Metadata = map[string]string{
						"thoughtSignature": p.ThoughtSignature,
					}
				}
				parts = append(parts, tc)
			}
		}
	}

	return &core.ModelResponse{
		Parts:        parts,
		Usage:        mapUsage(resp.UsageMetadata),
		ModelName:    modelName,
		FinishReason: mapFinishReason(resp),
		Timestamp:    time.Now(),
	}
}

// mapFinishReason maps Gemini finish reasons to gollem FinishReasons.
func mapFinishReason(resp *geminiResponse) core.FinishReason {
	if len(resp.Candidates) == 0 {
		return core.FinishReasonStop
	}
	switch resp.Candidates[0].FinishReason {
	case "STOP":
		return core.FinishReasonStop
	case "MAX_TOKENS":
		return core.FinishReasonLength
	case "SAFETY":
		return core.FinishReasonContentFilter
	case "RECITATION":
		return core.FinishReasonContentFilter
	default:
		return core.FinishReasonStop
	}
}

// mapUsage converts Gemini usage to gollem Usage.
func mapUsage(u geminiUsage) core.Usage {
	return core.Usage{
		InputTokens:  u.PromptTokenCount,
		OutputTokens: u.CandidatesTokenCount,
	}
}
