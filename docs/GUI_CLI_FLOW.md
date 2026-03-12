# GUI <-> Gatewayd Flow (Session-First vNext)

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
- enable or disable configured channels
- create or remove bindings

The GUI does not own routing logic or runtime truth.

## Transport Policy

The long-term GUI transport is direct control-plane RPC to `gatewayd`.

Rules:

- GUI business reads and writes should use `GatewayControl` gRPC directly.
- gRPC transport should use localhost TCP, not ACP and not direct state-file mutation.
- default listen address is `127.0.0.1:58473` unless `GATEWAYD_ADDR` overrides it.
- GUI must not speak ACP directly as product behavior.
- CLI JSON commands remain migration/bootstrap tools and internal operator workflows; they are not the long-term GUI data path.
- `Action` RPC may remain a migration contract for GUI work, but typed RPCs should replace GUI-critical reads and writes over time.

## Read Model

Primary reads:

1. `cag gatewayd-status --json` without auto-start
2. `GatewayControl/Status`
3. `GatewayControl/Action(session.list)`
4. `GatewayControl/Action(session.show)`
5. `GatewayControl/Action(session.messages)`
6. `GatewayControl/Action(binding.list)`
7. `GatewayControl/Action(channel.inbox)`

Rules:

- GUI should prefer gRPC responses over CLI JSON output.
- GUI does not read state files directly as product behavior.
- GUI may use `latest` only to choose an initial selected session.
- `session messages` is the GUI read model for both persisted chat messages and in-flight session activity.
- while a send is in flight, GUI should poll `session messages` and render ACP session updates from the returned timeline before the final assistant message is persisted.
- GUI should treat ACP `sessionUpdate` kinds such as `agent_message_chunk`, `agent_thought_chunk`, `tool_call`, `tool_call_update`, `plan`, and `available_commands_update` as structured activity blocks, not as raw text to concatenate into the visible answer bubble.
- if `gatewayd` is stopped, GUI should show the stopped state and skip business gRPC reads that require a live server.

## GUI Startup Flow

The GUI should use one explicit startup sequence instead of firing unrelated refreshes with no shared trace.

Recommended sequence:

1. load bundled app config and initialize the GUI logger
2. run local component checks for `cag` and `codex-acp`
3. load config snapshot for the settings panel
4. run `cag doctor --json` with `cag health --json` as fallback for local diagnostics
5. run `cag gatewayd-status --json` without auto-start
6. if `gatewayd` is running, open the gRPC client connection
7. if `gatewayd` is running, fetch session list, binding list, and inbox through gRPC
8. if a session is selected and `gatewayd` is running, fetch `session messages` through gRPC

Rules:

- startup should log one stable `boot_id` so all startup steps can be correlated in one log scan
- each startup step should emit `step_start` and `step_done` with elapsed time and summary status
- GUI startup must not auto-start `gatewayd`
- GUI shutdown must not auto-stop `gatewayd`
- startup must not treat a failed gRPC dial as permission to auto-start the server
- channel configuration failures must be logged and shown as health warnings, but they must not block gateway service startup by themselves
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
- ACP event fanout while execution is in flight

Recommended step names:

- `component_checks`
- `config_snapshot`
- `health`
- `gatewayd_status`
- `sessions`
- `access_users`
- `selected_session_chat`

## Status Semantics

The GUI must present one top-level gateway service state.

- the only user-facing service is `gatewayd`
- any runtime or worker state is internal implementation detail behind `gatewayd`
- GUI may still probe internal state to verify readiness, but it must not expose that probe as a second peer service

Rules:

- `Running` means the gateway service is fully ready
- `Stopped` means the gateway service is fully stopped
- if internal readiness probes disagree with `gatewayd-status`, the GUI must treat the gateway as failed and trigger one gateway-level recovery attempt instead of presenting two separate service states
- the header must show one gateway badge only
- session rows may still show session-local attachment state such as `attached` or `detached`
- operator logs should make it obvious when the gateway entered an internal mismatch state and when GUI triggered recovery

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

