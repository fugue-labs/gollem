package core

import "context"

// KnowledgeBase represents a system that can retrieve context relevant to a prompt
// and store insights from agent interactions. Implementations might include RAG systems,
// graph databases, memory services, or any external knowledge source.
type KnowledgeBase interface {
	// Retrieve fetches context relevant to the given query.
	// Returns an empty string if no relevant context is found.
	Retrieve(ctx context.Context, query string) (string, error)

	// Store persists content (e.g., successful agent responses) for future retrieval.
	Store(ctx context.Context, content string) error
}

// WithKnowledgeBase injects a knowledge system into the agent.
// When set, the agent will call Retrieve before each run and prepend
// any returned context as a system prompt.
func WithKnowledgeBase[T any](kb KnowledgeBase) AgentOption[T] {
	return func(a *Agent[T]) {
		a.knowledgeBase = kb
	}
}

// WithKnowledgeBaseAutoStore enables automatic storage of successful agent responses.
// When enabled along with a KnowledgeBase, the agent will call Store with the
// text content of the model's final response after a successful run.
func WithKnowledgeBaseAutoStore[T any]() AgentOption[T] {
	return func(a *Agent[T]) {
		a.kbAutoStore = true
	}
}

// StaticKnowledgeBase is a simple in-memory KnowledgeBase for testing.
// It always returns the same context string on Retrieve and discards Store calls.
type StaticKnowledgeBase struct {
	context string
	stored  []string
}

// NewStaticKnowledgeBase creates a StaticKnowledgeBase that always returns the given context.
func NewStaticKnowledgeBase(context string) *StaticKnowledgeBase {
	return &StaticKnowledgeBase{context: context}
}

// Retrieve returns the static context string.
func (kb *StaticKnowledgeBase) Retrieve(_ context.Context, _ string) (string, error) {
	return kb.context, nil
}

// Store records the content for later inspection in tests.
func (kb *StaticKnowledgeBase) Store(_ context.Context, content string) error {
	kb.stored = append(kb.stored, content)
	return nil
}

// Stored returns all content that was stored via Store calls.
func (kb *StaticKnowledgeBase) Stored() []string {
	return kb.stored
}
