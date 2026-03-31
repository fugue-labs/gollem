# Mission: AGUI ↔ UI Workbench Contract

## Why this file exists

This file defines the contract between `ext/agui` and the replacement `pkg/ui` workbench.

The workbench may change layout, routes, and HTML fragment structure, but it must continue to treat AGUI transport and session state as authoritative for live execution behavior.

This is the design target for replacing the current serve canvas-style UI with a CopilotKit-like thread workbench.

## Design baseline

Current code already establishes an important split:

- `cmd/gollem/serve.go` mounts `pkg/ui` and injects serve defaults.
- `pkg/ui/server.go` wires the HTML and run routes.
- `pkg/ui/state.go` creates one `RunRecord` with:
  - a `core.EventBus`
  - an `agui.Session`
  - an `agui.Adapter`
  - `transport.NewSSEHandler(...)`
  - `transport.NewActionHandler(...)`
- `pkg/ui/handlers.go` renders HTML and forwards run-scoped actions.
- `ext/agui/transport/sse.go` handles replayable SSE.
- `ext/agui/transport/action.go` handles approval/abort actions.

That split is correct and should be preserved.

## Product-level target

The product surface should become a **thread-oriented workbench**, not a single-run scene page.

### Desired operator experience

A user should feel like they are working inside one persistent thread that contains:

- a transcript
- one active run
- prior runs in history
- approvals and deferred-input requests
- a live work surface / artifact pane

This is similar to CopilotKit's chat + sidebar + canvas composition, but implemented using:

- Go templates
- HTMX fragment swaps
- vanilla JS
- AGUI SSE

## Identity model

## 1. Thread = UX identity

A thread is the user-facing container.

It owns:

- canonical navigation identity
- transcript continuity
- run history grouping
- selected/active run
- future persisted workbench metadata

A thread can outlive any one run.

## 2. Run = execution identity

A run is one concrete execution attempt within a thread.

It owns:

- execution lifecycle
- runtime event history
- tool activity
- current completion/failure outcome

A thread can contain many runs over time.

## 3. Session = transport identity

An AGUI session is the source of truth for live transport behavior.

From `ext/agui/session.go`, the session owns:

- `session_id`
- run linkage
- replay sequencing
- replay buffer
- waiting reason
- pending approvals
- pending external input
- session status transitions

This boundary must not move into UI-only state.

## Authoritative responsibilities

## AGUI responsibilities

`ext/agui` remains authoritative for:

- live event ordering
- replay cursors and `Last-Event-ID`
- reconnect behavior
- session status transitions
- waiting/approval/deferred state
- action decoding and execution
- raw event capture and snapshot fallback

The relevant preserved backend pieces are:

- `ext/agui/session.go`
- `ext/agui/event.go`
- `ext/agui/adapter.go`
- `ext/agui/transport/sse.go`
- `ext/agui/transport/action.go`

## UI responsibilities

`pkg/ui` owns:

- HTML route surface
- thread/run page composition
- fragment templates
- browser hydration
- DOM projection of AGUI state
- user navigation between threads and runs

`pkg/ui` may derive view models, but those are projections only.

## Contract rule

If derived UI state conflicts with AGUI session/transport state, **AGUI wins**.

## What is being replaced

The existing serve UI is effectively a **run dashboard plus single-run detail page**.

Current primary surfaces:

- `/`
- `/runs/{id}`
- `/runs/{id}/sidebar`
- `/runs/{id}/events`
- `/runs/{id}/action`

This behaves like a single-run canvas/scene UI.

### Replaced in the new workbench

- `/runs/{id}` as the primary operator destination
- the assumption that one page equals one execution context
- dashboard-first navigation where the top-level action is always "start a run"
- the current tight coupling between run page and sidebar fragment

### Intentionally preserved

- run-scoped SSE
- run-scoped action POSTs
- AGUI replay/reconnect semantics
- approval/deferred-input flows
- server-rendered HTML
- HTMX fragment refreshes
- `ui:fragment-loaded`-style fragment hydration triggers

## Route contract

## Canonical page routes

The replacement UI should expose thread-oriented HTML routes:

- `GET /threads`
- `POST /threads`
- `GET /threads/{threadID}`

These become the main workbench entrypoints.

## Canonical fragment routes

The workbench should compose stable fragments such as:

- `GET /threads/{threadID}/thread-list`
- `GET /threads/{threadID}/conversation`
- `GET /threads/{threadID}/workbench`
- `GET /threads/{threadID}/approvals`
- `GET /threads/{threadID}/runs/{runID}/timeline`

These are HTML concerns and therefore belong in `pkg/ui`.

## Preserved transport routes

These remain the canonical live execution routes:

- `POST /runs/start` or thread-aware equivalent that still creates a run
- `GET /runs/{runID}/events`
- `POST /runs/{runID}/action`

Even if a thread page is the main UX, AGUI transport remains run-scoped.

## Backward-compatibility guidance

These routes may remain as compatibility or diagnostic surfaces:

- `GET /`
- `GET /runs/{id}`
- `GET /runs/{id}/sidebar`

But they are no longer the primary UX contract.

## HTMX contract

HTMX is for HTML orchestration.

### HTMX should do

- navigate into a thread workbench
- fetch and swap fragments
- submit prompt/start-run forms
- submit approval/deferred/abort forms
- refresh thread/run fragments after actions
- listen for `HX-Trigger` metadata from fragment responses

### HTMX should not do

