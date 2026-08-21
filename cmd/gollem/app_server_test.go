package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	appserver "github.com/fugue-labs/gollem/appserver"
	"github.com/fugue-labs/gollem/appserver/catalog"
	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
	"github.com/fugue-labs/gollem/core"
	openaiprovider "github.com/fugue-labs/gollem/provider/openai"
)

func TestParseAppServerFlags(t *testing.T) {
	got, err := parseAppServerFlags([]string{
		"--workdir", "/tmp/work",
		"--store", "state/app.db",
		"--git-root", "/tmp/repo",
		"--worktree-root", "/tmp/worktrees",
		"--provider", "test",
		"--model", "test-model",
		"--location", "global",
		"--project", "test-project",
		"--allow-mutations",
	})
	if err != nil {
		t.Fatalf("parseAppServerFlags: %v", err)
	}
	if got.workDir != "/tmp/work" || got.storePath != "state/app.db" || got.gitRoot != "/tmp/repo" || got.worktreeRoot != "/tmp/worktrees" {
		t.Fatalf("flags = %#v", got)
	}
	if got.provider != "test" || got.modelName != "test-model" || got.location != "global" || got.project != "test-project" {
		t.Fatalf("runtime flags = %#v", got)
	}
	if !got.allowMutations || !got.stdio || !got.gitRootExplicit {
		t.Fatalf("boolean flags = %#v", got)
	}

	if _, err := parseAppServerFlags([]string{"--unknown"}); err == nil || !strings.Contains(err.Error(), "unknown app-server") {
		t.Fatalf("unknown flag error = %v", err)
	}

	network, err := parseAppServerFlags([]string{
		"--socket", filepath.Join(t.TempDir(), "gollem.sock"),
		"--websocket", "127.0.0.1:0",
		"--websocket-path", "/rpc",
	})
	if err != nil {
		t.Fatalf("parse network app-server flags: %v", err)
	}
	if network.stdio || network.socketPath == "" || network.websocketAddr != "127.0.0.1:0" || network.websocketPath != "/rpc" {
		t.Fatalf("network flags = %#v", network)
	}

	explicitStdio, err := parseAppServerFlags([]string{"--stdio=true", "--socket", filepath.Join(t.TempDir(), "gollem.sock")})
	if err != nil {
		t.Fatalf("parse explicit stdio app-server flags: %v", err)
	}
	if !explicitStdio.stdio || !explicitStdio.stdioExplicit {
		t.Fatalf("explicit stdio flags = %#v", explicitStdio)
	}

	if _, err := parseAppServerFlags([]string{"--stdio=false"}); err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("missing transport error = %v", err)
	}
	if _, err := parseAppServerFlags([]string{"--websocket", "127.0.0.1:0", "--websocket-path", "rpc"}); err == nil || !strings.Contains(err.Error(), "must start with /") {
		t.Fatalf("invalid websocket path error = %v", err)
	}
}

func TestRecoverCLIAppServerStateTerminalizesStaleRuntimeBeforeServing(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()
	storePath := filepath.Join(workDir, "app.db")
	st, err := store.NewSQLiteStore(storePath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	thread, err := st.CreateThread(ctx, store.CreateThreadRequest{Title: "recovery"})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	turn, err := st.CreateTurn(ctx, store.CreateTurnRequest{ThreadID: thread.ID})
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	if _, err := st.StartTurn(ctx, turn.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	activeItem, err := st.AppendItem(ctx, store.AppendItemRequest{
		ThreadID: thread.ID,
		TurnID:   turn.ID,
		Kind:     "commandExecution",
		Status:   "inProgress",
		Payload:  json.RawMessage(`{"status":"inProgress","command":"sleep"}`),
	})
	if err != nil {
		t.Fatalf("AppendItem: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := recoverCLIAppServerState(appServerFlags{
		workDir:   workDir,
		storePath: storePath,
	}); err != nil {
		t.Fatalf("recoverCLIAppServerState: %v", err)
	}

	reopened, err := store.NewSQLiteStore(storePath)
	if err != nil {
		t.Fatalf("reopen NewSQLiteStore: %v", err)
	}
	defer reopened.Close()
	recoveredTurn, err := reopened.GetTurn(ctx, turn.ID)
	if err != nil {
		t.Fatalf("GetTurn: %v", err)
	}
	if recoveredTurn.Status != store.TurnInterrupted || recoveredTurn.Error != store.RuntimeOwnerLostReason {
		t.Fatalf("recovered turn = %#v", recoveredTurn)
	}
	recoveredItem, err := reopened.GetItem(ctx, activeItem.ID)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if recoveredItem.Status != "failed" || !strings.Contains(string(recoveredItem.Payload), `"status":"failed"`) {
		t.Fatalf("recovered item = %#v", recoveredItem)
	}
	items, err := reopened.ListItems(ctx, store.ItemFilter{ThreadID: thread.ID, TurnID: turn.ID})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	recoveryMarkers := 0
	for _, item := range items {
		if item.Kind == "runtimeRecovery" {
			recoveryMarkers++
		}
	}
	if recoveryMarkers != 1 {
		t.Fatalf("runtime recovery markers = %d, want 1", recoveryMarkers)
	}
}

func TestCLIAppServerRecoversAfterHardKillAndRetriesExactlyOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hard process kill semantics differ on Windows")
	}
	workDir := t.TempDir()
	storePath := filepath.Join(workDir, "app.db")

	first := startHardKillCLIProcess(t, workDir, storePath, "30s")
	first.initialize(t)
	assertHardKillCLIStoreRejectsContender(t, workDir, storePath)
	startResp := first.request(t, "start", "thread/start", map[string]any{
		"prompt": "survive a hard kill",
	})
	if startResp.Error != nil {
		first.kill(t)
		t.Fatalf("thread/start error: %v", startResp.Error)
	}
	var started protocol.ThreadRunStartResult
	if err := json.Unmarshal(startResp.Result, &started); err != nil {
		first.kill(t)
		t.Fatalf("decode thread/start result: %v", err)
	}
	if started.Turn.ID == "" || started.Turn.Status != protocol.TurnLifecycleRunning {
		first.kill(t)
		t.Fatalf("started turn = %#v", started.Turn)
	}
	first.kill(t)

	second := startHardKillCLIProcess(t, workDir, storePath, "0s")
	defer second.killIfRunning()
	second.initialize(t)
	recovered := second.readThread(t, started.Thread.ID)
	if len(recovered.Turns) != 1 {
		t.Fatalf("recovered turns = %#v", recovered.Turns)
	}
	recoveredTurn := recovered.Turns[0]
	if recoveredTurn.ID != started.Turn.ID ||
		recoveredTurn.Status != protocol.TurnLifecycleInterrupted ||
		recoveredTurn.Error != store.RuntimeOwnerLostReason {
		t.Fatalf("recovered turn = %#v", recoveredTurn)
	}
	recoveryMarkers := 0
	for _, item := range recovered.Items {
		if item.Kind == "runtimeRecovery" && item.TurnID == started.Turn.ID {
			recoveryMarkers++
		}
	}
	if recoveryMarkers != 1 {
		t.Fatalf("runtime recovery markers = %d, items = %#v", recoveryMarkers, recovered.Items)
	}

	retryParams := map[string]any{
		"turnId":         started.Turn.ID,
		"idempotencyKey": "hard-kill-retry-1",
	}
	retryResp := second.request(t, "retry", "turn/retry", retryParams)
	if retryResp.Error != nil {
		t.Fatalf("turn/retry error: %v", retryResp.Error)
	}
	var retried protocol.TurnRunRetryResult
	if err := json.Unmarshal(retryResp.Result, &retried); err != nil {
		t.Fatalf("decode turn/retry result: %v", err)
	}
	if retried.Reused || retried.SourceTurnID != started.Turn.ID || retried.Turn.ID == "" {
		t.Fatalf("first retry = %#v", retried)
	}
	completed := second.waitForTurnStatus(t, started.Thread.ID, retried.Turn.ID, protocol.TurnLifecycleCompleted)
	if completed.Error != "" {
		t.Fatalf("completed retry = %#v", completed)
	}

	duplicateResp := second.request(t, "retry-duplicate", "turn/retry", retryParams)
	if duplicateResp.Error != nil {
		t.Fatalf("duplicate turn/retry error: %v", duplicateResp.Error)
	}
	var duplicate protocol.TurnRunRetryResult
	if err := json.Unmarshal(duplicateResp.Result, &duplicate); err != nil {
		t.Fatalf("decode duplicate turn/retry result: %v", err)
	}
	if !duplicate.Reused || duplicate.Turn.ID != retried.Turn.ID ||
		duplicate.IdempotencyKey != "hard-kill-retry-1" {
		t.Fatalf("duplicate retry = %#v, first = %#v", duplicate, retried)
	}
	final := second.readThread(t, started.Thread.ID)
	if len(final.Turns) != 2 {
		t.Fatalf("final turns = %#v, want interrupted source plus one retry", final.Turns)
	}
	if final.Turns[1].RetryOfTurnID != started.Turn.ID ||
		final.Turns[1].RetryIdempotencyKey != "hard-kill-retry-1" {
		t.Fatalf("durable retry lineage = %#v", final.Turns[1])
	}
	second.close(t)
}

