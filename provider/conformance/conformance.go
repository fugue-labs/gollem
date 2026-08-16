// Package conformance provides provider-neutral checks for model-driver tests.
//
// Drivers supply their own deterministic protocol fixture and then pass the
// resulting core.Model to Verify. This keeps wire-format details with each
// adapter while making capability claims observable at Gollem's model boundary.
package conformance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/fugue-labs/gollem/modelutil"
)

// Claims are the capabilities covered by a deterministic conformance fixture.
// A driver must not advertise one of these capabilities in Slang unless it has
// a matching deterministic fixture and this verification passes.
type Claims struct {
	ToolCalls             bool
	ToolSearch            bool
	StructuredOutput      bool
	Vision                bool
	CacheReadUsage        bool
	PromptCacheActivation bool
	Streaming             bool
	Usage                 bool
	Cancellation          bool
	PartialStream         bool
	MalformedStream       bool
	DisconnectStream      bool
	Retryability          bool
	RequestTimeout        bool
	StreamTimeout         bool
	ReasoningVisibility   bool
}

// Expectations declares the normalized outputs a deterministic fixture
// produces. ToolName, ToolCallID, and ToolArgumentsJSON are required when
// Claims.ToolCalls is true. ToolSearchText is required when Claims.ToolSearch
// is true. StructuredOutputValue is required when
// Claims.StructuredOutput is true. VisionText is required when Claims.Vision
// is true. CacheReadTokens is required when Claims.CacheReadUsage is true.
// StreamText is required when
// Claims.Streaming is true.
type Expectations struct {
	ResponseText          string
	ToolName              string
	ToolCallID            string
	ToolArgumentsJSON     string
	ToolSearchText        string
	StructuredOutputValue string
	VisionText            string
	CacheReadTokens       int
	StreamText            string
	PartialText           string
	DisconnectText        string
	RetryText             string
	StreamTimeoutText     string
	ReasoningText         string
}

// Driver binds a provider model to the common capability claims and expected
// normalized results that its deterministic fixture produces.
type Driver struct {
	Name                  string
	Model                 core.Model
	Claims                Claims
	Expectations          Expectations
	CancellationReady     <-chan struct{}
	RequestTimeoutReady   <-chan struct{}
	ReasoningModel        core.Model
	ToolSearchModel       core.Model
	PromptCacheActivation func() error
	ToolSearchActivation  func() error
}

