package appserver

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/fugue-labs/gollem/appserver/protocol"
)

const defaultRequestSchedulerLimit = 64

// RequestScheduler bounds app-server request backlog and serializes requests
// that share a mutation-sensitive scope. Transports should bypass it only for
// initialize and approval responses that unblock a pending request.
type RequestScheduler struct {
	slots chan struct{}
	mu    sync.Mutex
	locks map[string]chan struct{}
	limit int
}

// RequestLease represents one accepted request slot.
type RequestLease struct {
	scheduler *RequestScheduler
	lock      chan struct{}
	release   sync.Once
}

func NewRequestScheduler(limit int) *RequestScheduler {
	if limit <= 0 {
		limit = defaultRequestSchedulerLimit
	}
	return &RequestScheduler{
		slots: make(chan struct{}, limit),
		locks: make(map[string]chan struct{}),
		limit: limit,
	}
}

func (s *RequestScheduler) TryAcquire(method string, params json.RawMessage) (*RequestLease, *protocol.Error) {
	if s == nil {
		return nil, nil
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return nil, protocol.OverloadedError(method, s.limit, len(s.slots), "app-server request backlog is full")
	}
	scope := RequestScheduleFor(method, params)
	lease := &RequestLease{scheduler: s}
	if scope.Serial && scope.Scope != "" {
		lease.lock = s.scopeLock(scope.Scope)
	}
	return lease, nil
}

func (l *RequestLease) Run(ctx context.Context, fn func() error) error {
	if l == nil {
		return fn()
	}
	defer l.Release()
	if l.lock == nil {
		return fn()
	}
	select {
	case l.lock <- struct{}{}:
		defer func() { <-l.lock }()
		return fn()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *RequestLease) Release() {
	if l == nil || l.scheduler == nil {
		return
	}
	l.release.Do(func() {
		<-l.scheduler.slots
	})
}

func (s *RequestScheduler) scopeLock(scope string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock := s.locks[scope]
	if lock == nil {
		lock = make(chan struct{}, 1)
		s.locks[scope] = lock
	}
	return lock
}

type RequestSchedule struct {
	Scope  string
	Serial bool
}

func RequestScheduleFor(method string, params json.RawMessage) RequestSchedule {
	_ = params
	switch method {
	case "thread/list", "thread/search", "thread/loaded/list", "thread/unsubscribe", "thread/read", "thread/fork", "thread/compact/start", "thread/shellCommand", "thread/approveGuardianDeniedAction", "thread/rollback", "thread/archive", "thread/unarchive", "thread/delete", "thread/settings/update",
		"thread/goal/get", "thread/goal/set", "thread/goal/clear", "thread/metadata/update", "thread/memoryMode/set",
		"thread/name/set", "thread/turns/list", "thread/items/list", "thread/inject_items":
		return RequestSchedule{Scope: "thread", Serial: true}
	case "fs/readFile", "fs/writeFile", "fs/createDirectory", "fs/readDirectory", "fs/getMetadata", "fs/remove", "fs/copy", "fs/watch", "fs/unwatch":
		return RequestSchedule{Scope: "fs", Serial: true}
	case "command/exec", "command/exec/write", "command/exec/resize", "command/exec/terminate", "process/spawn", "process/writeStdin", "process/resizePty", "process/kill",
		"thread/backgroundTerminals/list", "thread/backgroundTerminals/terminate", "thread/backgroundTerminals/clean":
		return RequestSchedule{Scope: "process", Serial: true}
	case "git/status", "git/diff", "git/commit", "git/worktree/create", "git/worktree/list":
		return RequestSchedule{Scope: "git", Serial: true}
	case "cache/stats", "cache/benchmark":
		return RequestSchedule{Scope: "cache", Serial: true}
	case "memory/reset":
		return RequestSchedule{Scope: "memory", Serial: true}
	case "mcpServerStatus/list", "mcpServer/resource/read", "mcpServer/tool/call":
		return RequestSchedule{Scope: "mcp", Serial: true}
	case "skills/list", "plugin/list", "plugin/installed", "plugin/read", "plugin/skill/read",
		"plugin/install", "plugin/uninstall", "plugin/share/list", "plugin/share/save", "plugin/share/updateTargets", "plugin/share/checkout", "plugin/share/delete",
		"marketplace/add", "marketplace/remove", "marketplace/upgrade", "skills/config/write", "skills/extraRoots/set":
		return RequestSchedule{Scope: "skills", Serial: true}
	case "daemon/start", "daemon/stop", "daemon/restart", "daemon/status", "daemon/version":
		return RequestSchedule{Scope: "daemon", Serial: true}
	case "model/list", "modelProvider/capabilities/read", "provider/capabilities/read", "provider/list", "tool/list",
		"config/read", "config/value/write", "config/batchWrite", "configRequirements/read", "config/mcpServer/reload",
		"environment/add", "environment/info", "collaborationMode/list", "permissionProfile/list",
		"experimentalFeature/list", "experimentalFeature/enablement/set":
		return RequestSchedule{Scope: "catalog", Serial: true}
	default:
		return RequestSchedule{}
	}
}
