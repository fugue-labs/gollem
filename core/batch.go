package core

import (
	"context"
	"runtime"
	"sync"
)

// BatchResult holds the result of a single batch item.
type BatchResult[T any] struct {
	Index  int            // position in the input slice
	Result *RunResult[T]  // non-nil on success
	Err    error          // non-nil on failure
}

// WithBatchConcurrency sets the maximum number of concurrent batch executions.
func WithBatchConcurrency(n int) RunOption {
	return func(c *runConfig) {
		c.batchConcurrency = n
	}
}

// RunBatch executes multiple prompts through the agent concurrently.
// Results are returned in the same order as prompts.
// Concurrency defaults to GOMAXPROCS but can be limited via WithBatchConcurrency.
func (a *Agent[T]) RunBatch(ctx context.Context, prompts []string, opts ...RunOption) []BatchResult[T] {
	if len(prompts) == 0 {
		return nil
	}

	// Eagerly build output schema to avoid races from concurrent Run() calls.
	if a.outputSchema == nil {
		a.outputSchema = buildOutputSchema[T](a.outputOpts...)
	}

	cfg := &runConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	concurrency := runtime.GOMAXPROCS(0)
	if cfg.batchConcurrency > 0 {
		concurrency = cfg.batchConcurrency
	}

	results := make([]BatchResult[T], len(prompts))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, prompt := range prompts {
		wg.Add(1)
		go func(idx int, p string) {
			defer wg.Done()

			// Acquire semaphore.
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[idx] = BatchResult[T]{
					Index: idx,
					Err:   ctx.Err(),
				}
				return
			}

			// Run with non-batch options only (filter out batch concurrency).
			result, err := a.Run(ctx, p, opts...)
			results[idx] = BatchResult[T]{
				Index:  idx,
				Result: result,
				Err:    err,
			}
		}(i, prompt)
	}

	wg.Wait()
	return results
}
