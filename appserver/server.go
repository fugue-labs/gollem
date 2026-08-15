package appserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	iofs "io/fs"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	appcache "github.com/fugue-labs/gollem/appserver/cache"
	"github.com/fugue-labs/gollem/appserver/catalog"
	appconfig "github.com/fugue-labs/gollem/appserver/config"
	appmcp "github.com/fugue-labs/gollem/appserver/mcp"
	"github.com/fugue-labs/gollem/appserver/protocol"
	appskills "github.com/fugue-labs/gollem/appserver/skills"
	"github.com/fugue-labs/gollem/appserver/store"
	toolfs "github.com/fugue-labs/gollem/appserver/tools/fs"
	toolgit "github.com/fugue-labs/gollem/appserver/tools/git"
	toolprocess "github.com/fugue-labs/gollem/appserver/tools/process"
	"github.com/fugue-labs/gollem/core"
)

type connectionState int

const (
	stateNew connectionState = iota
	stateInitialized
	stateReady
)

// Server dispatches Codex-style app-server JSON-RPC requests to Gollem
// services. It is transport-neutral; stdio/socket/WebSocket layers can wrap it.
type Server struct {
	mu          sync.Mutex
	state       connectionState
	serverInfo  protocol.ImplementationInfo
	clientInfo  protocol.ClientInfo
	clientCaps  protocol.InitializeCapabilities
	appHome     string
	optOut      map[string]struct{}
	loaded      map[string]struct{}
	subscribed  map[string]struct{}
	idleUnload  map[string]*threadIdleUnload
	commands    map[string]threadShellCommandRun
	commandExec map[string]struct{}
	commandSeq  int

	workspaceCoordinator *WorkspaceMutationCoordinator

	store                store.Store
	terminalOwnership    store.TerminalOwnershipStore
	terminalWorkspaceKey string
	fs                   *toolfs.Service
	process              *toolprocess.Service
	git                  *toolgit.Service
	catalog              *catalog.Catalog
	config               *appconfig.Service
	cache                *appcache.Service
	mcp                  *appmcp.Service
	skills               *appskills.Service
	events               *EventQueue
	requests             *RequestQueue
	approvals            *ApprovalService
	interact             *InteractionService
	daemon               *DaemonService
	runtime              *RuntimeService
	memory               *MemoryService
	selectionValidator   RuntimeSelectionValidator

	requestSchedulerLimit int
	threadIdleUnloadAfter time.Duration
}

// Option configures a Server.
type Option func(*Server)

// RuntimeSelectionValidator rejects a requested provider/model before the
// server creates a durable thread or turn. It is optional so embedders that
// supply their own non-catalog runtime can define their own authority.
type RuntimeSelectionValidator func(RuntimeModelSelection) error

func WithRuntimeSelectionValidator(validator RuntimeSelectionValidator) Option {
	return func(s *Server) {
		s.selectionValidator = validator
	}
}

func WithImplementationInfo(info protocol.ImplementationInfo) Option {
	return func(s *Server) {
		if info.Name != "" {
			s.serverInfo = info
		}
	}
}

// WithInitializeHome sets the absolute Gollem state root exposed through the
// Codex-compatible initialize response field.
func WithInitializeHome(home string) Option {
	return func(s *Server) {
		home = strings.TrimSpace(home)
		if home == "" {
			return
		}
		if absolute, err := filepath.Abs(home); err == nil {
			home = absolute
		}
		s.appHome = filepath.Clean(home)
	}
}

func WithStore(st store.Store) Option {
	return func(s *Server) {
		s.store = st
	}
}

func WithFilesystem(fs *toolfs.Service) Option {
	return func(s *Server) {
		s.fs = fs
	}
}

// WithWorkspaceMutationCoordinator shares exact-revert reservations across
// every transport connection that can mutate the same workspace.
func WithWorkspaceMutationCoordinator(coordinator *WorkspaceMutationCoordinator) Option {
	return func(s *Server) {
		if coordinator != nil {
			s.workspaceCoordinator = coordinator
		}
	}
}

func WithProcess(process *toolprocess.Service) Option {
	return func(s *Server) {
		s.process = process
	}
}

func WithGit(git *toolgit.Service) Option {
	return func(s *Server) {
		s.git = git
	}
}

func WithCatalog(catalog *catalog.Catalog) Option {
	return func(s *Server) {
		s.catalog = catalog
	}
}

func WithConfig(config *appconfig.Service) Option {
	return func(s *Server) {
		s.config = config
	}
}

func WithCache(cache *appcache.Service) Option {
	return func(s *Server) {
		s.cache = cache
	}
}

func WithMCP(mcp *appmcp.Service) Option {
	return func(s *Server) {
		s.mcp = mcp
	}
}

func WithSkills(skills *appskills.Service) Option {
	return func(s *Server) {
		s.skills = skills
	}
}

func WithEventQueue(events *EventQueue) Option {
	return func(s *Server) {
		s.events = events
	}
}

func WithApprovalService(approvals *ApprovalService) Option {
	return func(s *Server) {
		s.approvals = approvals
	}
}

func WithInteractionService(interactions *InteractionService) Option {
	return func(s *Server) {
		s.interact = interactions
	}
}

func WithDaemonService(daemon *DaemonService) Option {
	return func(s *Server) {
		s.daemon = daemon
	}
}

func WithRuntimeService(runtime *RuntimeService) Option {
	return func(s *Server) {
		s.runtime = runtime
	}
}

func WithMemoryService(memory *MemoryService) Option {
	return func(s *Server) {
		s.memory = memory
	}
}

func WithThreadIdleUnloadAfter(after time.Duration) Option {
	return func(s *Server) {
		if after >= 0 {
			s.threadIdleUnloadAfter = after
		}
	}
}

func WithRequestSchedulerLimit(limit int) Option {
	return func(s *Server) {
		if limit > 0 {
			s.requestSchedulerLimit = limit
		}
	}
}

func NewServer(opts ...Option) *Server {
	s := &Server{
		serverInfo:            protocol.ImplementationInfo{Name: "gollem-appserver"},
		appHome:               defaultInitializeHome(),
		optOut:                make(map[string]struct{}),
		loaded:                make(map[string]struct{}),
		subscribed:            make(map[string]struct{}),
		idleUnload:            make(map[string]*threadIdleUnload),
		commands:              make(map[string]threadShellCommandRun),
		commandExec:           make(map[string]struct{}),
		catalog:               catalog.NewDefault(),
		config:                appconfig.NewService(),
		cache:                 appcache.NewService(),
		mcp:                   appmcp.NewService(),
		skills:                appskills.NewService(),
		events:                NewEventQueue(),
		requests:              NewRequestQueue(),
		approvals:             NewApprovalService(),
		interact:              NewInteractionService(),
		daemon:                NewDaemonService(),
		workspaceCoordinator:  NewWorkspaceMutationCoordinator(),
		requestSchedulerLimit: defaultRequestSchedulerLimit,
		threadIdleUnloadAfter: 30 * time.Minute,
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.requests == nil {
		s.requests = NewRequestQueue()
	}
	if s.approvals != nil {
		s.approvals.setRequestQueue(s.requests)
	}
	if s.interact != nil {
		s.interact.setRequestQueue(s.requests)
	}
	s.initializeTerminalOwnership()
	return s
}

// HandleJSON handles one raw JSON-RPC request or notification. Requests return
// a response payload and true. Notifications return false and no payload.
func (s *Server) HandleJSON(ctx context.Context, data []byte) ([]byte, bool, error) {
	var probe struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, false, err
	}
	if len(probe.ID) > 0 {
		if strings.TrimSpace(probe.Method) == "" {
			var resp protocol.Response
			if err := json.Unmarshal(data, &resp); err != nil {
				return nil, false, err
			}
			return nil, false, s.HandleResponse(ctx, resp)
		}
		var req protocol.Request
		if err := json.Unmarshal(data, &req); err != nil {
			return nil, false, err
		}
		resp := s.HandleRequest(ctx, req)
		out, err := json.Marshal(resp)
		return out, true, err
	}
	var notification protocol.Notification
	if err := json.Unmarshal(data, &notification); err != nil {
		return nil, false, err
	}
	return nil, false, s.HandleNotification(ctx, notification)
}

// HandleRequest handles one client-to-server request and always returns a
// JSON-RPC response.
func (s *Server) HandleRequest(ctx context.Context, req protocol.Request) protocol.Response {
	if s == nil {
		return errorResponse(req.ID, rpcError(protocol.CodeInternalError, "server not configured", nil))
	}
	if req.Method == "initialize" {
		return s.handleInitialize(req)
	}
	if err := s.requireReady(); err != nil {
		return errorResponse(req.ID, err)
	}
	result, rpcErr := s.dispatch(ctx, req.Method, req.Params)
	if rpcErr != nil {
		return errorResponse(req.ID, rpcErr)
	}
	return resultResponse(req.ID, result)
}

// HandleNotification handles one client-to-server notification.
func (s *Server) HandleNotification(ctx context.Context, notification protocol.Notification) error {
	_ = ctx
	if s == nil {
		return rpcError(protocol.CodeInternalError, "server not configured", nil)
	}
	if notification.Method == "initialized" {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch s.state {
		case stateNew:
			return rpcError(protocol.CodeInvalidRequest, "initialize required before initialized notification", nil)
		case stateInitialized:
			s.state = stateReady
			return nil
		case stateReady:
			return nil
		default:
			return rpcError(protocol.CodeInternalError, "invalid connection state", nil)
		}
	}
	if err := s.requireReady(); err != nil {
		return err
	}
	if !protocol.IsKnownMethod(notification.Method) {
		return protocol.MethodUnavailableError(notification.Method)
	}
	return protocol.MethodUnavailableErrorWithReason(notification.Method, "notification handler is not implemented in this Gollem build")
}

// HandleResponse handles one client response to a server-to-client request.
func (s *Server) HandleResponse(ctx context.Context, resp protocol.Response) error {
	if s == nil {
		return rpcError(protocol.CodeInternalError, "server not configured", nil)
	}
	if err := s.requireReady(); err != nil {
		return err
	}
	if s.approvals != nil {
		result, ok, err := s.approvals.RespondResponse(resp)
		if ok {
			if result.CancelTurn {
				if cancelErr := s.cancelApprovalTurn(ctx, result.TurnID); err == nil {
					err = cancelErr
				}
			}
			result.resolve()
			s.PublishNotification("serverRequest/resolved", serverRequestResolvedParams{
				ThreadID:  firstNonEmpty(result.ThreadID, approvalThreadID),
				RequestID: protocol.NewStringID(result.RequestID),
			})
			if err != nil {
				return invalidParams("invalid approval response", err)
			}
			return nil
		}
	}
	if s.interact == nil {
		return protocol.MethodUnavailableErrorWithReason("server response", "interaction service is not configured")
	}
	result, ok, err := s.interact.Respond(resp)
	if err != nil {
		return invalidParams("invalid server request response", err)
	}
	if !ok {
		return invalidParams("server request is not pending", fmt.Errorf("request %s is not pending", requestIDString(resp.ID)))
	}
	s.PublishNotification("serverRequest/resolved", serverRequestResolvedParams{
		ThreadID:  firstNonEmpty(result.ThreadID, "appserver"),
		RequestID: protocol.NewStringID(result.RequestID),
	})
	return nil
}

func (s *Server) cancelApprovalTurn(ctx context.Context, turnID string) error {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" || turnID == approvalTurnID {
		return nil
	}
	if s.store == nil {
		return errors.New("turn runtime is not configured for canceled approval")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	turn, err := s.store.GetTurn(ctx, turnID)
	if err != nil {
		return err
	}
	if fileChangeRevertTerminalTurnStatus(turn.Status) {
		return nil
	}
	if s.runtime == nil {
		return errors.New("turn runtime is not configured for canceled approval")
	}
	_, err = s.runtime.Interrupt(ctx, s.store, turnID)
	if errors.Is(err, ErrRuntimeTurnNotActive) {
		return nil
	}
	return err
}

// NotificationEnabled reports whether the connected client opted out of a
// server notification method.
func (s *Server) NotificationEnabled(method string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, disabled := s.optOut[method]
	return !disabled
}