- own replay cursors
- infer authoritative session state
- rebuild waiting/approval state from scratch
- replace AGUI's transport state machine

### Existing useful pattern to keep

`pkg/ui/handlers.go` already returns sidebar fragments with an `HX-Trigger` payload containing `ui:fragment-loaded` plus a scene snapshot.

That pattern should survive, but on workbench fragments instead of a run-only sidebar.

In other words:

- HTMX swaps HTML fragments
- JS consumes the trigger metadata to hydrate the fragment
- AGUI SSE continues to drive live state after the swap

## SSE contract

SSE is for live state, not HTML.

### Current AGUI/SSE behavior to preserve

From `ext/agui/transport/sse.go` and `ext/agui/session.go`:

- raw AGUI adapter output is captured into the session-owned normalized replay log
- `Event.Sequence` becomes the SSE `id`
- reconnect uses `Last-Event-ID` or `last_seq`
- replay gaps produce snapshot fallback rather than silent loss
- slow live clients may miss frames, but reconnect replay remains authoritative

This is the correct backend behavior and should not be replaced by ad hoc browser logic.

### Workbench usage model

The browser should open SSE for the active run only:

- select thread
- determine active run
- subscribe to `/runs/{runID}/events`
- update transcript/workbench panels from AGUI events
- on active-run switch, unsubscribe and resubscribe to the newly selected run

### SSE payloads should drive

- active status badges
- transcript streaming
- tool-call timeline updates
- approvals/deferred-input appearance
- work surface / A2UI updates
- reconnect snapshots

### SSE payloads should not drive

- HTML fragment transport
- route decisions
- AGUI action semantics

## Action contract

`ext/agui/transport/action.go` remains the execution-side action endpoint.

That means the workbench must continue to route approval, denial, deferred input, and abort through AGUI action handlers rather than inventing a UI-only control path.

### Important current behavior to preserve

`pkg/ui/handlers.go` currently rewrites incoming `session_id` to the live run session before forwarding the action.

That remains useful in the thread workbench because:

- the browser may hold stale or omitted session identity
- the server already knows which run is targeted
- the live session attached to that run is authoritative

### Contract

For workbench actions:

- page/form selection is thread- and run-aware
- final action execution remains session-aware and AGUI-owned

## Thread vs AGUI threadId

One subtle point matters for the future workbench.

`ext/agui/adapter.go` already carries an immutable `threadId` field in emitted AG-UI payloads.
Today, `pkg/ui/state.go` constructs the adapter with `agui.NewAdapter(runID)`, so AGUI `threadId` is effectively using the run ID.

### Target direction

When the UI grows a first-class thread model, adapter construction should use the real UI thread ID:

- AGUI `threadId` -> workbench thread ID
- AGUI `runId` -> concrete run ID
- AGUI session -> per-run transport identity

This aligns product semantics with the protocol without changing the transport ownership model.

### Migration rule

Do not overload session identity to simulate thread continuity.
Instead:

- thread continuity lives above AGUI transport
- session continuity remains per run
- AGUI `threadId` should eventually reflect the actual thread container

## Workbench composition contract

The workbench should be rendered as three cooperating layers.

## 1. HTML shell layer

Rendered by Go templates and HTMX fragments.

Contains:

- thread rail
- conversation column
- workbench side panel
- work surface container
- hidden hydration metadata for active thread/run/session

## 2. Hydration/controller layer

Rendered snapshot plus browser JS.

Contains:

- active thread/run selection state
- SSE subscription lifecycle
- fragment hydration from `HX-Trigger`
- DOM updates for transcript/timeline/work surface

## 3. AGUI transport layer

Owns:

- canonical event stream
- replay and snapshot recovery
- action routing
- waiting/resume lifecycle

## Detailed UX rules

## Page layout

The target thread workbench should present:

- **left rail**: threads, recent status, new thread action
- **center column**: transcript, composer, run history/timeline
- **right rail**: approvals, deferred input, current run metadata, tool activity
- **main work surface**: A2UI/artifact/canvas surface tied to the active run

This is the main CopilotKit-like replacement for the current canvas UI.

## Run selection rules

- a thread may show many historical runs
- exactly one run is active for live SSE at a time
- switching active run updates fragments and SSE subscription
- historical runs may render from server snapshots without a live stream

## Waiting/approval rules

- waiting state is derived from AGUI session state and/or AGUI-emitted events
- approval cards may be rendered inline in transcript or in the right rail
- resolution must still post through run action endpoints
- fragment refresh after action is optional convenience; the true state change comes from AGUI

## Implementation rules for `pkg/ui`

1. Keep `ext/agui` as the transport truth.
2. Add thread-level stores and views in `pkg/ui`, not `ext/agui`.
3. Do not fork approval logic from `transport/action.go`.
4. Do not fork replay logic from `transport/sse.go`.
5. Keep HTML route design separate from run transport design.
6. Prefer fragment endpoints that map cleanly to visible workbench regions.
7. Treat the current `/runs/{id}` page as a transitional diagnostic view, not the final product surface.

## Final contract summary

- **Primary UX unit:** thread
- **Primary execution unit:** run
- **Primary transport unit:** AGUI session
- **HTML mechanism:** HTMX + Go templates
- **Live state mechanism:** AGUI SSE
- **Source of truth:** `ext/agui`
- **What gets replaced:** current run-centric canvas/detail UI
- **What gets reused:** AGUI session, adapter, SSE transport, action transport, and run-scoped execution wiring
