package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
	"github.com/fugue-labs/gollem/core"
)

type turnStartupFailureStore struct {
	store.Store
	appendErr error
	startErr  error
	updateErr error
}

type failNextRuntimeItemListStore struct {
	store.Store
	mu       sync.Mutex
	failNext bool
}

func (s *failNextRuntimeItemListStore) arm() {
	s.mu.Lock()
	s.failNext = true
	s.mu.Unlock()
}

func (s *failNextRuntimeItemListStore) ListItems(
	ctx context.Context,
	filter store.ItemFilter,
) ([]*store.Item, error) {
	s.mu.Lock()
	fail := s.failNext
	s.failNext = false
	s.mu.Unlock()
	if fail {
		return nil, errors.New("item boundary unavailable")
	}
	return s.Store.ListItems(ctx, filter)
}

func (s turnStartupFailureStore) AppendItem(
	ctx context.Context,
	req store.AppendItemRequest,
) (*store.Item, error) {
	if s.appendErr != nil {
		return nil, s.appendErr
	}
	return s.Store.AppendItem(ctx, req)
}

func (s turnStartupFailureStore) StartTurn(ctx context.Context, id string) (*store.Turn, error) {
	if s.startErr != nil {
		return nil, s.startErr
	}
	return s.Store.StartTurn(ctx, id)
}

func (s turnStartupFailureStore) UpdateItem(
	ctx context.Context,
	req store.UpdateItemRequest,
) (*store.Item, error) {
	if s.updateErr != nil {
		return nil, s.updateErr
	}
	return s.Store.UpdateItem(ctx, req)
}

func TestRuntimeStartFailureTerminalizesCreatedTurn(t *testing.T) {
	for _, tc := range []struct {
		name      string
		appendErr error
		startErr  error
	}{
		{name: "append item", appendErr: errors.New("append startup failure")},
		{name: "start turn", startErr: errors.New("start startup failure")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := newRuntimeTestStore(t)
			thread, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Startup failure"})
			if err != nil {
				t.Fatalf("CreateThread: %v", err)
			}
			failing := turnStartupFailureStore{
				Store:     st,
				appendErr: tc.appendErr,
				startErr:  tc.startErr,
			}
			runtimeSvc := NewRuntimeService(WithRuntimeModel(
				core.NewTestModel(core.TextResponse("must not run")),
				RuntimeModelInfo{ProviderID: "test", Model: "test-model"},
			))

			if _, err := runtimeSvc.Start(ctx, failing, nil, RuntimeStartRequest{
				ThreadID: thread.ID,
				Prompt:   "fail before ownership",
			}); err == nil {
				t.Fatal("RuntimeService.Start unexpectedly succeeded")
			}
			turns, err := st.ListTurns(ctx, store.TurnFilter{ThreadID: thread.ID})
			if err != nil {
				t.Fatalf("ListTurns: %v", err)
			}
			if len(turns) != 1 || turns[0].Status != store.TurnFailed || turns[0].CompletedAt.IsZero() {
				t.Fatalf("startup failure turn = %#v, want one terminal failed turn", turns)
			}
		})
	}
}

func TestRuntimeStartPersistsExplicitReasoningEffort(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	thread, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Reasoning receipt"})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	effort := "high"
	runtimeSvc := NewRuntimeService(WithRuntimeModel(
		core.NewTestModel(core.TextResponse("done")),
		RuntimeModelInfo{ProviderID: "openai", Model: "gpt-5"},
	))

	started, err := runtimeSvc.Start(ctx, st, nil, RuntimeStartRequest{
		ThreadID: thread.ID,
		Prompt:   "persist the selected effort",
		Selection: RuntimeModelSelection{
			ProviderID: "openai",
			Model:      "gpt-5",
		},
		ModelSettings: core.ModelSettings{ReasoningEffort: &effort},
	})
	if err != nil {
		t.Fatalf("RuntimeService.Start: %v", err)
	}
	var input runtimeTurnInput
	if err := json.Unmarshal(started.Turn.Input, &input); err != nil {
		t.Fatalf("decode turn input: %v", err)
	}
	if input.ReasoningEffort != effort {
		t.Fatalf("turn reasoning effort = %q, want %q", input.ReasoningEffort, effort)
	}
	inherited := runtimeModelSettingsFromInput(started.Turn.Input)
	if inherited.ReasoningEffort == nil || *inherited.ReasoningEffort != effort {
		t.Fatalf("inherited reasoning settings = %#v, want %q", inherited, effort)
	}
}

func TestRuntimeStartPersistsExplicitPromptCacheSetting(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	thread, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Prompt cache receipt"})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	disabled := false
	runtimeSvc := NewRuntimeService(WithRuntimeModel(
		core.NewTestModel(core.TextResponse("done")),
		RuntimeModelInfo{ProviderID: "openai", Model: "gpt-5"},
	))

	started, err := runtimeSvc.Start(ctx, st, nil, RuntimeStartRequest{
		ThreadID: thread.ID,
		Prompt:   "persist the prompt cache preference",
		ModelSettings: core.ModelSettings{
			PromptCacheEnabled: &disabled,
		},
	})
	if err != nil {
		t.Fatalf("RuntimeService.Start: %v", err)
	}
	var input runtimeTurnInput
	if err := json.Unmarshal(started.Turn.Input, &input); err != nil {
		t.Fatalf("decode turn input: %v", err)
	}
	if input.PromptCacheEnabled == nil || *input.PromptCacheEnabled {
		t.Fatalf("turn prompt cache setting = %#v, want false", input.PromptCacheEnabled)
	}
	inherited := runtimeModelSettingsFromInput(started.Turn.Input)
	if inherited.PromptCacheEnabled == nil || *inherited.PromptCacheEnabled {
		t.Fatalf("inherited prompt cache setting = %#v, want false", inherited.PromptCacheEnabled)
	}
}

func TestServerRuntimeReasoningEffortFailsClosedAgainstModelCatalog(t *testing.T) {
	for _, tc := range []struct {
		name       string
		model      string
		effort     string
		wantCode   int
		wantReason string
	}{
		{
			name:       "model does not advertise reasoning",
			model:      "gpt-4o",
			effort:     "high",
			wantCode:   protocol.CodeInvalidParams,
			wantReason: "does not advertise reasoning",
		},
		{
			name:       "effort is not advertised",
			model:      "gpt-5",
			effort:     "ultra",
			wantCode:   protocol.CodeInvalidParams,
			wantReason: "does not advertise reasoning effort",
		},
		{
			name:       "model is unknown",
			model:      "future-model",
			effort:     "high",
			wantCode:   protocol.CodeInvalidParams,
			wantReason: "model capability is unavailable",
		},
		{
			name:   "advertised effort",
			model:  "gpt-5",
			effort: "high",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := newRuntimeTestStore(t)
			server := readyServer(
				WithStore(st),
				WithRuntimeService(NewRuntimeService(WithRuntimeModel(
					core.NewTestModel(core.TextResponse("done")),
					RuntimeModelInfo{ProviderID: "openai", Model: tc.model},
				))),
			)
			response := server.HandleRequest(ctx, request("thread/start", map[string]any{
				"workspace":       t.TempDir(),
				"prompt":          "use explicit reasoning",
				"providerId":      "openai",
				"model":           tc.model,
				"reasoningEffort": tc.effort,
			}))
			if tc.wantCode != 0 {
				if response.Error == nil || response.Error.Code != tc.wantCode {
					t.Fatalf("thread/start error = %#v, want code %d", response.Error, tc.wantCode)
				}
				if !strings.Contains(response.Error.Message, tc.wantReason) {
					t.Fatalf("thread/start error = %q, want %q", response.Error.Message, tc.wantReason)
				}
				threads, err := st.ListThreads(ctx, store.ThreadFilter{})
				if err != nil {
					t.Fatalf("ListThreads: %v", err)
				}
				if len(threads) != 0 {
					t.Fatalf("invalid reasoning created %d threads", len(threads))
				}
				return
			}
			if response.Error != nil {
				t.Fatalf("thread/start error: %v", response.Error)
			}
			var result protocol.ThreadRunStartResult
			decodeResult(t, response, &result)
			var input runtimeTurnInput
			if err := json.Unmarshal(result.Turn.Input, &input); err != nil {
				t.Fatalf("decode turn input: %v", err)
			}
			if input.ReasoningEffort != tc.effort {
				t.Fatalf("turn reasoning effort = %q, want %q", input.ReasoningEffort, tc.effort)
			}
			storedThread, err := st.GetThread(ctx, result.Thread.ID)
			if err != nil {
				t.Fatalf("GetThread: %v", err)
			}
			if storedThread.Settings["reasoningEffort"] != tc.effort {
				t.Fatalf(
					"thread reasoning setting = %#v, want %q",
					storedThread.Settings["reasoningEffort"],
					tc.effort,
				)
			}
			waitForNotificationSet(t, server, "turn/completed")
			nextResponse := server.HandleRequest(ctx, request("turn/start", map[string]any{
				"threadId": result.Thread.ID,
				"prompt":   "inherit the thread reasoning setting",
			}))
			if nextResponse.Error != nil {
				t.Fatalf("inherited turn/start error: %v", nextResponse.Error)
			}
			var next protocol.TurnRunStartResult
			decodeResult(t, nextResponse, &next)
			var nextInput runtimeTurnInput
			if err := json.Unmarshal(next.Turn.Input, &nextInput); err != nil {
				t.Fatalf("decode inherited turn input: %v", err)
			}
			if nextInput.ReasoningEffort != tc.effort {
				t.Fatalf(
					"inherited turn reasoning effort = %q, want %q",
					nextInput.ReasoningEffort,
					tc.effort,
				)
			}
		})
	}
}

