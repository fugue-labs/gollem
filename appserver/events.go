package appserver

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
	toolprocess "github.com/fugue-labs/gollem/appserver/tools/process"
)

const defaultEventLimit = 1024

// EventQueue buffers server-to-client app-server notifications for transports
// that need ordered delivery alongside request responses.
type EventQueue struct {
	mu     sync.Mutex
	limit  int
	events []protocol.Notification
	signal chan struct{}
}

func NewEventQueue() *EventQueue {
	return &EventQueue{
		limit:  defaultEventLimit,
		signal: make(chan struct{}, 1),
	}
}

func (q *EventQueue) Publish(method string, params any) {
	if q == nil || method == "" {
		return
	}
	var raw json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return
		}
		raw = data
	}
	q.PublishRaw(method, raw)
}

func (q *EventQueue) PublishRaw(method string, params json.RawMessage) {
	if q == nil || method == "" {
		return
	}
	q.mu.Lock()
	q.events = append(q.events, protocol.Notification{Method: method, Params: params})
	if q.limit > 0 && len(q.events) > q.limit {
		copy(q.events, q.events[len(q.events)-q.limit:])
		q.events = q.events[:q.limit]
	}
	q.mu.Unlock()
	q.signalReady()
}

func (q *EventQueue) Drain(filter func(string) bool) []protocol.Notification {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	events := append([]protocol.Notification(nil), q.events...)
	q.events = nil
	q.mu.Unlock()
	if filter == nil {
		return events
	}
	filtered := events[:0]
	for _, event := range events {
		if filter(event.Method) {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func (q *EventQueue) Signal() <-chan struct{} {
	if q == nil {
		return nil
	}
	return q.signal
}

func (q *EventQueue) signalReady() {
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

// RequestQueue buffers server-to-client app-server requests for transports
// that need ordered delivery alongside request responses.
type RequestQueue struct {
	mu       sync.Mutex
	limit    int
	requests []protocol.Request
	signal   chan struct{}
}

func NewRequestQueue() *RequestQueue {
	return &RequestQueue{
		limit:  defaultEventLimit,
		signal: make(chan struct{}, 1),
	}
}

func (q *RequestQueue) Publish(method string, id protocol.RequestID, params any) {
	if q == nil || method == "" || id.IsZero() {
		return
	}
	var raw json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return
		}
		raw = data
	}
	q.PublishRaw(protocol.Request{ID: id, Method: method, Params: raw})
}

func (q *RequestQueue) PublishRaw(req protocol.Request) {
	if q == nil || req.Method == "" || req.ID.IsZero() {
		return
	}
	q.mu.Lock()
	q.requests = append(q.requests, req)
	if q.limit > 0 && len(q.requests) > q.limit {
		copy(q.requests, q.requests[len(q.requests)-q.limit:])
		q.requests = q.requests[:q.limit]
	}
	q.mu.Unlock()
	q.signalReady()
}

func (q *RequestQueue) Drain() []protocol.Request {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	requests := append([]protocol.Request(nil), q.requests...)
	q.requests = nil
	q.mu.Unlock()
	return requests
}

func (q *RequestQueue) Signal() <-chan struct{} {
	if q == nil {
		return nil
	}
	return q.signal
}

func (q *RequestQueue) signalReady() {
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

type fileChangedParams struct {
	Path        string    `json:"path,omitempty"`
	Destination string    `json:"destination,omitempty"`
	Operation   string    `json:"operation"`
	At          time.Time `json:"at"`
}

type fsWatchChangedParams struct {
	WatchID      string   `json:"watchId"`
	ChangedPaths []string `json:"changedPaths"`
}

type processOutputDeltaParams struct {
	ID        string    `json:"id"`
	ProcessID string    `json:"processId"`
	PID       int       `json:"pid,omitempty"`
	Stream    string    `json:"stream"`
	Data      string    `json:"data"`
	Encoding  string    `json:"encoding"`
	At        time.Time `json:"at"`
}

type processExitedParams struct {
	ID        string             `json:"id"`
	ProcessID string             `json:"processId"`
	PID       int                `json:"pid,omitempty"`
	Status    toolprocess.Status `json:"status"`
	ExitCode  int                `json:"exitCode"`
	Error     string             `json:"error,omitempty"`
	At        time.Time          `json:"at"`
}

type threadNotificationParams struct {
	ThreadID string             `json:"threadId"`
	Status   store.ThreadStatus `json:"status,omitempty"`
	Thread   *store.Thread      `json:"thread,omitempty"`
	At       time.Time          `json:"at"`
}

type threadClosedNotificationParams struct {
	ThreadID string `json:"threadId"`
}

type threadNotLoadedStatusNotificationParams struct {
	ThreadID string            `json:"threadId"`
	Status   map[string]string `json:"status"`
	At       time.Time         `json:"at"`
}

type deprecationNoticeNotificationParams struct {
	Summary string  `json:"summary"`
	Details *string `json:"details"`
}

type contextCompactedNotificationParams = protocol.ThreadCompactedNotificationParams

type threadGoalNotificationParams struct {
	ThreadID string        `json:"threadId"`
	Goal     any           `json:"goal,omitempty"`
	Thread   *store.Thread `json:"thread,omitempty"`
	At       time.Time     `json:"at"`
}

type threadNameNotificationParams struct {
	ThreadID string        `json:"threadId"`
	Name     string        `json:"name"`
	Thread   *store.Thread `json:"thread,omitempty"`
	At       time.Time     `json:"at"`
}

func ProcessOutputNotification(event toolprocess.OutputEvent) (string, any) {
	data, encoding := encodeContent(event.Data)
	return "process/outputDelta", processOutputDeltaParams{
		ID:        event.ID,
		ProcessID: event.ID,
		PID:       event.PID,
		Stream:    string(event.Stream),
		Data:      data,
		Encoding:  encoding,
		At:        event.At,
	}
}

func ProcessExitedNotification(event toolprocess.ExitEvent) (string, any) {
	at := event.At
	if at.IsZero() {
		at = event.Snapshot.EndedAt
	}
	return "process/exited", processExitedParams{
		ID:        event.Snapshot.ID,
		ProcessID: event.Snapshot.ID,
		PID:       event.Snapshot.PID,
		Status:    event.Snapshot.Status,
		ExitCode:  event.Snapshot.ExitCode,
		Error:     event.Snapshot.Error,
		At:        at,
	}
}
