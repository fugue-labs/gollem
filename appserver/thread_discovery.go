package appserver

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/fugue-labs/gollem/appserver/protocol"
	"github.com/fugue-labs/gollem/appserver/store"
)

const (
	defaultThreadSearchLimit = 50
	maxThreadSearchLimit     = 100
	maxThreadSearchSnippet   = 240
)

type threadIdleUnload struct {
	timer *time.Timer
}

func (s *Server) handleThreadLoadedList(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	st, rpcErr := s.requireStore("thread/loaded/list")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params threadLoadedListParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	offset, rpcErr := parseCursor(params.Cursor, "thread/loaded/list")
	if rpcErr != nil {
		return nil, rpcErr
	}
	loaded := s.loadedThreadIDs()
	threads, err := st.ListThreads(ctx, store.ThreadFilter{IncludeDeleted: true})
	if err != nil {
		return nil, mapError("thread/loaded/list", err)
	}
	ids := make([]string, 0, len(loaded))
	for _, thread := range threads {
		if _, ok := loaded[thread.ID]; !ok || thread.Status == store.ThreadDeleted {
			continue
		}
		ids = append(ids, thread.ID)
	}
	data, nextCursor := paginateStrings(ids, offset, params.Limit)
	return threadLoadedListResult{
		Data:       data,
		NextCursor: nextCursor,
	}, nil
}

func (s *Server) handleThreadSearch(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
	st, rpcErr := s.requireStore("thread/search")
	if rpcErr != nil {
		return nil, rpcErr
	}
	var params threadSearchParams
	if rpcErr := decodeParams(raw, &params); rpcErr != nil {
		return nil, rpcErr
	}
	query := params.query()
	if query == "" {
		return nil, invalidParams("searchTerm is required", nil)
	}
	offset, rpcErr := parseCursor(params.Cursor, "thread/search")
	if rpcErr != nil {
		return nil, rpcErr
	}
	statuses := []store.ThreadStatus{store.ThreadActive}
	if params.Archived != nil && *params.Archived {
		statuses = []store.ThreadStatus{store.ThreadArchived}
	}
	threads, err := st.ListThreads(ctx, store.ThreadFilter{Statuses: statuses})
	if err != nil {
		return nil, mapError("thread/search", err)
	}
	threads = filterBySourceKinds(threads, params.SourceKinds)
	sortThreadsForSearch(threads, params.SortKey, params.SortDirection)

	results := make([]threadSearchResult, 0, min(len(threads), searchLimit(params.Limit)))
	for _, thread := range threads {
		snippet, ok, err := threadSearchSnippet(ctx, st, thread, query)
		if err != nil {
			return nil, mapError("thread/search", err)
		}
		if !ok {
			continue
		}
		results = append(results, threadSearchResult{
			Thread:  thread,
			Snippet: snippet,
		})
	}
	limit := searchLimit(params.Limit)
	page, nextCursor := paginateSearchResults(results, offset, limit)
	backwardsCursor := (*string)(nil)
	if len(page) > 0 {
		cursor := strconv.Itoa(offset)
		backwardsCursor = &cursor
	}
	return threadSearchResponse{
		Data:            page,
		NextCursor:      nextCursor,
		BackwardsCursor: backwardsCursor,
	}, nil
}

func (s *Server) markThreadLoaded(thread *store.Thread) {
	if s == nil || thread == nil || thread.ID == "" || thread.Status == store.ThreadDeleted {
		return
	}
	s.markThreadLoadedID(thread.ID)
}

func (s *Server) markThreadLoadedID(threadID string) {
	if s == nil || threadID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded == nil {
		s.loaded = make(map[string]struct{})
	}
	if s.subscribed == nil {
		s.subscribed = make(map[string]struct{})
	}
	s.loaded[threadID] = struct{}{}
	s.subscribed[threadID] = struct{}{}
	s.cancelThreadIdleUnloadLocked(threadID)
}

func (s *Server) markThreadUnloaded(threadID string) {
	if s == nil || threadID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.loaded, threadID)
	delete(s.subscribed, threadID)
	s.cancelThreadIdleUnloadLocked(threadID)
}