func TestServerRuntimeThinkingModesFailClosedAgainstModelCatalog(t *testing.T) {
	on := true
	budget := 2048
	for _, tc := range []struct {
		name       string
		model      string
		budget     *int
		adaptive   *bool
		wantReason string
	}{
		{
			name:       "manual thinking unavailable on opus 4.7",
			model:      "claude-opus-4-7",
			budget:     &budget,
			wantReason: "does not advertise manual thinking",
		},
		{
			name:       "adaptive thinking unavailable on haiku",
			model:      "claude-haiku-4-5-20251001",
			adaptive:   &on,
			wantReason: "does not advertise adaptive thinking",
		},
		{
			name:       "manual and adaptive thinking conflict",
			model:      "claude-sonnet-4-6",
			budget:     &budget,
			adaptive:   &on,
			wantReason: "mutually exclusive",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := newRuntimeTestStore(t)
			server := readyServer(
				WithStore(st),
				WithRuntimeService(NewRuntimeService(WithRuntimeModel(
					core.NewTestModel(core.TextResponse("must not run")),
					RuntimeModelInfo{ProviderID: "anthropic", Model: tc.model},
				))),
			)
			params := map[string]any{
				"workspace":  t.TempDir(),
				"prompt":     "validate the selected thinking mode",
				"providerId": "anthropic",
				"model":      tc.model,
			}
			if tc.budget != nil {
				params["thinkingBudget"] = *tc.budget
			}
			if tc.adaptive != nil {
				params["adaptiveThinking"] = *tc.adaptive
			}
			response := server.HandleRequest(ctx, request("thread/start", params))
			if response.Error == nil || response.Error.Code != protocol.CodeInvalidParams {
				t.Fatalf("thread/start error = %#v, want invalid params", response.Error)
			}
			if !strings.Contains(response.Error.Message, tc.wantReason) {
				t.Fatalf("thread/start error = %q, want %q", response.Error.Message, tc.wantReason)
			}
			threads, err := st.ListThreads(ctx, store.ThreadFilter{})
			if err != nil {
				t.Fatalf("ListThreads: %v", err)
			}
			if len(threads) != 0 {
				t.Fatalf("invalid thinking mode created %d threads", len(threads))
			}
		})
	}
}

func TestServerRuntimeRetryRetainsThinkingSettings(t *testing.T) {
	on := true
	budget := 2048
	for _, tc := range []struct {
		name     string
		budget   *int
		adaptive *bool
	}{
		{name: "manual", budget: &budget},
		{name: "adaptive", adaptive: &on},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := newRuntimeTestStore(t)
			model := core.NewTestModel(core.TextResponse("first"), core.TextResponse("retry"))
			server := readyServer(
				WithStore(st),
				WithRuntimeService(NewRuntimeService(WithRuntimeModel(
					model,
					RuntimeModelInfo{ProviderID: "anthropic", Model: "claude-sonnet-4-6"},
				))),
			)
			params := map[string]any{
				"workspace":  t.TempDir(),
				"prompt":     "persist the thinking selection",
				"providerId": "anthropic",
				"model":      "claude-sonnet-4-6",
			}
			if tc.budget != nil {
				params["thinkingBudget"] = *tc.budget
			}
			if tc.adaptive != nil {
				params["adaptiveThinking"] = *tc.adaptive
			}
			start := server.HandleRequest(ctx, request("thread/start", params))
			if start.Error != nil {
				t.Fatalf("thread/start error: %v", start.Error)
			}
			var started protocol.ThreadRunStartResult
			decodeResult(t, start, &started)
			waitForNotificationSet(t, server, "turn/completed")

			retry := server.HandleRequest(ctx, request("turn/retry", map[string]any{"turnId": started.Turn.ID}))
			if retry.Error != nil {
				t.Fatalf("turn/retry error: %v", retry.Error)
			}
			var retried protocol.TurnRunRetryResult
			decodeResult(t, retry, &retried)
			waitForNotificationSet(t, server, "turn/completed")

			persisted, err := st.GetTurn(ctx, retried.Turn.ID)
			if err != nil {
				t.Fatalf("GetTurn retry: %v", err)
			}
			var input runtimeTurnInput
			if err := json.Unmarshal(persisted.Input, &input); err != nil {
				t.Fatalf("decode retry input: %v", err)
			}
			if !sameIntPointer(input.ThinkingBudget, tc.budget) || !sameBoolPointer(input.AdaptiveThinking, tc.adaptive) {
				t.Fatalf("retry input thinking settings = %#v, want budget=%#v adaptive=%#v", input, tc.budget, tc.adaptive)
			}
			calls := model.Calls()
			if len(calls) != 2 {
				t.Fatalf("model calls = %d, want 2", len(calls))
			}
			if calls[1].Settings == nil || !sameIntPointer(calls[1].Settings.ThinkingBudget, tc.budget) || !sameBoolPointer(calls[1].Settings.AdaptiveThinking, tc.adaptive) {
				t.Fatalf("retry model settings = %#v, want budget=%#v adaptive=%#v", calls[1].Settings, tc.budget, tc.adaptive)
			}
		})
	}
}

func sameIntPointer(got, want *int) bool {
	if got == nil || want == nil {
		return got == want
	}
	return *got == *want
}

func sameBoolPointer(got, want *bool) bool {
	if got == nil || want == nil {
		return got == want
	}
	return *got == *want
}

func TestServerRuntimeSelectionValidatorFailsClosedBeforeThreadCreation(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	factoryCalls := 0
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(WithRuntimeModelFactory(
			func(context.Context, RuntimeModelSelection) (core.Model, RuntimeModelInfo, error) {
				factoryCalls++
				return core.NewTestModel(core.TextResponse("must not run")), RuntimeModelInfo{}, nil
			},
		))),
		WithRuntimeSelectionValidator(func(RuntimeModelSelection) error {
			return errors.New("selected model does not advertise streaming agent tool use")
		}),
	)

	response := server.HandleRequest(ctx, request("thread/start", map[string]any{
		"workspace":  t.TempDir(),
		"prompt":     "do not persist",
		"providerId": "openai",
		"model":      "gpt-4o",
	}))
	if response.Error == nil || response.Error.Code != protocol.CodeInvalidParams ||
		!strings.Contains(response.Error.Message, "does not advertise streaming agent tool use") {
		t.Fatalf("thread/start error = %#v", response.Error)
	}
	threads, err := st.ListThreads(ctx, store.ThreadFilter{})
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if len(threads) != 0 {
		t.Fatalf("rejected selection created threads: %#v", threads)
	}
	if factoryCalls != 0 {
		t.Fatalf("rejected selection called runtime factory %d times", factoryCalls)
	}
}

func TestServerThreadCompactStartFailureTerminalizesCreatedTurn(t *testing.T) {
	for _, tc := range []struct {
		name      string
		appendErr error
		startErr  error
	}{
		{name: "start turn", startErr: errors.New("compact start failure")},
		{name: "append item", appendErr: errors.New("compact append failure")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := newRuntimeTestStore(t)
			thread, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Compact failure"})
			if err != nil {
				t.Fatalf("CreateThread: %v", err)
			}
			failing := turnStartupFailureStore{
				Store:     st,
				appendErr: tc.appendErr,
				startErr:  tc.startErr,
			}
			server := readyServer(WithStore(failing))

			resp := server.HandleRequest(ctx, request("thread/compact/start", map[string]any{
				"threadId": thread.ID,
			}))
			if resp.Error == nil {
				t.Fatal("thread/compact/start unexpectedly succeeded")
			}
			turns, err := st.ListTurns(ctx, store.TurnFilter{ThreadID: thread.ID})
			if err != nil {
				t.Fatalf("ListTurns: %v", err)
			}
			if len(turns) != 1 || turns[0].Status != store.TurnFailed || turns[0].CompletedAt.IsZero() {
				t.Fatalf("compact failure turn = %#v, want one terminal failed turn", turns)
			}
		})
	}
}

func TestRuntimeStartLeaseIsSharedAndContextScoped(t *testing.T) {
	ctx := context.Background()
	coordinator := NewWorkspaceMutationCoordinator()
	server := readyServer(WithWorkspaceMutationCoordinator(coordinator))
	peer := readyServer(WithWorkspaceMutationCoordinator(coordinator))
	runtimeSvc := NewRuntimeService(
		WithRuntimeModel(core.NewTestModel(core.TextResponse("unused")), RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
	)

	leasedCtx, release, err := runtimeSvc.acquireStartLease(ctx, server)
	if err != nil {
		t.Fatalf("acquireStartLease: %v", err)
	}
	if !runtimeStartLeaseHeld(leasedCtx, runtimeSvc) || !workspaceMutationLeaseHeld(leasedCtx) {
		t.Fatal("combined runtime/workspace lease was not recorded in context")
	}
	type leaseResult struct {
		release func()
		err     error
	}
	peerLease := make(chan leaseResult, 1)
	go func() {
		release, err := peer.acquireTurnStartLease()
		peerLease <- leaseResult{release: release, err: err}
	}()
	select {
	case result := <-peerLease:
		if result.release != nil {
			result.release()
		}
		t.Fatalf("peer lease was not serialized: %v", result.err)
	case <-time.After(25 * time.Millisecond):
	}
	nestedCtx, releaseNested, err := runtimeSvc.acquireStartLease(leasedCtx, server)
	if err != nil {
		t.Fatalf("nested acquireStartLease: %v", err)
	}
	if nestedCtx != leasedCtx {
		t.Fatal("nested start lease replaced the leased context")
	}
	releaseNested()
	select {
	case result := <-peerLease:
		if result.release != nil {
			result.release()
		}
		t.Fatalf("nested release unlocked peer lease: %v", result.err)
	case <-time.After(25 * time.Millisecond):
	}
	release()
	release()
	select {
	case result := <-peerLease:
		if result.err != nil {
			t.Fatalf("peer lease after release: %v", result.err)
		}
		result.release()
	case <-time.After(time.Second):
		t.Fatal("peer lease remained blocked after release")
	}

	if runtimeStartLeaseHeld(ctx, nil) {
		t.Fatal("nil runtime service reported a held start lease")
	}
}

func TestServerRuntimeThreadStartCompletesTurn(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	model := core.NewTestModel(core.TextResponse("runtime answer"))
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}))),
	)

	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{
		"title":    "Runtime",
		"prompt":   "hello runtime",
		"provider": "test",
		"model":    "test-model",
	}))
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, resp, &started)
	if started.Thread.ID == "" || started.Turn.ID == "" || started.Turn.Status != store.TurnRunning {
		t.Fatalf("thread/start result = %#v", started)
	}

	waitForNotificationSet(t, server, "thread/started", "turn/started", "item/agentMessage/delta", "item/completed", "turn/completed")
	readResp := server.HandleRequest(ctx, request("thread/read", map[string]any{"threadId": started.Thread.ID}))
	if readResp.Error != nil {
		t.Fatalf("thread/read error: %v", readResp.Error)
	}
	var read legacyThreadReadResult
	decodeResult(t, readResp, &read)
	if len(read.Turns) != 1 || read.Turns[0].Status != store.TurnCompleted {
		t.Fatalf("turns = %#v", read.Turns)
	}
	if len(read.Items) != 2 {
		t.Fatalf("items = %#v", read.Items)
	}
	if got := runtimePromptFromInput(read.Turns[0].Input); got != "hello runtime" {
		t.Fatalf("turn input prompt = %q", got)
	}
	if len(model.Calls()) != 1 {
		t.Fatalf("model calls = %d, want 1", len(model.Calls()))
	}
}

