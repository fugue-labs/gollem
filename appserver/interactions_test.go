package appserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/appserver/catalog"
	"github.com/fugue-labs/gollem/appserver/protocol"
)

func TestInteractionUserInputRequestResolvesFromJSONRPCResponse(t *testing.T) {
	server := readyServer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resultCh := make(chan interactionResult, 1)
	go func() {
		resp, err := server.interact.RequestUserInput(ctx, UserInputRequest{
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			Prompt:   "Need a value",
			Options:  []string{"one", "two"},
			Required: true,
		})
		resultCh <- interactionResult{resp: resp, err: err}
	}()

	req := waitForServerRequest(t, server)
	if req.Method != InteractionRequestUserInput {
		t.Fatalf("request method = %q, want %s", req.Method, InteractionRequestUserInput)
	}
	var rawParams map[string]json.RawMessage
	if err := json.Unmarshal(req.Params, &rawParams); err != nil {
		t.Fatalf("decode raw request params: %v", err)
	}
	for name := range rawParams {
		switch name {
		case "threadId", "turnId", "itemId", "questions", "isBlocking", "autoResolutionMs":
		default:
			t.Fatalf("user input request retained legacy field %q: %s", name, req.Params)
		}
	}
	if len(rawParams) != 6 {
		t.Fatalf("user input request fields = %v, want source shape", rawParams)
	}
	var params protocol.ToolRequestUserInputParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("decode request params: %v", err)
	}
	if !params.IsBlocking || params.ThreadID != "thread-1" || params.TurnID != "turn-1" ||
		params.ItemID == "" || params.AutoResolutionMS != nil || len(params.Questions) != 1 ||
		params.Questions[0].ID != "input" || params.Questions[0].Header != "Input" ||
		len(params.Questions[0].Options) != 2 || params.Questions[0].Options[0].Label != "one" {
		t.Fatalf("request params = %#v", params)
	}
	if err := server.HandleResponse(ctx, protocol.Response{
		ID:     req.ID,
		Result: json.RawMessage(`{"answers":{"input":{"answers":["one"]}}}`),
	}); err != nil {
		t.Fatalf("HandleResponse: %v", err)
	}

	got := <-resultCh
	if got.err != nil {
		t.Fatalf("interaction result error: %v", got.err)
	}
	if string(got.resp.Result) != `{"answers":{"input":{"answers":["one"]}}}` || got.resp.ThreadID != "thread-1" {
		t.Fatalf("interaction response = %#v", got.resp)
	}
	notification := waitForNotification(t, server, "serverRequest/resolved")
	var resolved serverRequestResolvedParams
	if err := json.Unmarshal(notification.Params, &resolved); err != nil {
		t.Fatalf("decode resolved notification: %v", err)
	}
	requestID, _ := req.ID.Value().(string)
	if resolved.RequestID.Value() != requestID || resolved.ThreadID != "thread-1" {
		t.Fatalf("resolved params = %#v, requestID=%q", resolved, requestID)
	}
}

func TestInteractionUserInputResponseUsesSourceCompatibleContract(t *testing.T) {
	tests := []struct {
		name     string
		result   string
		wantFail bool
	}{
		{name: "answer", result: `{"answers":{"question-1":{"answers":["safe"]}}}`},
		{name: "empty answer map", result: `{"answers":{}}`},
		{name: "missing answers", result: `{}`, wantFail: true},
		{name: "null answers", result: `{"answers":null}`, wantFail: true},
		{name: "null answer list", result: `{"answers":{"question-1":{"answers":null}}}`, wantFail: true},
		{name: "unknown answer field", result: `{"answers":{"question-1":{"answers":[],"extra":true}}}`},
		{name: "unknown response field", result: `{"answers":{},"extra":true}`},
		{
			name:     "oversized",
			result:   `{"answers":{"question-1":{"answers":["` + strings.Repeat("x", runtimeInteractionPayloadMaxBytes) + `"]}}}`,
			wantFail: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := readyServer()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			resultCh := make(chan interactionResult, 1)
			go func() {
				resp, err := server.interact.RequestUserInput(ctx, UserInputRequest{
					ThreadID:   "thread-contract",
					TurnID:     "turn-contract",
					ItemID:     "item-contract",
					QuestionID: "question-1",
					Prompt:     "Choose",
				})
				resultCh <- interactionResult{resp: resp, err: err}
			}()
			req := waitForServerRequest(t, server)
			err := server.HandleResponse(ctx, protocol.Response{ID: req.ID, Result: json.RawMessage(tt.result)})
			got := <-resultCh
			if tt.wantFail {
				if err == nil || !errors.Is(got.err, ErrInteractionRequestFailed) || got.resp.Error == nil {
					t.Fatalf("HandleResponse error = %v, interaction = %#v/%v", err, got.resp, got.err)
				}
				return
			}
			if err != nil || got.err != nil || string(got.resp.Result) != tt.result {
				t.Fatalf("HandleResponse error = %v, interaction = %#v/%v", err, got.resp, got.err)
			}
		})
	}
}