func assertHardKillCLIStoreRejectsContender(t *testing.T, workDir, storePath string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCLIAppServerHardKillHelper$")
	cmd.Env = append(os.Environ(),
		"GOLLEM_HARD_KILL_HELPER=1",
		"GOLLEM_HARD_KILL_WORKDIR="+workDir,
		"GOLLEM_HARD_KILL_STORE="+storePath,
		"GOLLEM_TEST_MODEL_DELAY=0s",
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("contending app-server unexpectedly acquired the live store: %s", output)
	}
	if !strings.Contains(string(output), errAppServerStoreOwned.Error()) {
		t.Fatalf("contending app-server error = %v output=%s", err, output)
	}
}

func TestCLIAppServerHardKillHelper(t *testing.T) {
	if os.Getenv("GOLLEM_HARD_KILL_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	err := serveCLIAppServerTransports(context.Background(), appServerFlags{
		workDir:        os.Getenv("GOLLEM_HARD_KILL_WORKDIR"),
		storePath:      os.Getenv("GOLLEM_HARD_KILL_STORE"),
		provider:       "test",
		stdio:          true,
		allowMutations: true,
	})
	if err != nil {
		t.Fatalf("serveCLIAppServerTransports: %v", err)
	}
}

type hardKillCLIProcess struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	lines   <-chan string
	scanErr <-chan error
	stderr  *bytes.Buffer
	running bool
	nextID  int
}

func startHardKillCLIProcess(t *testing.T, workDir, storePath, delay string) *hardKillCLIProcess {
	t.Helper()
	stderr := &bytes.Buffer{}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCLIAppServerHardKillHelper$")
	cmd.Env = append(os.Environ(),
		"GOLLEM_HARD_KILL_HELPER=1",
		"GOLLEM_HARD_KILL_WORKDIR="+workDir,
		"GOLLEM_HARD_KILL_STORE="+storePath,
		"GOLLEM_TEST_MODEL_DELAY="+delay,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("hard-kill helper stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("hard-kill helper stdout: %v", err)
	}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start hard-kill helper: %v", err)
	}
	lines := make(chan string, 256)
	scanErr := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		scanErr <- scanner.Err()
		close(lines)
	}()
	return &hardKillCLIProcess{
		cmd:     cmd,
		stdin:   stdin,
		lines:   lines,
		scanErr: scanErr,
		stderr:  stderr,
		running: true,
	}
}

func (p *hardKillCLIProcess) initialize(t *testing.T) {
	t.Helper()
	resp := p.request(t, "init", "initialize", map[string]any{
		"clientInfo": map[string]any{"name": "hard-kill-test", "version": "1.0.0"},
	})
	if resp.Error != nil {
		t.Fatalf("initialize error: %v", resp.Error)
	}
	p.notify(t, "initialized", nil)
}

func (p *hardKillCLIProcess) request(t *testing.T, id, method string, params any) protocol.Response {
	t.Helper()
	line, err := json.Marshal(map[string]any{
		"id":     id,
		"method": method,
		"params": params,
	})
	if err != nil {
		t.Fatalf("marshal %s request: %v", method, err)
	}
	writeCLIInputLine(t, p.stdin, string(line))
	return p.waitResponse(t, id)
}

func (p *hardKillCLIProcess) notify(t *testing.T, method string, params any) {
	t.Helper()
	line, err := json.Marshal(map[string]any{
		"method": method,
		"params": params,
	})
	if err != nil {
		t.Fatalf("marshal %s notification: %v", method, err)
	}
	writeCLIInputLine(t, p.stdin, string(line))
}

func (p *hardKillCLIProcess) waitResponse(t *testing.T, id string) protocol.Response {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case line, ok := <-p.lines:
			if !ok {
				var scanErr error
				select {
				case scanErr = <-p.scanErr:
				default:
				}
				t.Fatalf("hard-kill helper output closed waiting for %q: scan=%v stderr=%s", id, scanErr, p.stderr)
			}
			var envelope struct {
				ID json.RawMessage `json:"id"`
			}
			if err := json.Unmarshal([]byte(line), &envelope); err != nil || len(envelope.ID) == 0 {
				continue
			}
			var resp protocol.Response
			if err := json.Unmarshal([]byte(line), &resp); err != nil {
				t.Fatalf("decode hard-kill helper response %s: %v", line, err)
			}
			if got, _ := resp.ID.Value().(string); got == id {
				return resp
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for response %q: stderr=%s", id, p.stderr)
		}
	}
}

