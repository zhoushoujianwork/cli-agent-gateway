# CLI Spec Preview (Cobra Transition)

This document is the vNext command preview.

It defines the target Cobra command tree, command purpose, and migration policy.

Status:

- design target for the current refactor
- grouped commands are the only user-facing product surface
- flat compatibility commands are legacy and should be sunset
- internal control-plane commands may still exist during migration
- control-plane config lives under `~/.cag`
- `session.workdir` is explicit per session

## Command Tree

```text
cag config

cag session create
cag session delete
cag session list
cag session show
cag session send
cag session messages
cag session clear
cag session attach
cag session detach

cag channel list
cag channel inbox
cag channel show

cag binding create
cag binding delete
cag binding list
cag binding show

cag runtime status
cag runtime ps
cag runtime logs
```

## Root-Level Policy

The Cobra root should expose only these user-facing top-level commands:

- `cag config`
- `cag session`
- `cag channel`
- `cag binding`
- `cag runtime`

These five commands are the stable product surface.

Other commands fall into migration-only categories:

- `internal` commands
  - used by GUI bootstrap, gatewayd lifecycle, or operator workflows
  - may remain callable during migration
  - should be hidden from normal help output
- `legacy` commands
  - compatibility shims for old flat names
  - must not gain new product behavior
  - should print actionable migration guidance

Recommended hidden/internal command set during migration:

- `cag doctor`
- `cag health`
- `cag gatewayd`
- `cag gatewayd-up`
- `cag gatewayd-down`
- `cag users`
- `cag user-allow`
- `cag user-block`
- `cag run`
- `cag start`
- `cag stop`
- `cag restart`

Recommended legacy compatibility set during migration:

- `cag send`
- `cag sessions`
- `cag messages`
- `cag session-new`
- `cag session-clear`
- `cag session-delete`
- `cag sessions-delete-all`
- `cag actions`

## Command Registry

Each command must keep four annotations:

- `path`
- `purpose`
- `feature_group`
- `sunset_group`

These annotations exist so an obsolete command family can be removed in one pass.

## Control-Plane Rules

- grouped commands talk to one user-scoped `gatewayd`
- command RPCs must not carry `repo_root`
- control-plane state/log/config live under `~/.cag`
- a repo checkout may be the shell cwd, but it is not a control-plane identity
- hidden/internal commands should not be described as the primary product workflow

## Session Commands

### `cag session create`

- `path`: `session create`
- `purpose`: create a new task session
- `feature_group`: `session-lifecycle`
- `sunset_group`: `session-v2`
- inputs:
  - `--key <session_key>`
  - `--workdir <abs-or-normalized-path>`
- result:
  - creates session metadata
  - does not require a channel binding yet
  - `--workdir` should be normalized before the request is sent

### `cag session delete`

- `path`: `session delete`
- `purpose`: delete a task session
- `feature_group`: `session-lifecycle`
- `sunset_group`: `session-v2`
- inputs:
  - `--key <session_key>`
- result:
  - removes the session
  - removes or invalidates related bindings
  - stops its runtime if attached

### `cag session list`

- `path`: `session list`
- `purpose`: list sessions for GUI and CLI
- `feature_group`: `session-read-model`
- `sunset_group`: `session-v2`
- result:
  - summary rows for all sessions
  - may mark one row as `latest` for UI default selection only

### `cag session show`

- `path`: `session show`
- `purpose`: inspect one session
- `feature_group`: `session-read-model`
- `sunset_group`: `session-v2`
- inputs:
  - `--key <session_key>`
- result:
  - workdir
  - status
  - runtime status
  - recent activity
  - current bindings

### `cag session send`

- `path`: `session send`
- `purpose`: send one message to one explicit session
- `feature_group`: `session-io`
- `sunset_group`: `session-v2`
- inputs:
  - `--key <session_key>`
  - exactly one of `--text` or `--file`
- rules:
  - must not default to `latest`
  - must not infer session from channel metadata

### `cag session messages`