// ClientCapabilities returns a defensive copy of the capabilities negotiated
// by the current connection.
func (s *Server) ClientCapabilities() protocol.InitializeCapabilities {
	if s == nil {
		return protocol.InitializeCapabilities{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	capabilities := s.clientCaps
	capabilities.OptOutNotificationMethods = append([]string(nil), capabilities.OptOutNotificationMethods...)
	if capabilities.Experimental != nil {
		capabilities.Experimental = make(map[string]bool, len(s.clientCaps.Experimental))
		for name, enabled := range s.clientCaps.Experimental {
			capabilities.Experimental[name] = enabled
		}
	}
	return capabilities
}

func (s *Server) NotificationSignal() <-chan struct{} {
	if s == nil || s.events == nil {
		return nil
	}
	return s.events.Signal()
}

func (s *Server) DrainNotifications() []protocol.Notification {
	if s == nil || s.events == nil {
		return nil
	}
	return s.events.Drain(s.NotificationEnabled)
}

func (s *Server) RequestSignal() <-chan struct{} {
	if s == nil || s.requests == nil {
		return nil
	}
	return s.requests.Signal()
}

func (s *Server) DrainRequests() []protocol.Request {
	if s == nil || s.requests == nil {
		return nil
	}
	return s.requests.Drain()
}

func (s *Server) RequestSchedulerLimit() int {
	if s == nil || s.requestSchedulerLimit <= 0 {
		return defaultRequestSchedulerLimit
	}
	return s.requestSchedulerLimit
}

func (s *Server) DaemonShutdownRequested() bool {
	return s != nil && s.daemon != nil && s.daemon.ShutdownRequested()
}

func (s *Server) PublishNotification(method string, params any) {
	if s == nil || s.events == nil || !s.NotificationEnabled(method) {
		return
	}
	s.events.Publish(method, params)
}

func (s *Server) handleInitialize(req protocol.Request) protocol.Response {
	s.mu.Lock()
	if s.state != stateNew {
		s.mu.Unlock()
		return errorResponse(req.ID, rpcError(protocol.CodeInvalidRequest, "initialize has already completed", nil))
	}
	s.mu.Unlock()

	var params protocol.InitializeParams
	if rpcErr := decodeParams(req.Params, &params); rpcErr != nil {
		return errorResponse(req.ID, rpcErr)
	}
	if strings.TrimSpace(params.ClientInfo.Name) == "" {
		return errorResponse(req.ID, invalidParams("invalid initialize params", errors.New("clientInfo.name is required")))
	}
	if strings.TrimSpace(params.ClientInfo.Version) == "" {
		return errorResponse(req.ID, invalidParams("invalid initialize params", errors.New("clientInfo.version is required")))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != stateNew {
		return errorResponse(req.ID, rpcError(protocol.CodeInvalidRequest, "initialize has already completed", nil))
	}
	s.clientInfo = params.ClientInfo
	s.clientCaps = protocol.InitializeCapabilities{}
	if params.Capabilities != nil {
		s.clientCaps = *params.Capabilities
	}
	s.optOut = make(map[string]struct{}, len(s.clientCaps.OptOutNotificationMethods))
	for _, method := range s.clientCaps.OptOutNotificationMethods {
		if method != "" {
			s.optOut[method] = struct{}{}
		}
	}
	s.state = stateInitialized
	return resultResponse(req.ID, protocol.DefaultInitializeResponse(s.serverInfo, s.appHome))
}

func defaultInitializeHome() string {
	home, err := filepath.Abs(".gollem")
	if err != nil {
		return ".gollem"
	}
	return home
}

func (s *Server) requireReady() *protocol.Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.state {
	case stateNew:
		return rpcError(protocol.CodeInvalidRequest, "initialize required before other requests", nil)
	case stateInitialized:
		return rpcError(protocol.CodeInvalidRequest, "initialized notification required before other requests", nil)
	case stateReady:
		return nil
	default:
		return rpcError(protocol.CodeInternalError, "invalid connection state", nil)
	}
}

func (s *Server) dispatch(ctx context.Context, method string, params json.RawMessage) (any, *protocol.Error) {
	switch method {
	case "thread/start":
		return s.handleThreadStart(ctx, params)
	case "thread/resume":
		return s.handleThreadResume(ctx, params)
	case "thread/list":
		return s.handleThreadList(ctx, params)
	case "thread/search":
		return s.handleThreadSearch(ctx, params)
	case "thread/loaded/list":
		return s.handleThreadLoadedList(ctx, params)
	case "thread/unsubscribe":
		return s.handleThreadUnsubscribe(ctx, params)
	case "thread/read":
		return s.handleThreadRead(ctx, params)
	case "thread/fork":
		return s.handleThreadFork(ctx, params)
	case "thread/compact/start":
		return s.handleThreadCompactStart(ctx, params)
	case "thread/shellCommand":
		return s.handleThreadShellCommand(ctx, params)
	case "thread/approveGuardianDeniedAction":
		return s.handleThreadApproveGuardianDeniedAction(ctx, params)
	case "thread/rollback":
		return s.handleThreadRollback(ctx, params)
	case "item/fileChange/revert":
		return s.handleFileChangeRevert(ctx, params)
	case "thread/archive":
		return s.handleThreadStatus(ctx, params, method, store.ThreadArchived)
	case "thread/unarchive":
		return s.handleThreadStatus(ctx, params, method, store.ThreadActive)
	case "thread/delete":
		return s.handleThreadStatus(ctx, params, method, store.ThreadDeleted)
	case "thread/settings/update":
		return s.handleThreadSettingsUpdate(ctx, params)
	case "thread/goal/get":
		return s.handleThreadGoalGet(ctx, params)
	case "thread/goal/set":
		return s.handleThreadGoalSet(ctx, params)
	case "thread/goal/clear":
		return s.handleThreadGoalClear(ctx, params)
	case "thread/metadata/update":
		return s.handleThreadMetadataUpdate(ctx, params)
	case "thread/memoryMode/set":
		return s.handleThreadMemoryModeSet(ctx, params)
	case "memory/reset":
		return s.handleMemoryReset(ctx)
	case "thread/name/set":
		return s.handleThreadNameSet(ctx, params)
	case "thread/backgroundTerminals/list":
		return s.handleBackgroundTerminalsList(ctx, params)
	case "thread/backgroundTerminals/read":
		return s.handleBackgroundTerminalRead(ctx, params)
	case "thread/backgroundTerminals/terminate":
		return s.handleBackgroundTerminalTerminate(ctx, params)
	case "thread/backgroundTerminals/write":
		return s.handleBackgroundTerminalWrite(ctx, params)
	case "thread/backgroundTerminals/resize":
		return s.handleBackgroundTerminalResize(ctx, params)
	case "thread/backgroundTerminals/clean":
		return s.handleBackgroundTerminalsClean(ctx, params)
	case "thread/turns/list":
		return s.handleThreadTurnsList(ctx, params)
	case "thread/items/list":
		return s.handleThreadItemsList(ctx, params)
	case "thread/inject_items":
		return s.handleThreadInjectItems(ctx, params)
	case "turn/start":
		return s.handleTurnStart(ctx, params)
	case "turn/interrupt":
		return s.handleTurnInterrupt(ctx, params)
	case "turn/steer":
		return s.handleTurnSteer(ctx, params)
	case "turn/retry":
		return s.handleTurnRetry(ctx, params)
	case "model/list":
		return s.handleModelList(params)
	case "modelProvider/capabilities/read", "provider/capabilities/read":
		return s.handleProviderCapabilities(params)
	case "provider/list":
		return s.handleProviderList(params)
	case "provider/health/probe":
		return s.handleProviderHealthProbe(ctx, params)
	case "tool/list":
		return s.handleToolList(params)
	case "collaborationMode/list":
		return s.handleCollaborationModeList()
	case "permissionProfile/list":
		return s.handlePermissionProfileList()
	case "experimentalFeature/list":
		return s.handleExperimentalFeatureList()
	case "experimentalFeature/enablement/set":
		return s.handleExperimentalFeatureEnablementSet(params)
	case "config/read":
		return s.handleConfigRead(params)
	case "config/value/write":
		return s.handleConfigValueWrite(params)
	case "config/batchWrite":
		return s.handleConfigBatchWrite(params)
	case "configRequirements/read":
		return s.handleConfigRequirementsRead()
	case "config/mcpServer/reload":
		return s.handleConfigMCPServerReload()
	case "mcpServerStatus/list":
		return s.handleMCPServerStatusList(ctx, params)
	case "mcpServer/resource/read":
		return s.handleMCPServerResourceRead(ctx, params)
	case "mcpServer/tool/call":
		return s.handleMCPServerToolCall(ctx, params)
	case "mcpServer/oauth/login":
		return nil, protocol.MethodUnavailableErrorWithReason(method, "MCP OAuth login is not implemented; register an already-authenticated MCP client")
	case "skills/list":
		return s.handleSkillsList(ctx, params)
	case "plugin/list", "plugin/installed":
		return s.handlePluginList(ctx, params)
	case "plugin/read":
		return s.handlePluginRead(ctx, params)
	case "plugin/skill/read":
		return s.handlePluginSkillRead(ctx, params)
	case "plugin/install", "plugin/uninstall":
		return nil, protocol.MethodUnavailableErrorWithReason(method, "plugin install and uninstall are not implemented; configure read-only skill roots on the app-server")
	case "plugin/share/list", "plugin/share/save", "plugin/share/updateTargets", "plugin/share/checkout", "plugin/share/delete":
		return nil, protocol.MethodUnavailableErrorWithReason(method, "plugin sharing is not implemented by this Gollem app-server build")
	case "marketplace/add", "marketplace/remove", "marketplace/upgrade":
		return nil, protocol.MethodUnavailableErrorWithReason(method, "plugin marketplace mutation is not implemented by this Gollem app-server build")
	case "skills/config/write", "skills/extraRoots/set":
		return nil, protocol.MethodUnavailableErrorWithReason(method, "skills configuration mutation is not implemented; start the app-server with configured skill roots")
	case "environment/info":
		return s.handleEnvironmentInfo()
	case "environment/add":
		return s.handleEnvironmentAdd(params)
	case "cache/stats":
		return s.handleCacheStats()
	case "cache/benchmark":
		return s.handleCacheBenchmark(params)
	case "approval/respond":
		return s.handleApprovalRespond(params)
	case "daemon/start":
		return s.handleDaemonStart()
	case "daemon/stop":
		return s.handleDaemonStop(params, false)
	case "daemon/restart":
		return s.handleDaemonStop(params, true)
	case "daemon/status":
		return s.handleDaemonStatus()
	case "daemon/version":
		return s.handleDaemonVersion()
	case "fs/readFile":
		return s.handleFSReadFile(ctx, params)
	case "fs/writeFile":
		return s.handleFSWriteFile(ctx, params)
	case "fs/createDirectory":
		return s.handleFSCreateDirectory(ctx, params)
	case "fs/readDirectory":
		return s.handleFSReadDirectory(ctx, params)
	case "fs/getMetadata":
		return s.handleFSMetadata(ctx, params)
	case "fs/remove":
		return s.handleFSRemove(ctx, params)
	case "fs/copy":
		return s.handleFSCopy(ctx, params)
	case "fs/watch":
		return s.handleFSWatch(ctx, params)
	case "fs/unwatch":
		return s.handleFSUnwatch(ctx, params)
	case "command/exec":
		return s.handleProcessStart(ctx, method, params, true)
	case "command/exec/write", "process/writeStdin":
		return s.handleProcessWriteStdin(ctx, method, params)
	case "command/exec/resize", "process/resizePty":
		return s.handleProcessResize(ctx, method, params)
	case "command/exec/terminate":
		return s.handleProcessTerminate(ctx, params)
	case "process/spawn":
		return s.handleProcessStart(ctx, method, params, false)
	case "process/kill":
		return s.handleProcessKill(ctx, params)
	case "git/status":
		return s.handleGitStatus(ctx, params)
	case "git/diff":
		return s.handleGitDiff(ctx, params)
	case "git/commit":
		return s.handleGitCommit(ctx, params)
	case "git/worktree/list":
		return s.handleGitWorktreeList(ctx, params)
	case "git/worktree/create":
		return s.handleGitWorktreeCreate(ctx, params)
	default:
		if protocol.IsKnownMethod(method) {
			return nil, protocol.MethodUnavailableError(method)
		}
		return nil, protocol.MethodUnavailableError(method)
	}
}

func (s *Server) handleThreadStart(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	st, runtimeSvc, rpcErr := s.requireRuntime("thread/start")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params threadStartParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	prompt := runtimePromptFromStartParams(params.Prompt, params.Message, params.Text, params.Input)
	if prompt == "" {
		return nil, invalidParams("prompt is required", nil)
	}
	settings := cloneSettings(params.Settings)
	settings = mergeRuntimeSelectionIntoSettings(settings, params.ProviderID, params.Provider, params.Model)
	selection := runtimeSelectionFromParams(params.ProviderID, params.Provider, params.Model)
	modelSettings := runtimeModelSettingsFromParams(params.RuntimeModelParams)
	settings = mergeRuntimeReasoningIntoSettings(settings, modelSettings.ReasoningEffort)
	if err := s.validateRuntimeSelection(selection, modelSettings); err != nil {
		return nil, invalidParams(err.Error(), err)
	}
	ctx, releaseStart, err := runtimeSvc.acquireStartLease(ctx, s)
	if err != nil {
		return nil, mapError("thread/start", err)
	}
	defer releaseStart()
	thread, err := st.CreateThread(ctx, store.CreateThreadRequest{
		Title:     params.Title,
		Workspace: params.Workspace,
		Settings:  settings,
		Metadata:  params.Metadata,
	})
	if err != nil {
		return nil, mapError("thread/start", err)
	}
	s.markThreadLoaded(thread)
	s.publishThreadNotification("thread/started", thread)
	turn, err := runtimeSvc.Start(ctx, st, s, RuntimeStartRequest{
		ThreadID:      thread.ID,
		Prompt:        prompt,
		Input:         params.Input,
		Metadata:      params.Metadata,
		Selection:     selection,
		ModelSettings: modelSettings,
	})
	if err != nil {
		return nil, mapError("thread/start", err)
	}
	releaseStart()
	return protocol.ThreadRunStartResult{
		Thread: protocolThreadRecord(thread),
		Turn:   protocolTurnRecord(turn.Turn),
	}, nil
}

func (s *Server) handleThreadResume(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	var params threadResumeParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	params.ThreadID = firstNonEmpty(params.ThreadID, params.ID)
	return s.startTurnWithParams(ctx, "thread/resume", params.turnStartParams())
}

func (s *Server) handleThreadList(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	st, rpcErr := s.requireStore("thread/list")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params protocol.ThreadListParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	return listThreadRecords(ctx, st, params)
}

func (s *Server) handleThreadRead(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	st, rpcErr := s.requireStore("thread/read")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params protocol.ThreadReadParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	threadID := params.EffectiveThreadID()
	if threadID == "" {
		return nil, invalidParams("threadId is required", nil)
	}
	thread, err := st.GetThread(ctx, threadID)
	if err != nil {
		return nil, mapError("thread/read", err)
	}
	s.markThreadLoaded(thread)
	result := protocol.ThreadReadResult{Thread: protocolThreadRecord(thread)}
	if boolDefault(params.IncludeTurns, true) {
		turns, err := st.ListTurns(ctx, store.TurnFilter{ThreadID: threadID, Limit: params.Limit})
		if err != nil {
			return nil, mapError("thread/read", err)
		}
		result.Turns = protocolTurnRecords(turns)
	}
	if boolDefault(params.IncludeItems, true) {
		items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: threadID, AfterSeq: params.AfterSeq, Limit: params.Limit})
		if err != nil {
			return nil, mapError("thread/read", err)
		}
		result.Items = protocolTimelineItems(items)
	}
	result.Thread.Turns = nestThreadTurns(result.Turns, result.Items)
	return result, nil
}

