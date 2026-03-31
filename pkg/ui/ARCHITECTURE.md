# Gollem UI Workbench Architecture

## Purpose

This document defines the target serve architecture for evolving the current `pkg/ui` dashboard into a CopilotKit-like gollem workbench. The goal is a thread-oriented operator UI with a stable workbench shell, HTMX-driven HTML fragments, and AGUI-backed live state where `ext/agui` remains the system of record for session/run transport semantics.

## Goals

- Replace the current run-centric "canvas scene" page with a workbench layout centered on a **thread**.
- Preserve the existing server-rendered Go template model and avoid introducing React/Next.js.
- Keep `ext/agui` transport, replay, reconnect, approval, and action handling as the canonical backend contract.
- Use HTMX for page navigation and fragment refreshes.
- Use SSE for live run/session event delivery, not for HTML rendering.
- Make room for multiple runs inside one thread while keeping a simple first implementation.

## Non-goals

- Recreating CopilotKit's React provider tree or hook APIs.
- Moving session state into browser-local state as the primary source of truth.
- Replacing AGUI event/action contracts with UI-specific websocket protocols.

## Source references

This target architecture is informed by:

- `contrib/copilotkit/PORTING_NOTES.md`
- `contrib/copilotkit/repomix-output.xml`
- `cmd/gollem/serve.go`
- `pkg/ui/server.go`
- `pkg/ui/handlers.go`
- `pkg/ui/state.go`
- `ext/agui/session.go`
- `ext/agui/event.go`
- `ext/agui/adapter.go`
- `ext/agui/transport/sse.go`
- `ext/agui/transport/action.go`

## What changes from the current UI

### Existing UI being replaced

Today the served UI is effectively a **run dashboard plus run detail scene**:

- `/` lists runs and contains the start form.
- `/runs/{id}` renders a single run page.
- `/runs/{id}/sidebar` returns the approval/status fragment.
- `/runs/{id}/events` streams run events.
- `/runs/{id}/action` forwards approval/deferred/abort actions.

The current `run.html` + `sidebar.html` + `renderer.js` composition behaves like a single-run canvas scene. That page is being replaced as the primary UX.

### New target UX

The new primary UX is a **workbench page scoped to a thread**:

- left rail: thread list and thread metadata
- center column: transcript / prompt composer / run timeline for the selected thread
- right rail: live work surface and run controls
- live status badges for the currently active run/session
- inline or side-panel approvals and deferred-input requests

This is conceptually similar to CopilotKit's chat + sidebar + canvas layout, but implemented with Go templates, HTMX, vanilla JS, and AGUI SSE.

## Core model

### Thread

A thread is the top-level user-facing conversation/work item.

Responsibilities:

- stable URL and navigation identity
- transcript grouping across multiple runs
- user prompt history and future thread-level metadata
- selection surface for the workbench shell

Rules:

- a thread may have zero or more runs
- only one run is considered the **active run** for the main live workbench at a time
- a thread survives run completion/failure and can start another run later

### Run

A run is one execution attempt within a thread.

Responsibilities:

- execution lifecycle: starting, running, waiting, resumed, completed, failed, aborted
- immutable event history for that execution
- association to exactly one AGUI session

Rules:

- every run belongs to exactly one thread
- a new user send in an existing thread normally creates a new run
- run IDs remain backend/runtime identifiers and stay valid even if the thread URL is the main navigation surface

### Session

A session is the transport/runtime identity owned by `ext/agui`.

From `ext/agui/session.go`, the session owns:

- stable `session_id`
- replay sequencing
- reconnect watermarks
- pending approvals
- pending external/deferred input state
- live status/waiting reason

Rules:

- exactly one AGUI session backs a run
- reconnect and action routing key off session identity, even when the page is thread-oriented
- session state is authoritative for live transport semantics; UI projections are derived from it

## Lifecycle contract

### Thread lifecycle

