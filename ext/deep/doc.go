// Package deep provides advanced capabilities for long-running, real-world agents.
// It includes context management (auto-offloading, summarization, compression),
// planning tools, checkpointing, and durable execution primitives.
//
// The deep package is designed for agents that run for extended periods, handle
// complex multi-step tasks, and need to manage limited context windows effectively.
//
// Context Management:
//
// The ContextManager implements a three-tier compression strategy:
//   - Tier 1: Offload large tool results to filesystem, replace with summary
//   - Tier 2: Offload large tool call inputs when approaching context limits
//   - Tier 3: Summarize older conversation turns via LLM
//
// The ContextManager satisfies the core.HistoryProcessor interface, so it
// integrates directly with agents:
//
//	cm := deep.NewContextManager(model, deep.WithMaxContextTokens(100000))
//	agent := core.NewAgent[string](model,
//	    core.WithHistoryProcessor(cm.AsHistoryProcessor()),
//	)
//
// Context Store:
//
// The FileStore provides filesystem-backed storage for offloaded content:
//
//	store, err := deep.NewFileStore("/tmp/gollem-context")
//	cm := deep.NewContextManager(model, deep.WithContextStore(store))
package deep