func (s *Server) handleThreadFork(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	st, rpcErr := s.requireStore("thread/fork")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params threadForkParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	sourceID := firstNonEmpty(params.SourceThreadID, params.ThreadID, params.ID)
	if sourceID == "" {
		return nil, invalidParams("sourceThreadId or threadId is required", nil)
	}
	if params.IdempotencyKey != nil {
		sourceID = strings.TrimSpace(sourceID)
		if sourceID == "" {
			return nil, invalidParams("threadId is required", nil)
		}
		idempotencyKey := strings.TrimSpace(*params.IdempotencyKey)
		if idempotencyKey == "" || len(idempotencyKey) > 256 {
			return nil, invalidParams("idempotencyKey must contain 1 to 256 bytes", nil)
		}
		if params.SourceThreadID != "" || params.ID != "" || params.Title != "" ||
			len(params.Metadata) > 0 || params.IncludeItems {
			return nil, invalidParams(
				"typed idempotent fork accepts only threadId and idempotencyKey",
				nil,
			)
		}
		recoveryStore, ok := st.(store.ThreadForkRecoveryStore)
		if !ok {
			return nil, protocol.MethodUnavailableErrorWithReason(
				"thread/fork",
				"configured store does not support response-loss-safe thread forks",
			)
		}
		prepared, err := recoveryStore.PrepareThreadFork(ctx, store.PrepareThreadForkRequest{
			SourceThreadID: sourceID,
			IdempotencyKey: idempotencyKey,
		})
		if err != nil {
			return nil, mapError("thread/fork", err)
		}
		s.markThreadLoaded(prepared.Thread)
		if prepared.Created {
			s.publishThreadNotification("thread/started", prepared.Thread)
		}
		return protocol.ThreadHistoryForkResult{
			Thread:                        protocolThreadRecord(prepared.Thread),
			SourceThreadID:                sourceID,
			IdempotencyKey:                idempotencyKey,
			Reused:                        !prepared.Created,
			HistoryCopied:                 true,
			FileChangeRecoveryTransferred: false,
		}, nil
	}
	thread, err := st.ForkThread(ctx, store.ForkThreadRequest{
		SourceThreadID: sourceID,
		Title:          params.Title,
		Metadata:       params.Metadata,
		IncludeItems:   params.IncludeItems,
	})
	if err != nil {
		return nil, mapError("thread/fork", err)
	}
	s.markThreadLoaded(thread)
	s.publishThreadNotification("thread/started", thread)
	return map[string]any{"thread": thread}, nil
}

func (s *Server) handleThreadStatus(ctx context.Context, raw json.RawMessage, method string, status store.ThreadStatus) (any, *protocol.Error) {
	st, rpcErr := s.requireStore(method)
	if rpcErr != nil {
		return nil, rpcErr
	}
	threadID, rpcErr := decodeThreadStatusID(raw, method)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if threadID == "" {
		return nil, invalidParams("threadId is required", nil)
	}
	var (
		thread *store.Thread
		err    error
	)
	if status == store.ThreadDeleted {
		releaseWorkspace, leaseErr := s.acquireWorkspaceMutationLease()
		if leaseErr != nil {
			return nil, rpcError(protocol.CodeInvalidRequest, "cannot delete a thread while a workspace file-change revert is in progress", nil)
		}
		defer releaseWorkspace()
	}
	switch status {
	case store.ThreadArchived:
		thread, err = st.ArchiveThread(ctx, threadID)
	case store.ThreadActive:
		thread, err = st.UnarchiveThread(ctx, threadID)
	case store.ThreadDeleted:
		thread, err = st.DeleteThread(ctx, threadID)
	default:
		return nil, invalidParams("unsupported thread status", nil)
	}
	if err != nil {
		if errors.Is(err, store.ErrThreadHasActiveTurn) {
			switch status {
			case store.ThreadArchived:
				return nil, rpcError(
					protocol.CodeInvalidRequest,
					"cannot archive a thread while a turn is active",
					nil,
				)
			case store.ThreadDeleted:
				return nil, rpcError(
					protocol.CodeInvalidRequest,
					"cannot delete a thread while a turn is active",
					nil,
				)
			}
		}
		return nil, mapError(method, err)
	}
	if status == store.ThreadDeleted {
		s.markThreadUnloaded(thread.ID)
	} else {
		s.markThreadLoaded(thread)
	}
	s.publishThreadNotification("thread/status/changed", thread)
	switch status {
	case store.ThreadArchived:
		s.publishThreadNotification("thread/archived", thread)
	case store.ThreadActive:
		s.publishThreadNotification("thread/unarchived", thread)
	case store.ThreadDeleted:
		s.publishThreadNotification("thread/deleted", thread)
	}
	return protocolThreadStatusResponse(method, thread), nil
}

func (s *Server) handleThreadSettingsUpdate(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	st, rpcErr := s.requireStore("thread/settings/update")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params threadSettingsUpdateParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	threadID := firstNonEmpty(params.ThreadID, params.ID)
	if threadID == "" {
		return nil, invalidParams("threadId is required", nil)
	}
	thread, err := st.UpdateThreadSettings(ctx, store.UpdateThreadSettingsRequest{
		ID:       threadID,
		Settings: params.Settings,
		Metadata: params.Metadata,
		Replace:  params.Replace,
	})
	if err != nil {
		return nil, mapError("thread/settings/update", err)
	}
	s.markThreadLoaded(thread)
	s.publishThreadNotification("thread/settings/updated", thread)
	return map[string]any{"thread": thread}, nil
}

func (s *Server) handleThreadGoalGet(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	st, rpcErr := s.requireStore("thread/goal/get")
	if rpcErr != nil {
		return nil, rpcErr
	}
	params, rpcErr := decodeThreadGoalGetParams(raw)
	if rpcErr != nil {
		return nil, rpcErr
	}
	thread, err := st.GetThread(ctx, params.EffectiveThreadID())
	if err != nil {
		return nil, mapError("thread/goal/get", err)
	}
	goal, set := threadGoalFromStored(thread)
	if !set {
		return threadGoalGetResponse(thread, nil), nil
	}
	goal = refreshThreadGoalUsage(ctx, st, goal)
	return threadGoalGetResponse(thread, &goal), nil
}

func (s *Server) handleThreadGoalSet(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	st, rpcErr := s.requireStore("thread/goal/set")
	if rpcErr != nil {
		return nil, rpcErr
	}
	params, rpcErr := decodeThreadGoalSetParams(raw)
	if rpcErr != nil {
		return nil, rpcErr
	}
	threadID := params.EffectiveThreadID()
	thread, err := st.GetThread(ctx, threadID)
	if err != nil {
		return nil, mapError("thread/goal/set", err)
	}
	turns, err := st.ListTurns(ctx, store.TurnFilter{ThreadID: threadID})
	if err != nil {
		return nil, mapError("thread/goal/set", err)
	}
	goal, rpcErr := applyThreadGoalSet(thread, params, turns, time.Now().UTC())
	if rpcErr != nil {
		return nil, rpcErr
	}
	thread, err = st.UpdateThreadSettings(ctx, store.UpdateThreadSettingsRequest{
		ID:       threadID,
		Settings: map[string]any{threadGoalSettingKey: goal},
	})
	if err != nil {
		return nil, mapError("thread/goal/set", err)
	}
	s.markThreadLoaded(thread)
	s.publishThreadNotification("thread/settings/updated", thread)
	s.publishThreadGoalUpdated(thread, goal, nil)
	return threadGoalSetResponse(thread, goal), nil
}

func (s *Server) handleThreadGoalClear(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	st, rpcErr := s.requireStore("thread/goal/clear")
	if rpcErr != nil {
		return nil, rpcErr
	}
	params, rpcErr := decodeThreadGoalClearParams(raw)
	if rpcErr != nil {
		return nil, rpcErr
	}
	threadID := params.EffectiveThreadID()
	thread, err := st.GetThread(ctx, threadID)
	if err != nil {
		return nil, mapError("thread/goal/clear", err)
	}
	_, hadGoal := thread.Settings[threadGoalSettingKey]
	if !hadGoal {
		return threadGoalClearResponse(thread, false), nil
	}
	nextSettings := cloneSettings(thread.Settings)
	delete(nextSettings, threadGoalSettingKey)
	thread, err = st.UpdateThreadSettings(ctx, store.UpdateThreadSettingsRequest{
		ID:       threadID,
		Settings: nextSettings,
		Metadata: cloneSettings(thread.Metadata),
		Replace:  true,
	})
	if err != nil {
		return nil, mapError("thread/goal/clear", err)
	}
	s.markThreadLoaded(thread)
	s.publishThreadNotification("thread/settings/updated", thread)
	s.publishThreadGoalCleared(thread)
	return threadGoalClearResponse(thread, hadGoal), nil
}