// Verify exercises the claimed common model surface through core.Model. It is
// intentionally independent of a provider's HTTP or RPC wire format.
func Verify(ctx context.Context, driver Driver) error {
	if strings.TrimSpace(driver.Name) == "" {
		return errors.New("provider conformance: driver name is required")
	}
	if driver.Model == nil {
		return fmt.Errorf("provider conformance: %s model is required", driver.Name)
	}
	if driver.Claims.ToolCalls && strings.TrimSpace(driver.Expectations.ToolName) == "" {
		return fmt.Errorf("provider conformance: %s tool-capable fixture must expect a tool call", driver.Name)
	}
	if driver.Claims.ToolCalls && strings.TrimSpace(driver.Expectations.ToolCallID) == "" {
		return fmt.Errorf("provider conformance: %s tool-capable fixture must expect a tool call ID", driver.Name)
	}
	if driver.Claims.ToolCalls && strings.TrimSpace(driver.Expectations.ToolArgumentsJSON) == "" {
		return fmt.Errorf("provider conformance: %s tool-capable fixture must expect tool arguments", driver.Name)
	}
	if driver.Claims.ToolSearch && driver.ToolSearchModel == nil {
		return fmt.Errorf("provider conformance: %s tool-search-capable fixture must supply a tool-search model", driver.Name)
	}
	if driver.Claims.ToolSearch && strings.TrimSpace(driver.Expectations.ToolSearchText) == "" {
		return fmt.Errorf("provider conformance: %s tool-search fixture must expect a response", driver.Name)
	}
	if driver.Claims.ToolSearch && driver.ToolSearchActivation == nil {
		return fmt.Errorf("provider conformance: %s tool-search fixture must observe deferred-tool activation", driver.Name)
	}
	if driver.Claims.StructuredOutput && strings.TrimSpace(driver.Expectations.StructuredOutputValue) == "" {
		return fmt.Errorf("provider conformance: %s structured-output fixture must expect a typed value", driver.Name)
	}
	if driver.Claims.Vision && strings.TrimSpace(driver.Expectations.VisionText) == "" {
		return fmt.Errorf("provider conformance: %s vision fixture must expect a response", driver.Name)
	}
	if driver.Claims.CacheReadUsage && driver.Expectations.CacheReadTokens <= 0 {
		return fmt.Errorf("provider conformance: %s cache-read fixture must expect positive cache-read tokens", driver.Name)
	}
	if driver.Claims.PromptCacheActivation && driver.PromptCacheActivation == nil {
		return fmt.Errorf("provider conformance: %s prompt-cache fixture must observe request activation", driver.Name)
	}
	if driver.Claims.Streaming && strings.TrimSpace(driver.Expectations.StreamText) == "" {
		return fmt.Errorf("provider conformance: %s streaming fixture must expect stream text", driver.Name)
	}
	if driver.Claims.Cancellation && driver.CancellationReady == nil {
		return fmt.Errorf("provider conformance: %s cancellation-capable fixture must signal request start", driver.Name)
	}
	if driver.Claims.PartialStream && strings.TrimSpace(driver.Expectations.PartialText) == "" {
		return fmt.Errorf("provider conformance: %s partial-stream fixture must expect partial text", driver.Name)
	}
	if driver.Claims.DisconnectStream && strings.TrimSpace(driver.Expectations.DisconnectText) == "" {
		return fmt.Errorf("provider conformance: %s disconnect-stream fixture must expect partial text", driver.Name)
	}
	if driver.Claims.Retryability && strings.TrimSpace(driver.Expectations.RetryText) == "" {
		return fmt.Errorf("provider conformance: %s retry fixture must expect retry text", driver.Name)
	}
	if driver.Claims.RequestTimeout && driver.RequestTimeoutReady == nil {
		return fmt.Errorf("provider conformance: %s timeout-capable fixture must signal request start", driver.Name)
	}
	if driver.Claims.StreamTimeout && strings.TrimSpace(driver.Expectations.StreamTimeoutText) == "" {
		return fmt.Errorf("provider conformance: %s stream-timeout fixture must expect partial text", driver.Name)
	}
	if driver.Claims.ReasoningVisibility && driver.ReasoningModel == nil {
		return fmt.Errorf("provider conformance: %s reasoning-capable fixture must supply a reasoning model", driver.Name)
	}
	if driver.Claims.ReasoningVisibility && strings.TrimSpace(driver.Expectations.ReasoningText) == "" {
		return fmt.Errorf("provider conformance: %s reasoning fixture must expect reasoning text", driver.Name)
	}

	params := &core.ModelRequestParameters{AllowTextOutput: true}
	if driver.Claims.ToolCalls {
		params.FunctionTools = []core.ToolDefinition{{
			Name:        "conformance_echo",
			Description: "Return the supplied value for deterministic driver testing.",
			ParametersSchema: core.Schema{
				"type": "object",
				"properties": map[string]any{
					"value": core.Schema{"type": "string"},
				},
				"required": []string{"value"},
			},
		}}
	}
	promptCacheEnabled := true
	promptCacheSettings := &core.ModelSettings{PromptCacheEnabled: &promptCacheEnabled}
	response, err := driver.Model.Request(ctx, conformanceMessages(), promptCacheSettings, params)
	if err != nil {
		return fmt.Errorf("provider conformance: %s request: %w", driver.Name, err)
	}
	if response == nil {
		return fmt.Errorf("provider conformance: %s request returned a nil response", driver.Name)
	}
	if err := verifyResponse(driver, response, false); err != nil {
		return err
	}
	if driver.Claims.CacheReadUsage {
		if err := verifyCacheReadUsage(driver, response); err != nil {
			return err
		}
	}
	if driver.Claims.StructuredOutput {
		if err := verifyStructuredOutput(ctx, driver); err != nil {
			return err
		}
	}
	if driver.Claims.Vision {
		if err := verifyVision(ctx, driver); err != nil {
			return err
		}
	}
	if driver.Claims.ToolSearch {
		if err := verifyToolSearch(ctx, driver); err != nil {
			return err
		}
	}
	if driver.Claims.Cancellation {
		if err := verifyCancellation(ctx, driver); err != nil {
			return err
		}
	}
	if driver.Claims.RequestTimeout {
		if err := verifyRequestTimeout(ctx, driver); err != nil {
			return err
		}
	}
	if driver.Claims.StreamTimeout {
		if err := verifyStreamTimeout(ctx, driver); err != nil {
			return err
		}
	}
	if driver.Claims.Retryability {
		if err := verifyRetryability(ctx, driver); err != nil {
			return err
		}
	}
	if driver.Claims.ReasoningVisibility {
		if err := verifyReasoningVisibility(ctx, driver); err != nil {
			return err
		}
	}

	if !driver.Claims.Streaming {
		return nil
	}
	stream, err := driver.Model.RequestStream(ctx, conformanceMessages(), promptCacheSettings, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		return fmt.Errorf("provider conformance: %s stream request: %w", driver.Name, err)
	}
	defer stream.Close()
	for {
		_, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("provider conformance: %s stream: %w", driver.Name, err)
		}
	}
	if err := verifyResponse(driver, stream.Response(), true); err != nil {
		return err
	}
	if driver.Claims.PromptCacheActivation {
		if err := driver.PromptCacheActivation(); err != nil {
			return fmt.Errorf("provider conformance: %s prompt-cache activation: %w", driver.Name, err)
		}
	}
	if driver.Claims.PartialStream {
		if err := verifyPartialStream(ctx, driver); err != nil {
			return err
		}
	}
	if driver.Claims.MalformedStream {
		if err := verifyMalformedStream(ctx, driver); err != nil {
			return err
		}
	}
	if driver.Claims.DisconnectStream {
		if err := verifyDisconnectStream(ctx, driver); err != nil {
			return err
		}
	}
	return nil
}

