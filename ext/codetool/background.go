package codetool

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fugue-labs/gollem/core"
)

const (
	maxBackgroundProcesses = 10
	ringBufferCapacity     = 1000
)

// processStatus represents the state of a background process.
type processStatus int

const (
	processRunning   processStatus = iota
	processCompleted               // exit code 0
	processFailed                  // exit code != 0
)

// RingBuffer is a fixed-capacity circular buffer of text lines.
// It is safe for concurrent use.
type RingBuffer struct {
	mu    sync.Mutex
	buf   []string
	size  int
	write int
	count int
}

// NewRingBuffer creates a ring buffer that holds up to size lines.
func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		buf:  make([]string, size),
		size: size,
	}
}

// WriteLine appends a line to the buffer, evicting the oldest if full.
func (r *RingBuffer) WriteLine(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.write] = line
	r.write = (r.write + 1) % r.size
	if r.count < r.size {
		r.count++
	}
}

// LastN returns the last n lines joined by newlines.
func (r *RingBuffer) LastN(n int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n > r.count {
		n = r.count
	}
	if n == 0 {
		return ""
	}
	result := make([]string, n)
	start := (r.write - n + r.size) % r.size
	for i := range n {
		result[i] = r.buf[(start+i)%r.size]
	}
	return strings.Join(result, "\n")
}

// Len returns the number of lines currently in the buffer.
func (r *RingBuffer) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

// BackgroundProcess tracks a single background process.
type BackgroundProcess struct {
	ID        string
	PID       int
	Command   string
	StartedAt time.Time
	Status    processStatus
	ExitCode  int
	Stdout    *RingBuffer // used for pipe-based capture (non-keep_alive)
	Stderr    *RingBuffer // used for pipe-based capture (non-keep_alive)
	LogPath   string      // used for file-based capture (keep_alive)
	KeepAlive bool
	Notified  bool
	cmd       *exec.Cmd
}

// BackgroundProcessManager manages background processes started by the bash tool.
type BackgroundProcessManager struct {
	mu        sync.Mutex
	processes map[string]*BackgroundProcess
	counter   int
}

// NewBackgroundProcessManager creates a new manager.
func NewBackgroundProcessManager() *BackgroundProcessManager {
	return &BackgroundProcessManager{
		processes: make(map[string]*BackgroundProcess),
	}
}