func TestInteractionToolCallErrorResponse(t *testing.T) {
	server := readyServer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resultCh := make(chan interactionResult, 1)
	go func() {
		resp, err := server.interact.RequestToolCall(ctx, DynamicToolCallRequest{
			ThreadID:  "thread-2",
			TurnID:    "turn-2",
			CallID:    "call-2",
			ToolName:  "client.search",
			Arguments: json.RawMessage(`{"query":"gollem"}`),
		})
		resultCh <- interactionResult{resp: resp, err: err}
	}()

	req := waitForServerRequest(t, server)
	if req.Method != InteractionToolCall {
		t.Fatalf("request method = %q, want %s", req.Method, InteractionToolCall)
	}
	var params protocol.DynamicToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("decode dynamic tool params: %v", err)
	}
	if params.ThreadID != "thread-2" || params.TurnID != "turn-2" || params.CallID != "call-2" ||
		params.Namespace != nil || params.Tool != "client.search" {
		t.Fatalf("dynamic tool params = %#v", params)
	}
	var rawParams map[string]json.RawMessage
	if err := json.Unmarshal(req.Params, &rawParams); err != nil {
		t.Fatalf("decode raw dynamic tool params: %v", err)
	}
	for _, forbidden := range []string{"requestId", "itemId", "startedAtMs", "toolName", "name", "metadata", "reason"} {
		if _, exists := rawParams[forbidden]; exists {
			t.Fatalf("dynamic tool params leak %s: %s", forbidden, req.Params)
		}
	}
	if err := server.HandleResponse(ctx, protocol.Response{
		ID:    req.ID,
		Error: &protocol.Error{Code: protocol.CodeInvalidRequest, Message: "tool refused"},
	}); err != nil {
		t.Fatalf("HandleResponse: %v", err)
	}
	got := <-resultCh
	if !errors.Is(got.err, ErrInteractionRequestFailed) {
		t.Fatalf("interaction error = %v, want ErrInteractionRequestFailed", got.err)
	}
	if got.resp.Error == nil || got.resp.Error.Message != "tool refused" {
		t.Fatalf("interaction response = %#v", got.resp)
	}
}

func TestInteractionToolCallResponseUsesExactBoundedContract(t *testing.T) {
	tests := []struct {
		name     string
		result   string
		wantFail bool
	}{
		{
			name:   "text image and audio",
			result: `{"contentItems":[{"type":"inputText","text":"match"},{"type":"inputImage","imageUrl":"data:image/png;base64,AA=="},{"type":"inputAudio","audioUrl":"data:audio/wav;base64,AA=="}],"success":true}`,
		},
		{name: "missing fields", result: `{}`, wantFail: true},
		{name: "null content", result: `{"contentItems":null,"success":true}`, wantFail: true},
		{name: "invalid variant", result: `{"contentItems":[{"type":"inputText","imageUrl":"bad"}],"success":true}`, wantFail: true},
		{name: "invalid success", result: `{"contentItems":[],"success":"yes"}`, wantFail: true},
		{
			name:     "oversized",
			result:   `{"contentItems":[{"type":"inputText","text":"` + strings.Repeat("x", runtimeInteractionPayloadMaxBytes) + `"}],"success":true}`,
			wantFail: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := readyServer()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			resultCh := make(chan interactionResult, 1)
			go func() {
				resp, err := server.interact.RequestToolCall(ctx, DynamicToolCallRequest{
					ThreadID:  "thread-contract",
					TurnID:    "turn-contract",
					ItemID:    "item-contract",
					ToolName:  "client.search",
					Arguments: json.RawMessage(`{"query":"gollem"}`),
				})
				resultCh <- interactionResult{resp: resp, err: err}
			}()
			req := waitForServerRequest(t, server)
			var params protocol.DynamicToolCallParams
			if err := json.Unmarshal(req.Params, &params); err != nil || params.CallID != "item-contract" {
				t.Fatalf("dynamic tool fallback params = %#v, error %v", params, err)
			}
			err := server.HandleResponse(ctx, protocol.Response{ID: req.ID, Result: json.RawMessage(tt.result)})
			got := <-resultCh
			if tt.wantFail {
				if err == nil || !errors.Is(got.err, ErrInteractionRequestFailed) || got.resp.Error == nil {
					t.Fatalf("HandleResponse error = %v, interaction = %#v/%v", err, got.resp, got.err)
				}
				return
			}
			if err != nil || got.err != nil || string(got.resp.Result) != tt.result {
				t.Fatalf("HandleResponse error = %v, interaction = %#v/%v", err, got.resp, got.err)
			}
		})
	}
}

