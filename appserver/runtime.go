package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
	"github.com/fugue-labs/gollem/core"
)

var (
	ErrRuntimeNotConfigured = errors.New("appserver/runtime: model factory is not configured")
	ErrRuntimeTurnActive    = errors.New("appserver/runtime: turn is already running")
	ErrRuntimeTurnNotActive = errors.New("appserver/runtime: turn is not running")
	ErrRuntimePromptEmpty   = errors.New("appserver/runtime: prompt is required")
	ErrRuntimeShuttingDown  = errors.New("appserver/runtime: runtime is shutting down")
)

type RuntimeModelSelection struct {
	ProviderID string `json:"providerId,omitempty"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
}

type RuntimeModelInfo struct {
	ProviderID string `json:"providerId,omitempty"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
}

type RuntimeModelFactory func(context.Context, RuntimeModelSelection) (core.Model, RuntimeModelInfo, error)

type RuntimeOption func(*RuntimeService)

func WithRuntimeModelFactory(factory RuntimeModelFactory) RuntimeOption {
	return func(s *RuntimeService) {
		s.modelFactory = factory
	}
}

func WithRuntimeModel(model core.Model, info RuntimeModelInfo) RuntimeOption {
	return WithRuntimeModelFactory(func(context.Context, RuntimeModelSelection) (core.Model, RuntimeModelInfo, error) {
		if model == nil {
			return nil, RuntimeModelInfo{}, ErrRuntimeNotConfigured
		}
		if info.Model == "" {
			info.Model = model.ModelName()
		}
		return model, info, nil
	})
}

// WithRuntimeTools registers provider-neutral core tools for app-server turns.
// Tool handlers are shared across turns and should be safe for concurrent use.
func WithRuntimeTools(tools ...core.Tool) RuntimeOption {
	cloned := append([]core.Tool(nil), tools...)
	return func(s *RuntimeService) {
		s.tools = append(s.tools, cloned...)
	}
}

type RuntimeService struct {
	startMu      sync.Mutex
	mu           sync.Mutex
	modelFactory RuntimeModelFactory
	tools        []core.Tool
	active       map[string]*activeRuntimeTurn
	shuttingDown bool
	wg           sync.WaitGroup
}

