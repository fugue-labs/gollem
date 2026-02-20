package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fugue-labs/gollem/core"
)

// storeKnowledgeBase wraps a Store as a core.KnowledgeBase.
type storeKnowledgeBase struct {
	store     Store
	namespace []string
}

// StoreKnowledgeBase wraps a Store as a core.KnowledgeBase.
// Retrieve performs a search across the store namespace and returns
// matching documents as formatted text. Store saves the content as
// a document keyed by timestamp.
func StoreKnowledgeBase(store Store, namespace ...string) core.KnowledgeBase {
	return &storeKnowledgeBase{
		store:     store,
		namespace: namespace,
	}
}

// Retrieve searches the store for documents matching the query and returns
// them as a formatted string.
func (kb *storeKnowledgeBase) Retrieve(ctx context.Context, query string) (string, error) {
	docs, err := kb.store.Search(ctx, kb.namespace, query, 10)
	if err != nil {
		return "", fmt.Errorf("knowledge base retrieve: %w", err)
	}

	if len(docs) == 0 {
		return "", nil
	}

	var parts []string
	for _, doc := range docs {
		data, err := json.Marshal(doc.Value)
		if err != nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("[%s] %s", doc.Key, string(data)))
	}

	return strings.Join(parts, "\n"), nil
}

// Store saves content as a document in the store, using a timestamp-based key.
func (kb *storeKnowledgeBase) Store(ctx context.Context, content string) error {
	key := fmt.Sprintf("kb-%d", time.Now().UnixNano())
	value := map[string]any{
		"content": content,
	}
	return kb.store.Put(ctx, kb.namespace, key, value)
}

// Verify storeKnowledgeBase implements core.KnowledgeBase.
var _ core.KnowledgeBase = (*storeKnowledgeBase)(nil)