func (p *hardKillCLIProcess) readThread(t *testing.T, threadID string) protocol.ThreadReadResult {
	t.Helper()
	p.nextID++
	resp := p.request(t, fmt.Sprintf("read-%d", p.nextID), "thread/read", map[string]any{
		"threadId":     threadID,
		"includeTurns": true,
		"includeItems": true,
	})
	if resp.Error != nil {
		t.Fatalf("thread/read error: %v", resp.Error)
	}
	var read protocol.ThreadReadResult
	if err := json.Unmarshal(resp.Result, &read); err != nil {
		t.Fatalf("decode thread/read result: %v", err)
	}
	return read
}

func (p *hardKillCLIProcess) waitForTurnStatus(
	t *testing.T,
	threadID string,
	turnID string,
	status protocol.TurnLifecycleStatus,
) protocol.TurnRecord {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		read := p.readThread(t, threadID)
		for _, turn := range read.Turns {
			if turn.ID == turnID && turn.Status == status {
				return turn
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("turn %s did not reach %s", turnID, status)
	return protocol.TurnRecord{}
}

func (p *hardKillCLIProcess) kill(t *testing.T) {
	t.Helper()
	if !p.running {
		return
	}
	if err := p.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill hard-kill helper: %v", err)
	}
	_ = p.stdin.Close()
	if err := p.cmd.Wait(); err == nil {
		t.Fatal("hard-kill helper exited successfully after Process.Kill")
	}
	p.running = false
}

func (p *hardKillCLIProcess) killIfRunning() {
	if p == nil || !p.running {
		return
	}
	_ = p.cmd.Process.Kill()
	_ = p.stdin.Close()
	_ = p.cmd.Wait()
	p.running = false
}

func (p *hardKillCLIProcess) close(t *testing.T) {
	t.Helper()
	if !p.running {
		return
	}
	if err := p.stdin.Close(); err != nil {
		t.Fatalf("close hard-kill helper stdin: %v", err)
	}
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- p.cmd.Wait()
	}()
	select {
	case err := <-waitCh:
		if err != nil {
			t.Fatalf("hard-kill helper shutdown: %v stderr=%s", err, p.stderr)
		}
		p.running = false
	case <-time.After(10 * time.Second):
		_ = p.cmd.Process.Kill()
		_ = p.stdin.Close()
		<-waitCh
		p.running = false
		t.Fatal("timed out shutting down hard-kill helper")
	}
}

func TestCLIAppServerDefaultRequiresMutationApproval(t *testing.T) {
	server, cleanup, err := newCLIAppServer(appServerFlags{
		workDir:   t.TempDir(),
		storePath: ":memory:",
		stdio:     true,
	})
	if err != nil {
		t.Fatalf("newCLIAppServer: %v", err)
	}
	defer cleanup()
	readyCLIAppServer(t, server)

	respCh := make(chan protocol.Response, 1)
	go func() {
		respCh <- server.HandleRequest(context.Background(), protocol.Request{
			ID:     protocol.NewStringID("write"),
			Method: "fs/writeFile",
			Params: json.RawMessage(`{"path":"blocked.txt","content":"nope"}`),
		})
	}()
	select {
	case <-server.RequestSignal():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval request")
	}
	requests := server.DrainRequests()
	if len(requests) != 1 || requests[0].Method != "item/fileChange/requestApproval" {
		t.Fatalf("approval requests = %#v", requests)
	}
	requestID, _ := requests[0].ID.Value().(string)
	denyResp := server.HandleRequest(context.Background(), protocol.Request{
		ID:     protocol.NewStringID("deny"),
		Method: "approval/respond",
		Params: json.RawMessage(`{"requestId":"` + requestID + `","approved":false,"message":"denied in test"}`),
	})
	if denyResp.Error != nil {
		t.Fatalf("approval/respond error: %v", denyResp.Error)
	}
	var resp protocol.Response
	select {
	case resp = <-respCh:
	case <-time.After(2 * time.Second):
		t.Fatal("write request did not finish after denied approval")
	}
	if resp.Error == nil || resp.Error.Code != protocol.CodeInvalidRequest {
		t.Fatalf("write response error = %#v, want invalid request", resp.Error)
	}
}

