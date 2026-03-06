package codetool

import (
	"context"

	"github.com/fugue-labs/gollem/core"
)

// BashStatusParams are the parameters for the bash_status tool.
type BashStatusParams struct {
	ID string `json:"id" jsonschema:"description=Background process ID (e.g. 'bg-1') or 'all' to list all background processes"`
}

// BashStatus creates a tool that checks the status of background processes.
func BashStatus(opts ...Option) core.Tool {
	cfg := applyOpts(opts)

	return core.FuncTool[BashStatusParams](
		"bash_status",
		"Check the status of background processes started with bash background=true. "+
			"Returns process state (running/completed/failed), exit code, and recent output. "+
			"Use id='all' to list all background processes or specify a process ID like 'bg-1'.",
		func(ctx context.Context, params BashStatusParams) (string, error) {
			if cfg.BackgroundProcessManager == nil {
				return "", &core.ModelRetryError{Message: "no background process manager available"}
			}
			if params.ID == "" {
				return "", &core.ModelRetryError{Message: "id is required — use a process ID like 'bg-1' or 'all' to list all"}
			}
			if params.ID == "all" {
				return cfg.BackgroundProcessManager.FormatAll(), nil
			}
			return cfg.BackgroundProcessManager.FormatProcess(params.ID)
		},
	)
}
