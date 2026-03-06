package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/fugue-labs/gollem/core"
	"github.com/gorilla/websocket"
)

type responsesWebSocketConn struct {
	conn *websocket.Conn
}

type responsesWSCreateEvent struct {
	Type string `json:"type"`
	responsesRequest
}

type responsesWSEvent struct {
	Type     string                `json:"type"`
	Status   int                   `json:"status,omitempty"`
	Error    *responsesWSError     `json:"error,omitempty"`
	Response *responsesAPIResponse `json:"response,omitempty"`
	Code     string                `json:"code,omitempty"`
	Message  string                `json:"message,omitempty"`
}

type responsesWSError struct {
	Type    string `json:"type,omitempty"`
	Message string `json:"message,omitempty"`
	Code    string `json:"code,omitempty"`
	Param   string `json:"param,omitempty"`
}

var (
	// fallbackWebSocketReadTimeout bounds per-turn blocking reads when the
	// caller does not provide a context deadline, and caps per-read
	// deadlines when the context deadline is much further out. Empirically,
	// the longest successful model call observed is ~44s (p99 ~25s) across
	// 1100+ calls with xhigh reasoning effort. 3 minutes provides headroom
	// for API load variance and model changes while still detecting hung
	// connections quickly (vs the full 30-60min task budget).
	fallbackWebSocketReadTimeout = 3 * time.Minute
	// fallbackWebSocketWriteTimeout bounds writes when the caller does not
	// provide a context deadline.
	fallbackWebSocketWriteTimeout = 30 * time.Second
)

const (
	// minBudgetForWSRetry is the minimum remaining context budget required
	// before attempting the inner websocket reconnect-retry. The model must
	// re-reason from scratch on the retry, so there's no point reconnecting
	// if there isn't enough time left.
	minBudgetForWSRetry = 60 * time.Second
)

func (p *Provider) requestViaResponsesWebSocket(ctx context.Context, req *responsesRequest) (*core.ModelResponse, error) {
	p.wsMu.Lock()
	defer p.wsMu.Unlock()

	conn, err := p.ensureResponsesWebSocketLocked(ctx)
	if err != nil {
		return nil, err
	}

	currSigs, err := responsesInputSignatures(req.Input)
	if err != nil {
		return nil, fmt.Errorf("openai websocket: failed to hash request input: %w", err)
	}

	sendReq := *req
	if delta, ok := responsesIncrementalInput(p.wsLastInputSigs, currSigs, req.Input); ok && p.wsPrevResponseID != "" {
		delta = trimContinuationDelta(delta)
		if len(delta) > 0 {
			sendReq.PreviousResponseID = p.wsPrevResponseID
			sendReq.Input = delta
		}
	}

	apiResp, err := p.sendResponsesCreateLocked(ctx, conn, &sendReq)
	if err != nil {
		// If continuation/cache state is lost, or socket lifetime is reached, or
		// connection dropped, reconnect once and resend full context as a new chain.
		// Skip the inner retry when the context budget is nearly exhausted — the
		// model would need to re-reason from scratch and won't finish in time.
		if isPreviousResponseNotFound(err) || isWebSocketConnectionLimitReached(err) || isWebSocketConnectionError(err) {
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) >= minBudgetForWSRetry {
				p.resetResponsesWebSocketLocked()
				conn, connErr := p.ensureResponsesWebSocketLocked(ctx)
				if connErr != nil {
					return nil, connErr
				}
				fullReq := *req
				fullReq.PreviousResponseID = ""
				apiResp, err = p.sendResponsesCreateLocked(ctx, conn, &fullReq)
				if err == nil {
					// New chain started; local previous-response cache is reset.
					p.wsPrevResponseID = ""
					p.wsLastInputSigs = nil
				}
			}
		}
		if err != nil {
			p.resetResponsesWebSocketLocked()
			return nil, err
		}
	}

	p.wsPrevResponseID = apiResp.ID
	p.wsLastInputSigs = append([]string(nil), currSigs...)
	return parseResponsesResponse(apiResp, p.model), nil
}

func (p *Provider) ensureResponsesWebSocketLocked(ctx context.Context) (*responsesWebSocketConn, error) {
	if p.wsConn != nil {
		return p.wsConn, nil
	}

	wsURL, err := responsesWebSocketURL(p.baseURL)
	if err != nil {
		return nil, err
	}

	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+p.apiKey)

	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 20 * time.Second,
	}

	conn, resp, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		statusCode := 0
		body := ""
		if resp != nil {
			statusCode = resp.StatusCode
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			body = string(respBody)
		}
		if statusCode != 0 {
			return nil, &core.ModelHTTPError{
				Message:    "openai websocket connect error: " + body,
				StatusCode: statusCode,
				Body:       body,
				ModelName:  p.model,
			}
		}
		return nil, fmt.Errorf("openai websocket connect failed: %w", err)
	}

	p.wsConn = &responsesWebSocketConn{conn: conn}
	return p.wsConn, nil
}