1. User opens `/threads` or `/threads/{threadID}`.
2. User submits a prompt in the composer.
3. If the thread does not exist yet, create a thread first.
4. Server creates a new run under that thread.
5. Server creates/attaches the AGUI session for the run.
6. Workbench switches the active pane to that run while preserving thread transcript/history.

### Run lifecycle

1. Run record is created.
2. AGUI session is opened and assigned to the run.
3. Runtime emits normalized events through the AGUI adapter.
4. SSE subscribers receive live events and replay on reconnect.
5. If approval or deferred input is needed, the run enters waiting.
6. HTMX forms post actions that resolve the waiting state through AGUI action handlers.
7. Run reaches terminal state: completed, failed, cancelled, or aborted.
8. Thread remains available for follow-up prompts, producing a later run.

### Session boundary

Session boundaries are **run boundaries**, not thread boundaries.

That means:

- thread continuity is a UI/product concept
- session continuity is a transport/runtime concept
- replay cursor, `Last-Event-ID`, pending approvals, and action authorization remain per-session/per-run

This is the key separation that lets the workbench feel thread-centric while keeping AGUI transport exact and replay-safe.

## Target page layout

## 1. Shell

A persistent workbench shell should replace the current single-purpose dashboard/run page split.

Regions:

- **App shell header**: brand, provider/model hints, connection state, active run status
- **Thread rail**: thread list, create/new thread affordance, recent status
- **Conversation column**: transcript, prompt composer, run timeline, follow-up prompts
- **Workbench side panel**: approvals, deferred input, tool activity, run metadata
- **Work surface**: structured/A2UI scene area for artifacts, tool renderers, future canvases

For narrow screens, the right rail/work surface may collapse below the conversation column, but the same fragment boundaries should remain.

## 2. Fragment boundaries

HTMX fragment boundaries should be explicit and stable.

### Full-page routes

- `GET /threads`
- `GET /threads/{threadID}`

### Fragments

- `GET /threads/{threadID}/thread-list` - left rail only
- `GET /threads/{threadID}/conversation` - transcript + composer column
- `GET /threads/{threadID}/workbench` - right-side live panel/work surface shell
- `GET /threads/{threadID}/approvals` - approval/deferred controls fragment
- `GET /threads/{threadID}/runs/{runID}/timeline` - run timeline fragment when a specific run is selected

### Live transport endpoints

- `GET /runs/{runID}/events`
- `POST /runs/{runID}/action`

The thread page composes HTML by thread, but live transport remains run-scoped.

## Route surface

### Preserved routes

These backend routes remain valid and continue to be used internally:

- `POST /runs/start`
- `GET /runs/{id}/events`
- `POST /runs/{id}/action`

These are the important preserved AGUI-aligned runtime surfaces.

### Reframed/legacy routes

These may remain temporarily for compatibility but are no longer the primary product surface:

- `GET /`
- `GET /runs/{id}`
- `GET /runs/{id}/sidebar`

`/runs/{id}` becomes a diagnostic/direct-link page for a single execution rather than the main workbench UX.

### New canonical routes

- `GET /threads`
- `POST /threads`
- `GET /threads/{threadID}`
- `POST /threads/{threadID}/runs`
- `GET /threads/{threadID}/thread-list`
- `GET /threads/{threadID}/conversation`
- `GET /threads/{threadID}/workbench`
- `GET /threads/{threadID}/approvals`
- `GET /threads/{threadID}/runs/{runID}/timeline`

### Route responsibility split

- **thread routes** own HTML navigation and fragment composition
- **run routes** own execution transport
- **AGUI transport routes** remain the source for event replay and action submission semantics

## HTMX contract

HTMX is responsible for HTML, not runtime truth.

### HTMX responsibilities

- navigate between thread pages without full client-side SPA infrastructure
- lazy-load shell fragments
- refresh approval/workbench fragments after actions
- swap transcript or timeline fragments when the selected run changes
- submit prompt/start-run forms and approval/deferred-input forms
- react to server-emitted `HX-Trigger` events such as `ui:fragment-loaded`

### HTMX is not responsible for