func (s *Server) handleThreadMetadataUpdate(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	st, rpcErr := s.requireStore("thread/metadata/update")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params protocol.ThreadMetadataUpdateParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	threadID := params.EffectiveThreadID()
	if threadID == "" {
		return nil, invalidParams("threadId is required", nil)
	}
	if params.Metadata == nil && !params.HasGitInfo() {
		return nil, invalidParams("gitInfo or metadata is required", nil)
	}
	req := store.UpdateThreadSettingsRequest{
		ID:       threadID,
		Metadata: params.Metadata,
	}
	var gitInfoPatch protocol.ThreadMetadataGitInfoUpdateParams
	if params.HasGitInfo() {
		var rpcErr *protocol.Error
		gitInfoPatch, rpcErr = normalizeThreadMetadataGitInfoPatch(params.GitInfo)
		if rpcErr != nil {
			return nil, rpcErr
		}
	}
	var current *store.Thread
	if params.Replace || params.HasGitInfo() {
		thread, err := st.GetThread(ctx, threadID)
		if err != nil {
			return nil, mapError("thread/metadata/update", err)
		}
		current = thread
	}
	if params.HasGitInfo() {
		gitInfo := current.Metadata[threadGitInfoMetadataKey]
		if supplied, ok := params.Metadata[threadGitInfoMetadataKey]; ok {
			gitInfo = supplied
		}
		req.Metadata = cloneSettings(params.Metadata)
		if req.Metadata == nil {
			req.Metadata = make(map[string]any)
		}
		req.Metadata[threadGitInfoMetadataKey] = applyThreadMetadataGitInfoPatch(gitInfo, gitInfoPatch)
	}
	if params.Replace {
		thread := current
		req.Settings = cloneSettings(thread.Settings)
		req.Replace = true
	}
	thread, err := st.UpdateThreadSettings(ctx, req)
	if err != nil {
		return nil, mapError("thread/metadata/update", err)
	}
	s.markThreadLoaded(thread)
	s.publishThreadNotification("thread/settings/updated", thread)
	return protocol.ThreadMetadataUpdateResult{
		Thread:   protocolThreadRecord(thread),
		Metadata: cloneSettings(thread.Metadata),
	}, nil
}

func (s *Server) handleThreadMemoryModeSet(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	st, rpcErr := s.requireStore("thread/memoryMode/set")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params protocol.ThreadMemoryModeSetParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	threadID := params.EffectiveThreadID()
	if threadID == "" {
		return nil, invalidParams("threadId is required", nil)
	}
	mode := protocol.ThreadMemoryMode(strings.ToLower(strings.TrimSpace(string(params.EffectiveMode()))))
	if !mode.Valid() {
		return nil, invalidParams("mode must be enabled or disabled", nil)
	}
	thread, err := st.UpdateThreadSettings(ctx, store.UpdateThreadSettingsRequest{
		ID:       threadID,
		Settings: map[string]any{threadMemoryModeSettingKey: string(mode)},
	})
	if err != nil {
		return nil, mapError("thread/memoryMode/set", err)
	}
	s.markThreadLoaded(thread)
	s.publishThreadNotification("thread/settings/updated", thread)
	record := protocolThreadRecord(thread)
	return protocol.ThreadMemoryModeSetResponse{
		ThreadID:   thread.ID,
		MemoryMode: mode,
		Thread:     &record,
	}, nil
}

func (s *Server) handleMemoryReset(ctx context.Context) (any, *protocol.Error) {
	memorySvc, rpcErr := s.requireMemory("memory/reset")
	if rpcErr != nil {
		return nil, rpcErr
	}
	result, err := memorySvc.Reset(ctx)
	if err != nil {
		return nil, mapError("memory/reset", err)
	}
	return result, nil
}

func (s *Server) handleThreadNameSet(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	st, rpcErr := s.requireStore("thread/name/set")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params protocol.ThreadSetNameParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	threadID := params.EffectiveThreadID()
	if threadID == "" {
		return nil, invalidParams("threadId is required", nil)
	}
	name := strings.TrimSpace(params.EffectiveName())
	if name == "" {
		return nil, invalidParams("name is required", nil)
	}
	thread, err := st.UpdateThreadTitle(ctx, threadID, name)
	if err != nil {
		return nil, mapError("thread/name/set", err)
	}
	s.markThreadLoaded(thread)
	s.publishThreadNameNotification(thread)
	record := protocolThreadRecord(thread)
	return protocol.ThreadSetNameResponse{
		ThreadID: thread.ID,
		Name:     thread.Title,
		Thread:   &record,
	}, nil
}

func (s *Server) handleBackgroundTerminalsList(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	processSvc, rpcErr := s.requireProcess("thread/backgroundTerminals/list")
	if rpcErr != nil {
		return nil, rpcErr
	}
	params, rpcErr := decodeOperationalListParams(raw)
	if rpcErr != nil {
		return nil, rpcErr
	}
	snapshots, err := processSvc.List(ctx)
	if err != nil {
		return nil, mapError("thread/backgroundTerminals/list", err)
	}
	terminals, err := s.backgroundTerminalInventory(ctx, snapshots)
	if err != nil {
		return nil, mapError("thread/backgroundTerminals/list", err)
	}
	snapshotID := operationalSnapshotID("thread/backgroundTerminals/list", terminals)
	start, end, nextCursor, rpcErr := operationalPageBounds(
		params, "thread/backgroundTerminals/list", snapshotID, len(terminals),
	)
	if rpcErr != nil {
		return nil, rpcErr
	}
	page := cloneOperationalTerminals(terminals[start:end])
	return protocol.BackgroundTerminalListResponse{
		Terminals:           page,
		BackgroundTerminals: cloneOperationalTerminals(page),
		Data:                cloneOperationalTerminals(page),
		Total:               len(terminals),
		Truncated:           len(page) < len(terminals),
		SnapshotID:          snapshotID,
		NextCursor:          nextCursor,
		ObservedAt:          operationalObservedAt(),
	}, nil
}

func (s *Server) handleBackgroundTerminalRead(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	processSvc, rpcErr := s.requireProcess("thread/backgroundTerminals/read")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params protocol.BackgroundTerminalReadParams
	if err := decodeOperationalParams(raw, &params); err != nil {
		return nil, invalidParams("invalid background terminal read params", err)
	}
	if strings.TrimSpace(params.ID) == "" {
		return nil, invalidParams("id is required", nil)
	}
	processID, record, err := s.resolveBackgroundTerminalID(ctx, params.ID)
	if err != nil {
		return nil, mapError("thread/backgroundTerminals/read", err)
	}
	if record != nil && record.Status == store.TerminalOwnershipOwnerLost {
		return nil, ownerLostTerminalUnavailable("thread/backgroundTerminals/read")
	}
	snapshot, err := processSvc.Snapshot(ctx, processID)
	if err != nil {
		return nil, mapError("thread/backgroundTerminals/read", err)
	}
	stdout, stdoutTruncated := operationalTerminalOutput(snapshot.Stdout, snapshot.StdoutTruncated)
	stderr, stderrTruncated := operationalTerminalOutput(snapshot.Stderr, snapshot.StderrTruncated)
	return protocol.BackgroundTerminalReadResponse{
		Terminal:        operationalBackgroundTerminalWithOwnership(processSvc.Root(), snapshot, record),
		StdoutBase64:    stdout,
		StderrBase64:    stderr,
		StdoutTruncated: stdoutTruncated,
		StderrTruncated: stderrTruncated,
		ObservedAt:      operationalObservedAt(),
	}, nil
}

