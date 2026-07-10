package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	workspacefs "github.com/fugue-labs/gollem/appserver/tools/fs"
)

const (
	defaultMaxProcesses = 32
	defaultOutputBytes  = 1 << 20
	defaultCancelWait   = 5 * time.Second
)

var (
	ErrEmptyCommand         = errors.New("appserver/process: command must not be empty")
	ErrInvalidWorkDir       = errors.New("appserver/process: workdir must be a directory")
	ErrApprovalDenied       = errors.New("appserver/process: operation denied by approval policy")
	ErrProcessNotFound      = errors.New("appserver/process: process not found")
	ErrProcessNotRunning    = errors.New("appserver/process: process is not running")
	ErrProcessAlreadyExists = errors.New("appserver/process: process already exists")
	ErrTooManyProcesses     = errors.New("appserver/process: too many running processes")
	ErrPTYUnsupported       = errors.New("appserver/process: pty resize is not supported")
	ErrInvalidOutputBufSize = errors.New("appserver/process: output buffer size must be positive")
)

type Status string

const (
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusKilled    Status = "killed"
	StatusTimedOut  Status = "timed_out"
)

type Stream string

const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
)

type OperationKind string

const (
	OperationStart      OperationKind = "start"
	OperationWriteStdin OperationKind = "writeStdin"
	OperationCloseStdin OperationKind = "closeStdin"
	OperationTerminate  OperationKind = "terminate"
	OperationKill       OperationKind = "kill"
	OperationCancel     OperationKind = "cancel"
	OperationResizePTY  OperationKind = "resizePty"
)

type Operation struct {
	Kind        OperationKind
	ID          string
	Command     string
	Args        []string
	WorkDir     string
	Signal      string
	Destructive bool
}

type AuditEvent struct {
	Operation Operation
	ID        string
	PID       int
	Allowed   bool
	Err       string
	At        time.Time
}

type OutputEvent struct {
	ID     string
	PID    int
	Stream Stream
	Data   []byte
	At     time.Time
}

type ExitEvent struct {
	Snapshot Snapshot
	At       time.Time
}

type ApprovalFunc func(context.Context, Operation) error
type AuditSink func(AuditEvent)
type OutputSink func(OutputEvent)
type ExitSink func(ExitEvent)

type Option func(*Service)

func WithApproval(fn ApprovalFunc) Option {
	return func(s *Service) {
		s.approve = fn
	}
}

func WithAuditSink(fn AuditSink) Option {
	return func(s *Service) {
		s.audit = fn
	}
}

func WithOutputSink(fn OutputSink) Option {
	return func(s *Service) {
		s.output = fn
	}
}

func WithExitSink(fn ExitSink) Option {
	return func(s *Service) {
		s.exit = fn
	}
}

func WithMaxProcesses(limit int) Option {
	return func(s *Service) {
		if limit > 0 {
			s.maxProcesses = limit
		}
	}
}

func WithDefaultOutputBytes(size int) Option {
	return func(s *Service) {
		if size > 0 {
			s.defaultOutputBytes = size
		}
	}
}

type Service struct {
	fs                 *workspacefs.Service
	mu                 sync.Mutex
	processes          map[string]*managedProcess
	counter            int
	maxProcesses       int
	defaultOutputBytes int
	approve            ApprovalFunc
	audit              AuditSink
	output             OutputSink
	exit               ExitSink
}

type StartRequest struct {
	ID                  string
	Command             string
	Args                []string
	Shell               bool
	WorkDir             string
	Env                 map[string]string
	Timeout             time.Duration
	MaxOutputBytes      int
	OutputSink          OutputSink
	ExitSink            ExitSink
	SuppressGlobalSinks bool
}

type Snapshot struct {
	ID              string
	PID             int
	Command         string
	Args            []string
	Shell           bool
	WorkDir         string
	Status          Status
	ExitCode        int
	StartedAt       time.Time
	EndedAt         time.Time
	Error           string
	Stdout          []byte
	Stderr          []byte
	StdoutTruncated bool
	StderrTruncated bool
}

type managedProcess struct {
	mu                  sync.Mutex
	stdinMu             sync.Mutex
	id                  string
	pid                 int
	command             string
	args                []string
	shell               bool
	workDir             string
	startedAt           time.Time
	endedAt             time.Time
	status              Status
	exitCode            int
	err                 string
	killStatus          Status
	stdout              *outputBuffer
	stderr              *outputBuffer
	stdin               io.WriteCloser
	cmd                 *exec.Cmd
	done                chan struct{}
	output              OutputSink
	exit                ExitSink
	suppressGlobalSinks bool
}