func TestServerRuntimeThreadResumeUsesPersistedHistory(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	model := core.NewTestModel(core.TextResponse("first answer"), core.TextResponse("second answer"))
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}))),
	)

	startResp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "first prompt"}))
	if startResp.Error != nil {
		t.Fatalf("thread/start error: %v", startResp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, startResp, &started)
	waitForNotificationSet(t, server, "turn/completed")

	resumeResp := server.HandleRequest(ctx, request("thread/resume", map[string]any{
		"threadId": started.Thread.ID,
		"prompt":   "second prompt",
	}))
	if resumeResp.Error != nil {
		t.Fatalf("thread/resume error: %v", resumeResp.Error)
	}
	waitForNotificationSet(t, server, "turn/completed")

	calls := model.Calls()
	if len(calls) != 2 {
		t.Fatalf("model calls = %d, want 2", len(calls))
	}
	secondMessages := calls[1].Messages
	if len(secondMessages) != 3 {
		t.Fatalf("second call messages = %#v", secondMessages)
	}
	assertRuntimeUserPrompt(t, secondMessages[0], "first prompt")
	assertRuntimeAssistantText(t, secondMessages[1], "first answer")
	assertRuntimeUserPrompt(t, secondMessages[2], "second prompt")
}

func TestServerRuntimePublishesThreadTokenUsageUpdated(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	first := core.TextResponse("first answer")
	first.Usage = core.Usage{
		InputTokens:     10,
		OutputTokens:    4,
		CacheReadTokens: 3,
		Details: map[string]int{
			"reasoning_tokens": 2,
		},
	}
	second := core.TextResponse("second answer")
	second.Usage = core.Usage{
		InputTokens:     5,
		OutputTokens:    6,
		CacheReadTokens: 1,
		Details: map[string]int{
			"reasoning_tokens": 1,
		},
	}
	model := core.NewTestModel(first, second)
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}))),
	)

	startResp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "first prompt"}))
	if startResp.Error != nil {
		t.Fatalf("thread/start error: %v", startResp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, startResp, &started)
	firstEvents := waitForNotificationSet(t, server, "thread/tokenUsage/updated", "turn/completed")
	firstUsage := decodeThreadTokenUsageNotification(t, firstEvents)
	if firstUsage.ThreadID != started.Thread.ID || firstUsage.TurnID != started.Turn.ID {
		t.Fatalf("first usage ids = thread %q turn %q, want %q/%q", firstUsage.ThreadID, firstUsage.TurnID, started.Thread.ID, started.Turn.ID)
	}
	if firstUsage.TokenUsage.ModelContextWindow != nil {
		t.Fatalf("first modelContextWindow = %v, want nil", *firstUsage.TokenUsage.ModelContextWindow)
	}
	assertTokenUsageBreakdown(t, "first last", firstUsage.TokenUsage.Last, 14, 10, 3, 4, 2)
	assertTokenUsageBreakdown(t, "first total", firstUsage.TokenUsage.Total, 14, 10, 3, 4, 2)

	resumeResp := server.HandleRequest(ctx, request("thread/resume", map[string]any{
		"threadId": started.Thread.ID,
		"prompt":   "second prompt",
	}))
	if resumeResp.Error != nil {
		t.Fatalf("thread/resume error: %v", resumeResp.Error)
	}
	var resumed struct {
		Turn *store.Turn `json:"turn"`
	}
	decodeResult(t, resumeResp, &resumed)
	secondEvents := waitForNotificationSet(t, server, "thread/tokenUsage/updated", "turn/completed")
	secondUsage := decodeThreadTokenUsageNotification(t, secondEvents)
	if secondUsage.ThreadID != started.Thread.ID || secondUsage.TurnID != resumed.Turn.ID {
		t.Fatalf("second usage ids = thread %q turn %q, want %q/%q", secondUsage.ThreadID, secondUsage.TurnID, started.Thread.ID, resumed.Turn.ID)
	}
	if secondUsage.TokenUsage.ModelContextWindow != nil {
		t.Fatalf("second modelContextWindow = %v, want nil", *secondUsage.TokenUsage.ModelContextWindow)
	}
	assertTokenUsageBreakdown(t, "second last", secondUsage.TokenUsage.Last, 11, 5, 1, 6, 1)
	assertTokenUsageBreakdown(t, "second total", secondUsage.TokenUsage.Total, 25, 15, 4, 10, 3)
}

func TestServerRuntimeAccountsStructuredThreadGoal(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	thread, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Goal runtime"})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	response := core.TextResponse("goal answer")
	response.Usage = core.Usage{InputTokens: 7, OutputTokens: 5}
	model := core.NewTestModel(response)
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}))),
	)

	goalResp := server.HandleRequest(ctx, request("thread/goal/set", map[string]any{
		"threadId":    thread.ID,
		"objective":   "Finish one runtime turn",
		"tokenBudget": 10,
	}))
	if goalResp.Error != nil {
		t.Fatalf("thread/goal/set error: %v", goalResp.Error)
	}
	_ = server.DrainNotifications()

	resumeResp := server.HandleRequest(ctx, request("thread/resume", map[string]any{
		"threadId": thread.ID,
		"prompt":   "account this turn",
	}))
	if resumeResp.Error != nil {
		t.Fatalf("thread/resume error: %v", resumeResp.Error)
	}
	var resumed struct {
		Turn *store.Turn `json:"turn"`
	}
	decodeResult(t, resumeResp, &resumed)
	events := waitForNotificationSet(t, server, "thread/tokenUsage/updated", "thread/goal/updated", "turn/completed")
	var goalUpdate protocol.ThreadGoalUpdatedNotification
	found := false
	for _, event := range events {
		if event.Method != "thread/goal/updated" {
			continue
		}
		if err := json.Unmarshal(event.Params, &goalUpdate); err != nil {
			t.Fatalf("decode goal update: %v", err)
		}
		found = goalUpdate.TurnID != nil && *goalUpdate.TurnID == resumed.Turn.ID
	}
	if !found || goalUpdate.Goal.TokensUsed != 12 || goalUpdate.Goal.Status != protocol.ThreadGoalBudgetLimited {
		t.Fatalf("runtime goal update = %#v", goalUpdate)
	}

	getResp := server.HandleRequest(ctx, request("thread/goal/get", map[string]any{"threadId": thread.ID}))
	if getResp.Error != nil {
		t.Fatalf("thread/goal/get error: %v", getResp.Error)
	}
	var got protocol.ThreadGoalGetResponse
	decodeResult(t, getResp, &got)
	if got.Goal == nil || got.Goal.TokensUsed != 12 || got.Goal.Status != protocol.ThreadGoalBudgetLimited {
		t.Fatalf("durable runtime goal = %#v", got.Goal)
	}
}

func TestServerRuntimePersistsDynamicToolCallLifecycle(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	type echoParams struct {
		Text string `json:"text"`
	}
	echo := core.FuncTool[echoParams]("echo", "Echo text.", func(_ context.Context, params echoParams) (string, error) {
		return "echo: " + params.Text, nil
	})
	echo.Definition.Namespace = "utility"
	unused := core.FuncTool[struct{}]("unused", "Unused test tool.", func(context.Context, struct{}) (string, error) {
		return "unused", nil
	})
	model := core.NewTestModel(
		core.ToolCallResponseWithID("echo", `{"text":"hello"}`, "call-echo"),
		core.TextResponse("tool finished"),
	)
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(
			WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
			WithRuntimeTools(echo),
			WithRuntimeTools(unused),
		)),
	)

	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "use echo"}))
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, resp, &started)
	events := waitForNotificationSet(t, server,
		"item/started",
		"item/completed",
		"item/completed",
		"turn/completed",
	)
	toolEvents := runtimeToolNotifications(t, events)
	if len(toolEvents) != 2 {
		t.Fatalf("dynamic tool notifications = %#v, want started and completed", toolEvents)
	}
	startedNotice := toolEvents[0]
	completedNotice := toolEvents[1]
	if startedNotice.Method != "item/started" || startedNotice.Params.StartedAtMS <= 0 {
		t.Fatalf("started tool notification = %#v", startedNotice)
	}
	if completedNotice.Method != "item/completed" || completedNotice.Params.CompletedAtMS <= 0 {
		t.Fatalf("completed tool notification = %#v", completedNotice)
	}
	if startedNotice.Params.ThreadID != started.Thread.ID || startedNotice.Params.TurnID != started.Turn.ID {
		t.Fatalf("started tool ids = %#v, want %q/%q", startedNotice.Params, started.Thread.ID, started.Turn.ID)
	}
	if startedNotice.Params.Item.ID == "" || completedNotice.Params.Item.ID != startedNotice.Params.Item.ID {
		t.Fatalf("tool item ids = started %q completed %q", startedNotice.Params.Item.ID, completedNotice.Params.Item.ID)
	}
	if startedNotice.Params.Item.Tool != "echo" || startedNotice.Params.Item.Status != "inProgress" || startedNotice.Params.Item.Success != nil {
		t.Fatalf("started tool item = %#v", startedNotice.Params.Item)
	}
	if startedNotice.Params.Item.Namespace == nil || *startedNotice.Params.Item.Namespace != "utility" {
		t.Fatalf("started tool namespace = %v", startedNotice.Params.Item.Namespace)
	}
	if got := startedNotice.Params.Item.Arguments["text"]; got != "hello" {
		t.Fatalf("started tool arguments = %#v", startedNotice.Params.Item.Arguments)
	}
	if completedNotice.Params.Item.Status != "completed" || completedNotice.Params.Item.Success == nil || !*completedNotice.Params.Item.Success {
		t.Fatalf("completed tool item = %#v", completedNotice.Params.Item)
	}
	if completedNotice.Params.Item.DurationMS == nil || *completedNotice.Params.Item.DurationMS < 0 {
		t.Fatalf("completed tool duration = %v", completedNotice.Params.Item.DurationMS)
	}
	if len(completedNotice.Params.Item.ContentItems) != 1 || !strings.Contains(completedNotice.Params.Item.ContentItems[0].Text, "echo: hello") {
		t.Fatalf("completed tool content = %#v", completedNotice.Params.Item.ContentItems)
	}

	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: started.Thread.ID, TurnID: started.Turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	toolItem := findRuntimeToolItem(t, items, "echo", "text", "hello")
	if toolItem.Status != "completed" || toolItem.Payload.ID != toolItem.Item.ID || toolItem.Payload.Tool != "echo" {
		t.Fatalf("stored tool item = %#v", toolItem)
	}
}