func TestCLIAppServerServesStdioWhenMutationsAllowed(t *testing.T) {
	workDir := t.TempDir()
	server, cleanup, err := newCLIAppServer(appServerFlags{
		workDir:        workDir,
		storePath:      filepath.Join(workDir, "app.db"),
		stdio:          true,
		allowMutations: true,
	})
	if err != nil {
		t.Fatalf("newCLIAppServer: %v", err)
	}
	defer cleanup()

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		err := appserver.ServeJSONLines(context.Background(), server, inR, outW)
		if err != nil {
			_ = outW.CloseWithError(err)
		} else {
			_ = outW.Close()
		}
		errCh <- err
	}()
	scanner := bufio.NewScanner(outR)
	writeCLIInputLine(t, inW, `{"id":"init","method":"initialize","params":{"clientInfo":{"name":"test-client","version":"1.0.0"}}}`)
	writeCLIInputLine(t, inW, `{"method":"initialized"}`)
	var initResp protocol.Response
	if err := json.Unmarshal([]byte(readCLIOutputLine(t, scanner)), &initResp); err != nil {
		t.Fatalf("decode init response: %v", err)
	}
	if initResp.Error != nil {
		t.Fatalf("init error: %v", initResp.Error)
	}
	var initResult protocol.InitializeResponse
	if err := json.Unmarshal(initResp.Result, &initResult); err != nil {
		t.Fatalf("decode init result: %v", err)
	}
	if initResult.CodexHome != filepath.Join(workDir, ".gollem") || initResult.UserAgent == "" ||
		initResult.PlatformFamily == "" || initResult.PlatformOS == "" {
		t.Fatalf("init compatibility fields = %+v", initResult)
	}

	writeCLIInputLine(t, inW, `{"id":"write","method":"fs/writeFile","params":{"path":"ok.txt","content":"ok"}}`)
	seenWrite := false
	seenChanged := false
	for !seenWrite || !seenChanged {
		line := readCLIOutputLine(t, scanner)
		var envelope struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Error  *protocol.Error `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			t.Fatalf("decode output envelope %q: %v", line, err)
		}
		switch {
		case envelope.Method != "":
			if envelope.Method != "fs/changed" {
				t.Fatalf("notification method = %q, want fs/changed", envelope.Method)
			}
			var changed protocol.Notification
			if err := json.Unmarshal([]byte(line), &changed); err != nil {
				t.Fatalf("decode fs changed notification: %v", err)
			}
			var changedParams struct {
				Path      string `json:"path"`
				Operation string `json:"operation"`
			}
			if err := json.Unmarshal(changed.Params, &changedParams); err != nil {
				t.Fatalf("decode fs changed params: %v", err)
			}
			if changedParams.Path != "ok.txt" || changedParams.Operation != "writeFile" {
				t.Fatalf("fs changed params = %#v", changedParams)
			}
			seenChanged = true
		case envelope.ID == "write":
			if envelope.Error != nil {
				t.Fatalf("write error: %v", envelope.Error)
			}
			seenWrite = true
		default:
			t.Fatalf("unexpected output line: %q", line)
		}
	}

	writeCLIInputLine(t, inW, `{"id":"read","method":"fs/readFile","params":{"path":"ok.txt"}}`)
	if err := inW.Close(); err != nil {
		t.Fatalf("close input: %v", err)
	}
	var read protocol.Response
	if err := json.Unmarshal([]byte(readCLIOutputLine(t, scanner)), &read); err != nil {
		t.Fatalf("decode read response: %v", err)
	}
	if read.Error != nil {
		t.Fatalf("read error: %v", read.Error)
	}
	var result struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(read.Result, &result); err != nil {
		t.Fatalf("decode read result: %v", err)
	}
	if result.Content != "ok" {
		t.Fatalf("content = %q, want ok", result.Content)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("ServeJSONLines: %v", err)
	}
}

func TestCLIAppServerServesUnixSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sockets are not supported on windows")
	}
	workDir := t.TempDir()
	socketDir, err := os.MkdirTemp("/tmp", "gollem-sock-*")
	if err != nil {
		t.Fatalf("create socket temp dir: %v", err)
	}
	defer os.RemoveAll(socketDir)
	socketPath := filepath.Join(socketDir, "gollem.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- serveAppServerUnixSocket(ctx, appServerFlags{
			workDir:        workDir,
			storePath:      ":memory:",
			socketPath:     socketPath,
			allowMutations: true,
		})
	}()

	var conn net.Conn
	err = nil
	dialer := net.Dialer{}
	for range 100 {
		conn, err = dialer.DialContext(ctx, "unix", socketPath)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial app-server socket: %v", err)
	}
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	writeCLIInputLine(t, conn, `{"id":"init","method":"initialize","params":{"clientInfo":{"name":"socket-test","version":"1.0.0"}}}`)
	writeCLIInputLine(t, conn, `{"method":"initialized"}`)
	writeCLIInputLine(t, conn, `{"id":"status","method":"daemon/status","params":{}}`)

	var initResp protocol.Response
	if err := json.Unmarshal([]byte(readCLIOutputLine(t, scanner)), &initResp); err != nil {
		t.Fatalf("decode init response: %v", err)
	}
	if initResp.Error != nil {
		t.Fatalf("initialize error: %v", initResp.Error)
	}
	var statusResp protocol.Response
	if err := json.Unmarshal([]byte(readCLIOutputLine(t, scanner)), &statusResp); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if statusResp.Error != nil {
		t.Fatalf("daemon/status error: %v", statusResp.Error)
	}
	var status appserver.DaemonStatus
	if err := json.Unmarshal(statusResp.Result, &status); err != nil {
		t.Fatalf("decode daemon/status: %v", err)
	}
	if status.Transport != "socket" {
		t.Fatalf("daemon/status transport = %q, want socket", status.Transport)
	}

	writeCLIInputLine(t, conn, `{"id":"stop","method":"daemon/stop","params":{"reason":"socket test"}}`)
	var stopResp protocol.Response
	if err := json.Unmarshal([]byte(readCLIOutputLine(t, scanner)), &stopResp); err != nil {
		t.Fatalf("decode stop response: %v", err)
	}
	if stopResp.Error != nil {
		t.Fatalf("daemon/stop error: %v", stopResp.Error)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close socket: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("serveAppServerUnixSocket: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("serveAppServerUnixSocket did not exit after daemon/stop")
	}
}

func TestCLIAppServerServesCatalogMethods(t *testing.T) {
	workDir := t.TempDir()
	server, cleanup, err := newCLIAppServer(appServerFlags{
		workDir:   workDir,
		storePath: ":memory:",
		stdio:     true,
	})
	if err != nil {
		t.Fatalf("newCLIAppServer: %v", err)
	}
	defer cleanup()

	input := strings.Join([]string{
		`{"id":"init","method":"initialize","params":{"clientInfo":{"name":"test-client","version":"1.0.0"}}}`,
		`{"method":"initialized"}`,
		`{"id":"providers","method":"provider/list","params":{}}`,
		`{"id":"models","method":"model/list","params":{"limit":2}}`,
		`{"id":"tools","method":"tool/list","params":{"includeUnavailable":true}}`,
		`{"id":"config","method":"config/read","params":{"keys":["workspace.root"]}}`,
		"",
	}, "\n")
	var output bytes.Buffer
	if err := appserver.ServeJSONLines(context.Background(), server, strings.NewReader(input), &output); err != nil {
		t.Fatalf("ServeJSONLines: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("output lines = %d, want 5\n%s", len(lines), output.String())
	}

	responses := map[string]protocol.Response{}
	for _, line := range lines[1:] {
		var resp protocol.Response
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("decode response line %q: %v", line, err)
		}
		id, _ := resp.ID.Value().(string)
		if id == "" {
			t.Fatalf("response missing string id: %#v", resp)
		}
		responses[id] = resp
	}
	providersResp := responses["providers"]
	var providers catalog.ProviderListResponse
	if err := json.Unmarshal(providersResp.Result, &providers); err != nil {
		t.Fatalf("decode provider/list result: %v", err)
	}
	if len(providers.Data) == 0 {
		t.Fatal("provider/list returned no providers")
	}

	modelsResp := responses["models"]
	var models catalog.ModelListResponse
	if err := json.Unmarshal(modelsResp.Result, &models); err != nil {
		t.Fatalf("decode model/list result: %v", err)
	}
	if len(models.Data) != 2 {
		t.Fatalf("model/list returned %d models, want 2", len(models.Data))
	}

	toolsResp := responses["tools"]
	var tools catalog.ToolListResponse
	if err := json.Unmarshal(toolsResp.Result, &tools); err != nil {
		t.Fatalf("decode tool/list result: %v", err)
	}
	if !containsTool(tools.Data, "provider-catalog") {
		t.Fatalf("tool/list missing provider catalog tool: %#v", tools.Data)
	}
	if !containsTool(tools.Data, "cache") {
		t.Fatalf("tool/list missing cache tool: %#v", tools.Data)
	}
	if !containsTool(tools.Data, "turn-runtime") {
		t.Fatalf("tool/list missing turn runtime tool: %#v", tools.Data)
	}
	if !containsTool(tools.Data, "config") {
		t.Fatalf("tool/list missing config tool: %#v", tools.Data)
	}
	if !containsTool(tools.Data, "skills") {
		t.Fatalf("tool/list missing skills tool: %#v", tools.Data)
	}

	configResp := responses["config"]
	var configRead struct {
		Values map[string]json.RawMessage `json:"values"`
	}
	if err := json.Unmarshal(configResp.Result, &configRead); err != nil {
		t.Fatalf("decode config/read result: %v", err)
	}
	if string(configRead.Values["workspace.root"]) == "" {
		t.Fatalf("config/read missing workspace root: %#v", configRead.Values)
	}
}

func TestCLIAppServerDiscoversWorkspaceSkills(t *testing.T) {
	workDir := t.TempDir()
	writeCLITestFile(t, workDir, ".gollem/plugins/example/.codex-plugin/plugin.json", `{"id":"example","name":"Example Plugin","version":"1.2.3"}`)
	writeCLITestFile(t, workDir, ".gollem/plugins/example/skills/review/SKILL.md", "# Review Skill\n\nReview code carefully.\n")
	server, cleanup, err := newCLIAppServer(appServerFlags{
		workDir:   workDir,
		storePath: ":memory:",
		stdio:     true,
	})
	if err != nil {
		t.Fatalf("newCLIAppServer: %v", err)
	}
	defer cleanup()
	readyCLIAppServer(t, server)

	skillsResp := server.HandleRequest(context.Background(), protocol.Request{
		ID:     protocol.NewStringID("skills"),
		Method: "skills/list",
	})
	if skillsResp.Error != nil {
		t.Fatalf("skills/list error: %v", skillsResp.Error)
	}
	var skills struct {
		Skills []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			PluginID string `json:"pluginId"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(skillsResp.Result, &skills); err != nil {
		t.Fatalf("decode skills/list: %v", err)
	}
	if len(skills.Skills) != 1 || skills.Skills[0].Name != "Review Skill" || skills.Skills[0].PluginID != "example" {
		t.Fatalf("skills/list = %#v", skills)
	}

	pluginsResp := server.HandleRequest(context.Background(), protocol.Request{
		ID:     protocol.NewStringID("plugins"),
		Method: "plugin/list",
		Params: json.RawMessage(`{"includeSkills":true}`),
	})
	if pluginsResp.Error != nil {
		t.Fatalf("plugin/list error: %v", pluginsResp.Error)
	}
	var plugins struct {
		Plugins []struct {
			ID         string `json:"id"`
			Version    string `json:"version"`
			SkillCount int    `json:"skillCount"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(pluginsResp.Result, &plugins); err != nil {
		t.Fatalf("decode plugin/list: %v", err)
	}
	if len(plugins.Plugins) != 1 || plugins.Plugins[0].ID != "example" || plugins.Plugins[0].SkillCount != 1 {
		t.Fatalf("plugin/list = %#v", plugins)
	}
}

func TestCLIAppServerThreadStartUsesRuntimeProviderFlag(t *testing.T) {
	server, cleanup, err := newCLIAppServer(appServerFlags{
		workDir:   t.TempDir(),
		storePath: ":memory:",
		provider:  "test",
		stdio:     true,
	})
	if err != nil {
		t.Fatalf("newCLIAppServer: %v", err)
	}
	defer cleanup()
	readyCLIAppServer(t, server)

	resp := server.HandleRequest(context.Background(), protocol.Request{
		ID:     protocol.NewStringID("start"),
		Method: "thread/start",
		Params: json.RawMessage(`{"prompt":"hello from cli runtime"}`),
	})
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	waitCLINotification(t, server, "turn/completed")
}

func TestCLIAppServerCapabilityGateRejectsUnconfiguredProvider(t *testing.T) {
	server, cleanup, err := newCLIAppServer(appServerFlags{
		workDir:   t.TempDir(),
		storePath: ":memory:",
		provider:  catalog.ProviderAnthropic,
		stdio:     true,
	})
	if err != nil {
		t.Fatalf("newCLIAppServer: %v", err)
	}
	defer cleanup()
	readyCLIAppServer(t, server)

	response := server.HandleRequest(context.Background(), protocol.Request{
		ID:     protocol.NewStringID("rejected-capability"),
		Method: "thread/start",
		Params: json.RawMessage(`{"prompt":"do not create a thread"}`),
	})
	if response.Error == nil || response.Error.Code != protocol.CodeInvalidParams ||
		!strings.Contains(response.Error.Message, "not configured") {
		t.Fatalf("thread/start error = %#v", response.Error)
	}
}

func TestCLIAppServerRuntimeFactoryUsesLocalOpenAICompatibleProfile(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer local" {
			t.Fatalf("Authorization = %q, want local credential", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-local","object":"chat.completion","model":"gpt-5.2-codex","choices":[{"message":{"role":"assistant","content":"runtime reply"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	t.Setenv(openaiprovider.LocalEndpointBaseURLEnv, server.URL)
	t.Setenv("OPENAI_API_KEY", "must-not-be-used")

	factory := appServerRuntimeModelFactory(appServerFlags{})
	model, info, err := factory(context.Background(), appserver.RuntimeModelSelection{
		ProviderID: catalog.ProviderOpenAICompatibleLocal,
		Model:      "gpt-5.2-codex",
	})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if info.ProviderID != catalog.ProviderOpenAICompatibleLocal || info.Model != "gpt-5.2-codex" {
		t.Fatalf("runtime info = %#v", info)
	}
	response, err := model.Request(context.Background(), []core.ModelMessage{
		core.ModelRequest{Parts: []core.ModelRequestPart{core.UserPromptPart{Content: "hello"}}},
	}, nil, nil)
	if err != nil {
		t.Fatalf("runtime request: %v", err)
	}
	if response.TextContent() != "runtime reply" || requests != 1 {
		t.Fatalf("runtime response = %#v, requests = %d", response, requests)
	}
}

func TestCLIAppServerThreadStartUsesLocalOpenAICompatibleProfile(t *testing.T) {
	serverFixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-local","object":"chat.completion","model":"local-tool-model","choices":[{"message":{"role":"assistant","content":"local thread reply"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`))
	}))
	defer serverFixture.Close()
	t.Setenv(openaiprovider.LocalEndpointBaseURLEnv, serverFixture.URL)
	t.Setenv(openaiprovider.LocalEndpointModelEnv, "local-tool-model")

	server, cleanup, err := newCLIAppServer(appServerFlags{
		workDir:   t.TempDir(),
		storePath: ":memory:",
		provider:  catalog.ProviderOpenAICompatibleLocal,
		stdio:     true,
	})
	if err != nil {
		t.Fatalf("newCLIAppServer: %v", err)
	}
	defer cleanup()
	readyCLIAppServer(t, server)

	response := server.HandleRequest(context.Background(), protocol.Request{
		ID:     protocol.NewStringID("start-local"),
		Method: "thread/start",
		Params: json.RawMessage(`{"prompt":"hello from local provider"}`),
	})
	if response.Error != nil {
		t.Fatalf("thread/start error: %v", response.Error)
	}
	waitCLINotification(t, server, "turn/completed")
}