type structuredOutput struct {
	Value string `json:"value"`
}

// verifyStructuredOutput exercises the public typed-agent path. Native mode
// permits a provider to return schema-constrained JSON text or the framework's
// schema-backed final_result tool call, but both must produce the same typed
// output at the core.Model boundary.
func verifyStructuredOutput(ctx context.Context, driver Driver) error {
	agent := core.NewAgent[structuredOutput](
		driver.Model,
		core.WithOutputOptions[structuredOutput](core.WithOutputMode(core.OutputModeNative)),
	)
	result, err := agent.Run(ctx, "structured output conformance")
	if err != nil {
		return fmt.Errorf("provider conformance: %s structured output: %w", driver.Name, err)
	}
	if result == nil {
		return fmt.Errorf("provider conformance: %s structured output returned a nil result", driver.Name)
	}
	if got := result.Output.Value; got != driver.Expectations.StructuredOutputValue {
		return fmt.Errorf(
			"provider conformance: %s structured output value = %q, want %q",
			driver.Name,
			got,
			driver.Expectations.StructuredOutputValue,
		)
	}
	return nil
}

// verifyVision sends a small deterministic PNG data URI through the public
// core.Model request path. Provider fixtures assert their native wire shape.
func verifyVision(ctx context.Context, driver Driver) error {
	response, err := driver.Model.Request(ctx, visionMessages(), nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		return fmt.Errorf("provider conformance: %s vision request: %w", driver.Name, err)
	}
	if response == nil {
		return fmt.Errorf("provider conformance: %s vision request returned a nil response", driver.Name)
	}
	if got := response.TextContent(); got != driver.Expectations.VisionText {
		return fmt.Errorf("provider conformance: %s vision text = %q, want %q", driver.Name, got, driver.Expectations.VisionText)
	}
	return nil
}

