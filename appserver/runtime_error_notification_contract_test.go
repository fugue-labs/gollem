package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
	"github.com/fugue-labs/gollem/core"
)

type runtimeErrorCaptureNotifier struct {
	method string
	params any
}

func (n *runtimeErrorCaptureNotifier) PublishNotification(method string, params any) {
	n.method = method
	n.params = params
}

func TestPublishRuntimeErrorUsesExactPublicShape(t *testing.T) {
	notifier := &runtimeErrorCaptureNotifier{}
	publishRuntimeError(notifier, &store.Turn{ID: "turn", ThreadID: "thread"}, "boom")
	if notifier.method != "error" {
		t.Fatalf("method = %q", notifier.method)
	}
	params, ok := notifier.params.(protocol.ErrorNotification)
	if !ok {
		t.Fatalf("params = %#v (%T), want protocol.ErrorNotification", notifier.params, notifier.params)
	}
	if params.Error.Message != runtimePublicErrorFailed || params.Error.CodexErrorInfo != nil || params.Error.AdditionalDetails != nil {
		t.Fatalf("error = %#v", params.Error)
	}
	if params.WillRetry || params.ThreadID != "thread" || params.TurnID != "turn" {
		t.Fatalf("params = %#v", params)
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	var exact protocol.ErrorNotification
	if err := json.Unmarshal(encoded, &exact); err != nil {
		t.Fatalf("exact public error notification rejected %s: %v", encoded, err)
	}

	notifier = &runtimeErrorCaptureNotifier{}
	publishRuntimeError(notifier, nil, "boom")
	if notifier.method != "" || notifier.params != nil {
		t.Fatalf("uncorrelated error notification = %q %#v", notifier.method, notifier.params)
	}
}

func TestRuntimePublicErrorRedactsUntrustedDetails(t *testing.T) {
	const secret = "https://provider.invalid/v1?api_key=super-secret"
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"generic", fmt.Errorf("request %s failed", secret), runtimePublicErrorFailed},
		{"cancelled", fmt.Errorf("request %s: %w", secret, context.Canceled), runtimePublicErrorInterrupted},
		{"timed out", fmt.Errorf("request %s: %w", secret, context.DeadlineExceeded), runtimePublicErrorTimedOut},
		{
			"provider HTTP status",
			fmt.Errorf("request %s: %w", secret, &core.ModelHTTPError{
				Message:    "Authorization: Bearer super-secret",
				StatusCode: 429,
				Body:       "prompt=super-secret",
			}),
			"Provider request failed (HTTP 429).",
		},
		{
			"invalid provider status",
			&core.ModelHTTPError{Message: secret, StatusCode: 0, Body: secret},
			runtimePublicErrorProvider,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runtimePublicError(tc.err)
			if got != tc.want {
				t.Fatalf("runtimePublicError(%v) = %q, want %q", tc.err, got, tc.want)
			}
			if got == "" || strings.Contains(got, "super-secret") || strings.Contains(got, "provider.invalid") {
				t.Fatalf("runtimePublicError(%v) exposed untrusted detail: %q", tc.err, got)
			}
		})
	}
}

func TestPublishRuntimeErrorNoopGuards(t *testing.T) {
	publishRuntimeError(nil, &store.Turn{ID: "turn", ThreadID: "thread"}, "boom")
	notifier := &runtimeErrorCaptureNotifier{}
	publishRuntimeError(notifier, &store.Turn{ID: "turn", ThreadID: "thread"}, "")
	if notifier.method != "" || notifier.params != nil {
		t.Fatalf("unexpected notification %q %#v", notifier.method, notifier.params)
	}
}

func TestPublishPublicRuntimeErrorUsesProjectedMessage(t *testing.T) {
	notifier := &runtimeErrorCaptureNotifier{}
	publishPublicRuntimeError(notifier, &store.Turn{ID: "turn", ThreadID: "thread"}, runtimePublicErrorInterrupted)
	params, ok := notifier.params.(runtimeErrorNotificationParams)
	if notifier.method != "error" || !ok || params.Error.Message != runtimePublicErrorInterrupted {
		t.Fatalf("notification = %q %#v", notifier.method, notifier.params)
	}
}