// Start launches a command as a background process. It returns immediately
// with a status message containing the process ID and PID.
//
// For keep_alive processes, stdout/stderr are redirected to a log file
// instead of pipes. This prevents SIGPIPE from killing the process when
// the parent Go process exits (pipe read ends close → child gets SIGPIPE
// on next write). Non-keep_alive processes use pipes for real-time ring
// buffer capture, since they're killed on agent exit anyway.
func (m *BackgroundProcessManager) Start(workDir, command string, keepAlive bool, timeout time.Duration) (string, error) {
	// Set up the command before acquiring the lock.
	// Use a background context — background processes outlive the calling tool's context.
	cmd := exec.CommandContext(context.Background(), "bash", "-c", command)
	if workDir != "" {
		cmd.Dir = workDir
	}
	if isPipCommand(command) {
		cmd.Env = append(os.Environ(), "PIP_BREAK_SYSTEM_PACKAGES=1")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Output capture strategy depends on keep_alive.
	var stdoutPipe, stderrPipe io.ReadCloser
	var logFile *os.File
	var logPath string

	if keepAlive {
		// File-based: process survives parent exit (no SIGPIPE).
		// Use a temp file; the child inherits the fd via fork.
		var err error
		logFile, err = os.CreateTemp("", "gollem-bg-*.log")
		if err != nil {
			return "", fmt.Errorf("create background log: %w", err)
		}
		logPath = logFile.Name()
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	} else {
		// Pipe-based: real-time ring buffer capture.
		var err error
		stdoutPipe, err = cmd.StdoutPipe()
		if err != nil {
			return "", fmt.Errorf("stdout pipe: %w", err)
		}
		stderrPipe, err = cmd.StderrPipe()
		if err != nil {
			return "", fmt.Errorf("stderr pipe: %w", err)
		}
	}

	// Hold the lock through the limit check, process start, and map
	// insertion to prevent TOCTOU races when multiple goroutines (e.g.,
	// parent agent + subagent) share the same manager. cmd.Start() is
	// a non-blocking fork+exec, so lock duration is negligible.
	m.mu.Lock()
	running := 0
	for _, p := range m.processes {
		if p.Status == processRunning {
			running++
		}
	}
	if running >= maxBackgroundProcesses {
		m.mu.Unlock()
		if logFile != nil {
			logFile.Close()
			//nolint:gosec // logPath is from os.CreateTemp, not user input.
			_ = os.Remove(logPath)
		}
		return "", &core.ModelRetryError{
			Message: fmt.Sprintf("maximum concurrent background processes (%d) reached. "+
				"Use bash_status with id='all' to check existing processes.", maxBackgroundProcesses),
		}
	}

	if err := cmd.Start(); err != nil {
		m.mu.Unlock()
		if logFile != nil {
			logFile.Close()
			//nolint:gosec // logPath is from os.CreateTemp, not user input.
			_ = os.Remove(logPath)
		}
		return "", fmt.Errorf("start background process: %w", err)
	}

	m.counter++
	id := fmt.Sprintf("bg-%d", m.counter)
	proc := &BackgroundProcess{
		ID:        id,
		PID:       cmd.Process.Pid,
		Command:   command,
		StartedAt: time.Now(),
		Status:    processRunning,
		Stdout:    NewRingBuffer(ringBufferCapacity),
		Stderr:    NewRingBuffer(ringBufferCapacity),
		LogPath:   logPath,
		KeepAlive: keepAlive,
		cmd:       cmd,
	}
	m.processes[id] = proc
	m.mu.Unlock()

	// Close the parent's copy of the log file fd. The child inherited
	// its own fd via fork, so it continues writing independently.
	if logFile != nil {
		logFile.Close()
	}

	// Channel to signal process completion (for timeout cancellation).
	done := make(chan struct{})

	if !keepAlive {
		// Pipe-based: read stdout/stderr into ring buffers.
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); scanToBuffer(stdoutPipe, proc.Stdout) }()
		go func() { defer wg.Done(); scanToBuffer(stderrPipe, proc.Stderr) }()

		go func() {
			wg.Wait()             // Wait for all output to be captured.
			waitErr := cmd.Wait() // Get exit status.
			close(done)
			m.mu.Lock()
			defer m.mu.Unlock()
			m.setExitStatus(proc, waitErr)
		}()
	} else {
		// File-based: no scanner goroutines needed.
		go func() {
			waitErr := cmd.Wait()
			close(done)
			m.mu.Lock()
			defer m.mu.Unlock()
			m.setExitStatus(proc, waitErr)
		}()
	}

	// Timeout enforcement: kill the process group if it exceeds the deadline.
	if timeout > 0 {
		go func() {
			select {
			case <-done:
				return
			case <-time.After(timeout):
				m.mu.Lock()
				if proc.Status == processRunning {
					_ = syscall.Kill(-proc.cmd.Process.Pid, syscall.SIGKILL)
					fmt.Fprintf(os.Stderr, "[gollem] background:%s timed out after %s\n", id, timeout)
				}
				m.mu.Unlock()
			}
		}()
	}

	return fmt.Sprintf("Background process started (id: %s, pid: %d).\n"+
		"Use `bash_status` tool with id '%s' to check progress.\n"+
		"You will receive a notification when the process completes.", id, proc.PID, id), nil
}

// setExitStatus updates a process's status from cmd.Wait's error.
// Caller must hold m.mu.
func (m *BackgroundProcessManager) setExitStatus(proc *BackgroundProcess, waitErr error) {
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			proc.ExitCode = exitErr.ExitCode()
		} else {
			proc.ExitCode = -1
		}
		proc.Status = processFailed
	} else {
		proc.ExitCode = 0
		proc.Status = processCompleted
	}
	fmt.Fprintf(os.Stderr, "[gollem] background:%s (pid %d) exited with code %d\n",
		proc.ID, proc.PID, proc.ExitCode)
}

// Adopt takes ownership of an already-running process, assigning it a
// background ID and wiring up a wait goroutine for status tracking.
// This is used when a foreground bash command is detached to the background
// (e.g., via a UI "move to background" action).
//
// The caller must have already called cmd.Start(). The provided stdout and
// stderr readers will be drained into ring buffers by scanner goroutines.
func (m *BackgroundProcessManager) Adopt(cmd *exec.Cmd, stdout, stderr io.Reader, command string) (string, error) {
	return m.AdoptWithWait(cmd, stdout, stderr, command, cmd.Wait)
}