func TestCLIAppServerRuntimeUsesApprovedScopedFilesystemTool(t *testing.T) {
	workDir := t.TempDir()
	model := core.NewTestModel(
		core.ToolCallResponseWithID(
			"workspace_write_file",
			`{"path":"from-runtime.txt","content":"cli runtime write\n"}`,
			"call-cli-write",
		),
		core.TextResponse("write complete"),
	)
	server, cleanup, err := newCLIAppServerWithRuntimeFactory(
		appServerFlags{workDir: workDir, storePath: ":memory:", stdio: true},
		"stdio",
		func(context.Context, appserver.RuntimeModelSelection) (core.Model, appserver.RuntimeModelInfo, error) {
			return model, appserver.RuntimeModelInfo{ProviderID: "test", Model: "test-model"}, nil
		},
	)
	if err != nil {
		t.Fatalf("newCLIAppServerWithRuntimeFactory: %v", err)
	}
	defer cleanup()
	readyCLIAppServer(t, server)

	resp := server.HandleRequest(context.Background(), protocol.Request{
		ID:     protocol.NewStringID("start-runtime-write"),
		Method: "thread/start",
		Params: json.RawMessage(`{"prompt":"write the file"}`),
	})
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	select {
	case <-server.RequestSignal():
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for runtime filesystem approval")
	}
	requests := server.DrainRequests()
	if len(requests) != 1 || requests[0].Method != "item/fileChange/requestApproval" {
		t.Fatalf("runtime approval requests = %#v", requests)
	}
	requestID, _ := requests[0].ID.Value().(string)
	approval := server.HandleRequest(context.Background(), protocol.Request{
		ID:     protocol.NewStringID("approve-runtime-write"),
		Method: "approval/respond",
		Params: json.RawMessage(`{"requestId":"` + requestID + `","approved":true}`),
	})
	if approval.Error != nil {
		t.Fatalf("approval/respond error: %v", approval.Error)
	}
	waitCLINotification(t, server, "turn/completed")

	data, err := os.ReadFile(filepath.Join(workDir, "from-runtime.txt"))
	if err != nil {
		t.Fatalf("read runtime-written file: %v", err)
	}
	if string(data) != "cli runtime write\n" {
		t.Fatalf("runtime-written content = %q", data)
	}
}