func TestInteractionMCPResponseUsesSharedPayloadBound(t *testing.T) {
	if err := validateInteractionResult(InteractionMCPElicitation, json.RawMessage(`{"action":"accept","content":{"choice":"safe"},"_meta":null}`)); err != nil {
		t.Fatalf("valid MCP response: %v", err)
	}
	result := json.RawMessage(`{"value":"` + strings.Repeat("x", runtimeInteractionPayloadMaxBytes) + `"}`)
	if err := validateInteractionResult(InteractionMCPElicitation, result); err == nil {
		t.Fatal("oversized MCP response succeeded")
	}
}

func TestInteractionMCPElicitationRequestUsesExactCanonicalForm(t *testing.T) {
	server := readyServer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resultCh := make(chan interactionResult, 1)
	go func() {
		resp, err := server.interact.RequestMCPElicitation(ctx, MCPElicitationRequest{
			ThreadID: "thread-mcp",
			TurnID:   "turn-mcp",
			ItemID:   "item-mcp",
			ServerID: "repo",
			Message:  "Choose access",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scopes": map[string]any{
						"type": "array",
						"items": map[string]any{
							"oneOf": []any{map[string]any{"const": "read", "title": "Read"}},
						},
					},
				},
			},
			Metadata: map[string]any{"source": "runtime"},
		})
		resultCh <- interactionResult{resp: resp, err: err}
	}()

	req := waitForServerRequest(t, server)
	var params protocol.McpServerElicitationRequestParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("decode MCP elicitation params: %v", err)
	}
	if params.ThreadID != "thread-mcp" || params.TurnID == nil || *params.TurnID != "turn-mcp" ||
		params.ServerName != "repo" || params.Mode != protocol.McpServerElicitationModeForm ||
		params.Message != "Choose access" ||
		!strings.Contains(string(params.Meta), `"source":"runtime"`) ||
		!strings.Contains(string(params.RequestedSchema), `"items":{"anyOf"`) {
		t.Fatalf("MCP elicitation params = %#v, schema=%s", params, params.RequestedSchema)
	}
	if err := server.HandleResponse(ctx, protocol.Response{
		ID:     req.ID,
		Result: json.RawMessage(`{"action":"cancel","content":null,"_meta":null}`),
	}); err != nil {
		t.Fatalf("HandleResponse: %v", err)
	}
	if got := <-resultCh; got.err != nil {
		t.Fatalf("MCP elicitation result: %v", got.err)
	}

	if _, err := server.interact.RequestMCPElicitation(ctx, MCPElicitationRequest{
		ThreadID: "thread-mcp",
		ServerID: "repo",
		Message:  "Invalid",
		Schema:   map[string]any{"type": "array"},
	}); err == nil || !strings.Contains(err.Error(), "validate MCP elicitation schema") {
		t.Fatalf("invalid schema error = %v", err)
	}
	if _, err := server.interact.RequestMCPElicitation(ctx, MCPElicitationRequest{
		ThreadID: "thread-mcp",
		ServerID: "repo",
		Message:  "Oversized",
		Metadata: map[string]any{"value": strings.Repeat("x", runtimeInteractionPayloadMaxBytes)},
	}); err == nil || !strings.Contains(err.Error(), "MCP elicitation request exceeds") {
		t.Fatalf("oversized request error = %v", err)
	}
}