func TestServerRuntimePersistsFailedDynamicToolCall(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	type failParams struct {
		Reason string `json:"reason"`
	}
	failing := core.FuncTool[failParams]("failing", "Fail with a reason.", func(_ context.Context, params failParams) (string, error) {
		return "", errors.New(params.Reason)
	})
	model := core.NewTestModel(
		core.ToolCallResponseWithID("failing", `{"reason":"boom"}`, "call-failing"),
		core.TextResponse("failure handled"),
	)
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(
			WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
			WithRuntimeTools(failing),
		)),
	)

	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "call failing"}))
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, resp, &started)
	events := waitForNotificationSet(t, server,
		"item/started",
		"item/completed",
		"item/completed",
		"turn/completed",
	)
	toolEvents := runtimeToolNotifications(t, events)
	if len(toolEvents) != 2 {
		t.Fatalf("dynamic tool notifications = %#v, want started and failed completion", toolEvents)
	}
	failed := toolEvents[1].Params.Item
	if failed.Status != "failed" || failed.Success == nil || *failed.Success {
		t.Fatalf("failed tool item = %#v", failed)
	}
	if len(failed.ContentItems) != 1 || !strings.Contains(failed.ContentItems[0].Text, "boom") {
		t.Fatalf("failed tool content = %#v", failed.ContentItems)
	}

	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: started.Thread.ID, TurnID: started.Turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	toolItem := findRuntimeToolItem(t, items, "failing", "reason", "boom")
	if toolItem.Status != "failed" || toolItem.Payload.Success == nil || *toolItem.Payload.Success {
		t.Fatalf("stored failed tool item = %#v", toolItem)
	}
}

func TestServerRuntimeTracksConcurrentDynamicToolCalls(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	type parallelParams struct {
		Value string `json:"value"`
	}
	entered := make(chan string, 2)
	release := make(chan struct{})
	parallel := core.FuncTool[parallelParams]("parallel", "Run concurrently.", func(ctx context.Context, params parallelParams) (string, error) {
		entered <- params.Value
		select {
		case <-release:
			return "done: " + params.Value, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}, core.WithToolConcurrencySafe(true))
	model := core.NewTestModel(
		core.MultiToolCallResponse(
			core.ToolCallPart{ToolName: "parallel", ToolCallID: "call-a", ArgsJSON: `{"value":"a"}`},
			core.ToolCallPart{ToolName: "parallel", ToolCallID: "call-b", ArgsJSON: `{"value":"b"}`},
		),
		core.TextResponse("parallel finished"),
	)
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(
			WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}),
			WithRuntimeTools(parallel),
		)),
	)

	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "run both"}))
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, resp, &started)
	seenValues := map[string]bool{}
	for len(seenValues) < 2 {
		select {
		case value := <-entered:
			seenValues[value] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("tool calls did not run concurrently; entered=%v", seenValues)
		}
	}
	close(release)
	events := waitForNotificationSet(t, server,
		"item/started",
		"item/started",
		"item/completed",
		"item/completed",
		"item/completed",
		"turn/completed",
	)
	toolEvents := runtimeToolNotifications(t, events)
	if len(toolEvents) != 4 {
		t.Fatalf("dynamic tool notifications = %#v, want two starts and two completions", toolEvents)
	}
	completedIDs := map[string]bool{}
	for _, event := range toolEvents {
		if event.Method != "item/completed" {
			continue
		}
		if event.Params.Item.Status != "completed" || event.Params.Item.Success == nil || !*event.Params.Item.Success {
			t.Fatalf("concurrent completed tool item = %#v", event.Params.Item)
		}
		completedIDs[event.Params.Item.ID] = true
	}
	if len(completedIDs) != 2 {
		t.Fatalf("completed concurrent tool ids = %#v", completedIDs)
	}

	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: started.Thread.ID, TurnID: started.Turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if findRuntimeToolItem(t, items, "parallel", "value", "a").Status != "completed" || findRuntimeToolItem(t, items, "parallel", "value", "b").Status != "completed" {
		t.Fatalf("concurrent tool items were not completed: %#v", items)
	}
}

func TestServerRuntimeThreadResumeUsesInjectedResponseItems(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	model := core.NewTestModel(core.TextResponse("after injection"))
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}))),
	)
	thread, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "Injected history"})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}

	injectResp := server.HandleRequest(ctx, request("thread/inject_items", map[string]any{
		"threadId": thread.ID,
		"items": []any{
			map[string]any{
				"type": "message",
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_text", "text": "injected user"},
				},
			},
			map[string]any{
				"type":  "message",
				"role":  "assistant",
				"model": "prior-model",
				"content": []any{
					map[string]any{"type": "output_text", "text": "injected assistant"},
				},
			},
		},
	}))
	if injectResp.Error != nil {
		t.Fatalf("thread/inject_items error: %v", injectResp.Error)
	}
	server.DrainNotifications()

	resumeResp := server.HandleRequest(ctx, request("thread/resume", map[string]any{
		"threadId": thread.ID,
		"prompt":   "next prompt",
	}))
	if resumeResp.Error != nil {
		t.Fatalf("thread/resume error: %v", resumeResp.Error)
	}
	waitForNotificationSet(t, server, "turn/completed")

	calls := model.Calls()
	if len(calls) != 1 {
		t.Fatalf("model calls = %d, want 1", len(calls))
	}
	messages := calls[0].Messages
	if len(messages) != 3 {
		t.Fatalf("messages = %#v", messages)
	}
	assertRuntimeUserPrompt(t, messages[0], "injected user")
	assertRuntimeAssistantText(t, messages[1], "injected assistant")
	assertRuntimeUserPrompt(t, messages[2], "next prompt")
}

func TestServerRuntimeThreadCompactBoundsResumeHistory(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	model := core.NewTestModel(
		core.TextResponse("first answer"),
		core.TextResponse("second answer"),
		core.TextResponse("third answer"),
	)
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}))),
	)

	startResp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "first prompt"}))
	if startResp.Error != nil {
		t.Fatalf("thread/start error: %v", startResp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, startResp, &started)
	waitForNotificationSet(t, server, "turn/completed")

	resumeResp := server.HandleRequest(ctx, request("thread/resume", map[string]any{
		"threadId": started.Thread.ID,
		"prompt":   "second prompt",
	}))
	if resumeResp.Error != nil {
		t.Fatalf("thread/resume error: %v", resumeResp.Error)
	}
	waitForNotificationSet(t, server, "turn/completed")

	compactResp := server.HandleRequest(ctx, request("thread/compact/start", map[string]any{"threadId": started.Thread.ID}))
	if compactResp.Error != nil {
		t.Fatalf("thread/compact/start error: %v", compactResp.Error)
	}
	waitForNotificationSet(t, server, "thread/compacted")

	resumeResp = server.HandleRequest(ctx, request("thread/resume", map[string]any{
		"threadId": started.Thread.ID,
		"prompt":   "third prompt",
	}))
	if resumeResp.Error != nil {
		t.Fatalf("thread/resume third error: %v", resumeResp.Error)
	}
	waitForNotificationSet(t, server, "turn/completed")

	calls := model.Calls()
	if len(calls) != 3 {
		t.Fatalf("model calls = %d, want 3", len(calls))
	}
	messages := calls[2].Messages
	if len(messages) != 2 {
		t.Fatalf("third call messages = %#v", messages)
	}
	assertRuntimeSystemPromptContains(t, messages[0], "first prompt", "second answer")
	assertRuntimeUserPrompt(t, messages[1], "third prompt")
}

