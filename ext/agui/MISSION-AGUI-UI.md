# AGUI UI Mission Notes

This note tracks the current `gollem serve` browser dashboard behavior so project-facing docs stay aligned with the shipped UI.

## Current state

`gollem serve` is no longer a placeholder or scaffold-only experiment. The command now serves a working embedded browser UI backed by the `pkg/ui` package:

- Go `embed` packages the HTML templates and static assets into the binary.
- `pkg/ui.Server` serves the dashboard, run detail page, sidebar fragment, static assets, and the live SSE endpoint.
- `cmd/gollem/serve.go` wires the browser experience to a real run starter, applies serve-time provider/model defaults, and exposes tool-enabled runs when `--tools` is set.
- The run page combines a live renderer, readable activity log, protocol event log, and a sidebar that refreshes while the run is active.

## Recommended serve workflow

Start the dashboard from a shell:

```bash
gollem serve --provider anthropic --open
gollem serve --provider openai --model gpt-5.3 --port 9090
gollem serve --provider anthropic --tools --workdir /path/to/project
```

Key behavior:

- `--port` changes the local HTTP port. Default: `8080`.
- `--open` opens the dashboard URL in the default browser.
- `--tools` enables coding tools for runs launched from the dashboard.
- `--workdir` sets the working directory used by tool-enabled runs.
- Provider/model are chosen at serve startup, not interactively per browser submission.

## Launching a run from the dashboard

The dashboard home page now has a real **Start a run** composer.

1. Start `gollem serve`.
2. Open the dashboard at `http://localhost:<port>/`.
3. Fill in:
   - **Title** — optional run label for cards and detail pages
   - **Summary** — optional short context shown on the dashboard card
   - **Prompt** — required run prompt
4. Click **Start run**.
5. The browser is redirected to `/runs/<id>` for the live run page.

Important detail: the browser form does **not** expose provider/model inputs in the normal served workflow. `cmd/gollem/serve.go` rewrites `/runs/start` submissions so the launched run uses the active serve defaults.

## Run page and live surfaces

The run detail page currently ships with:

- a live run-scene renderer fed by `/runs/<id>/events`
- connection, stream-state, waiting-state, step-count, entity-count, and last-event metrics
- a readable recent-activity narrative for waiting, resume, completion, failure, and tool outcomes
- a protocol-events log for replay/reconnect debugging
- the original prompt
- a live sidebar fragment that refreshes while the run is non-terminal

## Sidebar controls

The sidebar is the main control surface for human intervention.

### Abort run

- Visible while the run is still abortable.
- Posts the `abort_session` action to `/runs/<id>/action`.
- Stops the run before completion.
- Once the run reaches a terminal state, abort controls disappear.

### Approve

- Shown for each pending tool approval.
- Posts `approve_tool_call` with the tool call ID.
- Approving allows that tool call to execute and the run to continue.
- The UI then updates to show the approval as resolved and the run as resumed/continuing.

### Deny

- Shown beside Approve for each pending tool approval.
- Posts `deny_tool_call` with the tool call ID.
- Denying prevents that tool call from executing.
- The run records the denied approval and moves forward according to runtime behavior, which can mean failure or another terminal/waiting state depending on the run.

## Waiting and live refresh behavior

When a run is blocked on human approval, the dashboard surfaces that explicitly:

- status becomes a waiting-style state
- the sidebar shows **Pending approvals** with tool metadata and arguments
- the main run page shows waiting labels such as **Waiting for approval**
- the sidebar uses periodic HTMX refresh while the run is active
- the run scene listens to SSE for live events and reconnect/resume-safe updates

## Documentation guardrails

Docs that reference AGUI should describe the shipped browser dashboard as:

- embedded and binary-served, not a detached scaffold
- launched with `gollem serve`
- started from the dashboard composer using serve-configured provider/model defaults
- controlled from the sidebar with approve, deny, and abort actions
- updated live through SSE plus sidebar fragment refreshes