func TestInteractionMCPElicitationResponseMatchesSourceSerde(t *testing.T) {
	tests := []struct {
		name     string
		result   string
		wantFail bool
	}{
		{name: "accept", result: `{"action":"accept","content":{"choice":"safe"},"_meta":null}`},
		{name: "decline", result: `{"action":"decline","content":null,"_meta":{"reason":"policy"}}`},
		{name: "cancel", result: `{"action":"cancel","content":null,"_meta":null}`},
		{name: "missing action", result: `{"content":null,"_meta":null}`, wantFail: true},
		{name: "missing content", result: `{"action":"accept","_meta":null}`},
		{name: "missing meta", result: `{"action":"accept","content":null}`},
		{name: "invalid action", result: `{"action":"approve","content":null,"_meta":null}`, wantFail: true},
		{name: "unknown field", result: `{"action":"cancel","content":null,"_meta":null,"extra":true}`},
		{
			name:     "oversized",
			result:   `{"action":"accept","content":{"value":"` + strings.Repeat("x", runtimeInteractionPayloadMaxBytes) + `"},"_meta":null}`,
			wantFail: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := readyServer()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			resultCh := make(chan interactionResult, 1)
			go func() {
				resp, err := server.interact.RequestMCPElicitation(ctx, MCPElicitationRequest{
					ThreadID: "thread-mcp-contract",
					ServerID: "repo",
					Message:  "Choose",
				})
				resultCh <- interactionResult{resp: resp, err: err}
			}()
			req := waitForServerRequest(t, server)
			err := server.HandleResponse(ctx, protocol.Response{ID: req.ID, Result: json.RawMessage(tt.result)})
			got := <-resultCh
			if tt.wantFail {
				if err == nil || !errors.Is(got.err, ErrInteractionRequestFailed) || got.resp.Error == nil {
					t.Fatalf("HandleResponse error = %v, interaction = %#v/%v", err, got.resp, got.err)
				}
				return
			}
			if err != nil || got.err != nil || string(got.resp.Result) != tt.result {
				t.Fatalf("HandleResponse error = %v, interaction = %#v/%v", err, got.resp, got.err)
			}
		})
	}
}

func TestServeJSONLinesResolvesInteractionResponse(t *testing.T) {
	server := NewServer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		err := ServeJSONLines(ctx, server, inR, outW)
		if err != nil {
			_ = outW.CloseWithError(err)
		} else {
			_ = outW.Close()
		}
		errCh <- err
	}()
	scanner := bufio.NewScanner(outR)
	writeInputLine(t, inW, `{"id":"init","method":"initialize","params":{"clientInfo":{"name":"interaction-jsonl","version":"1.0.0"}}}`)
	var initResp protocol.Response
	if err := json.Unmarshal([]byte(readOutputLine(t, scanner)), &initResp); err != nil {
		t.Fatalf("decode init response: %v", err)
	}
	if initResp.Error != nil {
		t.Fatalf("initialize error: %v", initResp.Error)
	}
	writeInputLine(t, inW, `{"method":"initialized"}`)

	resultCh := make(chan interactionResult, 1)
	go func() {
		resp, err := server.interact.RequestMCPElicitation(ctx, MCPElicitationRequest{
			ThreadID: "thread-3",
			TurnID:   "turn-3",
			ServerID: "mcp-test",
			Message:  "choose",
		})
		resultCh <- interactionResult{resp: resp, err: err}
	}()

	var serverReq protocol.Request
	if err := json.Unmarshal([]byte(readOutputLine(t, scanner)), &serverReq); err != nil {
		t.Fatalf("decode server request: %v", err)
	}
	if serverReq.Method != InteractionMCPElicitation {
		t.Fatalf("server request method = %q, want %s", serverReq.Method, InteractionMCPElicitation)
	}
	var params protocol.McpServerElicitationRequestParams
	if err := json.Unmarshal(serverReq.Params, &params); err != nil {
		t.Fatalf("decode MCP elicitation params: %v", err)
	}
	if params.ThreadID != "thread-3" || params.TurnID == nil || *params.TurnID != "turn-3" ||
		params.ServerName != "mcp-test" || params.Mode != protocol.McpServerElicitationModeForm ||
		params.Message != "choose" {
		t.Fatalf("MCP elicitation params = %#v", params)
	}
	requestID, _ := serverReq.ID.Value().(string)
	writeInputLine(t, inW, `{"id":"`+requestID+`","result":{"action":"accept","content":{"choice":"safe"},"_meta":null}}`)
	resolvedLine := readOutputLine(t, scanner)
	var resolved protocol.Notification
	if err := json.Unmarshal([]byte(resolvedLine), &resolved); err != nil {
		t.Fatalf("decode resolved notification: %v", err)
	}
	if resolved.Method != "serverRequest/resolved" {
		t.Fatalf("resolved method = %q", resolved.Method)
	}
	got := <-resultCh
	if got.err != nil {
		t.Fatalf("interaction result error: %v", got.err)
	}
	if strings.TrimSpace(string(got.resp.Result)) != `{"action":"accept","content":{"choice":"safe"},"_meta":null}` {
		t.Fatalf("interaction result = %s", got.resp.Result)
	}
	if err := inW.Close(); err != nil {
		t.Fatalf("close input: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("ServeJSONLines: %v", err)
	}
}