func TestServerRuntimeTurnRetryBranchesBeforeSourceTurn(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	model := core.NewTestModel(
		core.TextResponse("first answer"),
		core.TextResponse("retry answer"),
		core.TextResponse("inherited retry answer"),
	)
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}))),
	)

	startResp := server.HandleRequest(ctx, request("thread/start", map[string]any{
		"prompt":             "original prompt",
		"promptCacheEnabled": false,
	}))
	if startResp.Error != nil {
		t.Fatalf("thread/start error: %v", startResp.Error)
	}
	var started struct {
		Turn *store.Turn `json:"turn"`
	}
	decodeResult(t, startResp, &started)
	waitForNotificationSet(t, server, "turn/completed")

	retryResp := server.HandleRequest(ctx, request("turn/retry", map[string]any{
		"turnId":             started.Turn.ID,
		"promptCacheEnabled": true,
	}))
	if retryResp.Error != nil {
		t.Fatalf("turn/retry error: %v", retryResp.Error)
	}
	var retried protocol.TurnRunRetryResult
	decodeResult(t, retryResp, &retried)
	waitForNotificationSet(t, server, "turn/completed")
	persisted, err := st.GetTurn(ctx, retried.Turn.ID)
	if err != nil {
		t.Fatalf("GetTurn retried: %v", err)
	}
	var persistedInput runtimeTurnInput
	if err := json.Unmarshal(persisted.Input, &persistedInput); err != nil {
		t.Fatalf("unmarshal retried input: %v", err)
	}
	if persistedInput.PromptCacheEnabled == nil || !*persistedInput.PromptCacheEnabled {
		t.Fatalf("persisted retry prompt-cache setting = %#v, want explicit true override", persistedInput)
	}

	calls := model.Calls()
	if len(calls) != 2 {
		t.Fatalf("model calls = %d, want 2", len(calls))
	}
	if len(calls[1].Messages) != 1 {
		t.Fatalf("retry messages = %#v", calls[1].Messages)
	}
	if calls[1].Settings == nil || calls[1].Settings.PromptCacheEnabled == nil || !*calls[1].Settings.PromptCacheEnabled {
		t.Fatalf("retry prompt-cache setting = %#v, want explicit true override", calls[1].Settings)
	}
	assertRuntimeUserPrompt(t, calls[1].Messages[0], "original prompt")

	inheritedRetryResp := server.HandleRequest(ctx, request("turn/retry", map[string]any{
		"turnId": retried.Turn.ID,
	}))
	if inheritedRetryResp.Error != nil {
		t.Fatalf("inherited turn/retry error: %v", inheritedRetryResp.Error)
	}
	waitForNotificationSet(t, server, "turn/completed")

	calls = model.Calls()
	if len(calls) != 3 {
		t.Fatalf("model calls after inherited retry = %d, want 3", len(calls))
	}
	if calls[2].Settings == nil || calls[2].Settings.PromptCacheEnabled == nil || !*calls[2].Settings.PromptCacheEnabled {
		t.Fatalf("inherited retry prompt-cache setting = %#v, want persisted true", calls[2].Settings)
	}
}

func TestServerRuntimeTurnRetryIsIdempotentAcrossDuplicateRequests(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	model := core.NewTestModel(core.TextResponse("first answer"), core.TextResponse("retry answer"))
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "test-model"}))),
	)

	startResp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "original prompt"}))
	if startResp.Error != nil {
		t.Fatalf("thread/start error: %v", startResp.Error)
	}
	var started struct {
		Turn *store.Turn `json:"turn"`
	}
	decodeResult(t, startResp, &started)
	waitForNotificationSet(t, server, "turn/completed")

	params := map[string]any{
		"turnId":         started.Turn.ID,
		"idempotencyKey": "desktop-retry-1",
	}
	firstResp := server.HandleRequest(ctx, request("turn/retry", params))
	if firstResp.Error != nil {
		t.Fatalf("first turn/retry error: %v", firstResp.Error)
	}
	var first protocol.TurnRunRetryResult
	decodeResult(t, firstResp, &first)
	if first.Reused || first.SourceTurnID != started.Turn.ID || first.Turn.ID == "" {
		t.Fatalf("first retry = %#v", first)
	}

	duplicateResp := server.HandleRequest(ctx, request("turn/retry", params))
	if duplicateResp.Error != nil {
		t.Fatalf("duplicate turn/retry error: %v", duplicateResp.Error)
	}
	var duplicate protocol.TurnRunRetryResult
	decodeResult(t, duplicateResp, &duplicate)
	if !duplicate.Reused || duplicate.Turn.ID != first.Turn.ID ||
		duplicate.IdempotencyKey != "desktop-retry-1" {
		t.Fatalf("duplicate retry = %#v, first = %#v", duplicate, first)
	}

	waitForNotificationSet(t, server, "turn/completed")
	if got := len(model.Calls()); got != 2 {
		t.Fatalf("model calls = %d, want 2", got)
	}
	turns, err := st.ListTurns(ctx, store.TurnFilter{ThreadID: started.Turn.ThreadID})
	if err != nil {
		t.Fatalf("ListTurns: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("turns = %#v, want source plus one retry", turns)
	}
}

func TestServerRuntimeTurnRetryCanBeCancelledAndRemainsIdempotent(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	thread, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "retry cancellation"})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	source, err := st.CreateTurn(ctx, store.CreateTurnRequest{
		ThreadID: thread.ID,
		Input:    json.RawMessage(`{"prompt":"retry me"}`),
	})
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	if _, err := st.CompleteTurn(ctx, store.CompleteTurnRequest{
		ID:     source.ID,
		Status: store.TurnInterrupted,
		Error:  store.RuntimeOwnerLostReason,
	}); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	model := &blockingRuntimeModel{started: make(chan struct{})}
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "blocking"}))),
	)

	params := map[string]any{
		"turnId":         source.ID,
		"idempotencyKey": "cancelled-retry-1",
	}
	retryResp := server.HandleRequest(ctx, request("turn/retry", params))
	if retryResp.Error != nil {
		t.Fatalf("turn/retry error: %v", retryResp.Error)
	}
	var retried protocol.TurnRunRetryResult
	decodeResult(t, retryResp, &retried)
	waitForBlockingModel(t, model)

	interruptResp := server.HandleRequest(ctx, request("turn/interrupt", map[string]any{"turnId": retried.Turn.ID}))
	if interruptResp.Error != nil {
		t.Fatalf("turn/interrupt error: %v", interruptResp.Error)
	}
	waitForNotificationSet(t, server, "turn/completed")
	cancelled, err := st.GetTurn(ctx, retried.Turn.ID)
	if err != nil {
		t.Fatalf("GetTurn: %v", err)
	}
	if cancelled.Status != store.TurnInterrupted {
		t.Fatalf("cancelled retry = %#v", cancelled)
	}

	duplicateResp := server.HandleRequest(ctx, request("turn/retry", params))
	if duplicateResp.Error != nil {
		t.Fatalf("duplicate turn/retry error: %v", duplicateResp.Error)
	}
	var duplicate protocol.TurnRunRetryResult
	decodeResult(t, duplicateResp, &duplicate)
	if !duplicate.Reused || duplicate.Turn.ID != retried.Turn.ID ||
		duplicate.Turn.Status != protocol.TurnLifecycleInterrupted {
		t.Fatalf("duplicate cancelled retry = %#v, first = %#v", duplicate, retried)
	}
}

func TestServerRuntimeTurnRetryIsTypedUnavailableWithoutRecoveryStore(t *testing.T) {
	ctx := context.Background()
	sqlite := newRuntimeTestStore(t)
	thread, err := sqlite.CreateThread(ctx, store.CreateThreadRequest{Title: "legacy store"})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	source, err := sqlite.CreateTurn(ctx, store.CreateTurnRequest{
		ThreadID: thread.ID,
		Input:    json.RawMessage(`{"prompt":"retry me"}`),
	})
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	if _, err := sqlite.CompleteTurn(ctx, store.CompleteTurnRequest{ID: source.ID}); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	legacy := struct{ store.Store }{Store: sqlite}
	server := readyServer(
		WithStore(legacy),
		WithRuntimeService(NewRuntimeService(WithRuntimeModel(
			core.NewTestModel(core.TextResponse("must not run")),
			RuntimeModelInfo{ProviderID: "test", Model: "test-model"},
		))),
	)

	resp := server.HandleRequest(ctx, request("turn/retry", map[string]any{
		"turnId":         source.ID,
		"idempotencyKey": "legacy-store-retry",
	}))
	if resp.Error == nil || resp.Error.Code != protocol.CodeMethodUnavailable ||
		resp.Error.Message != "method unavailable" ||
		!strings.Contains(string(resp.Error.Data), "restart-safe retry") {
		t.Fatalf("turn/retry unavailable error = %#v", resp.Error)
	}
}

func TestServerRuntimeTurnInterruptCancelsActiveRun(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	model := &blockingRuntimeModel{started: make(chan struct{})}
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "blocking"}))),
	)

	startResp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "block"}))
	if startResp.Error != nil {
		t.Fatalf("thread/start error: %v", startResp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	decodeResult(t, startResp, &started)
	waitForBlockingModel(t, model)

	interruptResp := server.HandleRequest(ctx, request("turn/interrupt", map[string]any{"turnId": started.Turn.ID}))
	if interruptResp.Error != nil {
		t.Fatalf("turn/interrupt error: %v", interruptResp.Error)
	}
	waitForNotificationSet(t, server, "turn/completed")
	turn, err := st.GetTurn(ctx, started.Turn.ID)
	if err != nil {
		t.Fatalf("GetTurn: %v", err)
	}
	if turn.Status != store.TurnInterrupted {
		t.Fatalf("turn status = %s, want interrupted; error=%q", turn.Status, turn.Error)
	}
}

func TestRuntimeShutdownCancelsActiveRunBeforeStoreClose(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	model := &blockingRuntimeModel{started: make(chan struct{})}
	runtimeSvc := NewRuntimeService(WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "blocking"}))
	server := readyServer(
		WithStore(st),
		WithRuntimeService(runtimeSvc),
	)

	startResp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "block"}))
	if startResp.Error != nil {
		t.Fatalf("thread/start error: %v", startResp.Error)
	}
	var started struct {
		Turn *store.Turn `json:"turn"`
	}
	decodeResult(t, startResp, &started)
	waitForBlockingModel(t, model)

	shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := runtimeSvc.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	turn, err := st.GetTurn(ctx, started.Turn.ID)
	if err != nil {
		t.Fatalf("GetTurn: %v", err)
	}
	if turn.Status != store.TurnInterrupted {
		t.Fatalf("turn status = %s, want interrupted; error=%q", turn.Status, turn.Error)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close store after runtime shutdown: %v", err)
	}
}