func TestCLIAppServerRuntimeUsesScopedProcessAndGitTools(t *testing.T) {
	workDir := t.TempDir()
	runCLIAppServerGit(t, workDir, "init")
	runCLIAppServerGit(t, workDir, "config", "user.name", "Test User")
	runCLIAppServerGit(t, workDir, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("initial\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runCLIAppServerGit(t, workDir, "add", "README.md")
	runCLIAppServerGit(t, workDir, "commit", "-m", "initial")

	model := core.NewTestModel(
		core.ToolCallResponseWithID("git_status", `{}`, "call-cli-git-before"),
		core.ToolCallResponseWithID(
			"workspace_run_command",
			`{"command":"sh","args":["-c","printf cli-output; printf cli-process > from-process.txt"]}`,
			"call-cli-process",
		),
		core.ToolCallResponseWithID("git_status", `{}`, "call-cli-git-after"),
		core.TextResponse("process and git complete"),
	)
	server, cleanup, err := newCLIAppServerWithRuntimeFactory(
		appServerFlags{workDir: workDir, storePath: ":memory:", stdio: true},
		"stdio",
		func(context.Context, appserver.RuntimeModelSelection) (core.Model, appserver.RuntimeModelInfo, error) {
			return model, appserver.RuntimeModelInfo{ProviderID: "test", Model: "test-model"}, nil
		},
	)
	if err != nil {
		t.Fatalf("newCLIAppServerWithRuntimeFactory: %v", err)
	}
	defer cleanup()
	readyCLIAppServer(t, server)

	resp := server.HandleRequest(context.Background(), protocol.Request{
		ID:     protocol.NewStringID("start-process-git"),
		Method: "thread/start",
		Params: json.RawMessage(`{"prompt":"inspect, run, and inspect again"}`),
	})
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
		Turn   *store.Turn   `json:"turn"`
	}
	if err := json.Unmarshal(resp.Result, &started); err != nil {
		t.Fatalf("decode thread/start: %v", err)
	}
	select {
	case <-server.RequestSignal():
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for runtime process approval")
	}
	requests := server.DrainRequests()
	if len(requests) != 1 || requests[0].Method != "item/commandExecution/requestApproval" {
		t.Fatalf("runtime process approval requests = %#v", requests)
	}
	requestID, _ := requests[0].ID.Value().(string)
	approval := server.HandleRequest(context.Background(), protocol.Request{
		ID:     protocol.NewStringID("approve-runtime-process"),
		Method: "approval/respond",
		Params: json.RawMessage(`{"requestId":"` + requestID + `","approved":true}`),
	})
	if approval.Error != nil {
		t.Fatalf("approval/respond error: %v", approval.Error)
	}
	waitCLINotification(t, server, "turn/completed")

	data, err := os.ReadFile(filepath.Join(workDir, "from-process.txt"))
	if err != nil {
		t.Fatalf("read process-written file: %v", err)
	}
	if string(data) != "cli-process" {
		t.Fatalf("process-written content = %q", data)
	}
	itemsResp := server.HandleRequest(context.Background(), protocol.Request{
		ID:     protocol.NewStringID("list-runtime-items"),
		Method: "thread/items/list",
		Params: json.RawMessage(`{"threadId":"` + started.Thread.ID + `"}`),
	})
	if itemsResp.Error != nil {
		t.Fatalf("thread/items/list error: %v", itemsResp.Error)
	}
	var listed struct {
		Items []*store.Item `json:"items"`
	}
	if err := json.Unmarshal(itemsResp.Result, &listed); err != nil {
		t.Fatalf("decode thread/items/list: %v", err)
	}
	toolCounts := map[string]int{}
	commandItems := 0
	for _, item := range listed.Items {
		if item == nil {
			continue
		}
		switch item.Kind {
		case "dynamicToolCall":
			var payload struct {
				Tool    string `json:"tool"`
				Status  string `json:"status"`
				Success *bool  `json:"success"`
			}
			if err := json.Unmarshal(item.Payload, &payload); err != nil {
				t.Fatalf("decode runtime tool item: %v", err)
			}
			if payload.Status != "completed" || payload.Success == nil || !*payload.Success {
				t.Fatalf("runtime tool payload = %#v", payload)
			}
			toolCounts[payload.Tool]++
		case "commandExecution":
			commandItems++
			if item.Status != "completed" || !strings.Contains(string(item.Payload), "cli-output") || !strings.Contains(string(item.Payload), `"source":"agent"`) {
				t.Fatalf("runtime command item = %#v", item)
			}
		}
	}
	if toolCounts["git_status"] != 2 || toolCounts["workspace_run_command"] != 1 || commandItems != 1 {
		t.Fatalf("runtime item counts: tools=%v commands=%d", toolCounts, commandItems)
	}
}

func TestCLIAppServerRuntimeUsesMCPAndSharedInteractionTools(t *testing.T) {
	model := core.NewTestModel(
		core.ToolCallResponseWithID("mcp_list_servers", `{}`, "call-cli-mcp-list"),
		core.ToolCallResponseWithID("curr_time", `{}`, "call-cli-current-time"),
		core.ToolCallResponseWithID("request_user_input", `{"prompt":"Choose","options":["one","two"]}`, "call-cli-input"),
		core.TextResponse("interaction complete"),
	)
	server, cleanup, err := newCLIAppServerWithRuntimeFactory(
		appServerFlags{workDir: t.TempDir(), storePath: ":memory:", stdio: true},
		"stdio",
		func(context.Context, appserver.RuntimeModelSelection) (core.Model, appserver.RuntimeModelInfo, error) {
			return model, appserver.RuntimeModelInfo{ProviderID: "test", Model: "test-model"}, nil
		},
	)
	if err != nil {
		t.Fatalf("newCLIAppServerWithRuntimeFactory: %v", err)
	}
	defer cleanup()
	readyCLIAppServer(t, server)

	resp := server.HandleRequest(context.Background(), protocol.Request{
		ID:     protocol.NewStringID("start-mcp-interaction"),
		Method: "thread/start",
		Params: json.RawMessage(`{"prompt":"inspect MCP and ask"}`),
	})
	if resp.Error != nil {
		t.Fatalf("thread/start error: %v", resp.Error)
	}
	var started struct {
		Thread *store.Thread `json:"thread"`
	}
	if err := json.Unmarshal(resp.Result, &started); err != nil {
		t.Fatalf("decode thread/start: %v", err)
	}
	select {
	case <-server.RequestSignal():
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for runtime interaction")
	}
	requests := server.DrainRequests()
	if len(requests) != 1 || requests[0].Method != appserver.InteractionCurrentTimeRead {
		t.Fatalf("runtime interaction requests = %#v", requests)
	}
	var currentTimeParams protocol.CurrentTimeReadParams
	if err := json.Unmarshal(requests[0].Params, &currentTimeParams); err != nil {
		t.Fatalf("decode runtime current-time request: %v", err)
	}
	if currentTimeParams.ThreadID != started.Thread.ID || string(requests[0].Params) != `{"threadId":"`+started.Thread.ID+`"}` {
		t.Fatalf("runtime current-time request = %#v raw=%s", currentTimeParams, requests[0].Params)
	}
	if err := server.HandleResponse(context.Background(), protocol.Response{
		ID:     requests[0].ID,
		Result: json.RawMessage(`{"currentTimeAt":1781717655}`),
	}); err != nil {
		t.Fatalf("HandleResponse current time: %v", err)
	}
	select {
	case <-server.RequestSignal():
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for runtime user input")
	}
	requests = server.DrainRequests()
	if len(requests) != 1 || requests[0].Method != appserver.InteractionRequestUserInput {
		t.Fatalf("runtime user-input requests = %#v", requests)
	}
	var inputParams protocol.ToolRequestUserInputParams
	if err := json.Unmarshal(requests[0].Params, &inputParams); err != nil {
		t.Fatalf("decode runtime user-input request: %v", err)
	}
	if len(inputParams.Questions) != 1 || inputParams.Questions[0].ID != "call-cli-input" ||
		inputParams.Questions[0].Question != "Choose" || len(inputParams.Questions[0].Options) != 2 {
		t.Fatalf("runtime user-input request = %#v", inputParams)
	}
	if err := server.HandleResponse(context.Background(), protocol.Response{
		ID:     requests[0].ID,
		Result: json.RawMessage(`{"answers":{"call-cli-input":{"answers":["one"]}}}`),
	}); err != nil {
		t.Fatalf("HandleResponse: %v", err)
	}
	waitCLINotification(t, server, "turn/completed")

	statusResp := server.HandleRequest(context.Background(), protocol.Request{
		ID:     protocol.NewStringID("mcp-status"),
		Method: "mcpServerStatus/list",
		Params: json.RawMessage(`{}`),
	})
	if statusResp.Error != nil || !strings.Contains(string(statusResp.Result), `"servers":[]`) {
		t.Fatalf("mcpServerStatus/list response = %#v", statusResp)
	}
	itemsResp := server.HandleRequest(context.Background(), protocol.Request{
		ID:     protocol.NewStringID("list-mcp-interaction-items"),
		Method: "thread/items/list",
		Params: json.RawMessage(`{"threadId":"` + started.Thread.ID + `"}`),
	})
	if itemsResp.Error != nil {
		t.Fatalf("thread/items/list error: %v", itemsResp.Error)
	}
	if !strings.Contains(string(itemsResp.Result), `"tool":"mcp_list_servers"`) ||
		!strings.Contains(string(itemsResp.Result), `"tool":"curr_time"`) ||
		!strings.Contains(string(itemsResp.Result), `It is 2026-06-17 17:34:15 UTC.`) ||
		!strings.Contains(string(itemsResp.Result), `"tool":"request_user_input"`) {
		t.Fatalf("runtime MCP/interaction items = %s", itemsResp.Result)
	}
}

func TestCLIAppServerCleanupStopsActiveRuntimeBeforeStoreClose(t *testing.T) {
	t.Setenv("GOLLEM_TEST_MODEL_DELAY", "250ms")
	workDir := t.TempDir()
	server, cleanup, err := newCLIAppServer(appServerFlags{
		workDir:        workDir,
		storePath:      filepath.Join(workDir, "app.db"),
		provider:       "test",
		stdio:          true,
		allowMutations: true,
	})
	if err != nil {
		t.Fatalf("newCLIAppServer: %v", err)
	}
	defer cleanup()

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		err := appserver.ServeJSONLines(context.Background(), server, inR, outW)
		if err != nil {
			_ = outW.CloseWithError(err)
		} else {
			_ = outW.Close()
		}
		errCh <- err
	}()
	lineCh := make(chan string, 1024)
	scanErrCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(outR)
		for scanner.Scan() {
			select {
			case lineCh <- scanner.Text():
			default:
			}
		}
		scanErrCh <- scanner.Err()
		close(lineCh)
	}()
	readLine := func() string {
		t.Helper()
		select {
		case line, ok := <-lineCh:
			if !ok {
				t.Fatal("output stream closed before expected line")
			}
			return line
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for output line")
			return ""
		}
	}
	writeCLIInputLine(t, inW, `{"id":"init","method":"initialize","params":{"clientInfo":{"name":"test-client","version":"1.0.0"}}}`)
	writeCLIInputLine(t, inW, `{"method":"initialized"}`)
	if err := json.Unmarshal([]byte(readLine()), &protocol.Response{}); err != nil {
		t.Fatalf("decode init response: %v", err)
	}

	writeCLIInputLine(t, inW, `{"id":"start","method":"thread/start","params":{"prompt":"slow runtime"}}`)
	var start protocol.Response
	for {
		line := readLine()
		var envelope struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			t.Fatalf("decode output envelope %q: %v", line, err)
		}
		if envelope.ID != "start" {
			continue
		}
		if err := json.Unmarshal([]byte(line), &start); err != nil {
			t.Fatalf("decode start response: %v", err)
		}
		break
	}
	if start.Error != nil {
		t.Fatalf("thread/start error: %v", start.Error)
	}
	if err := inW.Close(); err != nil {
		t.Fatalf("close input: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("ServeJSONLines: %v", err)
	}
	if err := <-scanErrCh; err != nil {
		t.Fatalf("scan output: %v", err)
	}
}

func writeCLITestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func TestCLIAppServerServesDaemonLifecycleMethods(t *testing.T) {
	workDir := t.TempDir()
	server, cleanup, err := newCLIAppServer(appServerFlags{
		workDir:   workDir,
		storePath: ":memory:",
		stdio:     true,
	})
	if err != nil {
		t.Fatalf("newCLIAppServer: %v", err)
	}
	defer cleanup()
	readyCLIAppServer(t, server)

	statusResp := server.HandleRequest(context.Background(), protocol.Request{
		ID:     protocol.NewStringID("status"),
		Method: "daemon/status",
	})
	if statusResp.Error != nil {
		t.Fatalf("daemon/status error: %v", statusResp.Error)
	}
	var status appserver.DaemonStatus
	if err := json.Unmarshal(statusResp.Result, &status); err != nil {
		t.Fatalf("decode daemon/status result: %v", err)
	}
	if status.Status != "running" || status.Name != "gollem-appserver" || status.WorkDir != workDir || status.StorePath != ":memory:" || status.ProtocolVersion != protocol.ProtocolVersion {
		t.Fatalf("daemon/status = %#v", status)
	}

	versionResp := server.HandleRequest(context.Background(), protocol.Request{
		ID:     protocol.NewStringID("version"),
		Method: "daemon/version",
	})
	if versionResp.Error != nil {
		t.Fatalf("daemon/version error: %v", versionResp.Error)
	}
	var version appserver.DaemonVersion
	if err := json.Unmarshal(versionResp.Result, &version); err != nil {
		t.Fatalf("decode daemon/version result: %v", err)
	}
	if version.Name != "gollem-appserver" || version.ProtocolVersion != protocol.ProtocolVersion || version.GoVersion == "" {
		t.Fatalf("daemon/version = %#v", version)
	}
}