func TestServeJSONLinesResolvesExactDynamicToolCall(t *testing.T) {
	server := NewServer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		err := ServeJSONLines(ctx, server, inR, outW)
		if err != nil {
			_ = outW.CloseWithError(err)
		} else {
			_ = outW.Close()
		}
		errCh <- err
	}()
	scanner := bufio.NewScanner(outR)
	writeInputLine(t, inW, `{"id":"init","method":"initialize","params":{"clientInfo":{"name":"dynamic-tool-jsonl","version":"1.0.0"}}}`)
	var initResp protocol.Response
	if err := json.Unmarshal([]byte(readOutputLine(t, scanner)), &initResp); err != nil || initResp.Error != nil {
		t.Fatalf("initialize response = %#v, error %v", initResp, err)
	}
	writeInputLine(t, inW, `{"method":"initialized"}`)

	resultCh := make(chan interactionResult, 1)
	go func() {
		resp, err := server.interact.RequestToolCall(ctx, DynamicToolCallRequest{
			ThreadID:  "thread-jsonl",
			TurnID:    "turn-jsonl",
			ItemID:    "item-jsonl",
			CallID:    "call-jsonl",
			ToolName:  "client.search",
			Arguments: json.RawMessage(`{"query":"gollem"}`),
		})
		resultCh <- interactionResult{resp: resp, err: err}
	}()

	var serverReq protocol.Request
	if err := json.Unmarshal([]byte(readOutputLine(t, scanner)), &serverReq); err != nil {
		t.Fatalf("decode server request: %v", err)
	}
	var params protocol.DynamicToolCallParams
	if err := json.Unmarshal(serverReq.Params, &params); err != nil {
		t.Fatalf("decode dynamic tool params: %v", err)
	}
	if serverReq.Method != InteractionToolCall || params.CallID != "call-jsonl" || params.Namespace != nil ||
		params.Tool != "client.search" || params.ThreadID != "thread-jsonl" || params.TurnID != "turn-jsonl" {
		t.Fatalf("dynamic tool request = %s/%#v", serverReq.Method, params)
	}
	responseLine, err := json.Marshal(protocol.Response{
		ID: serverReq.ID,
		Result: mustInteractionJSON(t, protocol.DynamicToolCallResponse{
			ContentItems: []protocol.DynamicToolCallOutputContentItem{{Type: "inputText", Text: "match"}},
			Success:      true,
		}),
	})
	if err != nil {
		t.Fatalf("encode response: %v", err)
	}
	writeInputLine(t, inW, string(responseLine))
	var resolved protocol.Notification
	if err := json.Unmarshal([]byte(readOutputLine(t, scanner)), &resolved); err != nil || resolved.Method != "serverRequest/resolved" {
		t.Fatalf("resolved notification = %#v, error %v", resolved, err)
	}
	got := <-resultCh
	if got.err != nil {
		t.Fatalf("dynamic tool result: %v", got.err)
	}
	var response protocol.DynamicToolCallResponse
	if err := json.Unmarshal(got.resp.Result, &response); err != nil || !response.Success || len(response.ContentItems) != 1 {
		t.Fatalf("dynamic tool response = %#v, error %v", response, err)
	}
	if err := inW.Close(); err != nil {
		t.Fatalf("close input: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("ServeJSONLines: %v", err)
	}
}

