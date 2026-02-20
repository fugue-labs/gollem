package memory

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fugue-labs/gollem/core"
)

func TestMemoryStore_PutGet(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	ns := []string{"test"}
	value := map[string]any{"name": "Alice", "age": float64(30)}

	if err := store.Put(ctx, ns, "user1", value); err != nil {
		t.Fatal(err)
	}

	doc, err := store.Get(ctx, ns, "user1")
	if err != nil {
		t.Fatal(err)
	}
	if doc == nil {
		t.Fatal("expected document, got nil")
	}
	if doc.Key != "user1" {
		t.Errorf("expected key 'user1', got %q", doc.Key)
	}
	if doc.Value["name"] != "Alice" {
		t.Errorf("expected name 'Alice', got %v", doc.Value["name"])
	}
	if doc.Value["age"] != float64(30) {
		t.Errorf("expected age 30, got %v", doc.Value["age"])
	}
	if doc.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
	if doc.UpdatedAt.IsZero() {
		t.Error("expected non-zero UpdatedAt")
	}

	// Get non-existent key returns nil.
	doc, err = store.Get(ctx, ns, "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if doc != nil {
		t.Errorf("expected nil for nonexistent key, got %+v", doc)
	}
}

func TestMemoryStore_PutOverwrite(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	ns := []string{"test"}

	if err := store.Put(ctx, ns, "key1", map[string]any{"v": "first"}); err != nil {
		t.Fatal(err)
	}

	doc1, _ := store.Get(ctx, ns, "key1")
	createdAt := doc1.CreatedAt

	if err := store.Put(ctx, ns, "key1", map[string]any{"v": "second"}); err != nil {
		t.Fatal(err)
	}

	doc2, _ := store.Get(ctx, ns, "key1")
	if doc2.Value["v"] != "second" {
		t.Errorf("expected 'second', got %v", doc2.Value["v"])
	}
	if !doc2.CreatedAt.Equal(createdAt) {
		t.Error("CreatedAt should not change on overwrite")
	}
	if doc2.UpdatedAt.Before(doc2.CreatedAt) {
		t.Error("UpdatedAt should be >= CreatedAt")
	}
}

func TestMemoryStore_List(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	ns := []string{"app", "users"}

	store.Put(ctx, ns, "user1", map[string]any{"name": "Alice"})
	store.Put(ctx, ns, "user2", map[string]any{"name": "Bob"})
	store.Put(ctx, ns, "user3", map[string]any{"name": "Charlie"})

	docs, err := store.List(ctx, ns)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 3 {
		t.Fatalf("expected 3 documents, got %d", len(docs))
	}

	// Verify all keys are present.
	keys := make(map[string]bool)
	for _, doc := range docs {
		keys[doc.Key] = true
	}
	for _, key := range []string{"user1", "user2", "user3"} {
		if !keys[key] {
			t.Errorf("expected key %q in results", key)
		}
	}
}

func TestMemoryStore_Search(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	ns := []string{"notes"}

	store.Put(ctx, ns, "note1", map[string]any{"text": "Go is a compiled language"})
	store.Put(ctx, ns, "note2", map[string]any{"text": "Python is an interpreted language"})
	store.Put(ctx, ns, "note3", map[string]any{"text": "Go has great concurrency support"})

	// Search for "Go" should match note1 and note3.
	docs, err := store.Search(ctx, ns, "Go", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 results for 'Go', got %d", len(docs))
	}

	// Search with limit.
	docs, err = store.Search(ctx, ns, "language", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 result with limit=1, got %d", len(docs))
	}

	// Case-insensitive search.
	docs, err = store.Search(ctx, ns, "go", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 results for case-insensitive 'go', got %d", len(docs))
	}
}

func TestMemoryStore_Delete(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	ns := []string{"test"}

	store.Put(ctx, ns, "key1", map[string]any{"v": "val"})

	// Verify it exists.
	doc, _ := store.Get(ctx, ns, "key1")
	if doc == nil {
		t.Fatal("expected document before delete")
	}

	// Delete it.
	if err := store.Delete(ctx, ns, "key1"); err != nil {
		t.Fatal(err)
	}

	// Verify it's gone.
	doc, _ = store.Get(ctx, ns, "key1")
	if doc != nil {
		t.Error("expected nil after delete")
	}

	// Deleting nonexistent key should not error.
	if err := store.Delete(ctx, ns, "nonexistent"); err != nil {
		t.Errorf("expected no error deleting nonexistent key, got %v", err)
	}
}