func (s *Server) handleBackgroundTerminalTerminate(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	processSvc, rpcErr := s.requireProcess("thread/backgroundTerminals/terminate")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params protocol.BackgroundTerminalTerminateParams
	if err := decodeOperationalParams(raw, &params); err != nil {
		return nil, invalidParams("invalid background terminal params", err)
	}
	id := params.EffectiveID()
	if id == "" {
		return nil, invalidParams("id, terminalId, backgroundTerminalId, or processId is required", nil)
	}
	processID, record, err := s.resolveBackgroundTerminalID(ctx, id)
	if err != nil {
		return nil, mapError("thread/backgroundTerminals/terminate", err)
	}
	if record != nil && record.Status == store.TerminalOwnershipOwnerLost {
		return nil, ownerLostTerminalUnavailable("thread/backgroundTerminals/terminate")
	}
	approvalCtx := withRuntimeApprovalItemID(
		ctx,
		operationalTerminalTerminateApprovalItemID(processID),
	)
	if err := processSvc.Terminate(approvalCtx, processID); err != nil {
		return nil, mapError("thread/backgroundTerminals/terminate", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	snapshot, err := processSvc.Wait(waitCtx, processID)
	if err != nil {
		return nil, mapError("thread/backgroundTerminals/terminate", err)
	}
	return protocol.BackgroundTerminalTerminateResponse{
		OK:       true,
		ID:       id,
		Terminal: operationalBackgroundTerminalWithOwnership(processSvc.Root(), snapshot, record),
	}, nil
}

func (s *Server) handleBackgroundTerminalWrite(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	processSvc, rpcErr := s.requireProcess("thread/backgroundTerminals/write")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params protocol.BackgroundTerminalWriteParams
	if err := decodeOperationalParams(raw, &params); err != nil {
		return nil, invalidParams("invalid background terminal write params", err)
	}
	id := strings.TrimSpace(params.ID)
	if id == "" {
		return nil, invalidParams("id is required", nil)
	}
	if len(params.Input) == 0 {
		return nil, invalidParams("input is required", nil)
	}
	if len(params.Input) > operationalTerminalWriteMaxBytes {
		return nil, invalidParams(fmt.Sprintf("input exceeds %d bytes", operationalTerminalWriteMaxBytes), nil)
	}
	processID, record, err := s.resolveBackgroundTerminalID(ctx, id)
	if err != nil {
		return nil, mapError("thread/backgroundTerminals/write", err)
	}
	if record != nil && record.Status == store.TerminalOwnershipOwnerLost {
		return nil, ownerLostTerminalUnavailable("thread/backgroundTerminals/write")
	}
	snapshot, err := processSvc.Snapshot(ctx, processID)
	if err != nil {
		return nil, mapError("thread/backgroundTerminals/write", err)
	}
	if snapshot.Status != toolprocess.StatusRunning {
		return nil, invalidParams("background terminal is not running", nil)
	}
	approvalCtx := withRuntimeApprovalItemID(ctx, operationalTerminalWriteApprovalItemID(processID))
	if err := processSvc.WriteStdin(approvalCtx, processID, []byte(params.Input)); err != nil {
		return nil, mapError("thread/backgroundTerminals/write", err)
	}
	snapshot, err = processSvc.Snapshot(ctx, processID)
	if err != nil {
		return nil, mapError("thread/backgroundTerminals/write", err)
	}
	return protocol.BackgroundTerminalWriteResponse{
		OK:           true,
		Terminal:     operationalBackgroundTerminalWithOwnership(processSvc.Root(), snapshot, record),
		WrittenBytes: len(params.Input),
		ObservedAt:   operationalObservedAt(),
	}, nil
}

func (s *Server) handleBackgroundTerminalResize(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	processSvc, rpcErr := s.requireProcess("thread/backgroundTerminals/resize")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params protocol.BackgroundTerminalResizeParams
	if err := decodeOperationalParams(raw, &params); err != nil {
		return nil, invalidParams("invalid background terminal resize params", err)
	}
	id := strings.TrimSpace(params.ID)
	if id == "" {
		return nil, invalidParams("id is required", nil)
	}
	if params.Size.Rows == 0 || params.Size.Cols == 0 {
		return nil, invalidParams("size rows and cols must be positive", nil)
	}
	processID, record, err := s.resolveBackgroundTerminalID(ctx, id)
	if err != nil {
		return nil, mapError("thread/backgroundTerminals/resize", err)
	}
	if record != nil && record.Status == store.TerminalOwnershipOwnerLost {
		return nil, ownerLostTerminalUnavailable("thread/backgroundTerminals/resize")
	}
	snapshot, err := processSvc.Snapshot(ctx, processID)
	if err != nil {
		return nil, mapError("thread/backgroundTerminals/resize", err)
	}
	if snapshot.Status != toolprocess.StatusRunning {
		return nil, invalidParams("background terminal is not running", nil)
	}
	if err := processSvc.ResizePTY(ctx, processID, int(params.Size.Cols), int(params.Size.Rows)); err != nil {
		return nil, mapError("thread/backgroundTerminals/resize", err)
	}
	snapshot, err = processSvc.Snapshot(ctx, processID)
	if err != nil {
		return nil, mapError("thread/backgroundTerminals/resize", err)
	}
	return protocol.BackgroundTerminalResizeResponse{
		OK:         true,
		Terminal:   operationalBackgroundTerminalWithOwnership(processSvc.Root(), snapshot, record),
		ObservedAt: operationalObservedAt(),
	}, nil
}

func (s *Server) handleBackgroundTerminalsClean(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	processSvc, rpcErr := s.requireProcess("thread/backgroundTerminals/clean")
	if rpcErr != nil {
		return nil, rpcErr
	}
	if rpcErr := rejectOperationalParams(raw); rpcErr != nil {
		return nil, rpcErr
	}
	removed, err := processSvc.CleanCompleted(ctx)
	if err != nil {
		return nil, mapError("thread/backgroundTerminals/clean", err)
	}
	if s.terminalOwnership != nil {
		if err := s.terminalOwnership.DeleteInactiveTerminalOwnership(ctx, s.terminalWorkspaceKey); err != nil {
			return nil, mapError("thread/backgroundTerminals/clean", err)
		}
	}
	terminals := operationalBackgroundTerminals(processSvc.Root(), removed)
	removedCount := len(terminals)
	terminals, truncated := boundedOperationalTerminals(terminals)
	return protocol.BackgroundTerminalCleanResponse{
		Removed:             terminals,
		BackgroundTerminals: cloneOperationalTerminals(terminals),
		Data:                cloneOperationalTerminals(terminals),
		RemovedCount:        removedCount,
		Truncated:           truncated,
		ObservedAt:          operationalObservedAt(),
	}, nil
}

func (s *Server) handleThreadTurnsList(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	st, rpcErr := s.requireStore("thread/turns/list")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params threadTurnsListParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	if params.ThreadID == "" {
		return nil, invalidParams("threadId is required", nil)
	}
	turns, err := st.ListTurns(ctx, store.TurnFilter{ThreadID: params.ThreadID, Statuses: params.Statuses, Limit: params.Limit})
	if err != nil {
		return nil, mapError("thread/turns/list", err)
	}
	return map[string]any{"turns": turns}, nil
}

func (s *Server) handleThreadItemsList(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	st, rpcErr := s.requireStore("thread/items/list")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params threadItemsListParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	if params.ThreadID == "" {
		return nil, invalidParams("threadId is required", nil)
	}
	items, err := st.ListItems(ctx, store.ItemFilter{
		ThreadID: params.ThreadID,
		TurnID:   params.TurnID,
		AfterSeq: params.AfterSeq,
		Limit:    params.Limit,
	})
	if err != nil {
		return nil, mapError("thread/items/list", err)
	}
	return map[string]any{"items": items}, nil
}

func (s *Server) handleTurnStart(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	var params turnStartParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	return s.startTurnWithParams(ctx, "turn/start", params)
}

func (s *Server) handleTurnInterrupt(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	st, runtimeSvc, rpcErr := s.requireRuntime("turn/interrupt")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params turnIDParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	turnID := firstNonEmpty(params.TurnID, params.ID)
	if turnID == "" {
		return nil, invalidParams("turnId is required", nil)
	}
	result, err := runtimeSvc.Interrupt(ctx, st, turnID)
	if err != nil && !errors.Is(err, ErrRuntimeTurnNotActive) {
		return nil, mapError("turn/interrupt", err)
	}
	response := protocol.TurnRunInterruptResult{
		OK:     result.OK,
		TurnID: result.TurnID,
	}
	if result.Turn != nil {
		turn := protocolTurnRecord(result.Turn)
		response.Turn = &turn
	}
	return response, nil
}

func (s *Server) handleTurnSteer(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	_, runtimeSvc, rpcErr := s.requireRuntime("turn/steer")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params protocol.TurnSteerParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	params.ThreadID = strings.TrimSpace(params.ThreadID)
	params.ExpectedTurnID = strings.TrimSpace(params.ExpectedTurnID)
	if params.ThreadID == "" {
		return nil, invalidParams("threadId is required", nil)
	}
	if params.ExpectedTurnID == "" {
		return nil, invalidParams("expectedTurnId is required", nil)
	}
	message, err := runtimeSteerText(params.Input)
	if err != nil {
		return nil, invalidParams("invalid steer input", err)
	}
	clientUserMessageID := ""
	if params.ClientUserMessageID != nil {
		clientUserMessageID = *params.ClientUserMessageID
	}
	result, err := runtimeSvc.Steer(ctx, RuntimeSteerRequest{
		ThreadID:            params.ThreadID,
		TurnID:              params.ExpectedTurnID,
		ClientUserMessageID: clientUserMessageID,
		Message:             message,
	})
	if err != nil {
		return nil, mapError("turn/steer", err)
	}
	return protocol.TurnSteerResponse{TurnID: result.TurnID}, nil
}

func (s *Server) handleTurnRetry(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	st, runtimeSvc, rpcErr := s.requireRuntime("turn/retry")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params turnRetryParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	turnID := firstNonEmpty(params.TurnID, params.ID)
	if turnID == "" {
		return nil, invalidParams("turnId is required", nil)
	}
	source, err := st.GetTurn(ctx, turnID)
	if err != nil {
		return nil, mapError("turn/retry", err)
	}
	prompt := runtimePromptFromInput(source.Input)
	if params.Prompt != "" || params.Message != "" || params.Text != "" || len(params.Input) > 0 {
		prompt = runtimePromptFromStartParams(params.Prompt, params.Message, params.Text, params.Input)
	}
	if prompt == "" {
		return nil, invalidParams("retry prompt is unavailable", nil)
	}
	idempotencyKey, err := params.retryIdempotencyKey()
	if err != nil {
		return nil, invalidParams(err.Error(), err)
	}
	beforeSeq, err := firstTurnItemSeq(ctx, st, source.ThreadID, source.ID)
	if err != nil {
		return nil, mapError("turn/retry", err)
	}
	history, err := s.loadThreadHistory(ctx, st, source.ThreadID, beforeSeq)
	if err != nil {
		return nil, mapError("turn/retry", err)
	}
	selection := runtimeSelectionFromParams(params.ProviderID, params.Provider, params.Model)
	selection = mergeRuntimeSelection(selection, runtimeSelectionFromInput(source.Input))
	modelSettings := runtimeModelSettingsFromParams(params.RuntimeModelParams)
	modelSettings = mergeRuntimeModelSettings(modelSettings, runtimeModelSettingsFromInput(source.Input))
	if err := s.validateRuntimeSelection(selection, modelSettings); err != nil {
		return nil, invalidParams(err.Error(), err)
	}
	retry, err := runtimeSvc.Retry(ctx, st, s, RuntimeRetryRequest{
		SourceTurnID:   source.ID,
		IdempotencyKey: idempotencyKey,
		Prompt:         prompt,
		Input:          params.Input,
		Metadata:       params.Metadata,
		Selection:      selection,
		ModelSettings:  modelSettings,
		History:        history,
	})
	if err != nil {
		return nil, mapError("turn/retry", err)
	}
	s.markThreadLoadedID(source.ThreadID)
	return protocol.TurnRunRetryResult{
		Turn:           protocolTurnRecord(retry.Turn),
		SourceTurnID:   retry.SourceTurnID,
		IdempotencyKey: retry.IdempotencyKey,
		Reused:         retry.Reused,
	}, nil
}

func (s *Server) startTurnWithParams(ctx context.Context, method string, params turnStartParams) (any, *protocol.Error) {
	st, runtimeSvc, rpcErr := s.requireRuntime(method)
	if rpcErr != nil {
		return nil, rpcErr
	}
	threadID := firstNonEmpty(params.ThreadID, params.ID)
	if threadID == "" {
		return nil, invalidParams("threadId is required", nil)
	}
	prompt := runtimePromptFromStartParams(params.Prompt, params.Message, params.Text, params.Input)
	if prompt == "" {
		return nil, invalidParams("prompt is required", nil)
	}
	thread, err := st.GetThread(ctx, threadID)
	if err != nil {
		return nil, mapError(method, err)
	}
	history, err := s.loadThreadHistory(ctx, st, thread.ID, 0)
	if err != nil {
		return nil, mapError(method, err)
	}
	selection := runtimeSelectionFromParams(params.ProviderID, params.Provider, params.Model)
	selection = runtimeSelectionWithThreadDefaults(selection, thread.Settings)
	modelSettings := runtimeModelSettingsFromParams(params.RuntimeModelParams)
	modelSettings = runtimeModelSettingsWithThreadDefaults(modelSettings, thread.Settings)
	if err := s.validateRuntimeSelection(selection, modelSettings); err != nil {
		return nil, invalidParams(err.Error(), err)
	}
	turn, err := runtimeSvc.Start(ctx, st, s, RuntimeStartRequest{
		ThreadID:      thread.ID,
		Prompt:        prompt,
		Input:         params.Input,
		Metadata:      params.Metadata,
		Selection:     selection,
		ModelSettings: modelSettings,
		History:       history,
	})
	if err != nil {
		return nil, mapError(method, err)
	}
	s.markThreadLoaded(thread)
	return protocol.TurnRunStartResult{
		Thread: protocolThreadRecord(thread),
		Turn:   protocolTurnRecord(turn.Turn),
	}, nil
}

func (s *Server) loadThreadHistory(ctx context.Context, st store.Store, threadID string, beforeSeq int64) ([]core.ModelMessage, error) {
	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: threadID})
	if err != nil {
		return nil, err
	}
	filtered := items[:0]
	for _, item := range items {
		if beforeSeq > 0 && item.Seq >= beforeSeq {
			break
		}
		filtered = append(filtered, item)
	}
	return runtimeMessagesFromItems(boundedRuntimeReplayItems(filtered)), nil
}

func firstTurnItemSeq(ctx context.Context, st store.Store, threadID, turnID string) (int64, error) {
	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: threadID, TurnID: turnID, Limit: 1})
	if err != nil {
		return 0, err
	}
	if len(items) == 0 || items[0] == nil {
		return 0, nil
	}
	return items[0].Seq, nil
}

func (s *Server) handleModelList(raw json.RawMessage) (any, *protocol.Error) {
	var params catalog.ModelListParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	result, err := s.requireCatalog().ListModels(params)
	if err != nil {
		return nil, invalidParams("invalid model/list params", err)
	}
	return result, nil
}

func (s *Server) handleProviderList(raw json.RawMessage) (any, *protocol.Error) {
	var params catalog.ProviderListParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	return s.requireCatalog().ListProviders(params), nil
}

func (s *Server) handleProviderHealthProbe(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	var params catalog.ProviderHealthProbeParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	result, err := s.requireCatalog().ProbeProvider(ctx, params)
	if err != nil {
		return nil, invalidParams("invalid provider health probe params", err)
	}
	return result, nil
}

func (s *Server) handleProviderCapabilities(raw json.RawMessage) (any, *protocol.Error) {
	var params catalog.CapabilitiesReadParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	providerID := firstNonEmpty(params.ProviderID, params.Provider, params.ModelProvider)
	caps, err := s.requireCatalog().ProviderCapabilities(providerID)
	if err != nil {
		return nil, invalidParams("invalid provider capabilities params", err)
	}
	return caps, nil
}

