// Package monty integrates the monty-go Python interpreter with gollem,
// enabling code mode for agents. Instead of the LLM making N sequential
// tool calls (N model round-trips), it writes a single Python script that
// calls tools as functions. The monty-go WASM interpreter executes the
// script, pausing at each function call so the corresponding gollem tool
// handler runs, then resumes. Result: N tool calls in 1 model round-trip.
//
// Usage:
//
//	runner, _ := montygo.New()
//	defer runner.Close()
//
//	searchTool := core.FuncTool[SearchParams]("search", "Search docs", doSearch)
//	calcTool := core.FuncTool[CalcParams]("calculate", "Run calculations", doCalc)
//
//	cm := monty.New(runner, []core.Tool{searchTool, calcTool})
//
//	agent := core.NewAgent[string](model,
//	    core.WithSystemPrompt[string](cm.SystemPrompt()),
//	    core.WithTools[string](cm.Tool()),
//	)
package monty