func TestMemoryStore_NamespaceIsolation(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	ns1 := []string{"app", "users"}
	ns2 := []string{"app", "settings"}

	store.Put(ctx, ns1, "key1", map[string]any{"scope": "users"})
	store.Put(ctx, ns2, "key1", map[string]any{"scope": "settings"})

	// Get from ns1.
	doc1, _ := store.Get(ctx, ns1, "key1")
	if doc1 == nil {
		t.Fatal("expected document in ns1")
	}
	if doc1.Value["scope"] != "users" {
		t.Errorf("expected 'users', got %v", doc1.Value["scope"])
	}

	// Get from ns2.
	doc2, _ := store.Get(ctx, ns2, "key1")
	if doc2 == nil {
		t.Fatal("expected document in ns2")
	}
	if doc2.Value["scope"] != "settings" {
		t.Errorf("expected 'settings', got %v", doc2.Value["scope"])
	}

	// List from ns1 should only return ns1 documents.
	docs, _ := store.List(ctx, ns1)
	if len(docs) != 1 {
		t.Fatalf("expected 1 document in ns1, got %d", len(docs))
	}

	// Delete from ns1 should not affect ns2.
	store.Delete(ctx, ns1, "key1")
	doc2Again, _ := store.Get(ctx, ns2, "key1")
	if doc2Again == nil {
		t.Fatal("expected document in ns2 after deleting from ns1")
	}

	// Search in ns2 should not find ns1 documents.
	store.Put(ctx, ns1, "key2", map[string]any{"data": "unique-ns1-value"})
	results, _ := store.Search(ctx, ns2, "unique-ns1-value", 10)
	if len(results) != 0 {
		t.Errorf("expected 0 search results in ns2 for ns1 data, got %d", len(results))
	}
}

func TestStoreKnowledgeBase_Retrieve(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	ns := []string{"kb"}

	// Populate store.
	store.Put(ctx, ns, "fact1", map[string]any{"content": "Go was created at Google"})
	store.Put(ctx, ns, "fact2", map[string]any{"content": "Go has garbage collection"})
	store.Put(ctx, ns, "fact3", map[string]any{"content": "Python uses indentation"})

	kb := StoreKnowledgeBase(store, ns...)

	// Retrieve should search and return matching content.
	result, err := kb.Retrieve(ctx, "Go")
	if err != nil {
		t.Fatal(err)
	}
	if result == "" {
		t.Fatal("expected non-empty retrieval result")
	}

	// Should not contain Python fact when searching for Go.
	// (Python doesn't contain "Go" substring, so it won't match.)
	// Result should contain fact1 and fact2.
	if !containsSubstring(result, "Google") {
		t.Error("expected result to contain 'Google'")
	}
	if !containsSubstring(result, "garbage collection") {
		t.Error("expected result to contain 'garbage collection'")
	}
}