type outputBuffer struct {
	mu        sync.Mutex
	buf       []byte
	size      int
	truncated bool
}

type processWriter struct {
	svc    *Service
	proc   *managedProcess
	stream Stream
}

func NewService(root string, opts ...Option) (*Service, error) {
	fsSvc, err := workspacefs.NewService(root)
	if err != nil {
		return nil, err
	}
	s := &Service{
		fs:                 fsSvc,
		processes:          make(map[string]*managedProcess),
		maxProcesses:       defaultMaxProcesses,
		defaultOutputBytes: defaultOutputBytes,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

func (s *Service) Root() string {
	if s == nil || s.fs == nil {
		return ""
	}
	return s.fs.Root()
}

func (s *Service) Start(ctx context.Context, req StartRequest) (*Snapshot, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, errors.New("appserver/process: nil service")
	}
	spec, err := normalizeStart(req)
	if err != nil {
		return nil, err
	}
	workDir, relWorkDir, err := s.resolveWorkDir(ctx, req.WorkDir)
	if err != nil {
		return nil, err
	}
	op := Operation{
		Kind:    OperationStart,
		Command: req.Command,
		Args:    cloneStrings(req.Args),
		WorkDir: relWorkDir,
	}
	if err := s.requireApproval(ctx, op); err != nil {
		s.emit(op, "", 0, false, err)
		return nil, err
	}

	cmd := exec.CommandContext(context.Background(), spec.name, spec.args...)
	cmd.Dir = workDir
	cmd.Env = buildEnv(req.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		s.emit(op, "", 0, false, err)
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	bufSize := req.MaxOutputBytes
	if bufSize == 0 {
		bufSize = s.defaultOutputBytes
	}
	if bufSize <= 0 {
		return nil, ErrInvalidOutputBufSize
	}
	proc := &managedProcess{
		command:             req.Command,
		args:                cloneStrings(req.Args),
		shell:               req.Shell,
		workDir:             relWorkDir,
		startedAt:           time.Now().UTC(),
		status:              StatusRunning,
		stdout:              newOutputBuffer(bufSize),
		stderr:              newOutputBuffer(bufSize),
		stdin:               stdin,
		cmd:                 cmd,
		done:                make(chan struct{}),
		output:              req.OutputSink,
		exit:                req.ExitSink,
		suppressGlobalSinks: req.SuppressGlobalSinks,
	}
	cmd.Stdout = processWriter{svc: s, proc: proc, stream: StreamStdout}
	cmd.Stderr = processWriter{svc: s, proc: proc, stream: StreamStderr}
	cmd.WaitDelay = 5 * time.Second

	s.mu.Lock()
	if s.runningCountLocked() >= s.maxProcesses {
		s.mu.Unlock()
		s.emit(op, "", 0, false, ErrTooManyProcesses)
		return nil, ErrTooManyProcesses
	}
	proc.id = strings.TrimSpace(req.ID)
	if proc.id == "" {
		s.counter++
		proc.id = fmt.Sprintf("proc-%d", s.counter)
	} else if _, exists := s.processes[proc.id]; exists {
		s.mu.Unlock()
		s.emit(op, proc.id, 0, false, ErrProcessAlreadyExists)
		return nil, ErrProcessAlreadyExists
	}
	if err := cmd.Start(); err != nil {
		s.mu.Unlock()
		s.emit(op, proc.id, 0, false, err)
		return nil, fmt.Errorf("start process: %w", err)
	}
	proc.setPID(cmd.Process.Pid)
	s.processes[proc.id] = proc
	s.mu.Unlock()

	go func() {
		waitErr := cmd.Wait()
		proc.finish(waitErr)
		s.emitExit(proc)
		close(proc.done)
	}()
	if req.Timeout > 0 {
		go enforceTimeout(proc, req.Timeout)
	}

	s.emit(op, proc.id, proc.pid, true, nil)
	snap := proc.snapshot()
	return &snap, nil
}

// Run starts one process and waits for it to finish. If ctx is canceled after
// the approved start, Run kills the process group it owns and waits briefly for
// cleanup without issuing a second approval request.
func (s *Service) Run(ctx context.Context, req StartRequest) (*Snapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	started, err := s.Start(ctx, req)
	if err != nil {
		return nil, err
	}
	snapshot, err := s.Wait(ctx, started.ID)
	if err == nil {
		return snapshot, nil
	}
	ctxErr := ctx.Err()
	if ctxErr == nil {
		return snapshot, err
	}

	cancelErr := s.cancelOwnedProcess(started.ID)
	waitCtx, cancel := context.WithTimeout(context.Background(), defaultCancelWait)
	defer cancel()
	finalSnapshot, waitErr := s.Wait(waitCtx, started.ID)
	return finalSnapshot, errors.Join(ctxErr, cancelErr, waitErr)
}

func (s *Service) Wait(ctx context.Context, id string) (*Snapshot, error) {
	proc, err := s.get(id)
	if err != nil {
		return nil, err
	}
	select {
	case <-proc.done:
		snap := proc.snapshot()
		return &snap, nil
	case <-ctxDone(ctx):
		return nil, ctx.Err()
	}
}

func (s *Service) Snapshot(ctx context.Context, id string) (*Snapshot, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	proc, err := s.get(id)
	if err != nil {
		return nil, err
	}
	snap := proc.snapshot()
	return &snap, nil
}

func (s *Service) List(ctx context.Context) ([]Snapshot, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, errors.New("appserver/process: nil service")
	}
	s.mu.Lock()
	ids := make([]string, 0, len(s.processes))
	for id := range s.processes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	procs := make([]*managedProcess, 0, len(ids))
	for _, id := range ids {
		procs = append(procs, s.processes[id])
	}
	s.mu.Unlock()

	out := make([]Snapshot, 0, len(procs))
	for _, proc := range procs {
		out = append(out, proc.snapshot())
	}
	return out, nil
}

func (s *Service) CleanCompleted(ctx context.Context) ([]Snapshot, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, errors.New("appserver/process: nil service")
	}
	s.mu.Lock()
	ids := make([]string, 0, len(s.processes))
	for id := range s.processes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	removed := make([]Snapshot, 0, len(ids))
	for _, id := range ids {
		proc := s.processes[id]
		if proc.isRunning() {
			continue
		}
		removed = append(removed, proc.snapshot())
		delete(s.processes, id)
	}
	s.mu.Unlock()
	return removed, nil
}