- GUI writes must use gRPC to `gatewayd`; they must not mutate session/runtime state through direct CLI mode as product behavior
- explicit writes such as `session create`, `session clear`, `session attach`, `session detach`, `runtime restart`, and `session send` must go through `gatewayd`
- if `gatewayd` is not running when the GUI performs a normal write, the write must fail with an actionable gateway-stopped error
- GUI `Restart` is the only operator action that may start or restart `gatewayd`
- GUI `Restart` should stop the current control-plane process when present, start a fresh one, and therefore truncate `gatewayd.log` before new lines are written

### Binding Operations

- list bindings
  - `cag binding list --json`
- bind a channel conversation
  - `cag session bind --key <session_key> --channel <name> --conversation-id <id> --json`
  - `cag binding create --channel <name> --conversation-id <id> --session-key <session_key> --json`
- unbind a channel conversation
  - `cag session unbind --key <session_key> --channel <name> --conversation-id <id> --json`
  - `cag binding delete --channel <name> --conversation-id <id> --json`

### Channel Availability Operations

- list channel availability
  - `cag channel list --json`
- enable one configured channel ingress
  - `cag channel enable --channel <name> --json`
- disable one configured channel ingress
  - `cag channel disable --channel <name> --json`

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
- the processing state should progressively load ACP activity from `session messages.timeline` so the assistant bubble updates during execution instead of waiting for the final persisted assistant message
- the assistant bubble should render one unified output that may contain:
  - streamed assistant text
  - grouped activity rows for ACP `tool_call` / `tool_call_update`
  - grouped thought or planning rows for ACP `agent_thought_chunk` / `plan`
  - command or skill availability rows for ACP `available_commands_update`
- GUI should group repeated updates for the same tool or activity key and show the latest status in-place instead of printing every update as flat raw text
- when an assistant reply is complete and visible in the chat panel, the bubble should show a lightweight copy affordance so the operator can copy the final assistant text with one click
- the wait budget for `session send` must align with the runtime's resolved `AGENT_TIMEOUT_SEC`; GUI/client-side waiting must not expire earlier because of a separate hard-coded default
- send progress is scoped to the target session only; one session's in-flight send must not block another session's chat UI
- the chat input should support session-local sent-message history navigation; only user-sent messages are eligible history items, assistant or system replies must never be selected by `Up` / `Down`
- pressing `Up` walks older user-sent messages for the selected session, while `Down` walks back toward newer user messages and finally restores the draft/input state that existed before history navigation started

### Channel Inbox Panel

Shows:

- channel conversations that currently have no binding
- last message preview
- channel metadata

Actions:

- bind to existing session
- create session and bind
- ignore or archive later if needed
- jump to channel availability controls when ingress should be paused globally

### Bindings Panel

Shows:

- all current conversation-to-session bindings
- filters by channel or session

Actions:

- remove binding
- jump to session
- jump to conversation detail

### Channel Settings Panel

Shows:

- configured channels
- whether each configured channel is enabled or disabled

Actions:

- enable configured channel ingress
- disable configured channel ingress

## Routing UX Rules

- GUI-selected session is explicit write target.
- `latest` is only a convenience for initial selection after refresh.
- Unassigned channel conversations are visible but must not execute.
- Disabled channels remain configured and visible, but new ingress must not execute until re-enabled.
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
- New GUI business reads and writes should target `gatewayd` gRPC instead of CLI stdout parsing.
- Do not interpret stderr as business result.
- Parse one JSON object per command invocation.

## Migration Note

During the Cobra transition:

- GUI reads and writes should migrate toward:
  - `GatewayControl/Status`
  - `GatewayControl/Action(session.*)`
  - `GatewayControl/Action(channel.*)`
  - `GatewayControl/Action(binding.*)`
  - `GatewayControl/Action(runtime.*)`
- GUI bootstrap and operator actions may temporarily continue to call hidden/internal commands such as:
  - `cag doctor`
  - `cag gatewayd-status`
  - `cag gatewayd-up`
  - `cag gatewayd-down`
  - `cag users`
  - `cag user-allow`
  - `cag user-block`

These internal commands are migration support only and should not receive new product semantics.

Explicit operator actions may still start `gatewayd`, but passive GUI startup and refresh must not mutate control-plane state so opening the app does not start the server.

## Non-Goals

- no heuristic auto-merge by sender/thread
- no default write to latest session
- no hidden recovery path that creates or reroutes a session silently