// AdoptWithWait is like Adopt but accepts a custom wait function. This is used
// when cmd.Wait is shared with another goroutine via sync.Once to prevent
// data races on the exec.Cmd.
func (m *BackgroundProcessManager) AdoptWithWait(cmd *exec.Cmd, stdout, stderr io.Reader, command string, waitFn func() error) (string, error) {
	m.mu.Lock()

	running := 0
	for _, p := range m.processes {
		if p.Status == processRunning {
			running++
		}
	}
	if running >= maxBackgroundProcesses {
		m.mu.Unlock()
		return "", &core.ModelRetryError{
			Message: fmt.Sprintf("maximum concurrent background processes (%d) reached. "+
				"Use bash_status with id='all' to check existing processes.", maxBackgroundProcesses),
		}
	}

	m.counter++
	id := fmt.Sprintf("bg-%d", m.counter)
	proc := &BackgroundProcess{
		ID:        id,
		PID:       cmd.Process.Pid,
		Command:   command,
		StartedAt: time.Now(),
		Status:    processRunning,
		Stdout:    NewRingBuffer(ringBufferCapacity),
		Stderr:    NewRingBuffer(ringBufferCapacity),
		cmd:       cmd,
	}
	m.processes[id] = proc
	m.mu.Unlock()

	// Drain stdout/stderr into ring buffers.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); scanToBuffer(stdout, proc.Stdout) }()
	go func() { defer wg.Done(); scanToBuffer(stderr, proc.Stderr) }()

	go func() {
		wg.Wait()
		waitErr := waitFn()
		m.mu.Lock()
		defer m.mu.Unlock()
		m.setExitStatus(proc, waitErr)
	}()

	fmt.Fprintf(os.Stderr, "[gollem] background:%s adopted (pid %d)\n", id, proc.PID)
	return id, nil
}

// adoptTrackedOutput takes ownership of a running process whose stdout/stderr
// are already being drained elsewhere into the provided ring buffers. This is
// used by detach-aware foreground bash execution, where the caller keeps the
// pipe readers and only hands off status tracking to the manager.
func (m *BackgroundProcessManager) adoptTrackedOutput(
	cmd *exec.Cmd,
	command string,
	stdout, stderr *RingBuffer,
	waitFn func() error,
) (string, error) {
	m.mu.Lock()

	running := 0
	for _, p := range m.processes {
		if p.Status == processRunning {
			running++
		}
	}
	if running >= maxBackgroundProcesses {
		m.mu.Unlock()
		return "", &core.ModelRetryError{
			Message: fmt.Sprintf("maximum concurrent background processes (%d) reached. "+
				"Use bash_status with id='all' to check existing processes.", maxBackgroundProcesses),
		}
	}

	m.counter++
	id := fmt.Sprintf("bg-%d", m.counter)
	proc := &BackgroundProcess{
		ID:        id,
		PID:       cmd.Process.Pid,
		Command:   command,
		StartedAt: time.Now(),
		Status:    processRunning,
		Stdout:    stdout,
		Stderr:    stderr,
		cmd:       cmd,
	}
	m.processes[id] = proc
	m.mu.Unlock()

	go func() {
		waitErr := waitFn()
		m.mu.Lock()
		defer m.mu.Unlock()
		m.setExitStatus(proc, waitErr)
	}()

	fmt.Fprintf(os.Stderr, "[gollem] background:%s adopted (pid %d)\n", id, proc.PID)
	return id, nil
}