func (s *Server) loadedThreadIDs() map[string]struct{} {
	out := map[string]struct{}{}
	if s == nil {
		return out
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.loaded {
		out[id] = struct{}{}
	}
	return out
}

func (s *Server) unsubscribeThread(threadID string) string {
	if s == nil || threadID == "" {
		return "notLoaded"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.loaded[threadID]; !ok {
		return "notLoaded"
	}
	if _, ok := s.subscribed[threadID]; !ok {
		return "notSubscribed"
	}
	delete(s.subscribed, threadID)
	s.scheduleThreadIdleUnloadLocked(threadID)
	return "unsubscribed"
}

func (s *Server) cancelThreadIdleUnloadLocked(threadID string) {
	if s.idleUnload == nil || threadID == "" {
		return
	}
	if entry := s.idleUnload[threadID]; entry != nil && entry.timer != nil {
		entry.timer.Stop()
	}
	delete(s.idleUnload, threadID)
}

func (s *Server) scheduleThreadIdleUnloadLocked(threadID string) {
	if threadID == "" || s.threadIdleUnloadAfter < 0 {
		return
	}
	if s.idleUnload == nil {
		s.idleUnload = make(map[string]*threadIdleUnload)
	}
	s.cancelThreadIdleUnloadLocked(threadID)
	entry := &threadIdleUnload{}
	entry.timer = time.AfterFunc(s.threadIdleUnloadAfter, func() {
		s.expireThreadIdleUnload(threadID, entry)
	})
	s.idleUnload[threadID] = entry
}

func (s *Server) expireThreadIdleUnload(threadID string, entry *threadIdleUnload) {
	if s == nil || threadID == "" {
		return
	}
	closed := false
	s.mu.Lock()
	if s.idleUnload != nil && s.idleUnload[threadID] == entry {
		delete(s.idleUnload, threadID)
		if _, subscribed := s.subscribed[threadID]; !subscribed {
			if _, loaded := s.loaded[threadID]; loaded {
				delete(s.loaded, threadID)
				closed = true
			}
		}
	}
	s.mu.Unlock()
	if closed {
		s.publishThreadClosed(threadID)
	}
}

func parseCursor(cursor string, method string) (int, *protocol.Error) {
	if strings.TrimSpace(cursor) == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(cursor)
	if err != nil || offset < 0 {
		return 0, invalidParams(method+" cursor is invalid", err)
	}
	return offset, nil
}

func paginateStrings(values []string, offset int, limit int) ([]string, *string) {
	if offset >= len(values) {
		return []string{}, nil
	}
	end := len(values)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	page := append([]string(nil), values[offset:end]...)
	if end >= len(values) {
		return page, nil
	}
	cursor := strconv.Itoa(end)
	return page, &cursor
}

func paginateSearchResults(values []threadSearchResult, offset int, limit int) ([]threadSearchResult, *string) {
	if offset >= len(values) {
		return []threadSearchResult{}, nil
	}
	end := len(values)
	if offset+limit < end {
		end = offset + limit
	}
	page := append([]threadSearchResult(nil), values[offset:end]...)
	if end >= len(values) {
		return page, nil
	}
	cursor := strconv.Itoa(end)
	return page, &cursor
}

func searchLimit(limit int) int {
	if limit <= 0 {
		return defaultThreadSearchLimit
	}
	if limit > maxThreadSearchLimit {
		return maxThreadSearchLimit
	}
	return limit
}

func filterBySourceKinds(threads []*store.Thread, sourceKinds []string) []*store.Thread {
	if len(sourceKinds) == 0 {
		return threads
	}
	allowsAppServer := false
	for _, source := range sourceKinds {
		normalized := strings.ToLower(strings.ReplaceAll(source, "_", ""))
		if normalized == "appserver" {
			allowsAppServer = true
			break
		}
	}
	if !allowsAppServer {
		return nil
	}
	out := threads[:0]
	for _, thread := range threads {
		source := strings.ToLower(strings.ReplaceAll(stringMapValue(thread.Metadata, "sourceKind"), "_", ""))
		if source == "" || source == "appserver" {
			out = append(out, thread)
		}
	}
	return out
}

func sortThreadsForSearch(threads []*store.Thread, sortKey string, sortDirection string) {
	key := strings.ToLower(strings.TrimSpace(sortKey))
	if key == "" {
		key = "created_at"
	}
	desc := strings.ToLower(strings.TrimSpace(sortDirection)) != "asc"
	sort.SliceStable(threads, func(i, j int) bool {
		left := threadSortTime(threads[i], key)
		right := threadSortTime(threads[j], key)
		if left.Equal(right) {
			if desc {
				return threads[i].ID > threads[j].ID
			}
			return threads[i].ID < threads[j].ID
		}
		if desc {
			return left.After(right)
		}
		return left.Before(right)
	})
}

func threadSortTime(thread *store.Thread, key string) time.Time {
	if thread == nil {
		return time.Time{}
	}
	switch key {
	case "updated_at", "updatedat", "recency_at", "recencyat":
		return thread.UpdatedAt
	default:
		return thread.CreatedAt
	}
}

func threadSearchSnippet(ctx context.Context, st store.Store, thread *store.Thread, query string) (string, bool, error) {
	normalizedQuery := strings.ToLower(query)
	candidates := threadSearchCandidates(thread)
	items, err := st.ListItems(ctx, store.ItemFilter{ThreadID: thread.ID})
	if err != nil {
		return "", false, err
	}
	for _, item := range items {
		candidates = append(candidates, itemSearchCandidates(item)...)
	}
	for _, candidate := range candidates {
		if strings.Contains(strings.ToLower(candidate), normalizedQuery) {
			return makeSearchSnippet(candidate, query), true, nil
		}
	}
	return "", false, nil
}

func threadSearchCandidates(thread *store.Thread) []string {
	if thread == nil {
		return nil
	}
	candidates := []string{
		"Title: " + thread.Title,
		"Workspace: " + thread.Workspace,
		"Thread: " + thread.ID,
	}
	if len(thread.Metadata) > 0 {
		candidates = append(candidates, "Metadata: "+jsonObjectString(thread.Metadata))
	}
	if len(thread.Settings) > 0 {
		candidates = append(candidates, "Settings: "+jsonObjectString(thread.Settings))
	}
	return candidates
}

func itemSearchCandidates(item *store.Item) []string {
	if item == nil {
		return nil
	}
	candidates := []string{
		"Item: " + item.ID,
		"Kind: " + item.Kind,
		"Status: " + item.Status,
	}
	if len(item.Payload) == 0 {
		return candidates
	}
	var payload any
	if err := json.Unmarshal(item.Payload, &payload); err == nil {
		candidates = append(candidates, collectJSONStrings(payload)...)
	}
	candidates = append(candidates, string(item.Payload))
	return candidates
}

func collectJSONStrings(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		var out []string
		for _, item := range typed {
			out = append(out, collectJSONStrings(item)...)
		}
		return out
	case map[string]any:
		var out []string
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			for _, text := range collectJSONStrings(typed[key]) {
				if text == "" {
					continue
				}
				out = append(out, key+": "+text)
			}
		}
		return out
	default:
		return nil
	}
}

func jsonObjectString(value map[string]any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func makeSearchSnippet(text string, query string) string {
	text = compactWhitespace(text)
	if len(text) <= maxThreadSearchSnippet {
		return text
	}
	lowerText := strings.ToLower(text)
	lowerQuery := strings.ToLower(query)
	match := strings.Index(lowerText, lowerQuery)
	if match < 0 {
		return text[:maxThreadSearchSnippet-3] + "..."
	}
	start := match - maxThreadSearchSnippet/3
	if start < 0 {
		start = 0
	}
	end := start + maxThreadSearchSnippet
	if end > len(text) {
		end = len(text)
		start = max(0, end-maxThreadSearchSnippet)
	}
	snippet := text[start:end]
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(text) {
		snippet += "..."
	}
	return snippet
}

func compactWhitespace(text string) string {
	return strings.Join(strings.FieldsFunc(text, unicode.IsSpace), " ")
}

func stringMapValue(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	value, ok := values[key]
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return text
}
