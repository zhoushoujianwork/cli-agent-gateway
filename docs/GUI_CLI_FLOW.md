# GUI <-> CLI Flow (Session-First vNext)

This document defines the target GUI behavior for the new session-first design.

## GUI Role

The GUI is a session manager and operator console.

It should help the user:

- create and delete sessions
- select the active session
- set session workdir
- inspect session history
- inspect runtime state
- inspect channel inbox
- create or remove bindings

The GUI does not own routing logic or runtime truth.

## Read Model

Primary reads:

1. `cag gatewayd-status --json`
2. `cag session list --json`
3. `cag session show --key <session_key> --json`
4. `cag session messages --key <session_key> --json`
5. `cag binding list --json`
6. `cag channel inbox --json`

Rules:

- GUI uses machine-readable CLI output only.
- GUI does not read state files directly as product behavior.
- GUI may use `latest` only to choose an initial selected session.

## GUI Startup Flow

The GUI should use one explicit startup sequence instead of firing unrelated refreshes with no shared trace.

Recommended sequence:

1. load bundled app config and initialize the GUI logger
2. run local component checks for `cag` and `codex-acp`
3. load config snapshot for the settings panel
4. run `cag doctor --json` with `cag health --json` as fallback in direct CLI mode
5. run `cag gatewayd-status --json` in direct CLI mode
6. run `cag session list --json` in direct CLI mode
7. run `cag binding list --json` in direct CLI mode
8. run `cag channel inbox --json` or user/access reads in direct CLI mode if the current GUI screen needs them
9. if a session is selected, run `cag session messages --key <session_key> --json` in direct CLI mode

Rules:

- startup should log one stable `boot_id` so all startup steps can be correlated in one log scan
- each startup step should emit `step_start` and `step_done` with elapsed time and summary status
- GUI startup must not auto-start `gatewayd`
- GUI shutdown must not auto-stop `gatewayd`
- startup may continue after a non-fatal step failure so the GUI can still show partial state
- startup completion should emit one summary line with overall outcome such as `ready`, `degraded`, or `blocked`

## GUI Startup Logging

The GUI log should be operator-readable without reconstructing app behavior from low-level shell timing lines.

Required log events:

- `startup phase=begin`
- `startup phase=step_start step=<name>`
- `startup phase=step_done step=<name> ok=<true|false> elapsed_ms=<n>`
- `startup phase=complete outcome=<ready|degraded|blocked> elapsed_ms=<n>`

Required write-path log events:

- GUI log:
  - `event=send phase=start session_key=<key> message_id=<id>`
  - `event=send phase=done session_key=<key> message_id=<id> ok=<true|false> elapsed_ms=<n>`
- `gatewayd` log:
  - use standard Go logger output with timestamped `INFO` / `ERROR` lines
  - every control-plane line must include `component=gatewayd`
  - `event=action phase=start action=<action>`
  - `event=action_timeline phase=<step> action=<action> elapsed_ms=<n>`
  - `event=action phase=done action=<action> ok=<true|false> elapsed_ms=<n>`
  - `event=action phase=error action=<action> elapsed_ms=<n> error=<message>`

- runtime / CLI execution log:
  - use the same standard Go logger output with timestamped `INFO` / `ERROR` lines
  - every worker/runtime line must include `component=cli`
  - ACP adapter debug lines should include `component=acp`

For `session send`, both GUI and `gatewayd` logs should include:

- `session_key`
- `message_id`
- `source`
- a short text preview so operators can correlate a user send like `123` with the backend trace

For `gatewayd` timeline steps, at minimum log:

- request accepted
- session loaded
- user message persisted
- runtime execute started
- runtime execute finished or failed
- state persisted

Recommended step names:

- `component_checks`
- `config_snapshot`
- `health`
- `gatewayd_status`
- `sessions`
- `access_users`
- `selected_session_chat`

## Status Semantics

The GUI must present one top-level gateway service state, not two peer daemon states.

- `gatewayd-status` reports the health of the managed `gatewayd` daemon
- that daemon includes both the external gRPC/control-plane surface and the core runtime management responsibilities
- session rows report per-session runtime attachment such as `attached` or `detached`
- `runtime status` is a deeper diagnostic view into runtime-related state owned by `gatewayd`; it should not be presented as a second independent service badge

Rules:

- the header should present one `gatewayd` service badge for top-level daemon health
- the GUI must not imply that `gatewayd` and runtime are separate peer processes that can be independently healthy at the top level
- a selected session may still show its own runtime attachment state such as `attached` or `detached`
- operator logs should make it obvious which state changed