- reconstructing runtime state from scratch
- sequencing live events
- replaying missed deltas
- deciding authoritative waiting/approval state

Those responsibilities remain in AGUI session/transport layers.

## SSE contract

SSE carries normalized AGUI events and lightweight scene payloads needed by the browser runtime.

### SSE endpoint usage

The browser opens SSE only for the currently active run:

- when a thread has an active run, subscribe to `/runs/{runID}/events`
- when the user selects an older run, either switch the live subscription or render it as static history with optional replay bootstrap
- on reconnect, browser sends `Last-Event-ID` and relies on AGUI replay/snapshot behavior

### SSE payload usage in the UI

SSE payloads should drive:

- live status badge updates
- transcript append/stream updates
- tool call and tool result timeline entries
- approval/deferred request appearance
- work surface / A2UI operation updates
- reconnect-aware snapshot replacement when replay gaps require it

### SSE should not deliver

- server-rendered HTML fragments
- business logic duplicated from AGUI state machines

If the UI needs fresh HTML, it should trigger HTMX fragment fetches; if it needs fresh runtime state, it should consume SSE.

## AGUI as source of truth

`ext/agui` stays authoritative for transport and waiting-state semantics.

### Intentionally reused backend pieces

The following backend pieces are explicitly reused, not replaced:

- `ext/agui/session.go`: session identity, status, replay buffer, pending approvals/external input
- `ext/agui/event.go`: normalized event envelope and event payload taxonomy
- `ext/agui/adapter.go`: translation from gollem runtime signals into AGUI events
- `ext/agui/transport/sse.go`: replay-safe SSE delivery and reconnect behavior
- `ext/agui/transport/action.go`: action decoding/validation and approval/deferred/abort handling

### UI projection rule

`pkg/ui` may maintain derived view models and thread-level indices, but must not become the source of truth for:

- event ordering
- session status
- pending approval/deferred state
- reconnect cursor handling
- run action semantics

If a UI projection disagrees with AGUI state, AGUI wins.

## Workbench state flow

1. Server renders thread shell and embeds initial thread/run snapshot.
2. HTMX loads secondary fragments as needed.
3. Browser JS subscribes to AGUI SSE for the active run.
4. Incoming AGUI events update DOM state and local projections.
5. When an action needs server-rendered markup refresh, JS or HTMX requests the relevant fragment.
6. Action forms post to run-scoped action endpoints.
7. Server resolves the action through AGUI transport/action handling.
8. Updated state arrives via SSE; refreshed fragments are optional projections, not the source of truth.

## Replacement mapping from current canvas UI

### Replaced

- the current single-run `/runs/{id}` page as the default operator destination
- the current notion that sidebar + scene belong only to a single execution page
- current dashboard-first navigation where run creation starts from `/`

### Preserved

- server-rendered templates
- vanilla JS renderer approach
- SSE event stream consumption
- AGUI-backed approval/deferred input workflows
- run-specific action POST handling

### Evolved

- `sidebar.html` becomes a workbench-side-panel fragment rather than a run-page-only sidebar
- `run.html` concepts split into thread conversation, workbench, and timeline fragments
- `renderer.js` evolves from single-page run scene logic into a thread workbench controller with one active run subscription

## Implementation guidance

### Phase 1

- add thread data model and thread routes
- keep existing run routes operational
- render workbench shell from Go templates
- mount active-run SSE into the thread page

### Phase 2

- split current run template into explicit fragments
- support run selection inside a thread
- promote approvals/deferred input to reusable HTMX fragments

### Phase 3

- support richer A2UI/work surface rendering in the right panel
- add thread persistence beyond in-memory state
- allow resumable historical thread views with optional replay bootstrap

## Decision summary

- **Primary navigation unit:** thread
- **Execution unit:** run
- **Transport unit:** AGUI session
- **HTML update mechanism:** HTMX fragment fetch/swap
- **Live state mechanism:** SSE carrying AGUI events
- **Backend source of truth:** `ext/agui`, especially session + adapter + SSE/action transport