func (p *Provider) resetResponsesWebSocketLocked() {
	if p.wsConn != nil && p.wsConn.conn != nil {
		_ = p.wsConn.conn.Close()
	}
	p.wsConn = nil
	p.wsPrevResponseID = ""
	p.wsLastInputSigs = nil
}

func (p *Provider) sendResponsesCreateLocked(ctx context.Context, conn *responsesWebSocketConn, req *responsesRequest) (*responsesAPIResponse, error) {
	event := responsesWSCreateEvent{
		Type:             "response.create",
		responsesRequest: *req,
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("openai websocket: marshal create event: %w", err)
	}

	reqDeadline, hasReqDeadline := p.requestIODeadline(ctx, time.Now())
	if hasReqDeadline {
		_ = conn.conn.SetWriteDeadline(reqDeadline)
	} else {
		_ = conn.conn.SetWriteDeadline(time.Now().Add(fallbackWebSocketWriteTimeout))
	}

	if err := conn.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		return nil, fmt.Errorf("openai websocket write failed: %w", err)
	}

	for {
		readDeadline, hasReadDeadline := p.requestIODeadline(ctx, time.Now())
		if hasReadDeadline {
			_ = conn.conn.SetReadDeadline(readDeadline)
		} else {
			_ = conn.conn.SetReadDeadline(time.Now().Add(fallbackWebSocketReadTimeout))
		}

		_, data, err := conn.conn.ReadMessage()
		if err != nil {
			return nil, fmt.Errorf("openai websocket read failed: %w", err)
		}
		var event responsesWSEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("openai websocket decode failed: %w", err)
		}
		switch event.Type {
		case "response.done", "response.completed":
			if event.Response == nil {
				return nil, errors.New("openai websocket: terminal response event missing response payload")
			}
			return event.Response, nil
		case "response.incomplete":
			return nil, responsesIncompleteError(event, p.model)
		case "error":
			return nil, responsesWebSocketError(event, p.model)
		case "response.failed":
			return nil, responsesFailedError(event, p.model)
		default:
			// Ignore progress/delta events and keep reading.
		}
	}
}

// requestIODeadline returns the applicable request deadline for websocket I/O.
//
// Unlike HTTP requests (where http.Client.Timeout bounds a single roundtrip),
// websocket reads may legitimately block for extended periods while the model
// reasons (especially with high/xhigh reasoning effort). However, we still
// cap per-read deadlines at fallbackWebSocketReadTimeout (3m) to prevent a
// hung API call from consuming the entire task budget. A single model turn
// (even with xhigh reasoning) should complete well within 3 minutes; if it
// doesn't, the connection is likely hung and the retry/fallback logic should
// kick in.
//
// When the context deadline is closer than the cap, we use the context
// deadline (existing behavior). When the context deadline is much further
// out (e.g. a 30-minute task budget), we cap at 3 minutes per read so
// hung connections fail fast and can be retried.
func (p *Provider) requestIODeadline(ctx context.Context, now time.Time) (time.Time, bool) {
	if d, ok := ctx.Deadline(); ok {
		if cap := now.Add(fallbackWebSocketReadTimeout); cap.Before(d) {
			return cap, true
		}
		return d, true
	}
	return time.Time{}, false
}

func responsesInputSignatures(input []map[string]any) ([]string, error) {
	sigs := make([]string, len(input))
	for i, item := range input {
		raw, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		sigs[i] = string(raw)
	}
	return sigs, nil
}

func responsesIncrementalInput(prevSigs, currSigs []string, currInput []map[string]any) ([]map[string]any, bool) {
	if len(prevSigs) == 0 || len(currSigs) <= len(prevSigs) {
		return nil, false
	}
	for i := range prevSigs {
		if prevSigs[i] != currSigs[i] {
			return nil, false
		}
	}
	delta := currInput[len(prevSigs):]
	if len(delta) == 0 {
		return nil, false
	}
	return delta, true
}

func trimContinuationDelta(delta []map[string]any) []map[string]any {
	start := 0
	for start < len(delta) && isAssistantGeneratedInputItem(delta[start]) {
		start++
	}
	return delta[start:]
}