func TestCLIAppServerDaemonStatusReportsTransport(t *testing.T) {
	server, cleanup, err := newCLIAppServerWithTransport(appServerFlags{
		workDir:   t.TempDir(),
		storePath: ":memory:",
	}, "websocket")
	if err != nil {
		t.Fatalf("newCLIAppServerWithTransport: %v", err)
	}
	defer cleanup()
	readyCLIAppServer(t, server)

	statusResp := server.HandleRequest(context.Background(), protocol.Request{
		ID:     protocol.NewStringID("status"),
		Method: "daemon/status",
	})
	if statusResp.Error != nil {
		t.Fatalf("daemon/status error: %v", statusResp.Error)
	}
	var status appserver.DaemonStatus
	if err := json.Unmarshal(statusResp.Result, &status); err != nil {
		t.Fatalf("decode daemon/status result: %v", err)
	}
	if status.Transport != "websocket" {
		t.Fatalf("daemon/status transport = %q, want websocket", status.Transport)
	}
}

func readyCLIAppServer(t *testing.T, server *appserver.Server) {
	t.Helper()
	initResp := server.HandleRequest(context.Background(), protocol.Request{
		ID:     protocol.NewStringID("init"),
		Method: "initialize",
		Params: json.RawMessage(`{"clientInfo":{"name":"test-client","version":"1.0.0"}}`),
	})
	if initResp.Error != nil {
		t.Fatalf("initialize: %v", initResp.Error)
	}
	if err := server.HandleNotification(context.Background(), protocol.Notification{Method: "initialized"}); err != nil {
		t.Fatalf("initialized: %v", err)
	}
}

func waitCLINotification(t *testing.T, server *appserver.Server, method string) {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case <-server.NotificationSignal():
			for _, notification := range server.DrainNotifications() {
				if notification.Method == method {
					return
				}
			}
		case <-timeout:
			t.Fatalf("timed out waiting for notification %q", method)
		}
	}
}

func containsTool(tools []catalog.Tool, id string) bool {
	for _, tool := range tools {
		if tool.ID == id {
			return true
		}
	}
	return false
}

func runCLIAppServerGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	blocked := map[string]struct{}{
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": {},
		"GIT_COMMON_DIR":                   {},
		"GIT_DIR":                          {},
		"GIT_INDEX_FILE":                   {},
		"GIT_NAMESPACE":                    {},
		"GIT_OBJECT_DIRECTORY":             {},
		"GIT_PREFIX":                       {},
		"GIT_QUARANTINE_PATH":              {},
		"GIT_WORK_TREE":                    {},
	}
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if _, skip := blocked[key]; !skip {
			cmd.Env = append(cmd.Env, value)
		}
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func writeCLIInputLine(t *testing.T, writer io.Writer, line string) {
	t.Helper()
	if _, err := io.WriteString(writer, line+"\n"); err != nil {
		t.Fatalf("write input line: %v", err)
	}
}

func readCLIOutputLine(t *testing.T, scanner *bufio.Scanner) string {
	t.Helper()
	type result struct {
		line string
		ok   bool
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		ok := scanner.Scan()
		ch <- result{line: scanner.Text(), ok: ok, err: scanner.Err()}
	}()
	select {
	case got := <-ch:
		if got.err != nil {
			t.Fatalf("scan output: %v", got.err)
		}
		if !got.ok {
			t.Fatal("output stream closed before expected line")
		}
		return got.line
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for output line")
		return ""
	}
}