func (s *Service) WriteStdin(ctx context.Context, id string, data []byte) error {
	op := Operation{Kind: OperationWriteStdin, ID: id}
	if err := checkContext(ctx); err != nil {
		s.emit(op, id, 0, false, err)
		return err
	}
	proc, err := s.get(id)
	if err != nil {
		s.emit(op, id, 0, false, err)
		return err
	}
	if err := s.requireApproval(ctx, op); err != nil {
		s.emit(op, id, proc.pid, false, err)
		return err
	}
	if err := proc.writeStdin(data); err != nil {
		s.emit(op, id, proc.pid, false, err)
		return err
	}
	s.emit(op, id, proc.pid, true, nil)
	return nil
}

func (s *Service) CloseStdin(ctx context.Context, id string) error {
	op := Operation{Kind: OperationCloseStdin, ID: id}
	if err := checkContext(ctx); err != nil {
		s.emit(op, id, 0, false, err)
		return err
	}
	proc, err := s.get(id)
	if err != nil {
		s.emit(op, id, 0, false, err)
		return err
	}
	if err := s.requireApproval(ctx, op); err != nil {
		s.emit(op, id, proc.pid, false, err)
		return err
	}
	if err := proc.closeStdin(); err != nil {
		s.emit(op, id, proc.pid, false, err)
		return err
	}
	s.emit(op, id, proc.pid, true, nil)
	return nil
}

func (s *Service) Terminate(ctx context.Context, id string) error {
	return s.signal(ctx, id, OperationTerminate, "SIGTERM", syscall.SIGTERM)
}

func (s *Service) Kill(ctx context.Context, id string) error {
	return s.signal(ctx, id, OperationKill, "SIGKILL", syscall.SIGKILL)
}

func (s *Service) ResizePTY(ctx context.Context, id string, cols, rows int) error {
	op := Operation{Kind: OperationResizePTY, ID: id}
	if err := checkContext(ctx); err != nil {
		s.emit(op, id, 0, false, err)
		return err
	}
	proc, err := s.get(id)
	if err != nil {
		s.emit(op, id, 0, false, err)
		return err
	}
	if cols <= 0 || rows <= 0 {
		err := errors.New("appserver/process: pty size must be positive")
		s.emit(op, id, proc.pid, false, err)
		return err
	}
	s.emit(op, id, proc.pid, false, ErrPTYUnsupported)
	return ErrPTYUnsupported
}

