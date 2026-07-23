package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
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
	Item     *responsesOutputItem  `json:"item,omitempty"`
	Delta    string                `json:"delta,omitempty"`
	Text     string                `json:"text,omitempty"`
}

type responsesWSError struct {
	Type    string `json:"type,omitempty"`
	Message string `json:"message,omitempty"`
	Code    string `json:"code,omitempty"`
	Param   string `json:"param,omitempty"`
}

// wsDebugEnabled is evaluated once at package init; flipping
// GOLLEM_DEBUG_WS at runtime has no effect. Keeps hot-path reads cheap.
var wsDebugEnabled = os.Getenv("GOLLEM_DEBUG_WS") != ""

func wsDebugf(format string, args ...any) {
	if !wsDebugEnabled {
		return
	}
	fmt.Fprintf(os.Stderr, format, args...)
}

var (
	// fallbackWebSocketReadTimeout bounds per-event blocking reads when
	// the caller does not provide a context deadline, and caps per-read
	// deadlines when the context deadline is much further out. It
	// measures the silence tolerance between any two websocket events
	// (progress deltas, reasoning chunks, tool deltas, terminal
	// response.completed, etc.) — not the full turn wall-clock.
	//
	// For text-only chat, xhigh reasoning p99 is ~25s and max observed
	// ~44s (1100+ calls). For vision workloads with accumulated image
	// context (10+ high-detail images per turn plus long history) the
	// model can legitimately reason in silence for 5-8 minutes before
	// emitting the first event. 10 minutes captures that tail without
	// letting truly hung connections hold the slot forever.
	fallbackWebSocketReadTimeout = 10 * time.Minute
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

	if wsDebugEnabled {
		wsDebugf("[gollem-ws-full] %s\n", summarizeInputForDebug(req.Input, "(full)"))
	}

	sendReq := *req
	if delta, ok := responsesIncrementalInput(p.wsLastInputSigs, currSigs, req.Input); ok && p.wsPrevResponseID != "" {
		preTrim := delta
		delta = trimContinuationDelta(delta)
		wsDebugf("[gollem-ws-delta] pre_trim=%d post_trim=%d\n", len(preTrim), len(delta))
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

	if wsDebugEnabled {
		wsDebugf("[gollem-ws-recv] id=%s output_items=%d\n", apiResp.ID, len(apiResp.Output))
		for i, item := range apiResp.Output {
			wsDebugf("  [%d] type=%s name=%s call_id=%s\n", i, item.Type, item.Name, item.CallID)
		}
	}

	return p.parseBoundResponsesResponse(apiResp)
}

func (p *Provider) ensureResponsesWebSocketLocked(ctx context.Context) (*responsesWebSocketConn, error) {
	token, err := p.authorizationToken()
	if err != nil {
		return nil, err
	}
	if p.wsConn != nil && token == p.wsAuthToken {
		return p.wsConn, nil
	}
	if p.wsConn != nil {
		// A rotated OAuth token must not leave a long-lived connection bound
		// to stale authentication.
		p.resetResponsesWebSocketLocked()
	}

	wsURL, err := responsesWebSocketURL(p.baseURL, p.responsesEP())
	if err != nil {
		return nil, err
	}

	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+token)
	if p.hasChatGPTAuth() {
		if p.chatgptAccountID != "" {
			headers.Set("ChatGPT-Account-ID", p.chatgptAccountID)
		}
		headers.Set("User-Agent", chatgptUserAgent)
		headers.Set("originator", "codex_cli_rs")
		if isGPT56Model(p.model) {
			headers.Set(chatgptResponsesLiteHdr, "true")
		}
	}

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
			body = readProviderErrorClassification(resp.Body)
			resp.Body.Close()
		}
		if statusCode != 0 {
			return nil, sanitizedProviderHTTPError("openai websocket connect error", statusCode, body, p.model)
		}
		return nil, fmt.Errorf("openai websocket connect failed: %w", err)
	}

	p.wsConn = &responsesWebSocketConn{conn: conn}
	p.wsAuthToken = token
	return p.wsConn, nil
}