func (s *Server) handleToolList(raw json.RawMessage) (any, *protocol.Error) {
	var params catalog.ToolListParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	_, fileRecoveryAvailable := s.store.(store.FileChangeRecoveryStore)
	return catalog.ListTools(params, catalog.ToolServices{
		Filesystem:   s.fs != nil,
		Process:      s.process != nil,
		Git:          s.git != nil,
		Cache:        s.cache != nil,
		Config:       s.config != nil,
		MCP:          s.mcp != nil,
		Skills:       s.skills != nil,
		Runtime:      s.runtime != nil,
		Interactions: s.interact != nil,
		Memory:       s.memory != nil,
		FileRecovery: fileRecoveryAvailable,
	}), nil
}

func (s *Server) handleCollaborationModeList() (any, *protocol.Error) {
	return s.requireConfig().CollaborationModes(), nil
}

func (s *Server) handlePermissionProfileList() (any, *protocol.Error) {
	return s.requireConfig().PermissionProfiles(), nil
}

func (s *Server) handleExperimentalFeatureList() (any, *protocol.Error) {
	return s.requireConfig().ExperimentalFeatures(), nil
}

func (s *Server) handleExperimentalFeatureEnablementSet(raw json.RawMessage) (any, *protocol.Error) {
	var params appconfig.ExperimentalFeatureSetParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	result, err := s.requireConfig().SetExperimentalFeature(params)
	if err != nil {
		return nil, invalidParams("invalid experimentalFeature/enablement/set params", err)
	}
	return result, nil
}

func (s *Server) handleConfigRead(raw json.RawMessage) (any, *protocol.Error) {
	var params appconfig.ReadParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	return s.requireConfig().Read(params), nil
}

func (s *Server) handleConfigValueWrite(raw json.RawMessage) (any, *protocol.Error) {
	var params appconfig.ValueWriteParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	result, err := s.requireConfig().WriteValue(params)
	if err != nil {
		return nil, invalidParams("invalid config/value/write params", err)
	}
	return result, nil
}

func (s *Server) handleConfigBatchWrite(raw json.RawMessage) (any, *protocol.Error) {
	var params appconfig.BatchWriteParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	result, err := s.requireConfig().BatchWrite(params)
	if err != nil {
		return nil, invalidParams("invalid config/batchWrite params", err)
	}
	return result, nil
}

func (s *Server) handleConfigRequirementsRead() (any, *protocol.Error) {
	return s.requireConfig().Requirements(), nil
}

func (s *Server) handleConfigMCPServerReload() (any, *protocol.Error) {
	if s.mcp != nil {
		reload := s.mcp.Reload()
		return appconfig.MCPReloadResponse{
			Reloaded: reload.Reloaded,
			Status:   reload.Status,
			Reason:   reload.Reason,
			Count:    reload.Count,
		}, nil
	}
	return s.requireConfig().ReloadMCPServers(), nil
}

func (s *Server) handleEnvironmentInfo() (any, *protocol.Error) {
	return s.requireConfig().EnvironmentInfo(), nil
}

func (s *Server) handleEnvironmentAdd(raw json.RawMessage) (any, *protocol.Error) {
	var params appconfig.EnvironmentAddParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	result, err := s.requireConfig().AddEnvironment(params)
	if err != nil {
		return nil, invalidParams("invalid environment/add params", err)
	}
	return result, nil
}

func (s *Server) handleFSReadFile(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	fsSvc, rpcErr := s.requireFS("fs/readFile")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params pathParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	if params.Path == "" {
		return nil, invalidParams("path is required", nil)
	}
	file, err := fsSvc.ReadFile(ctx, params.Path)
	if err != nil {
		return nil, mapError("fs/readFile", err)
	}
	content, encoding := encodeContent(file.Content)
	mode := uint32(file.Mode)
	contentTruncated := false
	return protocol.FsReadFileResponse{
		DataBase64:       base64.StdEncoding.EncodeToString(file.Content),
		Path:             &file.Path,
		Content:          &content,
		Encoding:         &encoding,
		Size:             &file.Size,
		Mode:             &mode,
		ModTime:          &file.ModTime,
		ContentTruncated: &contentTruncated,
	}, nil
}

func (s *Server) handleFSWriteFile(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	fsSvc, rpcErr := s.requireFS("fs/writeFile")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params writeFileParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	if params.Path == "" {
		return nil, invalidParams("path is required", nil)
	}
	var content []byte
	var err error
	if params.DataBase64 != nil {
		content, err = base64.StdEncoding.DecodeString(*params.DataBase64)
		if err != nil {
			return nil, invalidParams("invalid dataBase64", err)
		}
	} else {
		content, err = decodeContent(params.Content, params.Encoding)
		if err != nil {
			return nil, invalidParams("invalid file content encoding", err)
		}
	}
	if err := fsSvc.WriteFile(ctx, params.Path, content, iofs.FileMode(params.Mode)); err != nil {
		return nil, mapError("fs/writeFile", err)
	}
	s.publishFileChanged("writeFile", params.Path, "")
	ok := true
	return protocol.FsWriteFileResponse{OK: &ok, Path: &params.Path}, nil
}

func (s *Server) handleFSCreateDirectory(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	fsSvc, rpcErr := s.requireFS("fs/createDirectory")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params pathParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	if params.Path == "" {
		return nil, invalidParams("path is required", nil)
	}
	if err := fsSvc.CreateDirectoryWithOptions(ctx, params.Path, toolfs.CreateDirectoryOptions{
		Recursive: boolDefault(params.Recursive, true),
	}); err != nil {
		return nil, mapError("fs/createDirectory", err)
	}
	s.publishFileChanged("createDirectory", params.Path, "")
	ok := true
	return protocol.FsCreateDirectoryResponse{OK: &ok, Path: &params.Path}, nil
}

func (s *Server) handleFSReadDirectory(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	fsSvc, rpcErr := s.requireFS("fs/readDirectory")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params pathParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	entries, err := fsSvc.ReadDirectory(ctx, params.Path)
	if err != nil {
		return nil, mapError("fs/readDirectory", err)
	}
	result := protocol.FsReadDirectoryResponse{Entries: make([]protocol.FsReadDirectoryEntry, 0, len(entries))}
	for _, entry := range entries {
		path := entry.Path
		name := entry.Name
		isDir := entry.IsDir
		size := entry.Size
		mode := uint32(entry.Mode)
		modTime := entry.ModTime
		result.Entries = append(result.Entries, protocol.FsReadDirectoryEntry{
			FileName:      entry.Name,
			IsDirectory:   entry.IsDir,
			IsFile:        entry.IsFile,
			LegacyPath:    &path,
			LegacyName:    &name,
			LegacyIsDir:   &isDir,
			LegacySize:    &size,
			LegacyMode:    &mode,
			LegacyModTime: &modTime,
		})
	}
	return result, nil
}

func (s *Server) handleFSMetadata(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	fsSvc, rpcErr := s.requireFS("fs/getMetadata")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params pathParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	if params.Path == "" {
		return nil, invalidParams("path is required", nil)
	}
	meta, err := fsSvc.Metadata(ctx, params.Path)
	if err != nil {
		return nil, mapError("fs/getMetadata", err)
	}
	isDir := meta.IsDir
	mode := uint32(meta.Mode)
	return protocol.FsGetMetadataResponse{
		IsDirectory:  meta.IsDir,
		IsFile:       meta.IsFile,
		IsSymlink:    meta.IsSymlink,
		CreatedAtMS:  0,
		ModifiedAtMS: meta.ModTime.UnixMilli(),
		Path:         &meta.Path,
		IsDir:        &isDir,
		Size:         &meta.Size,
		Mode:         &mode,
		ModTime:      &meta.ModTime,
	}, nil
}

func (s *Server) handleFSRemove(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	fsSvc, rpcErr := s.requireFS("fs/remove")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params pathParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	if params.Path == "" {
		return nil, invalidParams("path is required", nil)
	}
	if err := fsSvc.RemoveWithOptions(ctx, params.Path, toolfs.RemoveOptions{
		Recursive: boolDefault(params.Recursive, true),
		Force:     boolDefault(params.Force, true),
	}); err != nil {
		return nil, mapError("fs/remove", err)
	}
	s.publishFileChanged("remove", params.Path, "")
	ok := true
	return protocol.FsRemoveResponse{OK: &ok, Path: &params.Path}, nil
}

func (s *Server) handleFSCopy(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	fsSvc, rpcErr := s.requireFS("fs/copy")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params copyParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	publicShape := params.SourcePath != "" || params.DestinationPath != ""
	src := firstNonEmpty(params.SourcePath, params.Source, params.Path)
	dst := firstNonEmpty(params.DestinationPath, params.Destination)
	if src == "" || dst == "" {
		return nil, invalidParams("source and destination are required", nil)
	}
	if err := fsSvc.CopyWithOptions(ctx, src, dst, toolfs.CopyOptions{
		Recursive: boolDefault(params.Recursive, !publicShape),
	}); err != nil {
		return nil, mapError("fs/copy", err)
	}
	s.publishFileChanged("copy", src, dst)
	ok := true
	return protocol.FsCopyResponse{OK: &ok, Source: &src, Destination: &dst}, nil
}

func (s *Server) handleFSWatch(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	fsSvc, rpcErr := s.requireFS("fs/watch")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params fsWatchParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	if strings.TrimSpace(params.WatchID) == "" {
		return nil, invalidParams("watchId is required", nil)
	}
	if strings.TrimSpace(params.Path) == "" {
		return nil, invalidParams("path is required", nil)
	}
	if params.PollIntervalMillis < 0 {
		return nil, invalidParams("pollIntervalMillis must be non-negative", nil)
	}
	result, err := fsSvc.Watch(ctx, toolfs.WatchRequest{
		WatchID:      params.WatchID,
		Path:         params.Path,
		PollInterval: time.Duration(params.PollIntervalMillis) * time.Millisecond,
	}, s.publishWatchChanged)
	if err != nil {
		return nil, mapError("fs/watch", err)
	}
	return protocol.FsWatchResponse{Path: protocol.AbsolutePathBuf(result.Path)}, nil
}

func (s *Server) handleFSUnwatch(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	fsSvc, rpcErr := s.requireFS("fs/unwatch")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params fsUnwatchParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	if strings.TrimSpace(params.WatchID) == "" {
		return nil, invalidParams("watchId is required", nil)
	}
	if err := fsSvc.Unwatch(ctx, params.WatchID); err != nil {
		return nil, mapError("fs/unwatch", err)
	}
	return protocol.FsUnwatchResponse{}, nil
}

func (s *Server) handleProcessStart(ctx context.Context, method string, raw json.RawMessage, defaultShell bool) (any, *protocol.Error) {
	processSvc, rpcErr := s.requireProcess(method)
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params processStartParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	if params.TimeoutMillis < 0 {
		return nil, invalidParams("timeoutMillis must be non-negative", nil)
	}
	shell := boolDefault(params.Shell, defaultShell)
	processID := strings.TrimSpace(firstNonEmpty(params.ProcessID, params.ID))
	registeredCommandExec := false
	if method == "command/exec" {
		if processID == "" {
			processID = s.nextCommandExecProcessID()
		}
		registeredCommandExec = s.registerCommandExecProcess(processID)
	}
	snapshot, err := processSvc.Start(ctx, toolprocess.StartRequest{
		ID:             processID,
		Command:        params.Command,
		Args:           params.Args,
		Shell:          shell,
		WorkDir:        params.WorkDir,
		Env:            params.Env,
		Timeout:        time.Duration(params.TimeoutMillis) * time.Millisecond,
		MaxOutputBytes: params.MaxOutputBytes,
		PTY:            params.PTY,
		PTYSize: toolprocess.TerminalSize{
			Rows: params.EffectivePTYRows(),
			Cols: params.EffectivePTYCols(),
		},
	})
	if err != nil {
		if registeredCommandExec {
			s.unregisterCommandExecProcess(processID)
		}
		return nil, mapError(method, err)
	}
	return map[string]any{"process": processSnapshotResultFrom(snapshot)}, nil
}