func TestRuntimeShutdownRejectsNewStarts(t *testing.T) {
	ctx := context.Background()
	runtimeSvc := NewRuntimeService(WithRuntimeModel(
		core.NewTestModel(core.TextResponse("late")),
		RuntimeModelInfo{ProviderID: "test", Model: "test-model"},
	))
	server := readyServer(
		WithStore(newRuntimeTestStore(t)),
		WithRuntimeService(runtimeSvc),
	)
	if err := runtimeSvc.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	resp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "after shutdown"}))
	if resp.Error == nil || resp.Error.Code != protocol.CodeMethodUnavailable {
		t.Fatalf("thread/start after shutdown error = %#v, want method unavailable", resp.Error)
	}
	var data protocol.UnavailableData
	if err := json.Unmarshal(resp.Error.Data, &data); err != nil {
		t.Fatalf("decode unavailable data: %v", err)
	}
	if data.Method != "thread/start" || data.Reason != "turn runtime is shutting down" {
		t.Fatalf("unavailable data = %+v", data)
	}
}

func TestServerRuntimeTurnSteerIsConsumedAndCompleted(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	model := newSteerRuntimeModel()
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(WithRuntimeModel(model, RuntimeModelInfo{ProviderID: "test", Model: "steer"}))),
	)

	startResp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "block"}))
	if startResp.Error != nil {
		t.Fatalf("thread/start error: %v", startResp.Error)
	}
	var started struct {
		Turn *store.Turn `json:"turn"`
	}
	decodeResult(t, startResp, &started)
	model.waitStarted(t)

	steerResp := server.HandleRequest(ctx, request("turn/steer", map[string]any{
		"threadId":            started.Turn.ThreadID,
		"expectedTurnId":      started.Turn.ID,
		"clientUserMessageId": "steer-client-1",
		"input": []map[string]any{{
			"type":          "text",
			"text":          "adjust course",
			"text_elements": []any{},
		}},
	}))
	if steerResp.Error != nil {
		t.Fatalf("turn/steer error: %v", steerResp.Error)
	}
	var steer struct {
		TurnID string `json:"turnId"`
	}
	decodeResult(t, steerResp, &steer)
	if steer.TurnID != started.Turn.ID {
		t.Fatalf("turn/steer result = %#v", steer)
	}
	model.releaseFirst()
	notifications := waitForNotificationSet(t, server, "turn/completed")
	assertRuntimeSteerNotificationOrder(t, notifications)
	assertRuntimeSteerDeltaBoundary(t, notifications)

	calls := model.callsSnapshot()
	if len(calls) != 2 || !runtimeMessagesContainText(calls[1], "adjust course") {
		t.Fatalf("model calls = %#v, want consumed steer in second request", calls)
	}
	readResp := server.HandleRequest(ctx, request("thread/read", map[string]any{
		"threadId": started.Turn.ThreadID,
	}))
	if readResp.Error != nil {
		t.Fatalf("thread/read error: %v", readResp.Error)
	}
	var read protocol.ThreadReadResult
	decodeResult(t, readResp, &read)
	var steerItem *protocol.TimelineItem
	for i := range read.Items {
		if read.Items[i].Kind == runtimeSteerItemKind {
			steerItem = &read.Items[i]
		}
	}
	if steerItem == nil || steerItem.Status != runtimeSteerStatusComplete {
		t.Fatalf("public steer item = %#v, want completed", steerItem)
	}
	var payload struct {
		ClientUserMessageID string     `json:"clientUserMessageId"`
		Status              string     `json:"status"`
		ConsumedAfterSeq    int64      `json:"consumedAfterSeq"`
		ConsumedAt          *time.Time `json:"consumedAt"`
	}
	if err := json.Unmarshal(steerItem.Payload, &payload); err != nil {
		t.Fatalf("decode public steer payload: %v", err)
	}
	if payload.ClientUserMessageID != "steer-client-1" ||
		payload.Status != runtimeSteerStatusComplete ||
		payload.ConsumedAfterSeq <= 0 ||
		payload.ConsumedAt == nil {
		t.Fatalf("steer payload = %#v", payload)
	}
}

func TestServerRuntimeTurnSteerRejectsMismatchedUnsupportedAndOversizedInput(t *testing.T) {
	ctx, st, server, model, turn := startSteerRuntimeTest(t)
	cases := []struct {
		name   string
		params map[string]any
	}{
		{
			name: "mismatched thread",
			params: runtimeSteerTestParams(
				"wrong-thread", turn.ID, "mismatch-thread", "do not enqueue",
			),
		},
		{
			name: "mismatched turn",
			params: runtimeSteerTestParams(
				turn.ThreadID, "wrong-turn", "mismatch-turn", "do not enqueue",
			),
		},
		{
			name: "unsupported image input",
			params: map[string]any{
				"threadId":            turn.ThreadID,
				"expectedTurnId":      turn.ID,
				"clientUserMessageId": "unsupported-image",
				"input": []map[string]any{{
					"type": "image",
					"url":  "image.png",
				}},
			},
		},
		{
			name: "oversized message",
			params: runtimeSteerTestParams(
				turn.ThreadID, turn.ID, "oversized-message",
				strings.Repeat("x", runtimeSteerMessageMaxSize+1),
			),
		},
		{
			name: "oversized client ID",
			params: runtimeSteerTestParams(
				turn.ThreadID, turn.ID,
				strings.Repeat("x", runtimeSteerIDMaxSize+1), "do not enqueue",
			),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := server.HandleRequest(ctx, request("turn/steer", tc.params))
			if resp.Error == nil || resp.Error.Code != protocol.CodeInvalidParams {
				t.Fatalf("turn/steer error = %#v, want invalid params", resp.Error)
			}
		})
	}

	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: turn.ThreadID, TurnID: turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	for _, item := range items {
		if item.Kind == runtimeSteerItemKind {
			t.Fatalf("rejected steer persisted item %#v", item)
		}
	}
	if interrupt := server.HandleRequest(ctx, request("turn/interrupt", map[string]any{"turnId": turn.ID})); interrupt.Error != nil {
		t.Fatalf("turn/interrupt error: %v", interrupt.Error)
	}
	waitForNotificationSet(t, server, "turn/completed")
	if got := len(model.callsSnapshot()); got != 1 {
		t.Fatalf("model calls = %d, want only initial request", got)
	}
}

func TestServerRuntimeTurnSteerIsIdempotentWhileActive(t *testing.T) {
	ctx, st, server, model, turn := startSteerRuntimeTest(t)
	params := runtimeSteerTestParams(
		turn.ThreadID, turn.ID, "stable-client-message", "adjust once",
	)
	for attempt := 1; attempt <= 2; attempt++ {
		resp := server.HandleRequest(ctx, request("turn/steer", params))
		if resp.Error != nil {
			t.Fatalf("turn/steer attempt %d error: %v", attempt, resp.Error)
		}
		var result protocol.TurnSteerResponse
		decodeResult(t, resp, &result)
		if result.TurnID != turn.ID {
			t.Fatalf("turn/steer attempt %d result = %#v", attempt, result)
		}
	}
	conflict := server.HandleRequest(ctx, request("turn/steer", runtimeSteerTestParams(
		turn.ThreadID, turn.ID, "stable-client-message", "different instruction",
	)))
	if conflict.Error == nil || conflict.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("conflicting turn/steer error = %#v, want invalid params", conflict.Error)
	}

	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: turn.ThreadID, TurnID: turn.ID})
	if err != nil {
		t.Fatalf("ListItems before consume: %v", err)
	}
	if got := runtimeSteerItemCount(items); got != 1 {
		t.Fatalf("steer items before consume = %d, want 1", got)
	}

	model.releaseFirst()
	waitForNotificationSet(t, server, "turn/completed")
	calls := model.callsSnapshot()
	if len(calls) != 2 || runtimeMessageTextCount(calls[1], "adjust once") != 1 {
		t.Fatalf("model calls = %#v, want one idempotent steer in second request", calls)
	}
	items, err = st.ListItems(ctx, store.ItemFilter{ThreadID: turn.ThreadID, TurnID: turn.ID})
	if err != nil {
		t.Fatalf("ListItems after consume: %v", err)
	}
	if got := runtimeSteerItemCount(items); got != 1 {
		t.Fatalf("steer items after consume = %d, want 1", got)
	}
}

func TestServerRuntimeTurnSteerQueueCapacityIsDurableAndIdempotent(t *testing.T) {
	ctx, st, server, _, turn := startSteerRuntimeTest(t)
	for i := range core.SteerQueueMaxPending {
		resp := server.HandleRequest(ctx, request("turn/steer", runtimeSteerTestParams(
			turn.ThreadID,
			turn.ID,
			"capacity-"+strings.Repeat("x", i+1),
			"queued instruction",
		)))
		if resp.Error != nil {
			t.Fatalf("turn/steer %d error: %v", i, resp.Error)
		}
	}
	overflow := runtimeSteerTestParams(
		turn.ThreadID, turn.ID, "capacity-overflow", "overflow instruction",
	)
	for attempt := 1; attempt <= 2; attempt++ {
		resp := server.HandleRequest(ctx, request("turn/steer", overflow))
		if resp.Error == nil || resp.Error.Code != protocol.CodeOverloaded {
			t.Fatalf("overflow attempt %d error = %#v, want overloaded", attempt, resp.Error)
		}
	}

	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: turn.ThreadID, TurnID: turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if got := runtimeSteerItemCount(items); got != core.SteerQueueMaxPending+1 {
		t.Fatalf("steer items = %d, want %d", got, core.SteerQueueMaxPending+1)
	}
	foundOverflow := false
	for _, item := range items {
		if item.Kind != runtimeSteerItemKind {
			continue
		}
		var payload runtimeSteerPayload
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			t.Fatalf("decode steer payload: %v", err)
		}
		if payload.ClientUserMessageID == "capacity-overflow" {
			foundOverflow = true
			if item.Status != runtimeSteerStatusFailed ||
				payload.Status != runtimeSteerStatusFailed ||
				!strings.Contains(payload.Error, core.ErrSteerQueueFull.Error()) {
				t.Fatalf("overflow item = %#v, payload = %#v", item, payload)
			}
		}
	}
	if !foundOverflow {
		t.Fatal("durable overflow steer item not found")
	}
	if interrupt := server.HandleRequest(ctx, request("turn/interrupt", map[string]any{"turnId": turn.ID})); interrupt.Error != nil {
		t.Fatalf("turn/interrupt error: %v", interrupt.Error)
	}
	waitForNotificationSet(t, server, "turn/completed")
}