## Write Model

### Session Operations

- create session
  - `cag session create --key <generated_key> --workdir <path> --json`
- delete session
  - `cag session delete --key <session_key> --json`
- clear session context
  - `cag session clear --key <session_key> --json`
- attach runtime
  - `cag session attach --key <session_key> --json`
- detach runtime
  - `cag session detach --key <session_key> --json`
- restart runtime
  - `cag runtime restart --session-key <session_key> --json`
- send message
  - `cag session send --key <session_key> --text "<text>" --json`

Rules:

- passive reads may use direct CLI mode
- GUI writes must not mutate session/runtime state in direct CLI mode as product behavior
- explicit writes such as `session create`, `session clear`, `session attach`, `session detach`, `runtime restart`, and `session send` must go through `gatewayd`
- if `gatewayd` is not running when the GUI performs a write, the CLI layer may start it first, but the resulting write must still be observable in `gatewayd` logs
- GUI `Restart` is an operator action that force-restarts `gatewayd`; it should stop the current control-plane process, start a fresh one, and therefore truncate `gatewayd.log` before new lines are written

### Binding Operations

- list bindings
  - `cag binding list --json`
- bind a channel conversation
  - `cag binding create --channel <name> --conversation-id <id> --session-key <session_key> --json`
- unbind a channel conversation
  - `cag binding delete --channel <name> --conversation-id <id> --json`

### Channel Inbox Operations

- list unassigned conversations
  - `cag channel inbox --json`
- inspect one conversation
  - `cag channel show --channel <name> --conversation-id <id> --json`

## GUI Panels

### Sessions Panel

Shows:

- session key
- title or last message summary
- workdir
- runtime status
- whether the session already has explicit channel bindings
- latest activity time

Actions:

- create
- delete one session from the session row context menu
- delete all listed sessions from the sessions toolbar only after explicit confirmation
- open a bind flow from the session row context menu to attach one explicit unassigned conversation
- clear
- attach
- detach
- restart runtime
- select

### Chat Panel

Shows:

- message history for the selected session
- send box targeting the selected session only
- runtime status for the selected session
- a transient processing indicator while `cag session send --json` is still waiting on the agent reply

Rules:

- if no session is selected, send must be disabled
- send must never auto-route to `latest`
- while a send is in flight, the GUI should keep a visible local processing state until the assistant reply is persisted
- the wait budget for `session send` must align with the runtime's resolved `AGENT_TIMEOUT_SEC`; GUI/client-side waiting must not expire earlier because of a separate hard-coded default
- send progress is scoped to the target session only; one session's in-flight send must not block another session's chat UI

### Channel Inbox Panel

Shows:

- channel conversations that currently have no binding
- last message preview
- channel metadata

Actions:

- bind to existing session
- create session and bind
- ignore or archive later if needed

### Bindings Panel

Shows:

- all current conversation-to-session bindings
- filters by channel or session

Actions:

- remove binding
- jump to session
- jump to conversation detail

## Routing UX Rules

- GUI-selected session is explicit write target.
- `latest` is only a convenience for initial selection after refresh.
- Unassigned channel conversations are visible but must not execute.
- Binding from the GUI must always be explicit: choose a concrete unassigned conversation, then choose the target session.
- The GUI should clearly show whether a conversation is:
  - bound
  - unassigned
  - attached to a live runtime

## Command Design Constraints For GUI

- Use grouped commands only.
- Do not build new GUI behavior on top of legacy flat commands.
- GUI may still use hidden/internal control-plane commands during migration only.
- Hidden/internal commands should be treated as operator APIs, not as end-user navigation.
- Do not interpret stderr as business result.
- Parse one JSON object per command invocation.

## Migration Note

During the Cobra transition:

- GUI reads and writes should continue to prefer:
  - `cag session ...`
  - `cag channel ...`
  - `cag binding ...`
  - `cag runtime ...`
- GUI bootstrap and operator actions may temporarily continue to call hidden/internal commands such as:
  - `cag doctor`
  - `cag gatewayd-status`
  - `cag gatewayd-up`
  - `cag gatewayd-down`
  - `cag users`
  - `cag user-allow`
  - `cag user-block`

These internal commands are migration support only and should not receive new product semantics.

Explicit operator actions may still start `gatewayd`, but passive GUI startup and refresh should stay in direct CLI mode so opening the app does not mutate control-plane state.

## Non-Goals

- no heuristic auto-merge by sender/thread
- no default write to latest session
- no hidden recovery path that creates or reroutes a session silently