func (s *Server) handleProcessWriteStdin(ctx context.Context, method string, raw json.RawMessage) (any, *protocol.Error) {
	processSvc, rpcErr := s.requireProcess(method)
	if rpcErr != nil {
		return nil, rpcErr
	}
	var id, input, encoding string
	var closeStdin bool
	if method == "command/exec/write" {
		var params protocol.CommandExecWriteParams
		if rpcErr := decodeParams(raw, &params); rpcErr != nil {
			return nil, rpcErr
		}
		id = params.EffectiveProcessID()
		input, encoding = params.EffectiveInput()
		closeStdin = params.ShouldCloseStdin()
	} else {
		var params processWriteParams
		if rpcErr := decodeParams(raw, &params); rpcErr != nil {
			return nil, rpcErr
		}
		id = firstNonEmpty(params.ID, params.ProcessID)
		input, encoding, closeStdin = params.Data, params.Encoding, params.Close
	}
	if id == "" {
		return nil, invalidParams("id or processId is required", nil)
	}
	if method == "command/exec/write" && !s.isCommandExecProcess(id) {
		return nil, invalidParams("processId is not an active command/exec process", nil)
	}
	data, err := decodeContent(input, encoding)
	if err != nil {
		return nil, invalidParams("invalid stdin encoding", err)
	}
	if len(data) > 0 {
		if err := processSvc.WriteStdin(ctx, id, data); err != nil {
			return nil, mapError(method, err)
		}
	}
	if closeStdin {
		if err := processSvc.CloseStdin(ctx, id); err != nil {
			return nil, mapError(method, err)
		}
	}
	if method == "command/exec/write" {
		return protocol.CommandExecWriteResponse{OK: true, Path: id}, nil
	}
	return okResult(id), nil
}

func (s *Server) handleProcessResize(ctx context.Context, method string, raw json.RawMessage) (any, *protocol.Error) {
	processSvc, rpcErr := s.requireProcess(method)
	if rpcErr != nil {
		return nil, rpcErr
	}
	var id string
	var cols, rows int
	if method == "command/exec/resize" {
		var params protocol.CommandExecResizeParams
		if rpcErr := decodeParams(raw, &params); rpcErr != nil {
			return nil, rpcErr
		}
		id = params.EffectiveProcessID()
		var valid bool
		cols, rows, valid = params.EffectiveSize()
		if !valid {
			return nil, invalidParams("size must not be null", nil)
		}
	} else {
		var params processResizeParams
		if rpcErr := decodeParams(raw, &params); rpcErr != nil {
			return nil, rpcErr
		}
		id, cols, rows = firstNonEmpty(params.ID, params.ProcessID), params.Cols, params.Rows
	}
	if id == "" {
		return nil, invalidParams("id or processId is required", nil)
	}
	if method == "command/exec/resize" && !s.isCommandExecProcess(id) {
		return nil, invalidParams("processId is not an active command/exec process", nil)
	}
	if err := processSvc.ResizePTY(ctx, id, cols, rows); err != nil {
		return nil, mapError(method, err)
	}
	if method == "command/exec/resize" {
		return protocol.CommandExecResizeResponse{OK: true, Path: id}, nil
	}
	return okResult(id), nil
}

func (s *Server) handleProcessTerminate(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	processSvc, rpcErr := s.requireProcess("command/exec/terminate")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params protocol.CommandExecTerminateParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	id := params.EffectiveProcessID()
	if id == "" {
		return nil, invalidParams("id or processId is required", nil)
	}
	if !s.isCommandExecProcess(id) {
		return nil, invalidParams("processId is not an active command/exec process", nil)
	}
	if err := processSvc.Terminate(ctx, id); err != nil {
		return nil, mapError("command/exec/terminate", err)
	}
	return protocol.CommandExecTerminateResponse{OK: true, Path: id}, nil
}

func (s *Server) handleProcessKill(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	processSvc, rpcErr := s.requireProcess("process/kill")
	if rpcErr != nil {
		return nil, rpcErr
	}
	id, rpcErr := decodeProcessID(raw)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if err := processSvc.Kill(ctx, id); err != nil {
		return nil, mapError("process/kill", err)
	}
	return okResult(id), nil
}

func (s *Server) handleGitStatus(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	gitSvc, rpcErr := s.requireGit("git/status")
	if rpcErr != nil {
		return nil, rpcErr
	}
	params, rpcErr := decodeOperationalListParams(raw)
	if rpcErr != nil {
		return nil, rpcErr
	}
	status, err := gitSvc.Status(ctx)
	if err != nil {
		return nil, mapError("git/status", err)
	}
	branch, branchTruncated, entries, clean := operationalGitStatus(status)
	snapshotID := operationalSnapshotID("git/status", struct {
		BranchLine      string
		BranchTruncated bool
		Entries         []protocol.GitStatusEntry
		Clean           bool
	}{BranchLine: branch, BranchTruncated: branchTruncated, Entries: entries, Clean: clean})
	start, end, nextCursor, rpcErr := operationalPageBounds(params, "git/status", snapshotID, len(entries))
	if rpcErr != nil {
		return nil, rpcErr
	}
	return protocol.GitStatusResponse{
		Status: protocol.GitStatusSnapshot{
			BranchLine:       branch,
			BranchTruncated:  branchTruncated,
			Entries:          append([]protocol.GitStatusEntry{}, entries[start:end]...),
			Clean:            clean,
			EntryCount:       len(entries),
			EntriesTruncated: end-start < len(entries),
		},
		SnapshotID: snapshotID,
		NextCursor: nextCursor,
		ObservedAt: operationalObservedAt(),
	}, nil
}

func (s *Server) handleGitDiff(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	gitSvc, rpcErr := s.requireGit("git/diff")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params gitDiffParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	diff, err := gitSvc.Diff(ctx, toolgit.DiffRequest{Ref: params.Ref, Pathspecs: params.Pathspecs, Cached: params.Cached})
	if err != nil {
		return nil, mapError("git/diff", err)
	}
	return map[string]any{"diff": diff}, nil
}

func (s *Server) handleGitCommit(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	gitSvc, rpcErr := s.requireGit("git/commit")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params gitCommitParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	result, err := gitSvc.Commit(ctx, toolgit.CommitRequest{
		Message:    params.Message,
		All:        params.All,
		Pathspecs:  params.Pathspecs,
		AllowEmpty: params.AllowEmpty,
	})
	if err != nil {
		return nil, mapError("git/commit", err)
	}
	return map[string]any{"commit": result}, nil
}

func (s *Server) handleGitWorktreeList(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	gitSvc, rpcErr := s.requireGit("git/worktree/list")
	if rpcErr != nil {
		return nil, rpcErr
	}
	params, rpcErr := decodeOperationalListParams(raw)
	if rpcErr != nil {
		return nil, rpcErr
	}
	worktrees, err := gitSvc.WorktreeList(ctx)
	if err != nil {
		return nil, mapError("git/worktree/list", err)
	}
	records := operationalGitWorktrees(worktrees)
	snapshotID := operationalSnapshotID("git/worktree/list", records)
	start, end, nextCursor, rpcErr := operationalPageBounds(
		params, "git/worktree/list", snapshotID, len(records),
	)
	if rpcErr != nil {
		return nil, rpcErr
	}
	return protocol.GitWorktreeListResponse{
		Worktrees:  append([]protocol.GitWorktree{}, records[start:end]...),
		Total:      len(records),
		Truncated:  end-start < len(records),
		SnapshotID: snapshotID,
		NextCursor: nextCursor,
		ObservedAt: operationalObservedAt(),
	}, nil
}

func (s *Server) handleGitWorktreeCreate(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	gitSvc, rpcErr := s.requireGit("git/worktree/create")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params gitWorktreeCreateParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	worktree, err := gitSvc.WorktreeCreate(ctx, toolgit.WorktreeCreateRequest{
		Path:   params.Path,
		Branch: params.Branch,
		Base:   params.Base,
		Force:  params.Force,
	})
	if err != nil {
		return nil, mapError("git/worktree/create", err)
	}
	return map[string]any{"worktree": worktree}, nil
}

func (s *Server) requireStore(method string) (store.Store, *protocol.Error) {
	if s.store == nil {
		return nil, protocol.MethodUnavailableErrorWithReason(method, "thread store is not configured")
	}
	return s.store, nil
}

func (s *Server) requireFS(method string) (*toolfs.Service, *protocol.Error) {
	if s.fs == nil {
		return nil, protocol.MethodUnavailableErrorWithReason(method, "filesystem service is not configured")
	}
	return s.fs, nil
}

func (s *Server) requireProcess(method string) (*toolprocess.Service, *protocol.Error) {
	if s.process == nil {
		return nil, protocol.MethodUnavailableErrorWithReason(method, "process service is not configured")
	}
	return s.process, nil
}

func (s *Server) requireGit(method string) (*toolgit.Service, *protocol.Error) {
	if s.git == nil {
		return nil, protocol.MethodUnavailableErrorWithReason(method, "git service is not configured")
	}
	return s.git, nil
}

func (s *Server) requireCatalog() *catalog.Catalog {
	if s.catalog == nil {
		return catalog.NewDefault()
	}
	return s.catalog
}

func (s *Server) requireConfig() *appconfig.Service {
	if s.config == nil {
		return appconfig.NewService()
	}
	return s.config
}

func (s *Server) requireDaemon(method string) (*DaemonService, *protocol.Error) {
	if s.daemon == nil {
		return nil, protocol.MethodUnavailableErrorWithReason(method, "daemon service is not configured")
	}
	return s.daemon, nil
}

func (s *Server) requireMemory(method string) (*MemoryService, *protocol.Error) {
	if s.memory == nil {
		return nil, protocol.MethodUnavailableErrorWithReason(method, "memory service is not configured")
	}
	return s.memory, nil
}

func (s *Server) requireRuntime(method string) (store.Store, *RuntimeService, *protocol.Error) {
	st, rpcErr := s.requireStore(method)
	if rpcErr != nil {
		return nil, nil, rpcErr
	}
	if s.runtime == nil {
		return nil, nil, protocol.MethodUnavailableErrorWithReason(method, "turn runtime is not configured")
	}
	return st, s.runtime, nil
}

func decodeParams(raw json.RawMessage, out any) *protocol.Error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return invalidParams("invalid params", err)
	}
	return nil
}

func decodeProcessID(raw json.RawMessage) (string, *protocol.Error) {
	var params processIDParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return "", rpcErr
	}
	id := firstNonEmpty(params.ID, params.ProcessID)
	if id == "" {
		return "", invalidParams("id or processId is required", nil)
	}
	return id, nil
}

func resultResponse(id protocol.RequestID, result any) protocol.Response {
	data, err := json.Marshal(result)
	if err != nil {
		return errorResponse(id, rpcError(protocol.CodeInternalError, "marshal response result", err))
	}
	return protocol.Response{ID: id, Result: data}
}

func errorResponse(id protocol.RequestID, err *protocol.Error) protocol.Response {
	return protocol.Response{ID: id, Error: err}
}

func invalidParams(message string, err error) *protocol.Error {
	return rpcError(protocol.CodeInvalidParams, message, err)
}

func rpcError(code int, message string, err error) *protocol.Error {
	data := map[string]string{}
	if err != nil {
		data["reason"] = err.Error()
	}
	if message == "" {
		message = "app-server error"
	}
	var raw json.RawMessage
	if len(data) > 0 {
		raw, _ = json.Marshal(data)
	}
	return &protocol.Error{Code: code, Message: message, Data: raw}
}

