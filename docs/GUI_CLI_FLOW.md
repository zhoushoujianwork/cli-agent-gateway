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

1. `cag runtime status --json`
2. `cag session list --json`
3. `cag session show --key <session_key> --json`
4. `cag session messages --key <session_key> --json`
5. `cag binding list --json`
6. `cag channel inbox --json`

Rules:

- GUI uses machine-readable CLI output only.
- GUI does not read state files directly as product behavior.
- GUI may use `latest` only to choose an initial selected session.

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
- send message
  - `cag session send --key <session_key> --text "<text>" --json`

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
- latest activity time

Actions:

- create
- delete one session from the session row context menu
- delete all listed sessions from the sessions toolbar only after explicit confirmation
- clear
- attach
- detach
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
  - `cag gatewayd-up`
  - `cag gatewayd-down`
  - `cag users`
  - `cag user-allow`
  - `cag user-block`

These internal commands are migration support only and should not receive new product semantics.

## Non-Goals

- no heuristic auto-merge by sender/thread
- no default write to latest session
- no hidden recovery path that creates or reroutes a session silently