// verifyToolSearch exercises deferred tool definitions through the public
// model boundary. The driver-owned fixture then verifies its native search
// primitive and per-tool defer-loading representation.
func verifyToolSearch(ctx context.Context, driver Driver) error {
	response, err := driver.ToolSearchModel.Request(ctx, toolSearchMessages(), nil, &core.ModelRequestParameters{
		AllowTextOutput: true,
		FunctionTools: []core.ToolDefinition{{
			Name:             "conformance_deferred",
			Description:      "A deferred tool used only for provider conformance.",
			ParametersSchema: core.Schema{"type": "object"},
			DeferLoading:     true,
		}},
	})
	if err != nil {
		return fmt.Errorf("provider conformance: %s tool-search request: %w", driver.Name, err)
	}
	if response == nil {
		return fmt.Errorf("provider conformance: %s tool-search request returned a nil response", driver.Name)
	}
	if got := response.TextContent(); got != driver.Expectations.ToolSearchText {
		return fmt.Errorf("provider conformance: %s tool-search text = %q, want %q", driver.Name, got, driver.Expectations.ToolSearchText)
	}
	if err := driver.ToolSearchActivation(); err != nil {
		return fmt.Errorf("provider conformance: %s tool-search activation: %w", driver.Name, err)
	}
	return nil
}

// verifyCacheReadUsage protects the provider-neutral accounting surface used
// by cost tracking and quota reporting. It deliberately does not claim that
// prompt-cache activation is portable across provider request APIs.
func verifyCacheReadUsage(driver Driver, response *core.ModelResponse) error {
	if got := response.Usage.CacheReadTokens; got != driver.Expectations.CacheReadTokens {
		return fmt.Errorf(
			"provider conformance: %s cache-read tokens = %d, want %d",
			driver.Name,
			got,
			driver.Expectations.CacheReadTokens,
		)
	}
	return nil
}

func verifyCancellation(ctx context.Context, driver Driver) error {
	requestContext, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := driver.Model.Request(requestContext, cancellationMessages(), nil, &core.ModelRequestParameters{AllowTextOutput: true})
		result <- err
	}()

	select {
	case <-driver.CancellationReady:
		cancel()
	case <-ctx.Done():
		return fmt.Errorf("provider conformance: %s cancellation request did not start: %w", driver.Name, ctx.Err())
	case <-time.After(time.Second):
		return fmt.Errorf("provider conformance: %s cancellation request did not start", driver.Name)
	}

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			return fmt.Errorf("provider conformance: %s cancellation error, want context canceled: %w", driver.Name, err)
		}
		return nil
	case <-time.After(time.Second):
		return fmt.Errorf("provider conformance: %s cancellation request did not finish", driver.Name)
	}
}

func verifyRequestTimeout(ctx context.Context, driver Driver) error {
	requestContext, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := driver.Model.Request(requestContext, timeoutMessages(), nil, &core.ModelRequestParameters{AllowTextOutput: true})
		result <- err
	}()

	select {
	case <-driver.RequestTimeoutReady:
	case <-ctx.Done():
		return fmt.Errorf("provider conformance: %s timeout request did not start: %w", driver.Name, ctx.Err())
	case <-time.After(time.Second):
		return fmt.Errorf("provider conformance: %s timeout request did not start", driver.Name)
	}

	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("provider conformance: %s timeout error, want context deadline exceeded: %w", driver.Name, err)
		}
		return nil
	case <-time.After(time.Second):
		return fmt.Errorf("provider conformance: %s timeout request did not finish", driver.Name)
	}
}

func verifyStreamTimeout(ctx context.Context, driver Driver) error {
	requestContext, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	stream, err := driver.Model.RequestStream(requestContext, streamTimeoutMessages(), nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		return fmt.Errorf("provider conformance: %s stream-timeout request: %w", driver.Name, err)
	}
	defer stream.Close()
	for {
		_, err := stream.Next()
		if err == nil {
			continue
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("provider conformance: %s stream-timeout error, want context deadline exceeded: %w", driver.Name, err)
		}
		break
	}
	if got := stream.Response().TextContent(); got != driver.Expectations.StreamTimeoutText {
		return fmt.Errorf("provider conformance: %s stream-timeout text = %q, want %q", driver.Name, got, driver.Expectations.StreamTimeoutText)
	}
	return nil
}