func mapError(method string, err error) *protocol.Error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return rpcError(protocol.CodeInvalidRequest, "request context ended", err)
	case errors.Is(err, core.ErrSteerQueueFull):
		return rpcError(protocol.CodeOverloaded, "steer queue is full", err)
	case errors.Is(err, toolprocess.ErrPTYUnsupported):
		return protocol.MethodUnavailableErrorWithReason(method, "pty resize is not supported until a PTY backend is configured")
	case errors.Is(err, toolfs.ErrPathOutsideRoot),
		errors.Is(err, toolfs.ErrInvalidCopyDestination),
		errors.Is(err, toolfs.ErrRecursiveRequired),
		errors.Is(err, toolfs.ErrRefusingRoot),
		errors.Is(err, toolfs.ErrExactStateMismatch),
		errors.Is(err, toolfs.ErrExactRevertSymlink),
		errors.Is(err, toolfs.ErrExactRevertUnsupported),
		errors.Is(err, toolfs.ErrExactRevertPending),
		errors.Is(err, toolfs.ErrWatchPathNotAbsolute),
		errors.Is(err, toolfs.ErrWatchIDRequired),
		errors.Is(err, toolfs.ErrWatchAlreadyExists),
		errors.Is(err, toolfs.ErrWatchNotFound),
		errors.Is(err, toolgit.ErrPathOutsideRoot),
		errors.Is(err, toolgit.ErrInvalidPathspec),
		errors.Is(err, toolgit.ErrInvalidMessage),
		errors.Is(err, toolprocess.ErrEmptyCommand),
		errors.Is(err, toolprocess.ErrInvalidWorkDir),
		errors.Is(err, toolprocess.ErrProcessNotFound),
		errors.Is(err, toolprocess.ErrProcessNotRunning),
		errors.Is(err, toolprocess.ErrProcessAlreadyExists),
		errors.Is(err, toolprocess.ErrInvalidPTYSize),
		errors.Is(err, toolprocess.ErrPTYUnsupported),
		errors.Is(err, toolprocess.ErrPTYCloseStdin),
		errors.Is(err, store.ErrThreadNotFound),
		errors.Is(err, store.ErrTurnNotFound),
		errors.Is(err, store.ErrItemNotFound),
		errors.Is(err, store.ErrFileChangeRecoveryNotFound),
		errors.Is(err, store.ErrThreadDeleted),
		errors.Is(err, store.ErrThreadArchived),
		errors.Is(err, store.ErrTurnNotTerminal),
		errors.Is(err, store.ErrRetryIdempotencyConflict),
		errors.Is(err, store.ErrThreadHasActiveTurn),
		errors.Is(err, store.ErrThreadForkIdempotencyConflict),
		errors.Is(err, store.ErrFileChangeRevertIdempotencyConflict),
		errors.Is(err, ErrMemoryRootRequired),
		errors.Is(err, ErrMemoryRootUnsafe),
		errors.Is(err, ErrWorkspaceRevertInProgress),
		errors.Is(err, ErrRuntimePromptEmpty),
		errors.Is(err, ErrRuntimeTurnNotActive),
		errors.Is(err, ErrRuntimeSteerIdempotencyConflict),
		errors.Is(err, ErrRuntimeSteerMessageTooLarge),
		errors.Is(err, ErrRuntimeSteerIDTooLarge),
		errors.Is(err, core.ErrSteerMessageEmpty):
		return invalidParams("invalid params", err)
	case errors.Is(err, toolfs.ErrApprovalDenied),
		errors.Is(err, toolgit.ErrApprovalDenied),
		errors.Is(err, toolprocess.ErrApprovalDenied):
		return rpcError(protocol.CodeInvalidRequest, "operation denied by approval policy", err)
	case errors.Is(err, ErrRuntimeNotConfigured):
		return protocol.MethodUnavailableErrorWithReason(method, "turn runtime model factory is not configured")
	case errors.Is(err, ErrRuntimeRecoveryUnavailable):
		return protocol.MethodUnavailableErrorWithReason(method, "configured store does not support restart-safe retry")
	case errors.Is(err, ErrRuntimeShuttingDown):
		return protocol.MethodUnavailableErrorWithReason(method, "turn runtime is shutting down")
	default:
		return rpcError(protocol.CodeInternalError, "internal app-server error", err)
	}
}

func encodeContent(data []byte) (string, string) {
	if utf8.Valid(data) {
		return string(data), "utf-8"
	}
	return base64.StdEncoding.EncodeToString(data), "base64"
}

func decodeContent(content, encoding string) ([]byte, error) {
	switch strings.ToLower(encoding) {
	case "", "utf-8", "utf8":
		return []byte(content), nil
	case "base64":
		return base64.StdEncoding.DecodeString(content)
	default:
		return nil, fmt.Errorf("unsupported encoding %q", encoding)
	}
}

func processSnapshotResultFrom(snapshot *toolprocess.Snapshot) processSnapshotResult {
	if snapshot == nil {
		return processSnapshotResult{}
	}
	stdout, stdoutEncoding := encodeContent(snapshot.Stdout)
	stderr, stderrEncoding := encodeContent(snapshot.Stderr)
	var ptySize *toolprocess.TerminalSize
	if snapshot.PTY {
		size := snapshot.PTYSize
		ptySize = &size
	}
	return processSnapshotResult{
		ID:             snapshot.ID,
		PID:            snapshot.PID,
		Command:        snapshot.Command,
		Args:           snapshot.Args,
		Shell:          snapshot.Shell,
		PTY:            snapshot.PTY,
		PTYSize:        ptySize,
		WorkDir:        snapshot.WorkDir,
		Status:         snapshot.Status,
		ExitCode:       snapshot.ExitCode,
		StartedAt:      snapshot.StartedAt,
		EndedAt:        snapshot.EndedAt,
		Error:          snapshot.Error,
		Stdout:         stdout,
		StdoutEncoding: stdoutEncoding,
		Stderr:         stderr,
		StderrEncoding: stderrEncoding,
	}
}

func okResult(path string) map[string]any {
	if path == "" {
		return map[string]any{"ok": true}
	}
	return map[string]any{"ok": true, "path": path}
}

func boolDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type threadSearchParams struct {
	Cursor        string   `json:"cursor,omitempty"`
	Limit         int      `json:"limit,omitempty"`
	SortKey       string   `json:"sortKey,omitempty"`
	SortDirection string   `json:"sortDirection,omitempty"`
	SourceKinds   []string `json:"sourceKinds,omitempty"`
	Archived      *bool    `json:"archived,omitempty"`
	SearchTerm    string   `json:"searchTerm,omitempty"`
	Query         string   `json:"query,omitempty"`
	Q             string   `json:"q,omitempty"`
}

func (p threadSearchParams) query() string {
	return strings.TrimSpace(firstNonEmpty(p.SearchTerm, p.Query, p.Q))
}

type threadSearchResult struct {
	Thread  *store.Thread `json:"thread"`
	Snippet string        `json:"snippet"`
}

type threadSearchResponse struct {
	Data            []threadSearchResult `json:"data"`
	NextCursor      *string              `json:"nextCursor"`
	BackwardsCursor *string              `json:"backwardsCursor"`
}

type threadForkParams struct {
	ID             string         `json:"id,omitempty"`
	ThreadID       string         `json:"threadId,omitempty"`
	SourceThreadID string         `json:"sourceThreadId,omitempty"`
	IdempotencyKey *string        `json:"idempotencyKey,omitempty"`
	Title          string         `json:"title,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	IncludeItems   bool           `json:"includeItems,omitempty"`
}

func (p *threadForkParams) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, typed := fields["idempotencyKey"]; typed {
		var params protocol.ThreadHistoryForkParams
		if err := json.Unmarshal(data, &params); err != nil {
			return err
		}
		*p = threadForkParams{
			ThreadID:       params.ThreadID,
			IdempotencyKey: &params.IdempotencyKey,
		}
		return nil
	}
	type legacy threadForkParams
	var params legacy
	if err := json.Unmarshal(data, &params); err != nil {
		return err
	}
	*p = threadForkParams(params)
	return nil
}

type threadTurnsListParams struct {
	ThreadID string             `json:"threadId"`
	Statuses []store.TurnStatus `json:"statuses,omitempty"`
	Limit    int                `json:"limit,omitempty"`
}

type threadItemsListParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId,omitempty"`
	AfterSeq int64  `json:"afterSeq,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

type pathParams struct {
	Path      string `json:"path,omitempty"`
	Recursive *bool  `json:"recursive,omitempty"`
	Force     *bool  `json:"force,omitempty"`
}

type writeFileParams struct {
	Path       string  `json:"path"`
	DataBase64 *string `json:"dataBase64"`
	Content    string  `json:"content"`
	Encoding   string  `json:"encoding,omitempty"`
	Mode       uint32  `json:"mode,omitempty"`
}

type copyParams struct {
	Path            string `json:"path,omitempty"`
	Source          string `json:"source,omitempty"`
	SourcePath      string `json:"sourcePath,omitempty"`
	Destination     string `json:"destination,omitempty"`
	DestinationPath string `json:"destinationPath,omitempty"`
	Recursive       *bool  `json:"recursive,omitempty"`
}

type fsWatchParams struct {
	WatchID            string `json:"watchId"`
	Path               string `json:"path"`
	PollIntervalMillis int64  `json:"pollIntervalMillis,omitempty"`
}

type fsWatchResult struct {
	Path string `json:"path"`
}

type fsUnwatchParams struct {
	WatchID string `json:"watchId"`
}

type fileContentResult struct {
	Path             string    `json:"path"`
	Content          string    `json:"content"`
	Encoding         string    `json:"encoding"`
	Size             int64     `json:"size"`
	Mode             uint32    `json:"mode"`
	ModTime          time.Time `json:"modTime"`
	ContentTruncated bool      `json:"contentTruncated"`
}

type processStartParams struct {
	ID             string                        `json:"id,omitempty"`
	ProcessID      string                        `json:"processId,omitempty"`
	Command        string                        `json:"command"`
	Args           []string                      `json:"args,omitempty"`
	Shell          *bool                         `json:"shell,omitempty"`
	WorkDir        string                        `json:"workDir,omitempty"`
	Env            map[string]string             `json:"env,omitempty"`
	TimeoutMillis  int64                         `json:"timeoutMillis,omitempty"`
	MaxOutputBytes int                           `json:"maxOutputBytes,omitempty"`
	PTY            bool                          `json:"pty,omitempty"`
	PTYSize        *protocol.ProcessTerminalSize `json:"ptySize,omitempty"`
}

func (p processStartParams) EffectivePTYRows() int {
	if p.PTYSize == nil {
		return 0
	}
	return int(p.PTYSize.Rows)
}

func (p processStartParams) EffectivePTYCols() int {
	if p.PTYSize == nil {
		return 0
	}
	return int(p.PTYSize.Cols)
}

type processIDParams struct {
	ID        string `json:"id,omitempty"`
	ProcessID string `json:"processId,omitempty"`
}

type processWriteParams struct {
	ID        string `json:"id,omitempty"`
	ProcessID string `json:"processId,omitempty"`
	Data      string `json:"data,omitempty"`
	Encoding  string `json:"encoding,omitempty"`
	Close     bool   `json:"close,omitempty"`
}

type processResizeParams struct {
	ID        string `json:"id,omitempty"`
	ProcessID string `json:"processId,omitempty"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
}

type processSnapshotResult struct {
	ID             string                    `json:"id"`
	PID            int                       `json:"pid"`
	Command        string                    `json:"command"`
	Args           []string                  `json:"args,omitempty"`
	Shell          bool                      `json:"shell"`
	PTY            bool                      `json:"pty"`
	PTYSize        *toolprocess.TerminalSize `json:"ptySize,omitempty"`
	WorkDir        string                    `json:"workDir"`
	Status         toolprocess.Status        `json:"status"`
	ExitCode       int                       `json:"exitCode"`
	StartedAt      time.Time                 `json:"startedAt"`
	EndedAt        time.Time                 `json:"endedAt,omitempty"`
	Error          string                    `json:"error,omitempty"`
	Stdout         string                    `json:"stdout,omitempty"`
	StdoutEncoding string                    `json:"stdoutEncoding,omitempty"`
	Stderr         string                    `json:"stderr,omitempty"`
	StderrEncoding string                    `json:"stderrEncoding,omitempty"`
}

type gitDiffParams struct {
	Ref       string   `json:"ref,omitempty"`
	Pathspecs []string `json:"pathspecs,omitempty"`
	Cached    bool     `json:"cached,omitempty"`
}

type gitCommitParams struct {
	Message    string   `json:"message"`
	All        bool     `json:"all,omitempty"`
	Pathspecs  []string `json:"pathspecs,omitempty"`
	AllowEmpty bool     `json:"allowEmpty,omitempty"`
}

type gitWorktreeCreateParams struct {
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
	Base   string `json:"base,omitempty"`
	Force  bool   `json:"force,omitempty"`
}