func TestServerRuntimeTurnSteerFailsWhenInterruptedBeforeConsumption(t *testing.T) {
	ctx, st, server, model, turn := startSteerRuntimeTest(t)
	resp := server.HandleRequest(ctx, request("turn/steer", runtimeSteerTestParams(
		turn.ThreadID, turn.ID, "interrupt-client-message", "never consume",
	)))
	if resp.Error != nil {
		t.Fatalf("turn/steer error: %v", resp.Error)
	}
	if interrupt := server.HandleRequest(ctx, request("turn/interrupt", map[string]any{"turnId": turn.ID})); interrupt.Error != nil {
		t.Fatalf("turn/interrupt error: %v", interrupt.Error)
	}
	notifications := waitForNotificationSet(t, server, "turn/completed")
	assertRuntimeSteerNotificationOrder(t, notifications)

	if got := len(model.callsSnapshot()); got != 1 {
		t.Fatalf("model calls = %d, want no request containing interrupted steer", got)
	}
	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: turn.ThreadID, TurnID: turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	var steerItem *store.Item
	for _, item := range items {
		if item.Kind == runtimeSteerItemKind {
			steerItem = item
			break
		}
	}
	if steerItem == nil || steerItem.Status != runtimeSteerStatusFailed {
		t.Fatalf("steer item = %#v, want failed", steerItem)
	}
	var payload runtimeSteerPayload
	if err := json.Unmarshal(steerItem.Payload, &payload); err != nil {
		t.Fatalf("decode steer payload: %v", err)
	}
	if payload.Status != runtimeSteerStatusFailed ||
		payload.FailedAt == nil ||
		payload.ConsumedAt != nil ||
		payload.Error == "" {
		t.Fatalf("steer payload = %#v, want rejected before consumption", payload)
	}
}

func TestServerRuntimeTurnSteerFailsWhenModelFactoryCannotStart(t *testing.T) {
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	factoryStarted := make(chan struct{})
	releaseFactory := make(chan struct{})
	runtimeSvc := NewRuntimeService(WithRuntimeModelFactory(func(
		context.Context,
		RuntimeModelSelection,
	) (core.Model, RuntimeModelInfo, error) {
		close(factoryStarted)
		<-releaseFactory
		return nil, RuntimeModelInfo{ProviderID: "unavailable"}, errors.New("model factory unavailable")
	}))
	server := readyServer(WithStore(st), WithRuntimeService(runtimeSvc))
	startResp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "wait for factory"}))
	if startResp.Error != nil {
		t.Fatalf("thread/start error: %v", startResp.Error)
	}
	var started struct {
		Turn *store.Turn `json:"turn"`
	}
	decodeResult(t, startResp, &started)
	select {
	case <-factoryStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("model factory did not start")
	}

	steerResp := server.HandleRequest(ctx, request("turn/steer", runtimeSteerTestParams(
		started.Turn.ThreadID, started.Turn.ID, "factory-failure", "cannot consume",
	)))
	if steerResp.Error != nil {
		t.Fatalf("turn/steer error: %v", steerResp.Error)
	}
	close(releaseFactory)
	notifications := waitForNotificationSet(t, server, "turn/completed")
	assertRuntimeSteerNotificationOrder(t, notifications)

	items, err := st.ListItems(ctx, store.ItemFilter{
		ThreadID: started.Turn.ThreadID,
		TurnID:   started.Turn.ID,
	})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	for _, item := range items {
		if item.Kind != runtimeSteerItemKind {
			continue
		}
		if item.Status != runtimeSteerStatusFailed {
			t.Fatalf("steer item status = %q, want failed", item.Status)
		}
		var payload runtimeSteerPayload
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			t.Fatalf("decode steer payload: %v", err)
		}
		if payload.FailedAt == nil || payload.ConsumedAt != nil || !strings.Contains(payload.Error, "model factory unavailable") {
			t.Fatalf("steer payload = %#v", payload)
		}
		return
	}
	t.Fatal("steer item not found")
}

func TestServerRuntimeTurnSteerFailsClosedWhenConsumptionBoundaryCannotPersist(t *testing.T) {
	ctx := context.Background()
	base := newRuntimeTestStore(t)
	failing := &failNextRuntimeItemListStore{Store: base}
	model := newSteerRuntimeModel()
	server := readyServer(
		WithStore(failing),
		WithRuntimeService(NewRuntimeService(WithRuntimeModel(
			model,
			RuntimeModelInfo{ProviderID: "test", Model: "steer"},
		))),
	)
	startResp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "block"}))
	if startResp.Error != nil {
		t.Fatalf("thread/start error: %v", startResp.Error)
	}
	var started struct {
		Turn *store.Turn `json:"turn"`
	}
	decodeResult(t, startResp, &started)
	model.waitStarted(t)
	steerResp := server.HandleRequest(ctx, request("turn/steer", runtimeSteerTestParams(
		started.Turn.ThreadID, started.Turn.ID, "boundary-failure", "fail closed",
	)))
	if steerResp.Error != nil {
		t.Fatalf("turn/steer error: %v", steerResp.Error)
	}

	failing.arm()
	model.releaseFirst()
	waitForNotificationSet(t, server, "turn/completed")
	turn, err := base.GetTurn(ctx, started.Turn.ID)
	if err != nil {
		t.Fatalf("GetTurn: %v", err)
	}
	if turn.Status != store.TurnFailed || !strings.Contains(turn.Error, "item boundary unavailable") {
		t.Fatalf("turn = %#v, want failed consumption acknowledgement", turn)
	}
	items, err := base.ListItems(ctx, store.ItemFilter{
		ThreadID: started.Turn.ThreadID,
		TurnID:   started.Turn.ID,
	})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	for _, item := range items {
		if item.Kind != runtimeSteerItemKind {
			continue
		}
		var payload runtimeSteerPayload
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			t.Fatalf("decode steer payload: %v", err)
		}
		if item.Status != runtimeSteerStatusFailed ||
			payload.Status != runtimeSteerStatusFailed ||
			payload.ConsumedAfterSeq != 0 ||
			payload.ConsumedAt != nil ||
			payload.FailedAt == nil {
			t.Fatalf("failed steer item = %#v, payload = %#v", item, payload)
		}
		return
	}
	t.Fatal("steer item not found")
}

func startSteerRuntimeTest(
	t *testing.T,
) (context.Context, *store.SQLiteStore, *Server, *steerRuntimeModel, *store.Turn) {
	t.Helper()
	ctx := context.Background()
	st := newRuntimeTestStore(t)
	model := newSteerRuntimeModel()
	server := readyServer(
		WithStore(st),
		WithRuntimeService(NewRuntimeService(WithRuntimeModel(
			model,
			RuntimeModelInfo{ProviderID: "test", Model: "steer"},
		))),
	)
	startResp := server.HandleRequest(ctx, request("thread/start", map[string]any{"prompt": "block"}))
	if startResp.Error != nil {
		t.Fatalf("thread/start error: %v", startResp.Error)
	}
	var started struct {
		Turn *store.Turn `json:"turn"`
	}
	decodeResult(t, startResp, &started)
	model.waitStarted(t)
	return ctx, st, server, model, started.Turn
}

func runtimeSteerTestParams(threadID, turnID, clientID, message string) map[string]any {
	return map[string]any{
		"threadId":            threadID,
		"expectedTurnId":      turnID,
		"clientUserMessageId": clientID,
		"input": []map[string]any{{
			"type":          "text",
			"text":          message,
			"text_elements": []any{},
		}},
	}
}

func runtimeSteerItemCount(items []*store.Item) int {
	count := 0
	for _, item := range items {
		if item != nil && item.Kind == runtimeSteerItemKind {
			count++
		}
	}
	return count
}

func assertRuntimeSteerNotificationOrder(t *testing.T, notifications []protocol.Notification) {
	t.Helper()
	started := -1
	completed := -1
	for i, notification := range notifications {
		if notification.Method != "item/started" && notification.Method != "item/completed" {
			continue
		}
		var params runtimeItemNotificationParams
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			t.Fatalf("decode %s: %v", notification.Method, err)
		}
		if params.Item == nil || params.Item.Kind != runtimeSteerItemKind {
			continue
		}
		switch notification.Method {
		case "item/started":
			started = i
		case "item/completed":
			completed = i
		}
	}
	if started < 0 || completed < 0 || started >= completed {
		t.Fatalf("steer lifecycle order = started:%d completed:%d in %v", started, completed, notificationMethods(notifications))
	}
}

func assertRuntimeSteerDeltaBoundary(t *testing.T, notifications []protocol.Notification) {
	t.Helper()
	firstDelta := -1
	completed := -1
	secondDelta := -1
	for i, notification := range notifications {
		switch notification.Method {
		case "item/agentMessage/delta":
			var params runtimeDeltaNotificationParams
			if err := json.Unmarshal(notification.Params, &params); err != nil {
				t.Fatalf("decode agent-message delta: %v", err)
			}
			switch params.Delta {
			case "first answer":
				firstDelta = i
			case "steered answer":
				secondDelta = i
			}
		case "item/completed":
			var params runtimeItemNotificationParams
			if err := json.Unmarshal(notification.Params, &params); err != nil {
				t.Fatalf("decode item/completed: %v", err)
			}
			if params.Item != nil && params.Item.Kind == runtimeSteerItemKind {
				completed = i
			}
		}
	}
	if firstDelta < 0 || completed < 0 || secondDelta < 0 ||
		firstDelta >= completed || completed >= secondDelta {
		t.Fatalf(
			"steer delta boundary = first:%d completed:%d second:%d in %v",
			firstDelta,
			completed,
			secondDelta,
			notificationMethods(notifications),
		)
	}
}

func newRuntimeTestStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func waitForNotificationSet(t *testing.T, server *Server, want ...string) []protocol.Notification {
	t.Helper()
	remaining := make(map[string]int, len(want))
	for _, method := range want {
		remaining[method]++
	}
	var seen []protocol.Notification
	timeout := time.After(3 * time.Second)
	for len(remaining) > 0 {
		select {
		case <-server.NotificationSignal():
			for _, notification := range server.DrainNotifications() {
				seen = append(seen, notification)
				if remaining[notification.Method] > 1 {
					remaining[notification.Method]--
				} else if remaining[notification.Method] == 1 {
					delete(remaining, notification.Method)
				}
			}
		case <-timeout:
			t.Fatalf("timed out waiting for %v; seen=%v", remaining, notificationMethods(seen))
		}
	}
	return seen
}

func notificationMethods(notifications []protocol.Notification) []string {
	methods := make([]string, len(notifications))
	for i, notification := range notifications {
		methods[i] = notification.Method
	}
	return methods
}

type runtimeToolNotification struct {
	Method string
	Params struct {
		ThreadID      string                         `json:"threadId"`
		TurnID        string                         `json:"turnId"`
		Item          runtimeDynamicToolCallTestItem `json:"item"`
		StartedAtMS   int64                          `json:"startedAtMs"`
		CompletedAtMS int64                          `json:"completedAtMs"`
	}
}

type runtimeDynamicToolCallTestItem struct {
	Type         string                                  `json:"type"`
	ID           string                                  `json:"id"`
	Namespace    *string                                 `json:"namespace"`
	Tool         string                                  `json:"tool"`
	Arguments    map[string]any                          `json:"arguments"`
	Status       string                                  `json:"status"`
	ContentItems []runtimeDynamicToolCallTestContentItem `json:"contentItems"`
	Success      *bool                                   `json:"success"`
	DurationMS   *int64                                  `json:"durationMs"`
}

type runtimeDynamicToolCallTestContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func runtimeToolNotifications(t *testing.T, notifications []protocol.Notification) []runtimeToolNotification {
	t.Helper()
	var out []runtimeToolNotification
	for _, notification := range notifications {
		if notification.Method != "item/started" && notification.Method != "item/completed" {
			continue
		}
		var decoded runtimeToolNotification
		decoded.Method = notification.Method
		if err := json.Unmarshal(notification.Params, &decoded.Params); err != nil {
			t.Fatalf("decode %s: %v", notification.Method, err)
		}
		if decoded.Params.Item.Type == "dynamicToolCall" {
			out = append(out, decoded)
		}
	}
	return out
}

type storedRuntimeToolItem struct {
	Item    *store.Item
	Status  string
	Payload runtimeDynamicToolCallTestItem
}

func findRuntimeToolItem(t *testing.T, items []*store.Item, tool, argumentKey string, argumentValue any) storedRuntimeToolItem {
	t.Helper()
	for _, item := range items {
		if item == nil || item.Kind != "dynamicToolCall" {
			continue
		}
		var payload runtimeDynamicToolCallTestItem
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			t.Fatalf("decode stored dynamic tool item: %v", err)
		}
		if payload.Tool == tool && payload.Arguments[argumentKey] == argumentValue {
			return storedRuntimeToolItem{Item: item, Status: item.Status, Payload: payload}
		}
	}
	t.Fatalf("dynamic tool item %q with %s=%v not found in %#v", tool, argumentKey, argumentValue, items)
	return storedRuntimeToolItem{}
}

func decodeThreadTokenUsageNotification(t *testing.T, notifications []protocol.Notification) threadTokenUsageUpdatedNotificationParams {
	t.Helper()
	for _, notification := range notifications {
		if notification.Method != "thread/tokenUsage/updated" {
			continue
		}
		var params threadTokenUsageUpdatedNotificationParams
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			t.Fatalf("decode thread/tokenUsage/updated params: %v", err)
		}
		return params
	}
	t.Fatalf("thread/tokenUsage/updated notification missing from %v", notificationMethods(notifications))
	return threadTokenUsageUpdatedNotificationParams{}
}

func assertTokenUsageBreakdown(t *testing.T, label string, got tokenUsageBreakdown, total, input, cachedInput, output, reasoningOutput int64) {
	t.Helper()
	want := tokenUsageBreakdown{
		TotalTokens:           total,
		InputTokens:           input,
		CachedInputTokens:     cachedInput,
		OutputTokens:          output,
		ReasoningOutputTokens: reasoningOutput,
	}
	if got != want {
		t.Fatalf("%s usage = %+v, want %+v", label, got, want)
	}
}

func assertRuntimeUserPrompt(t *testing.T, message core.ModelMessage, want string) {
	t.Helper()
	req, ok := message.(core.ModelRequest)
	if !ok || len(req.Parts) != 1 {
		t.Fatalf("message = %#v, want one-part user request", message)
	}
	part, ok := req.Parts[0].(core.UserPromptPart)
	if !ok || part.Content != want {
		t.Fatalf("request part = %#v, want user prompt %q", req.Parts[0], want)
	}
}

func assertRuntimeSystemPromptContains(t *testing.T, message core.ModelMessage, want ...string) {
	t.Helper()
	req, ok := message.(core.ModelRequest)
	if !ok || len(req.Parts) != 1 {
		t.Fatalf("message = %#v, want one-part system request", message)
	}
	part, ok := req.Parts[0].(core.SystemPromptPart)
	if !ok {
		t.Fatalf("request part = %#v, want system prompt", req.Parts[0])
	}
	for _, text := range want {
		if !strings.Contains(part.Content, text) {
			t.Fatalf("system prompt = %q, want substring %q", part.Content, text)
		}
	}
}

func assertRuntimeAssistantText(t *testing.T, message core.ModelMessage, want string) {
	t.Helper()
	resp, ok := message.(core.ModelResponse)
	if !ok || resp.TextContent() != want {
		t.Fatalf("message = %#v, want assistant text %q", message, want)
	}
}

type blockingRuntimeModel struct {
	started chan struct{}
}

func (m *blockingRuntimeModel) Request(ctx context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (*core.ModelResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (m *blockingRuntimeModel) RequestStream(ctx context.Context, _ []core.ModelMessage, _ *core.ModelSettings, _ *core.ModelRequestParameters) (core.StreamedResponse, error) {
	close(m.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (m *blockingRuntimeModel) ModelName() string {
	return "blocking"
}

func waitForBlockingModel(t *testing.T, model *blockingRuntimeModel) {
	t.Helper()
	select {
	case <-model.started:
	case <-time.After(2 * time.Second):
		t.Fatal("blocking model did not start")
	}
}

type steerRuntimeModel struct {
	mu      sync.Mutex
	calls   [][]core.ModelMessage
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newSteerRuntimeModel() *steerRuntimeModel {
	return &steerRuntimeModel{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (m *steerRuntimeModel) Request(
	context.Context,
	[]core.ModelMessage,
	*core.ModelSettings,
	*core.ModelRequestParameters,
) (*core.ModelResponse, error) {
	return nil, errors.New("streaming required")
}

func (m *steerRuntimeModel) RequestStream(
	ctx context.Context,
	messages []core.ModelMessage,
	_ *core.ModelSettings,
	_ *core.ModelRequestParameters,
) (core.StreamedResponse, error) {
	m.mu.Lock()
	call := append([]core.ModelMessage(nil), messages...)
	m.calls = append(m.calls, call)
	index := len(m.calls)
	m.mu.Unlock()
	if index == 1 {
		m.once.Do(func() { close(m.started) })
		return &gatedRuntimeStream{
			ctx:      ctx,
			release:  m.release,
			response: core.TextResponse("first answer"),
		}, nil
	}
	return newRuntimeResponseStream(core.TextResponse("steered answer")), nil
}

func (*steerRuntimeModel) ModelName() string { return "steer" }

func (m *steerRuntimeModel) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-m.started:
	case <-time.After(2 * time.Second):
		t.Fatal("steer model did not start")
	}
}

func (m *steerRuntimeModel) releaseFirst() {
	close(m.release)
}

func (m *steerRuntimeModel) callsSnapshot() [][]core.ModelMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]core.ModelMessage, len(m.calls))
	for i := range m.calls {
		out[i] = append([]core.ModelMessage(nil), m.calls[i]...)
	}
	return out
}

func runtimeMessagesContainText(messages []core.ModelMessage, want string) bool {
	return runtimeMessageTextCount(messages, want) > 0
}

func runtimeMessageTextCount(messages []core.ModelMessage, want string) int {
	count := 0
	for _, message := range messages {
		request, ok := message.(core.ModelRequest)
		if !ok {
			continue
		}
		for _, part := range request.Parts {
			if prompt, ok := part.(core.UserPromptPart); ok && prompt.Content == want {
				count++
			}
		}
	}
	return count
}

type gatedRuntimeStream struct {
	ctx      context.Context
	release  <-chan struct{}
	response *core.ModelResponse
	inner    core.StreamedResponse
}

func (s *gatedRuntimeStream) Next() (core.ModelResponseStreamEvent, error) {
	if s.inner == nil {
		select {
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		case <-s.release:
			s.inner = newRuntimeResponseStream(s.response)
		}
	}
	return s.inner.Next()
}

func (s *gatedRuntimeStream) Response() *core.ModelResponse {
	if s.inner == nil {
		return s.response
	}
	return s.inner.Response()
}

func (s *gatedRuntimeStream) Usage() core.Usage {
	if s.inner == nil {
		return s.response.Usage
	}
	return s.inner.Usage()
}

func (s *gatedRuntimeStream) Close() error {
	if s.inner != nil {
		return s.inner.Close()
	}
	return nil
}

func newRuntimeResponseStream(response *core.ModelResponse) core.StreamedResponse {
	return &runtimeResponseStream{response: response}
}

type runtimeResponseStream struct {
	response *core.ModelResponse
	phase    int
}

func (s *runtimeResponseStream) Next() (core.ModelResponseStreamEvent, error) {
	switch s.phase {
	case 0:
		s.phase++
		return core.PartStartEvent{Index: 0, Part: s.response.Parts[0]}, nil
	case 1:
		s.phase++
		return core.PartEndEvent{Index: 0}, nil
	default:
		return nil, io.EOF
	}
}

func (s *runtimeResponseStream) Response() *core.ModelResponse { return s.response }
func (s *runtimeResponseStream) Usage() core.Usage             { return s.response.Usage }
func (s *runtimeResponseStream) Close() error                  { return nil }