func verifyPartialStream(ctx context.Context, driver Driver) error {
	stream, err := driver.Model.RequestStream(ctx, partialStreamMessages(), nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		return fmt.Errorf("provider conformance: %s partial stream request: %w", driver.Name, err)
	}
	defer stream.Close()
	for {
		_, err := stream.Next()
		if err == nil {
			continue
		}
		var incomplete *core.StreamIncompleteError
		if !errors.As(err, &incomplete) {
			return fmt.Errorf("provider conformance: %s partial stream error = %w, want StreamIncompleteError", driver.Name, err)
		}
		break
	}
	if got := stream.Response().TextContent(); got != driver.Expectations.PartialText {
		return fmt.Errorf("provider conformance: %s partial text = %q, want %q", driver.Name, got, driver.Expectations.PartialText)
	}
	return nil
}

func verifyMalformedStream(ctx context.Context, driver Driver) error {
	stream, err := driver.Model.RequestStream(ctx, malformedStreamMessages(), nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		return fmt.Errorf("provider conformance: %s malformed stream request: %w", driver.Name, err)
	}
	defer stream.Close()
	for {
		_, err := stream.Next()
		if err == nil {
			continue
		}
		var protocol *core.StreamProtocolError
		if !errors.As(err, &protocol) {
			return fmt.Errorf("provider conformance: %s malformed stream error = %w, want StreamProtocolError", driver.Name, err)
		}
		return nil
	}
}

func verifyDisconnectStream(ctx context.Context, driver Driver) error {
	stream, err := driver.Model.RequestStream(ctx, disconnectStreamMessages(), nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		return fmt.Errorf("provider conformance: %s disconnect stream request: %w", driver.Name, err)
	}
	defer stream.Close()
	for {
		_, err := stream.Next()
		if err == nil {
			continue
		}
		var transport *core.StreamTransportError
		if !errors.As(err, &transport) {
			return fmt.Errorf("provider conformance: %s disconnect stream error = %w, want StreamTransportError", driver.Name, err)
		}
		break
	}
	if got := stream.Response().TextContent(); got != driver.Expectations.DisconnectText {
		return fmt.Errorf("provider conformance: %s disconnect text = %q, want %q", driver.Name, got, driver.Expectations.DisconnectText)
	}
	return nil
}

func verifyRetryability(ctx context.Context, driver Driver) error {
	retryingModel := modelutil.NewRetryModel(driver.Model, modelutil.RetryConfig{
		MaxRetries:     1,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
		BackoffFactor:  1,
		Jitter:         false,
		MinRemaining:   0,
	})
	response, err := retryingModel.Request(ctx, retryMessages(), nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		return fmt.Errorf("provider conformance: %s retry request: %w", driver.Name, err)
	}
	if response == nil {
		return fmt.Errorf("provider conformance: %s retry request returned a nil response", driver.Name)
	}
	if got := response.TextContent(); got != driver.Expectations.RetryText {
		return fmt.Errorf("provider conformance: %s retry text = %q, want %q", driver.Name, got, driver.Expectations.RetryText)
	}
	return nil
}

func verifyReasoningVisibility(ctx context.Context, driver Driver) error {
	stream, err := driver.ReasoningModel.RequestStream(ctx, reasoningMessages(), nil, &core.ModelRequestParameters{AllowTextOutput: true})
	if err != nil {
		return fmt.Errorf("provider conformance: %s reasoning stream request: %w", driver.Name, err)
	}
	defer stream.Close()

	var (
		started bool
		deltas  strings.Builder
	)
	for {
		event, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("provider conformance: %s reasoning stream: %w", driver.Name, err)
		}
		switch value := event.(type) {
		case core.PartStartEvent:
			if part, ok := value.Part.(core.ThinkingPart); ok {
				started = true
				deltas.WriteString(part.Content)
			}
		case core.PartDeltaEvent:
			if delta, ok := value.Delta.(core.ThinkingPartDelta); ok {
				deltas.WriteString(delta.ContentDelta)
			}
		}
	}
	if !started {
		return fmt.Errorf("provider conformance: %s reasoning stream did not emit a ThinkingPart start", driver.Name)
	}
	if got := deltas.String(); got != driver.Expectations.ReasoningText {
		return fmt.Errorf("provider conformance: %s reasoning deltas = %q, want %q", driver.Name, got, driver.Expectations.ReasoningText)
	}
	for _, part := range stream.Response().Parts {
		if thinking, ok := part.(core.ThinkingPart); ok && thinking.Content == driver.Expectations.ReasoningText {
			return nil
		}
	}
	return fmt.Errorf("provider conformance: %s final response did not retain reasoning text", driver.Name)
}

