package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
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

func TestPublishRuntimeErrorRetainsLiveExtensionShape(t *testing.T) {
	for _, tc := range []struct {
		name     string
		turn     *store.Turn
		wantKeys []string
	}{
		{"thread turn", &store.Turn{ID: "turn", ThreadID: "thread"}, []string{"at", "error", "threadId", "turnId"}},
		{"global", nil, []string{"at", "error"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			notifier := &runtimeErrorCaptureNotifier{}
			publishRuntimeError(notifier, tc.turn, "boom")
			if notifier.method != "error" {
				t.Fatalf("method = %q", notifier.method)
			}
			params, ok := notifier.params.(runtimeErrorNotificationParams)
			if !ok || params.Error != runtimePublicErrorFailed || params.At.IsZero() {
				t.Fatalf("params = %#v (%T)", notifier.params, notifier.params)
			}
			if tc.turn != nil && (params.ThreadID != "thread" || params.TurnID != "turn") {
				t.Fatalf("correlated ids = %q/%q", params.ThreadID, params.TurnID)
			}
			if tc.turn == nil && (params.ThreadID != "" || params.TurnID != "") {
				t.Fatalf("global ids = %q/%q", params.ThreadID, params.TurnID)
			}
			encoded, err := json.Marshal(params)
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &payload); err != nil {
				t.Fatal(err)
			}
			keys := make([]string, 0, len(payload))
			for key := range payload {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			if !reflect.DeepEqual(keys, tc.wantKeys) {
				t.Fatalf("keys = %v, want %v", keys, tc.wantKeys)
			}
			var exact protocol.ErrorNotification
			if err := json.Unmarshal(encoded, &exact); err == nil {
				t.Fatal("incompatible live extension decoded as exact public error")
			}
		})
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