func TestStoreKnowledgeBase_Store(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	ns := []string{"kb"}
	kb := StoreKnowledgeBase(store, ns...)

	// Store content.
	if err := kb.Store(ctx, "Rust has ownership semantics"); err != nil {
		t.Fatal(err)
	}

	// Verify it was stored by searching.
	docs, err := store.Search(ctx, ns, "Rust", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	if docs[0].Value["content"] != "Rust has ownership semantics" {
		t.Errorf("expected stored content, got %v", docs[0].Value["content"])
	}
}

func TestMemoryTool_SaveAndRetrieve(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	ns := []string{"agent"}
	tool := MemoryTool(store, ns...)

	// Verify tool definition.
	if tool.Definition.Name != "memory" {
		t.Errorf("expected tool name 'memory', got %q", tool.Definition.Name)
	}
	if tool.Definition.Kind != core.ToolKindFunction {
		t.Errorf("expected tool kind 'function', got %q", tool.Definition.Kind)
	}

	// Save a document.
	saveArgs, _ := json.Marshal(map[string]any{
		"operation": "save",
		"key":       "greeting",
		"value":     `{"text": "hello world"}`,
	})
	result, err := tool.Handler(ctx, &core.RunContext{}, string(saveArgs))
	if err != nil {
		t.Fatal(err)
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if resultMap["status"] != "saved" {
		t.Errorf("expected status 'saved', got %v", resultMap["status"])
	}

	// Get the document.
	getArgs, _ := json.Marshal(map[string]any{
		"operation": "get",
		"key":       "greeting",
	})
	result, err = tool.Handler(ctx, &core.RunContext{}, string(getArgs))
	if err != nil {
		t.Fatal(err)
	}
	doc, ok := result.(*Document)
	if !ok {
		t.Fatalf("expected *Document, got %T", result)
	}
	if doc.Key != "greeting" {
		t.Errorf("expected key 'greeting', got %q", doc.Key)
	}
	if doc.Value["text"] != "hello world" {
		t.Errorf("expected value 'hello world', got %v", doc.Value["text"])
	}

	// Search for the document.
	searchArgs, _ := json.Marshal(map[string]any{
		"operation": "search",
		"query":     "hello",
	})
	result, err = tool.Handler(ctx, &core.RunContext{}, string(searchArgs))
	if err != nil {
		t.Fatal(err)
	}
	searchResult, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	count, ok := searchResult["count"].(int)
	if !ok {
		t.Fatalf("expected int count, got %T", searchResult["count"])
	}
	if count != 1 {
		t.Errorf("expected 1 search result, got %d", count)
	}

	// Delete the document.
	deleteArgs, _ := json.Marshal(map[string]any{
		"operation": "delete",
		"key":       "greeting",
	})
	result, err = tool.Handler(ctx, &core.RunContext{}, string(deleteArgs))
	if err != nil {
		t.Fatal(err)
	}
	deleteResult, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if deleteResult["status"] != "deleted" {
		t.Errorf("expected status 'deleted', got %v", deleteResult["status"])
	}

	// Verify deletion.
	result, err = tool.Handler(ctx, &core.RunContext{}, string(getArgs))
	if err != nil {
		t.Fatal(err)
	}
	notFoundResult, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if notFoundResult["status"] != "not_found" {
		t.Errorf("expected status 'not_found', got %v", notFoundResult["status"])
	}
}

func TestMemoryTool_Errors(t *testing.T) {
	store := NewMemoryStore()
	tool := MemoryTool(store, "test")
	ctx := context.Background()

	tests := []struct {
		name string
		args map[string]any
	}{
		{"save without key", map[string]any{"operation": "save", "value": `{"x":1}`}},
		{"save without value", map[string]any{"operation": "save", "key": "k"}},
		{"get without key", map[string]any{"operation": "get"}},
		{"search without query", map[string]any{"operation": "search"}},
		{"delete without key", map[string]any{"operation": "delete"}},
		{"unknown operation", map[string]any{"operation": "invalid"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			argsJSON, _ := json.Marshal(tc.args)
			_, err := tool.Handler(ctx, &core.RunContext{}, string(argsJSON))
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestSQLiteStore_Persistence(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"

	// Create store and write data.
	store1, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}

	ctx := context.Background()
	ns := []string{"persist"}

	if err := store1.Put(ctx, ns, "key1", map[string]any{"data": "survives restart"}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Close the store.
	store1.(*SQLiteStore).Close()

	// Reopen and verify data persists.
	store2, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore (reopen): %v", err)
	}
	defer store2.(*SQLiteStore).Close()

	doc, err := store2.Get(ctx, ns, "key1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if doc == nil {
		t.Fatal("expected document to survive close and reopen")
	}
	if doc.Value["data"] != "survives restart" {
		t.Errorf("expected 'survives restart', got %v", doc.Value["data"])
	}
}

func TestSQLiteStore_CRUD(t *testing.T) {
	dbPath := t.TempDir() + "/crud.db"
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.(*SQLiteStore).Close()

	ctx := context.Background()
	ns := []string{"test"}

	// Put.
	if err := store.Put(ctx, ns, "k1", map[string]any{"name": "Alice"}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Get.
	doc, err := store.Get(ctx, ns, "k1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if doc == nil || doc.Value["name"] != "Alice" {
		t.Fatalf("expected Alice, got %v", doc)
	}

	// List.
	store.Put(ctx, ns, "k2", map[string]any{"name": "Bob"})
	docs, err := store.List(ctx, ns)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs, got %d", len(docs))
	}

	// Search.
	results, err := store.Search(ctx, ns, "Alice", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 search result, got %d", len(results))
	}

	// Delete.
	if err := store.Delete(ctx, ns, "k1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	doc, _ = store.Get(ctx, ns, "k1")
	if doc != nil {
		t.Error("expected nil after delete")
	}
}

// containsSubstring checks if s contains substr.
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