func TestServeJSONLinesResolvesExactUserInput(t *testing.T) {
	server := NewServer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		err := ServeJSONLines(ctx, server, inR, outW)
		if err != nil {
			_ = outW.CloseWithError(err)
		} else {
			_ = outW.Close()
		}
		errCh <- err
	}()
	scanner := bufio.NewScanner(outR)
	writeInputLine(t, inW, `{"id":"init","method":"initialize","params":{"clientInfo":{"name":"user-input-jsonl","version":"1.0.0"}}}`)
	var initResp protocol.Response
	if err := json.Unmarshal([]byte(readOutputLine(t, scanner)), &initResp); err != nil || initResp.Error != nil {
		t.Fatalf("initialize response = %#v, error %v", initResp, err)
	}
	writeInputLine(t, inW, `{"method":"initialized"}`)

	autoResolutionMS := uint64(1500)
	resultCh := make(chan interactionResult, 1)
	go func() {
		resp, err := server.interact.RequestUserInput(ctx, UserInputRequest{
			ThreadID:         "thread-input-jsonl",
			TurnID:           "turn-input-jsonl",
			ItemID:           "item-input-jsonl",
			QuestionID:       "question-input-jsonl",
			Header:           "Target",
			Prompt:           "Choose a target",
			Placeholder:      "target name",
			Required:         true,
			IsOther:          true,
			IsSecret:         false,
			Options:          []string{"staging", "production"},
			AutoResolutionMS: &autoResolutionMS,
		})
		resultCh <- interactionResult{resp: resp, err: err}
	}()

	var serverReq protocol.Request
	if err := json.Unmarshal([]byte(readOutputLine(t, scanner)), &serverReq); err != nil {
		t.Fatalf("decode server request: %v", err)
	}
	var params protocol.ToolRequestUserInputParams
	if err := json.Unmarshal(serverReq.Params, &params); err != nil {
		t.Fatalf("decode user input params: %v", err)
	}
	if serverReq.Method != InteractionRequestUserInput || params.ThreadID != "thread-input-jsonl" ||
		params.TurnID != "turn-input-jsonl" || params.ItemID != "item-input-jsonl" ||
		params.AutoResolutionMS == nil || *params.AutoResolutionMS != autoResolutionMS || len(params.Questions) != 1 {
		t.Fatalf("user input request = %s/%#v", serverReq.Method, params)
	}
	question := params.Questions[0]
	if question.ID != "question-input-jsonl" || question.Header != "Target" || question.Question != "Choose a target" ||
		!question.IsOther || question.IsSecret || len(question.Options) != 2 || question.Options[0].Label != "staging" ||
		question.Options[0].Description != "" || !params.IsBlocking {
		t.Fatalf("user input question = %#v, params = %#v", question, params)
	}
	responseLine, err := json.Marshal(protocol.Response{
		ID: serverReq.ID,
		Result: mustInteractionJSON(t, protocol.ToolRequestUserInputResponse{
			Answers: map[string]protocol.ToolRequestUserInputAnswer{
				"question-input-jsonl": {Answers: []string{"production"}},
			},
		}),
	})
	if err != nil {
		t.Fatalf("encode response: %v", err)
	}
	writeInputLine(t, inW, string(responseLine))
	var resolved protocol.Notification
	if err := json.Unmarshal([]byte(readOutputLine(t, scanner)), &resolved); err != nil || resolved.Method != "serverRequest/resolved" {
		t.Fatalf("resolved notification = %#v, error %v", resolved, err)
	}
	got := <-resultCh
	if got.err != nil {
		t.Fatalf("user input result: %v", got.err)
	}
	var response protocol.ToolRequestUserInputResponse
	if err := json.Unmarshal(got.resp.Result, &response); err != nil ||
		len(response.Answers["question-input-jsonl"].Answers) != 1 ||
		response.Answers["question-input-jsonl"].Answers[0] != "production" {
		t.Fatalf("user input response = %#v, error %v", response, err)
	}
	if err := inW.Close(); err != nil {
		t.Fatalf("close input: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("ServeJSONLines: %v", err)
	}
}

func TestToolListIncludesInteractions(t *testing.T) {
	server := readyServer()
	resp := server.HandleRequest(context.Background(), request("tool/list", map[string]any{"includeUnavailable": true}))
	if resp.Error != nil {
		t.Fatalf("tool/list error: %v", resp.Error)
	}
	var tools catalog.ToolListResponse
	if err := json.Unmarshal(resp.Result, &tools); err != nil {
		t.Fatalf("decode tool/list: %v", err)
	}
	for _, tool := range tools.Data {
		if tool.ID != "interactions" {
			continue
		}
		for _, method := range tool.Methods {
			if method == InteractionCurrentTimeRead {
				return
			}
		}
		t.Fatalf("interactions tool missing %q: %#v", InteractionCurrentTimeRead, tool)
	}
	t.Fatalf("tool/list missing interactions tool: %#v", tools.Data)
}

type interactionResult struct {
	resp InteractionResponse
	err  error
}

func mustInteractionJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return data
}