- `path`: `session messages`
- `purpose`: read message history for one session
- `feature_group`: `session-read-model`
- `sunset_group`: `session-v2`
- inputs:
  - `--key <session_key>`

### `cag session clear`

- `path`: `session clear`
- `purpose`: reset the live context of one session while keeping the session identity
- `feature_group`: `session-runtime`
- `sunset_group`: `session-v2`
- inputs:
  - `--key <session_key>`

### `cag session attach`

- `path`: `session attach`
- `purpose`: attach or start a live runtime for one session
- `feature_group`: `session-runtime`
- `sunset_group`: `session-v2`
- inputs:
  - `--key <session_key>`

### `cag session detach`

- `path`: `session detach`
- `purpose`: detach the live runtime from one session while keeping metadata and history
- `feature_group`: `session-runtime`
- `sunset_group`: `session-v2`
- inputs:
  - `--key <session_key>`

## Channel Commands

### `cag channel list`

- `path`: `channel list`
- `purpose`: list supported channel types and configured channel backends
- `feature_group`: `channel-read-model`
- `sunset_group`: `channel-v2`

### `cag channel inbox`

- `path`: `channel inbox`
- `purpose`: list unassigned channel conversations
- `feature_group`: `channel-routing`
- `sunset_group`: `channel-v2`
- rules:
  - inbox items are visible but not executable until bound

### `cag channel show`

- `path`: `channel show`
- `purpose`: inspect one channel conversation
- `feature_group`: `channel-read-model`
- `sunset_group`: `channel-v2`
- inputs:
  - `--channel <name>`
  - `--conversation-id <id>`
  - optional `--thread-id <id>`

## Binding Commands

### `cag binding create`

- `path`: `binding create`
- `purpose`: bind one channel conversation to one session
- `feature_group`: `binding-routing`
- `sunset_group`: `binding-v2`
- inputs:
  - `--channel <name>`
  - `--conversation-id <id>`
  - optional `--thread-id <id>`
  - `--session-key <session_key>`

### `cag binding delete`

- `path`: `binding delete`
- `purpose`: remove an explicit conversation-to-session binding
- `feature_group`: `binding-routing`
- `sunset_group`: `binding-v2`
- inputs:
  - `--channel <name>`
  - `--conversation-id <id>`
  - optional `--thread-id <id>`

### `cag binding list`

- `path`: `binding list`
- `purpose`: list all bindings
- `feature_group`: `binding-read-model`
- `sunset_group`: `binding-v2`
- filters:
  - optional `--session-key`
  - optional `--channel`

### `cag binding show`

- `path`: `binding show`
- `purpose`: inspect one binding
- `feature_group`: `binding-read-model`
- `sunset_group`: `binding-v2`
- inputs:
  - `--channel <name>`
  - `--conversation-id <id>`
  - optional `--thread-id <id>`

## Runtime Commands

### `cag runtime status`

- `path`: `runtime status`
- `purpose`: inspect global runtime health
- `feature_group`: `runtime-ops`
- `sunset_group`: `runtime-v2`

### `cag runtime ps`

- `path`: `runtime ps`
- `purpose`: list currently attached session runtimes
- `feature_group`: `runtime-ops`
- `sunset_group`: `runtime-v2`

### `cag runtime logs`

- `path`: `runtime logs`
- `purpose`: inspect runtime logs globally or for one session
- `feature_group`: `runtime-ops`
- `sunset_group`: `runtime-v2`
- inputs:
  - optional `--key <session_key>`
  - optional `--follow`

## Routing Contract

- `session send` must always target an explicit `--key`.
- `channel inbox` items are not auto-routed.
- binding is the only write-routing rule for channel ingress.
- `latest` is read-model only.

## Legacy Surface

Legacy flat commands include:

- `sessions`
- `messages`
- `session-new`
- `session-clear`
- `session-delete`
- `sessions-delete-all`
- `send --session-key`

Rules:

- keep them stable only as needed during migration
- do not add new product semantics there
- all new product behavior must land in grouped commands
- Cobra help should de-emphasize or hide them
