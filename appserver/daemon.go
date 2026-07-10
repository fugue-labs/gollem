package appserver

import (
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/fugue-labs/gollem/appserver/protocol"
)

const daemonStatusRunning = "running"

type DaemonService struct {
	mu        sync.Mutex
	name      string
	version   string
	transport string
	workDir   string
	storePath string
	startedAt time.Time
	pid       int
	shutdown  daemonShutdownState
}

type daemonShutdownState = protocol.DaemonShutdownState

type DaemonOption func(*DaemonService)

func WithDaemonName(name string) DaemonOption {
	return func(d *DaemonService) {
		if strings.TrimSpace(name) != "" {
			d.name = strings.TrimSpace(name)
		}
	}
}

func WithDaemonVersion(version string) DaemonOption {
	return func(d *DaemonService) {
		if strings.TrimSpace(version) != "" {
			d.version = strings.TrimSpace(version)
		}
	}
}

func WithDaemonTransport(transport string) DaemonOption {
	return func(d *DaemonService) {
		d.transport = strings.TrimSpace(transport)
	}
}

func WithDaemonWorkDir(workDir string) DaemonOption {
	return func(d *DaemonService) {
		d.workDir = strings.TrimSpace(workDir)
	}
}

func WithDaemonStorePath(storePath string) DaemonOption {
	return func(d *DaemonService) {
		d.storePath = strings.TrimSpace(storePath)
	}
}

func NewDaemonService(opts ...DaemonOption) *DaemonService {
	d := &DaemonService{
		name:      "gollem-appserver",
		version:   "dev",
		startedAt: time.Now().UTC(),
		pid:       os.Getpid(),
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

type DaemonStatus = protocol.DaemonStatus
type DaemonVersion = protocol.DaemonVersion
type DaemonStartResult = protocol.DaemonStartResult
type DaemonStopResult = protocol.DaemonStopResult
type daemonShutdownParams = protocol.DaemonShutdownParams

func (d *DaemonService) Status() DaemonStatus {
	if d == nil {
		d = NewDaemonService()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.statusLocked(time.Now().UTC())
}

func (d *DaemonService) Version() DaemonVersion {
	if d == nil {
		d = NewDaemonService()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.versionLocked()
}

func (d *DaemonService) Start() DaemonStartResult {
	status := d.Status()
	return DaemonStartResult{OK: true, AlreadyRunning: true, Status: status}
}

func (d *DaemonService) Stop(reason string) DaemonStopResult {
	return d.requestShutdown(false, reason)
}

func (d *DaemonService) Restart(reason string) DaemonStopResult {
	return d.requestShutdown(true, reason)
}

func (d *DaemonService) ShutdownRequested() bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.shutdown.Requested
}

func (d *DaemonService) requestShutdown(restart bool, reason string) DaemonStopResult {
	if d == nil {
		d = NewDaemonService()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.shutdown = daemonShutdownState{
		Requested: true,
		Restart:   restart,
		Reason:    strings.TrimSpace(reason),
	}
	return DaemonStopResult{
		OK:       true,
		Stopping: true,
		Restart:  restart,
		Status:   d.statusLocked(time.Now().UTC()),
	}
}

func (d *DaemonService) statusLocked(now time.Time) DaemonStatus {
	status := daemonStatusRunning
	if d.shutdown.Requested {
		status = "stopping"
	}
	return DaemonStatus{
		Status:            status,
		Name:              d.name,
		Version:           d.version,
		ProtocolVersion:   protocol.ProtocolVersion,
		PID:               d.pid,
		StartedAt:         d.startedAt,
		UptimeMillis:      max(0, now.Sub(d.startedAt).Milliseconds()),
		Transport:         d.transport,
		WorkDir:           d.workDir,
		StorePath:         d.storePath,
		ShutdownRequested: d.shutdown.Requested,
		RestartRequested:  d.shutdown.Restart,
		Shutdown:          d.shutdown,
	}
}

func (d *DaemonService) versionLocked() DaemonVersion {
	return DaemonVersion{
		Name:            d.name,
		Version:         d.version,
		ProtocolVersion: protocol.ProtocolVersion,
		GoVersion:       runtime.Version(),
		GOOS:            runtime.GOOS,
		GOARCH:          runtime.GOARCH,
	}
}

func (s *Server) handleDaemonStatus() (any, *protocol.Error) {
	daemon, rpcErr := s.requireDaemon("daemon/status")
	if rpcErr != nil {
		return nil, rpcErr
	}
	return daemon.Status(), nil
}

func (s *Server) handleDaemonVersion() (any, *protocol.Error) {
	daemon, rpcErr := s.requireDaemon("daemon/version")
	if rpcErr != nil {
		return nil, rpcErr
	}
	return daemon.Version(), nil
}

func (s *Server) handleDaemonStart() (any, *protocol.Error) {
	daemon, rpcErr := s.requireDaemon("daemon/start")
	if rpcErr != nil {
		return nil, rpcErr
	}
	return daemon.Start(), nil
}

func (s *Server) handleDaemonStop(raw []byte, restart bool) (any, *protocol.Error) {
	method := "daemon/stop"
	if restart {
		method = "daemon/restart"
	}
	daemon, rpcErr := s.requireDaemon(method)
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params daemonShutdownParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	if restart {
		return daemon.Restart(params.Reason), nil
	}
	return daemon.Stop(params.Reason), nil
}