func isAssistantGeneratedInputItem(item map[string]any) bool {
	typ, _ := item["type"].(string)
	switch typ {
	case "message":
		role, _ := item["role"].(string)
		return role == "assistant"
	case "function_call":
		// Tool calls are model-generated assistant output and are already part
		// of the previous response chain during continuation.
		return true
	default:
		return false
	}
}

func responsesWebSocketError(event responsesWSEvent, model string) error {
	msg := strings.TrimSpace(event.Message)
	code := strings.TrimSpace(event.Code)
	typ := ""
	if event.Error != nil {
		if m := strings.TrimSpace(event.Error.Message); m != "" {
			msg = m
		}
		if c := strings.TrimSpace(event.Error.Code); c != "" {
			code = c
		}
		typ = strings.TrimSpace(event.Error.Type)
	}
	if msg == "" {
		msg = "unknown websocket error"
	}
	status := event.Status
	if status == 0 {
		status = inferResponsesWSErrorStatus(code, typ, msg)
	}
	raw, _ := json.Marshal(event)
	return &core.ModelHTTPError{
		Message:    "openai websocket error: " + msg,
		StatusCode: status,
		Body:       string(raw),
		ModelName:  model,
	}
}

func responsesIncompleteError(event responsesWSEvent, model string) error {
	reason := ""
	if event.Response != nil && event.Response.IncompleteDetails != nil {
		reason = strings.TrimSpace(event.Response.IncompleteDetails.Reason)
	}
	if reason == "" {
		reason = "response.incomplete"
	}
	raw, _ := json.Marshal(event)
	return &core.ModelHTTPError{
		Message:    "openai websocket response incomplete: " + reason,
		StatusCode: http.StatusBadRequest,
		Body:       string(raw),
		ModelName:  model,
	}
}

func responsesFailedError(event responsesWSEvent, model string) error {
	reason := ""
	if event.Response != nil && event.Response.IncompleteDetails != nil {
		reason = strings.TrimSpace(event.Response.IncompleteDetails.Reason)
	}
	if reason == "" {
		reason = "response.failed"
	}
	raw, _ := json.Marshal(event)
	return &core.ModelHTTPError{
		Message:    "openai websocket response failed: " + reason,
		StatusCode: http.StatusBadRequest,
		Body:       string(raw),
		ModelName:  model,
	}
}

func inferResponsesWSErrorStatus(code, typ, message string) int {
	joined := strings.ToLower(code + " " + typ + " " + message)
	switch {
	case strings.Contains(joined, "resource_exhausted"),
		strings.Contains(joined, "rate_limit"),
		strings.Contains(joined, "too many requests"),
		strings.Contains(joined, " 429"):
		return http.StatusTooManyRequests
	case strings.Contains(joined, "invalid_request"),
		strings.Contains(joined, "previous_response_not_found"),
		strings.Contains(joined, "bad request"),
		strings.Contains(joined, " 400"):
		return http.StatusBadRequest
	case strings.Contains(joined, "unauthorized"), strings.Contains(joined, " 401"):
		return http.StatusUnauthorized
	case strings.Contains(joined, "forbidden"), strings.Contains(joined, " 403"):
		return http.StatusForbidden
	case strings.Contains(joined, "server_error"),
		strings.Contains(joined, "internal"),
		strings.Contains(joined, " 500"):
		return http.StatusInternalServerError
	case strings.Contains(joined, "timeout"), strings.Contains(joined, " 504"):
		return http.StatusGatewayTimeout
	default:
		return 0
	}
}

func isPreviousResponseNotFound(err error) bool {
	var httpErr *core.ModelHTTPError
	if errors.As(err, &httpErr) {
		lower := strings.ToLower(httpErr.Message + " " + httpErr.Body)
		return strings.Contains(lower, "previous_response_not_found")
	}
	return false
}

func isWebSocketConnectionLimitReached(err error) bool {
	var httpErr *core.ModelHTTPError
	if errors.As(err, &httpErr) {
		lower := strings.ToLower(httpErr.Message + " " + httpErr.Body)
		return strings.Contains(lower, "websocket_connection_limit_reached")
	}
	return false
}

func isWebSocketConnectionError(err error) bool {
	if err == nil {
		return false
	}
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	lower := strings.ToLower(err.Error())
	for _, s := range []string{
		"websocket: close",
		"broken pipe",
		"connection reset",
		"connection refused",
		"timeout",
		"i/o timeout",
		"eof",
	} {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}