func (p *Provider) resetResponsesWebSocketLocked() {
	if p.wsConn != nil && p.wsConn.conn != nil {
		_ = p.wsConn.conn.Close()
	}
	p.wsConn = nil
	p.wsAuthToken = ""
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

	if wsDebugEnabled {
		wsDebugf("[gollem-ws-send] %s\n", summarizeInputForDebug(req.Input, req.PreviousResponseID))
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

	var streamedItems []responsesOutputItem
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
		if wsDebugEnabled {
			snippet := data
			if len(snippet) > 400 {
				snippet = snippet[:400]
			}
			wsDebugf("[gollem-ws-event] type=%s raw=%s\n", event.Type, string(snippet))
		}
		switch event.Type {
		case "response.reasoning_summary_text.delta":
			// Codex-style WS emits reasoning summaries incrementally as
			// deltas. Forward each chunk so callers (vcr's progress
			// printer, etc.) can surface the model's thinking live
			// rather than only seeing a single blob at .done.
			if p.reasoningSummaryHandler != nil && event.Delta != "" {
				p.reasoningSummaryHandler(event.Delta)
			}
		case "response.reasoning_summary_text.done":
			if p.reasoningSummaryHandler != nil && event.Text != "" {
				p.reasoningSummaryHandler(event.Text)
			}
		case "response.output_item.done":
			// Codex-style websocket streams output items incrementally and
			// the terminal response.completed event may arrive with an
			// empty Output slice. Accumulate completed items so we can
			// reconstruct the response output.
			if event.Item != nil {
				streamedItems = append(streamedItems, *event.Item)
			}
		case "response.done", "response.completed":
			if event.Response == nil {
				return nil, errors.New("openai websocket: terminal response event missing response payload")
			}
			if len(event.Response.Output) == 0 && len(streamedItems) > 0 {
				event.Response.Output = streamedItems
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
	classification := classifyProviderError(code, typ, msg)
	return sanitizedProviderHTTPError("openai websocket error", status, classification, model)
}

func responsesIncompleteError(event responsesWSEvent, model string) error {
	reason := ""
	if event.Response != nil && event.Response.IncompleteDetails != nil {
		reason = strings.TrimSpace(event.Response.IncompleteDetails.Reason)
	}
	if reason == "" {
		reason = "response.incomplete"
	}
	classification := classifyProviderError("response.incomplete", reason)
	return sanitizedProviderHTTPError("openai websocket response incomplete", http.StatusBadRequest, classification, model)
}

func responsesFailedError(event responsesWSEvent, model string) error {
	reason := ""
	if event.Response != nil && event.Response.IncompleteDetails != nil {
		reason = strings.TrimSpace(event.Response.IncompleteDetails.Reason)
	}
	if reason == "" {
		reason = "response.failed"
	}
	classification := classifyProviderError("response.failed", reason)
	return sanitizedProviderHTTPError("openai websocket response failed", http.StatusBadRequest, classification, model)
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

func summarizeInputForDebug(input []map[string]any, prevID string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "prev=%q items=%d", prevID, len(input))
	for i, item := range input {
		typ, _ := item["type"].(string)
		callID, _ := item["call_id"].(string)
		role, _ := item["role"].(string)
		name, _ := item["name"].(string)
		outLen := 0
		if out, ok := item["output"].(string); ok {
			outLen = len(out)
		}
		contentN := 0
		if c, ok := item["content"].([]map[string]any); ok {
			contentN = len(c)
		} else if c, ok := item["content"].([]any); ok {
			contentN = len(c)
		}
		fmt.Fprintf(&b, "\n  [%d] type=%s", i, typ)
		if callID != "" {
			fmt.Fprintf(&b, " call_id=%s", callID)
		}
		if role != "" {
			fmt.Fprintf(&b, " role=%s", role)
		}
		if name != "" {
			fmt.Fprintf(&b, " name=%s", name)
		}
		if outLen > 0 {
			fmt.Fprintf(&b, " output_len=%d", outLen)
		}
		if contentN > 0 {
			fmt.Fprintf(&b, " content_parts=%d", contentN)
		}
	}
	return b.String()
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