func (s *Service) signal(ctx context.Context, id string, kind OperationKind, signalName string, sig syscall.Signal) error {
	op := Operation{Kind: kind, ID: id, Signal: signalName, Destructive: true}
	if err := checkContext(ctx); err != nil {
		s.emit(op, id, 0, false, err)
		return err
	}
	proc, err := s.get(id)
	if err != nil {
		s.emit(op, id, 0, false, err)
		return err
	}
	if err := s.requireApproval(ctx, op); err != nil {
		s.emit(op, id, proc.pid, false, err)
		return err
	}
	if err := proc.requestKill(StatusKilled); err != nil {
		s.emit(op, id, proc.pid, false, err)
		return err
	}
	if err := signalProcessGroup(proc.pid, sig); err != nil {
		s.emit(op, id, proc.pid, false, err)
		return err
	}
	s.emit(op, id, proc.pid, true, nil)
	return nil
}

func (s *Service) cancelOwnedProcess(id string) error {
	op := Operation{Kind: OperationCancel, ID: id, Signal: "SIGKILL", Destructive: true}
	proc, err := s.get(id)
	if err != nil {
		s.emit(op, id, 0, false, err)
		return err
	}
	if err := proc.requestKill(StatusKilled); err != nil {
		if errors.Is(err, ErrProcessNotRunning) {
			return nil
		}
		s.emit(op, id, proc.pidValue(), false, err)
		return err
	}
	if err := signalProcessGroup(proc.pidValue(), syscall.SIGKILL); err != nil {
		s.emit(op, id, proc.pidValue(), false, err)
		return err
	}
	s.emit(op, id, proc.pidValue(), true, nil)
	return nil
}

func (s *Service) get(id string) (*managedProcess, error) {
	if s == nil {
		return nil, errors.New("appserver/process: nil service")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	proc, ok := s.processes[id]
	if !ok {
		return nil, ErrProcessNotFound
	}
	return proc, nil
}

func (s *Service) runningCountLocked() int {
	count := 0
	for _, proc := range s.processes {
		if proc.isRunning() {
			count++
		}
	}
	return count
}

func (s *Service) resolveWorkDir(ctx context.Context, path string) (string, string, error) {
	if path == "" {
		path = "."
	}
	meta, err := s.fs.Metadata(ctx, path)
	if err != nil {
		return "", "", err
	}
	if !meta.IsDir {
		return "", "", ErrInvalidWorkDir
	}
	rel := meta.Path
	if rel == "." {
		return s.fs.Root(), rel, nil
	}
	return filepath.Join(s.fs.Root(), filepath.FromSlash(rel)), rel, nil
}

func (s *Service) requireApproval(ctx context.Context, op Operation) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if s.approve == nil {
		return nil
	}
	if err := s.approve(ctx, op); err != nil {
		return fmt.Errorf("%w: %w", ErrApprovalDenied, err)
	}
	return nil
}

func (s *Service) emit(op Operation, id string, pid int, allowed bool, err error) {
	if s == nil || s.audit == nil {
		return
	}
	event := AuditEvent{
		Operation: op,
		ID:        id,
		PID:       pid,
		Allowed:   allowed,
		At:        time.Now().UTC(),
	}
	if err != nil {
		event.Err = err.Error()
	}
	s.audit(event)
}

func (s *Service) emitExit(proc *managedProcess) {
	if s == nil || proc == nil {
		return
	}
	snap := proc.snapshot()
	event := ExitEvent{Snapshot: snap, At: snap.EndedAt}
	if s.exit != nil && !proc.suppressGlobalSinks {
		s.exit(event)
	}
	if proc.exit != nil {
		proc.exit(event)
	}
}

func (p *managedProcess) snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Snapshot{
		ID:              p.id,
		PID:             p.pid,
		Command:         p.command,
		Args:            cloneStrings(p.args),
		Shell:           p.shell,
		WorkDir:         p.workDir,
		Status:          p.status,
		ExitCode:        p.exitCode,
		StartedAt:       p.startedAt,
		EndedAt:         p.endedAt,
		Error:           p.err,
		Stdout:          p.stdout.bytes(),
		Stderr:          p.stderr.bytes(),
		StdoutTruncated: p.stdout.wasTruncated(),
		StderrTruncated: p.stderr.wasTruncated(),
	}
}