// CompletionPrompt returns a dynamic system prompt message for any background
// processes that have completed since the last call. It marks notified
// processes so each completion is reported only once.
//
// This is intended to be used with core.WithDynamicSystemPrompt.
func (m *BackgroundProcessManager) CompletionPrompt(_ context.Context, _ *core.RunContext) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var parts []string
	for _, proc := range m.processes {
		if proc.Status != processRunning && !proc.Notified {
			proc.Notified = true
			status := "completed"
			if proc.Status == processFailed {
				status = "failed"
			}
			elapsed := time.Since(proc.StartedAt).Round(time.Second)
			msg := fmt.Sprintf("[Background process %s %s (exit code %d) after %s]\nCommand: %s",
				proc.ID, status, proc.ExitCode, elapsed, proc.Command)
			if proc.LogPath != "" {
				if out := tailFile(proc.LogPath, 20); out != "" {
					msg += "\nLast output:\n" + out
				}
			} else {
				if lastOut := proc.Stdout.LastN(20); lastOut != "" {
					msg += "\nLast stdout:\n" + lastOut
				}
				if lastErr := proc.Stderr.LastN(20); lastErr != "" {
					msg += "\nLast stderr:\n" + lastErr
				}
			}
			parts = append(parts, msg)
		}
	}
	if len(parts) == 0 {
		return "", nil
	}
	return strings.Join(parts, "\n\n"), nil
}

// FormatProcess returns a formatted status string for a specific process.
func (m *BackgroundProcessManager) FormatProcess(id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	proc, ok := m.processes[id]
	if !ok {
		return "", &core.ModelRetryError{
			Message: fmt.Sprintf("no background process with id '%s'. Use id='all' to list all processes.", id),
		}
	}
	return formatProcessStatus(proc), nil
}

// FormatAll returns a formatted status string for all background processes.
func (m *BackgroundProcessManager) FormatAll() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.processes) == 0 {
		return "No background processes."
	}

	ids := make([]string, 0, len(m.processes))
	for id := range m.processes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var parts []string
	for _, id := range ids {
		parts = append(parts, formatProcessStatus(m.processes[id]))
	}
	return strings.Join(parts, "\n\n")
}

// Cleanup kills all running background processes that are not marked KeepAlive.
// Called when the agent run ends.
func (m *BackgroundProcessManager) Cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, proc := range m.processes {
		if proc.Status == processRunning && !proc.KeepAlive {
			_ = syscall.Kill(-proc.cmd.Process.Pid, syscall.SIGKILL)
			fmt.Fprintf(os.Stderr, "[gollem] background:%s cleaned up (pid %d)\n", proc.ID, proc.PID)
		}
	}
}

// formatProcessStatus formats a single process's status for display.
// Caller must hold m.mu (or the process must be otherwise safe to read).
func formatProcessStatus(proc *BackgroundProcess) string {
	elapsed := time.Since(proc.StartedAt).Round(time.Second)

	var status string
	switch proc.Status {
	case processRunning:
		status = fmt.Sprintf("running for %s", elapsed)
	case processCompleted:
		status = fmt.Sprintf("completed (exit code 0) after %s", elapsed)
	case processFailed:
		status = fmt.Sprintf("failed (exit code %d) after %s", proc.ExitCode, elapsed)
	}

	result := fmt.Sprintf("%s (pid %d): %s\nCommand: %s", proc.ID, proc.PID, status, proc.Command)

	// Output: file-based for keep_alive, ring buffers for pipe-based.
	if proc.LogPath != "" {
		if out := tailFile(proc.LogPath, 20); out != "" {
			result += "\nLast output:\n" + out
		}
	} else {
		if lastOut := proc.Stdout.LastN(20); lastOut != "" {
			result += "\nLast stdout:\n" + lastOut
		}
		if lastErr := proc.Stderr.LastN(20); lastErr != "" {
			result += "\nLast stderr:\n" + lastErr
		}
	}
	if proc.KeepAlive {
		result += "\n[keep_alive: will persist after agent exit]"
	}
	return result
}

// scanToBuffer reads lines from r and writes them to buf until EOF.
func scanToBuffer(r io.Reader, buf *RingBuffer) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024) // up to 1MB lines
	for scanner.Scan() {
		buf.WriteLine(scanner.Text())
	}
}

// tailFile returns the last n lines from a file. Returns empty string
// on any error. Reads at most 1MB from the end of the file.
func tailFile(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	const maxRead = 1 << 20 // 1MB
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return ""
	}

	readSize := info.Size()
	offset := int64(0)
	if readSize > maxRead {
		offset = readSize - maxRead
		readSize = maxRead
	}

	buf := make([]byte, readSize)
	if _, err := f.ReadAt(buf, offset); err != nil && err != io.EOF {
		return ""
	}

	lines := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