func NewRuntimeService(opts ...RuntimeOption) *RuntimeService {
	s := &RuntimeService{
		active: make(map[string]*activeRuntimeTurn),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

type RuntimeStartRequest struct {
	ThreadID      string
	Prompt        string
	Input         json.RawMessage
	Metadata      map[string]any
	Selection     RuntimeModelSelection
	ModelSettings core.ModelSettings
	History       []core.ModelMessage
}

type RuntimeStartResult struct {
	Turn *store.Turn `json:"turn"`
}

type RuntimeInterruptResult struct {
	OK     bool        `json:"ok"`
	TurnID string      `json:"turnId"`
	Turn   *store.Turn `json:"turn,omitempty"`
}

type runtimeNotifier interface {
	PublishNotification(method string, params any)
}

type activeRuntimeTurn struct {
	cancel context.CancelFunc
}

func (s *RuntimeService) Start(ctx context.Context, st store.Store, notifier runtimeNotifier, req RuntimeStartRequest) (*RuntimeStartResult, error) {
	if s == nil || s.modelFactory == nil {
		return nil, ErrRuntimeNotConfigured
	}
	if st == nil {
		return nil, ErrRuntimeNotConfigured
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		return nil, ErrRuntimePromptEmpty
	}
	s.startMu.Lock()
	defer s.startMu.Unlock()
	s.mu.Lock()
	if s.shuttingDown {
		s.mu.Unlock()
		return nil, ErrRuntimeShuttingDown
	}
	s.mu.Unlock()
	if len(req.Input) == 0 {
		input, err := json.Marshal(runtimeTurnInput{
			Prompt:      req.Prompt,
			ProviderID:  req.Selection.ProviderID,
			Provider:    req.Selection.Provider,
			Model:       req.Selection.Model,
			Metadata:    cloneRuntimeMap(req.Metadata),
			SubmittedAt: time.Now().UTC(),
		})
		if err != nil {
			return nil, fmt.Errorf("marshal runtime input: %w", err)
		}
		req.Input = input
	}
	turn, err := st.CreateTurn(ctx, store.CreateTurnRequest{
		ThreadID: req.ThreadID,
		Input:    cloneRuntimeRaw(req.Input),
		Metadata: cloneRuntimeMap(req.Metadata),
	})
	if err != nil {
		return nil, err
	}
	if _, err := st.AppendItem(ctx, store.AppendItemRequest{
		ThreadID: turn.ThreadID,
		TurnID:   turn.ID,
		Kind:     "message",
		Status:   "completed",
		Payload:  mustRuntimeJSON(runtimeMessagePayload{Role: "user", Text: req.Prompt, CreatedAt: time.Now().UTC()}),
	}); err != nil {
		return nil, err
	}
	started, err := st.StartTurn(ctx, turn.ID)
	if err != nil {
		return nil, err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if _, exists := s.active[started.ID]; exists {
		s.mu.Unlock()
		cancel()
		return nil, ErrRuntimeTurnActive
	}
	s.active[started.ID] = &activeRuntimeTurn{cancel: cancel}
	s.wg.Add(1)
	s.mu.Unlock()

	publishTurnStarted(notifier, started)
	go func() {
		defer s.wg.Done()
		s.run(runCtx, st, notifier, started, req)
	}()
	return &RuntimeStartResult{Turn: started}, nil
}

func (s *RuntimeService) Interrupt(ctx context.Context, st store.Store, turnID string) (*RuntimeInterruptResult, error) {
	if s == nil {
		return nil, ErrRuntimeNotConfigured
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return nil, store.ErrTurnNotFound
	}
	s.mu.Lock()
	active := s.active[turnID]
	s.mu.Unlock()
	if active == nil {
		turn, err := st.GetTurn(ctx, turnID)
		if err != nil {
			return nil, err
		}
		return &RuntimeInterruptResult{OK: false, TurnID: turnID, Turn: turn}, ErrRuntimeTurnNotActive
	}
	active.cancel()
	turn, _ := st.GetTurn(ctx, turnID)
	return &RuntimeInterruptResult{OK: true, TurnID: turnID, Turn: turn}, nil
}

func (s *RuntimeService) IsActive(turnID string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.active[turnID]
	return ok
}

func (s *RuntimeService) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.startMu.Lock()
	defer s.startMu.Unlock()
	s.mu.Lock()
	s.shuttingDown = true
	for _, active := range s.active {
		if active != nil && active.cancel != nil {
			active.cancel()
		}
	}
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *RuntimeService) run(ctx context.Context, st store.Store, notifier runtimeNotifier, turn *store.Turn, req RuntimeStartRequest) {
	defer func() {
		s.mu.Lock()
		delete(s.active, turn.ID)
		s.mu.Unlock()
	}()

	ctx = withRuntimeTurnContext(ctx, turn.ThreadID, turn.ID)
	model, info, err := s.modelFactory(ctx, req.Selection)
	if err != nil {
		s.complete(st, notifier, turn, store.TurnFailed, nil, err, info)
		return
	}
	defer closeRuntimeModel(model)
	if info.Model == "" && model != nil {
		info.Model = model.ModelName()
	}

	bus := core.NewEventBus()
	defer bus.Close()
	unsubscribeDelta := core.Subscribe(bus, func(event core.ModelDeltaEvent) {
		publishModelDelta(notifier, turn, event)
	})
	defer unsubscribeDelta()
	unsubscribeError := core.Subscribe(bus, func(event core.ErrorRaisedEvent) {
		publishRuntimeError(notifier, turn, event.Error)
	})
	defer unsubscribeError()
	toolItems := newRuntimeToolItemTracker(st, notifier, turn, s.tools)
	unsubscribeToolCalled := core.Subscribe(bus, toolItems.toolCalled)
	defer unsubscribeToolCalled()
	unsubscribeToolCompleted := core.Subscribe(bus, toolItems.toolCompleted)
	defer unsubscribeToolCompleted()
	unsubscribeToolFailed := core.Subscribe(bus, toolItems.toolFailed)
	defer unsubscribeToolFailed()
	unsubscribeToolItemID := core.Subscribe(bus, toolItems.resolveItemID)
	defer unsubscribeToolItemID()
	fileChangeItems := newRuntimeFileChangeTracker(st, notifier, turn, toolItems)
	unsubscribeArtifactChanged := core.Subscribe(bus, fileChangeItems.artifactChanged)
	defer unsubscribeArtifactChanged()
	commandItems := newRuntimeCommandItemTracker(st, notifier, turn, toolItems)
	unsubscribeCommandStarted := core.Subscribe(bus, commandItems.commandStarted)
	defer unsubscribeCommandStarted()
	unsubscribeCommandOutput := core.Subscribe(bus, commandItems.commandOutput)
	defer unsubscribeCommandOutput()
	unsubscribeCommandCompleted := core.Subscribe(bus, commandItems.commandCompleted)
	defer unsubscribeCommandCompleted()
	mcpItems := newRuntimeMCPItemTracker(st, notifier, turn, toolItems)
	unsubscribeMCPStarted := core.Subscribe(bus, mcpItems.toolStarted)
	defer unsubscribeMCPStarted()
	unsubscribeMCPProgress := core.Subscribe(bus, mcpItems.toolProgress)
	defer unsubscribeMCPProgress()
	unsubscribeMCPCompleted := core.Subscribe(bus, mcpItems.toolCompleted)
	defer unsubscribeMCPCompleted()

	agentOptions := []core.AgentOption[string]{core.WithEventBus[string](bus)}
	if len(s.tools) > 0 {
		agentOptions = append(agentOptions, core.WithTools[string](s.tools...))
	}
	agent := core.NewAgent[string](model, agentOptions...)
	runOpts := make([]core.RunOption, 0, 2)
	if len(req.History) > 0 {
		runOpts = append(runOpts, core.WithMessages(req.History...))
	}
	if hasRuntimeModelSettings(req.ModelSettings) {
		runOpts = append(runOpts, core.WithRunModelSettings(req.ModelSettings))
	}
	stream, err := agent.RunStream(ctx, req.Prompt, runOpts...)
	if err != nil {
		s.complete(st, notifier, turn, statusFromRuntimeError(err), nil, err, info)
		return
	}
	defer stream.Close()

	var streamErr error
	for _, err := range stream.StreamEvents() {
		if err != nil {
			streamErr = err
			break
		}
	}
	result, err := stream.Result()
	if err == nil {
		err = streamErr
	}
	if err == nil {
		err = toolItems.Err()
	}
	if err == nil {
		err = fileChangeItems.Err()
	}
	if err == nil {
		err = commandItems.Err()
	}
	if err == nil {
		err = mcpItems.Err()
	}
	s.complete(st, notifier, turn, statusFromRuntimeError(err), result, err, info)
}

func (s *RuntimeService) complete(st store.Store, notifier runtimeNotifier, turn *store.Turn, status store.TurnStatus, result *core.RunResult[string], runErr error, info RuntimeModelInfo) {
	resultPayload := runtimeResultPayload{
		ProviderID:  info.ProviderID,
		Provider:    info.Provider,
		Model:       info.Model,
		CompletedAt: time.Now().UTC(),
	}
	var usage map[string]any
	if result != nil {
		resultPayload.Output = result.Output
		resultPayload.RunID = result.RunID
		resultPayload.Text = lastRuntimeAssistantText(result.Messages)
		resultPayload.ToolState = result.ToolState
		usage = runtimeUsageMap(result.Usage)
	}
	if resultPayload.Text != "" {
		item, err := st.AppendItem(context.Background(), store.AppendItemRequest{
			ThreadID: turn.ThreadID,
			TurnID:   turn.ID,
			Kind:     "message",
			Status:   "completed",
			Payload: mustRuntimeJSON(runtimeMessagePayload{
				Role:      "assistant",
				Text:      resultPayload.Text,
				Model:     info.Model,
				Provider:  firstRuntimeNonEmpty(info.ProviderID, info.Provider),
				CreatedAt: time.Now().UTC(),
			}),
		})
		if err == nil {
			publishItemCompleted(notifier, turn, item)
		}
	}
	var rawResult json.RawMessage
	if payload, err := json.Marshal(resultPayload); err == nil {
		rawResult = payload
	}
	errorText := ""
	if runErr != nil {
		errorText = runErr.Error()
	}
	completed, err := st.CompleteTurn(context.Background(), store.CompleteTurnRequest{
		ID:     turn.ID,
		Status: status,
		Result: rawResult,
		Error:  errorText,
		Usage:  usage,
	})
	if err == nil {
		if result != nil {
			publishThreadTokenUsage(notifier, st, completed, result.Usage)
		}
		publishTurnCompleted(notifier, completed)
	}
}

func statusFromRuntimeError(err error) store.TurnStatus {
	if err == nil {
		return store.TurnCompleted
	}
	if errors.Is(err, context.Canceled) {
		return store.TurnInterrupted
	}
	return store.TurnFailed
}

type runtimeTurnInput struct {
	Prompt      string         `json:"prompt"`
	ProviderID  string         `json:"providerId,omitempty"`
	Provider    string         `json:"provider,omitempty"`
	Model       string         `json:"model,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	SubmittedAt time.Time      `json:"submittedAt"`
}

type runtimeResultPayload struct {
	Output      string         `json:"output,omitempty"`
	Text        string         `json:"text,omitempty"`
	RunID       string         `json:"runId,omitempty"`
	ProviderID  string         `json:"providerId,omitempty"`
	Provider    string         `json:"provider,omitempty"`
	Model       string         `json:"model,omitempty"`
	ToolState   map[string]any `json:"toolState,omitempty"`
	CompletedAt time.Time      `json:"completedAt"`
}

type runtimeMessagePayload struct {
	Role      string    `json:"role"`
	Text      string    `json:"text,omitempty"`
	Model     string    `json:"model,omitempty"`
	Provider  string    `json:"provider,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type turnNotificationParams struct {
	ThreadID string           `json:"threadId"`
	TurnID   string           `json:"turnId"`
	Status   store.TurnStatus `json:"status,omitempty"`
	Turn     *store.Turn      `json:"turn,omitempty"`
	At       time.Time        `json:"at"`
}

type runtimeItemNotificationParams = protocol.ItemLifecycleNotificationParams

type runtimeDeltaNotificationParams struct {
	ThreadID string    `json:"threadId"`
	TurnID   string    `json:"turnId"`
	Delta    string    `json:"delta"`
	Index    int       `json:"index,omitempty"`
	At       time.Time `json:"at"`
}

type runtimeErrorNotificationParams struct {
	ThreadID string    `json:"threadId,omitempty"`
	TurnID   string    `json:"turnId,omitempty"`
	Error    string    `json:"error"`
	At       time.Time `json:"at"`
}

type threadTokenUsageUpdatedNotificationParams = protocol.ThreadTokenUsageUpdatedNotificationParams
type threadTokenUsagePayload = protocol.TokenUsage
type tokenUsageBreakdown = protocol.TokenUsageBreakdown

func publishTurnStarted(notifier runtimeNotifier, turn *store.Turn) {
	if notifier == nil || turn == nil {
		return
	}
	notifier.PublishNotification("turn/started", turnNotificationParams{
		ThreadID: turn.ThreadID,
		TurnID:   turn.ID,
		Status:   turn.Status,
		Turn:     turn,
		At:       time.Now().UTC(),
	})
}

func publishTurnCompleted(notifier runtimeNotifier, turn *store.Turn) {
	if notifier == nil || turn == nil {
		return
	}
	notifier.PublishNotification("turn/completed", turnNotificationParams{
		ThreadID: turn.ThreadID,
		TurnID:   turn.ID,
		Status:   turn.Status,
		Turn:     turn,
		At:       time.Now().UTC(),
	})
}

func publishItemCompleted(notifier runtimeNotifier, turn *store.Turn, item *store.Item) {
	if notifier == nil || turn == nil || item == nil {
		return
	}
	notifier.PublishNotification("item/completed", runtimeItemNotificationParams{
		ThreadID: turn.ThreadID,
		TurnID:   turn.ID,
		ItemID:   item.ID,
		Item:     protocolTimelineItem(item),
		At:       time.Now().UTC(),
	})
}

func protocolTimelineItem(item *store.Item) *protocol.TimelineItem {
	if item == nil {
		return nil
	}
	return &protocol.TimelineItem{
		ID:           item.ID,
		ThreadID:     item.ThreadID,
		TurnID:       item.TurnID,
		ParentItemID: item.ParentItemID,
		Seq:          item.Seq,
		Kind:         item.Kind,
		Status:       item.Status,
		Payload:      append(json.RawMessage(nil), item.Payload...),
		CreatedAt:    item.CreatedAt,
		UpdatedAt:    item.UpdatedAt,
	}
}

func publishThreadTokenUsage(notifier runtimeNotifier, st store.Store, turn *store.Turn, last core.RunUsage) {
	if notifier == nil || st == nil || turn == nil {
		return
	}
	total := threadTokenUsageTotal(context.Background(), st, turn.ThreadID, last)
	notifier.PublishNotification("thread/tokenUsage/updated", threadTokenUsageUpdatedNotificationParams{
		ThreadID: turn.ThreadID,
		TurnID:   turn.ID,
		TokenUsage: threadTokenUsagePayload{
			Total:              tokenUsageBreakdownFromUsage(total.Usage),
			Last:               tokenUsageBreakdownFromUsage(last.Usage),
			ModelContextWindow: nil,
		},
	})
}

func publishModelDelta(notifier runtimeNotifier, turn *store.Turn, event core.ModelDeltaEvent) {
	if notifier == nil || turn == nil || event.ContentDelta == "" {
		return
	}
	method := "item/agentMessage/delta"
	if event.DeltaKind == "thinking" {
		method = "item/reasoning/textDelta"
	}
	notifier.PublishNotification(method, runtimeDeltaNotificationParams{
		ThreadID: turn.ThreadID,
		TurnID:   turn.ID,
		Delta:    event.ContentDelta,
		Index:    event.PartIndex,
		At:       time.Now().UTC(),
	})
}

func publishRuntimeError(notifier runtimeNotifier, turn *store.Turn, text string) {
	if notifier == nil || text == "" {
		return
	}
	params := runtimeErrorNotificationParams{Error: text, At: time.Now().UTC()}
	if turn != nil {
		params.ThreadID = turn.ThreadID
		params.TurnID = turn.ID
	}
	notifier.PublishNotification("error", params)
}

func runtimeUsageMap(usage core.RunUsage) map[string]any {
	usageMap := map[string]any{
		"inputTokens":      usage.InputTokens,
		"outputTokens":     usage.OutputTokens,
		"cacheWriteTokens": usage.CacheWriteTokens,
		"cacheReadTokens":  usage.CacheReadTokens,
		"totalTokens":      usage.TotalTokens(),
	}
	if len(usage.Details) > 0 {
		details := make(map[string]any, len(usage.Details))
		for key, value := range usage.Details {
			details[key] = value
		}
		usageMap["details"] = details
	}
	return map[string]any{
		"requests":  usage.Requests,
		"toolCalls": usage.ToolCalls,
		"usage":     usageMap,
	}
}

func threadTokenUsageTotal(ctx context.Context, st store.Store, threadID string, fallback core.RunUsage) core.RunUsage {
	turns, err := st.ListTurns(ctx, store.TurnFilter{ThreadID: threadID})
	if err != nil {
		return fallback
	}
	var total core.RunUsage
	for _, turn := range turns {
		if turn == nil || len(turn.Usage) == 0 {
			continue
		}
		total.IncrRun(runtimeUsageFromStoredMap(turn.Usage))
	}
	if total.Requests == 0 && total.ToolCalls == 0 && total.TotalTokens() == 0 {
		return fallback
	}
	return total
}

func runtimeUsageFromStoredMap(values map[string]any) core.RunUsage {
	var usage core.RunUsage
	if len(values) == 0 {
		return usage
	}
	usage.Requests = runtimeIntFromAny(values["requests"])
	usage.ToolCalls = runtimeIntFromAny(values["toolCalls"])
	tokenValues := runtimeMapFromAny(values["usage"])
	if tokenValues == nil {
		tokenValues = values
	}
	usage.InputTokens = runtimeIntFromAny(tokenValues["inputTokens"])
	usage.OutputTokens = runtimeIntFromAny(tokenValues["outputTokens"])
	usage.CacheWriteTokens = runtimeIntFromAny(tokenValues["cacheWriteTokens"])
	usage.CacheReadTokens = runtimeIntFromAny(tokenValues["cacheReadTokens"])
	if details := runtimeMapFromAny(tokenValues["details"]); len(details) > 0 {
		usage.Details = make(map[string]int, len(details))
		for key, value := range details {
			usage.Details[key] = runtimeIntFromAny(value)
		}
	}
	return usage
}

func tokenUsageBreakdownFromUsage(usage core.Usage) tokenUsageBreakdown {
	return tokenUsageBreakdown{
		TotalTokens:           int64(usage.TotalTokens()),
		InputTokens:           int64(usage.InputTokens),
		CachedInputTokens:     int64(usage.CacheReadTokens),
		OutputTokens:          int64(usage.OutputTokens),
		ReasoningOutputTokens: int64(runtimeReasoningOutputTokens(usage.Details)),
	}
}

func runtimeReasoningOutputTokens(details map[string]int) int {
	for _, key := range []string{"reasoning_tokens", "reasoningTokens", "reasoningOutputTokens"} {
		if value := details[key]; value != 0 {
			return value
		}
	}
	return 0
}

func runtimeMapFromAny(value any) map[string]any {
	if value == nil {
		return nil
	}
	if mapped, ok := value.(map[string]any); ok {
		return mapped
	}
	return nil
}

func runtimeIntFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		if i, err := typed.Int64(); err == nil {
			return int(i)
		}
		if f, err := typed.Float64(); err == nil {
			return int(f)
		}
	}
	return 0
}

func lastRuntimeAssistantText(messages []core.ModelMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if resp, ok := messages[i].(core.ModelResponse); ok {
			return resp.TextContent()
		}
	}
	return ""
}

func runtimePromptFromInput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var obj struct {
		Prompt  string `json:"prompt"`
		Message string `json:"message"`
		Text    string `json:"text"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	return strings.TrimSpace(firstRuntimeNonEmpty(obj.Prompt, obj.Message, obj.Text))
}

func mustRuntimeJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}

func cloneRuntimeRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	out := make([]byte, len(raw))
	copy(out, raw)
	return out
}

func cloneRuntimeMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func firstRuntimeNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func closeRuntimeModel(model core.Model) {
	closer, ok := model.(interface{ Close() error })
	if !ok {
		return
	}
	_ = closer.Close()
}

func hasRuntimeModelSettings(settings core.ModelSettings) bool {
	return settings.MaxTokens != nil ||
		settings.Temperature != nil ||
		settings.TopP != nil ||
		settings.ToolChoice != nil ||
		settings.ThinkingBudget != nil ||
		settings.AdaptiveThinking != nil ||
		settings.ReasoningEffort != nil ||
		len(settings.StopSequences) > 0
}