func (p *managedProcess) setPID(pid int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pid = pid
}

func (p *managedProcess) pidValue() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pid
}

func (p *managedProcess) isRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status == StatusRunning
}

func (p *managedProcess) writeStdin(data []byte) error {
	p.mu.Lock()
	if p.status != StatusRunning {
		p.mu.Unlock()
		return ErrProcessNotRunning
	}
	stdin := p.stdin
	p.mu.Unlock()

	p.stdinMu.Lock()
	defer p.stdinMu.Unlock()
	_, err := stdin.Write(data)
	if err != nil {
		return fmt.Errorf("write stdin: %w", err)
	}
	return nil
}

func (p *managedProcess) closeStdin() error {
	p.mu.Lock()
	if p.status != StatusRunning {
		p.mu.Unlock()
		return ErrProcessNotRunning
	}
	stdin := p.stdin
	p.mu.Unlock()

	p.stdinMu.Lock()
	defer p.stdinMu.Unlock()
	if err := stdin.Close(); err != nil {
		return fmt.Errorf("close stdin: %w", err)
	}
	return nil
}

func (p *managedProcess) requestKill(status Status) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.status != StatusRunning {
		return ErrProcessNotRunning
	}
	p.killStatus = status
	return nil
}

func (p *managedProcess) finish(waitErr error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.endedAt = time.Now().UTC()
	switch {
	case p.killStatus != "":
		p.status = p.killStatus
		if waitErr != nil {
			p.exitCode = exitCode(waitErr)
			p.err = waitErr.Error()
		}
	case waitErr == nil:
		p.status = StatusCompleted
		p.exitCode = 0
	default:
		p.exitCode = exitCode(waitErr)
		p.status = StatusFailed
		p.err = waitErr.Error()
	}
}

func (p *managedProcess) appendOutput(stream Stream, data []byte) {
	if stream == StreamStderr {
		p.stderr.write(data)
		return
	}
	p.stdout.write(data)
}

func (w processWriter) Write(data []byte) (int, error) {
	chunk := append([]byte(nil), data...)
	w.proc.appendOutput(w.stream, chunk)
	if w.svc != nil && w.svc.output != nil && !w.proc.suppressGlobalSinks {
		w.svc.output(OutputEvent{ID: w.proc.id, PID: w.proc.pidValue(), Stream: w.stream, Data: chunk, At: time.Now().UTC()})
	}
	if w.proc.output != nil {
		w.proc.output(OutputEvent{ID: w.proc.id, PID: w.proc.pidValue(), Stream: w.stream, Data: chunk, At: time.Now().UTC()})
	}
	return len(data), nil
}

func newOutputBuffer(size int) *outputBuffer {
	return &outputBuffer{size: size}
}

func (b *outputBuffer) write(data []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, data...)
	if len(b.buf) > b.size {
		b.truncated = true
		b.buf = append([]byte(nil), b.buf[len(b.buf)-b.size:]...)
	}
}

func (b *outputBuffer) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf...)
}

func (b *outputBuffer) wasTruncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

type commandSpec struct {
	name string
	args []string
}

func normalizeStart(req StartRequest) (commandSpec, error) {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return commandSpec{}, ErrEmptyCommand
	}
	if req.Shell {
		return commandSpec{name: "bash", args: []string{"-c", req.Command}}, nil
	}
	return commandSpec{name: req.Command, args: cloneStrings(req.Args)}, nil
}

func buildEnv(extra map[string]string) []string {
	if len(extra) == 0 {
		return nil
	}
	env := os.Environ()
	keys := make([]string, 0, len(extra))
	for key := range extra {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+extra[key])
	}
	return env
}

func enforceTimeout(proc *managedProcess, timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-proc.done:
		return
	case <-timer.C:
		if err := proc.requestKill(StatusTimedOut); err == nil {
			_ = signalProcessGroup(proc.pid, syscall.SIGKILL)
		}
	}
}

func signalProcessGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return ErrProcessNotRunning
	}
	if err := syscall.Kill(-pid, sig); err != nil {
		return fmt.Errorf("signal process group: %w", err)
	}
	return nil
}

func exitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func ctxDone(ctx context.Context) <-chan struct{} {
	if ctx == nil {
		return nil
	}
	return ctx.Done()
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
}
