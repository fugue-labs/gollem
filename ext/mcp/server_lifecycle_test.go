package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestServerWaitIdleContextWaitsForRealHandlerLifetime(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := NewServer()
	server.attachWriter(func([]byte) error { return nil })
	server.AddTool(Tool{Name: "block", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, *RequestContext, map[string]any) (*ToolResult, error) {
		close(started)
		<-release
		return textToolResult("done"), nil
	})
	message := &jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      rawJSONID(1),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"block","arguments":{}}`),
	}
	server.HandleMessage(context.Background(), message)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := server.WaitIdleContext(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitIdleContext while busy = %v, want deadline", err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	close(release)
	doneCtx, doneCancel := context.WithTimeout(context.Background(), time.Second)
	defer doneCancel()
	if err := server.WaitIdleContext(doneCtx); err != nil {
		t.Fatalf("WaitIdleContext after release: %v", err)
	}
}

func TestServerHandlerAccountingIsSafeUnderConcurrentAdmissionWaitAndClose(t *testing.T) {
	server := NewServer()
	server.attachWriter(func([]byte) error { return nil })
	server.AddTool(Tool{Name: "fast", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, *RequestContext, map[string]any) (*ToolResult, error) {
		return textToolResult("ok"), nil
	})
	message := &jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      rawJSONID(1),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"fast","arguments":{}}`),
	}

	const submitters = 8
	const perSubmitter = 200
	var completed atomic.Int64
	var submitWG sync.WaitGroup
	for range submitters {
		submitWG.Add(1)
		go func() {
			defer submitWG.Done()
			for range perSubmitter {
				server.handleMessageWithCompletion(context.Background(), message, func() {
					completed.Add(1)
				})
			}
		}()
	}

	stopWaiters := make(chan struct{})
	var waiterWG sync.WaitGroup
	for range 4 {
		waiterWG.Add(1)
		go func() {
			defer waiterWG.Done()
			for {
				select {
				case <-stopWaiters:
					return
				default:
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
				_ = server.WaitIdleContext(ctx)
				cancel()
			}
		}()
	}

	submitWG.Wait()
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.WaitIdleContext(ctx); err != nil {
		t.Fatalf("final WaitIdleContext: %v", err)
	}
	close(stopWaiters)
	waiterWG.Wait()
	if got, want := completed.Load(), int64(submitters*perSubmitter); got != want {
		t.Fatalf("completion callbacks = %d, want %d", got, want)
	}

	// Closed admission still completes the transport callback exactly once.
	var closedCompletions atomic.Int64
	server.handleMessageWithCompletion(context.Background(), message, func() { closedCompletions.Add(1) })
	if closedCompletions.Load() != 1 {
		t.Fatalf("closed completion callback count = %d, want 1", closedCompletions.Load())
	}
}