func verifyResponse(driver Driver, response *core.ModelResponse, streaming bool) error {
	if response == nil {
		return fmt.Errorf("provider conformance: %s returned a nil response", driver.Name)
	}
	wantText := driver.Expectations.ResponseText
	if streaming {
		wantText = driver.Expectations.StreamText
	}
	if got := response.TextContent(); got != wantText {
		return fmt.Errorf("provider conformance: %s text = %q, want %q", driver.Name, got, wantText)
	}
	if driver.Claims.ToolCalls && !streaming {
		if err := verifyToolCall(driver, response.Parts); err != nil {
			return err
		}
	}
	if driver.Claims.Usage && response.Usage.InputTokens+response.Usage.OutputTokens == 0 {
		return fmt.Errorf("provider conformance: %s response did not report usage", driver.Name)
	}
	return nil
}

func verifyToolCall(driver Driver, parts []core.ModelResponsePart) error {
	foundName := false
	foundCallID := false
	for _, part := range parts {
		call, ok := part.(core.ToolCallPart)
		if !ok || call.ToolName != driver.Expectations.ToolName {
			continue
		}
		foundName = true
		if call.ToolCallID != driver.Expectations.ToolCallID {
			continue
		}
		foundCallID = true
		if call.ArgsJSON == driver.Expectations.ToolArgumentsJSON {
			return nil
		}
	}
	if !foundName {
		return fmt.Errorf(
			"provider conformance: %s response did not contain tool %q",
			driver.Name,
			driver.Expectations.ToolName,
		)
	}
	if !foundCallID {
		return fmt.Errorf(
			"provider conformance: %s response did not contain tool %q call ID %q",
			driver.Name,
			driver.Expectations.ToolName,
			driver.Expectations.ToolCallID,
		)
	}
	return fmt.Errorf(
		"provider conformance: %s response did not contain tool %q arguments %q for call ID %q",
		driver.Name,
		driver.Expectations.ToolName,
		driver.Expectations.ToolArgumentsJSON,
		driver.Expectations.ToolCallID,
	)
}

func conformanceMessages() []core.ModelMessage {
	return []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.SystemPromptPart{Content: "stable conformance context"},
			core.UserPromptPart{Content: "run conformance"},
		}},
	}
}

func visionMessages() []core.ModelMessage {
	return []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{
			core.UserPromptPart{Content: "vision conformance"},
			core.ImagePart{URL: core.BinaryContent([]byte{1, 2, 3}, "image/png"), MIMEType: "image/png", Detail: "low"},
		}},
	}
}

func toolSearchMessages() []core.ModelMessage {
	return []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "tool search conformance"}}},
	}
}

func cancellationMessages() []core.ModelMessage {
	return []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "cancel conformance"}}},
	}
}

func partialStreamMessages() []core.ModelMessage {
	return []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "partial stream conformance"}}},
	}
}

func malformedStreamMessages() []core.ModelMessage {
	return []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "malformed stream conformance"}}},
	}
}

func disconnectStreamMessages() []core.ModelMessage {
	return []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "disconnect stream conformance"}}},
	}
}

func retryMessages() []core.ModelMessage {
	return []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "retry conformance"}}},
	}
}

func timeoutMessages() []core.ModelMessage {
	return []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "timeout conformance"}}},
	}
}

func streamTimeoutMessages() []core.ModelMessage {
	return []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "stream timeout conformance"}}},
	}
}

func reasoningMessages() []core.ModelMessage {
	return []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "reasoning conformance"}}},
	}
}
