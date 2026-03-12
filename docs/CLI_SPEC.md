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
cag session bind
cag session unbind
cag session send
cag session messages
cag session clear
cag session attach
cag session detach

cag channel list
cag channel enable
cag channel disable
cag channel inbox
cag channel show

cag binding create
cag binding delete
cag binding list
cag binding show

cag runtime status
cag runtime ps
cag runtime restart
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
- `cag gatewayd-status`
- `cag gatewayd-up`
- `cag gatewayd-down`
- `cag users`
- `cag user-allow`
- `cag user-block`
- `cag run`
- `cag start`
- `cag stop`
- `cag restart`

Internal operator note:

- `path`: `gatewayd-status`
- `purpose`: inspect managed gateway daemon state without auto-starting it
- `feature_group`: `control-plane-ops`
- `sunset_group`: `gatewayd`

GUI migration note:

- long-term GUI communication should use `GatewayControl` gRPC directly
- hidden/internal CLI commands may still be used for daemon lifecycle or diagnostics during migration
- GUI business reads and writes should not depend on CLI stdout parsing as the steady-state product path

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
- `gatewayd` is the managed daemon that exposes gRPC/operator APIs and hosts runtime management
- command RPCs must not carry `repo_root`
- control-plane state/log/config live under `~/.cag`
- a repo checkout may be the shell cwd, but it is not a control-plane identity
- hidden/internal commands should not be described as the primary product workflow
- `gatewayd-status` is a read-only internal operator command and must not auto-start `gatewayd`
- writes routed through grouped commands should be observable in `gatewayd` logs with action, target session, and elapsed time

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

### `cag session bind`

- `path`: `session bind`
- `purpose`: bind one channel conversation to one explicit session
- `feature_group`: `session-routing`
- `sunset_group`: `session-v2`
- inputs:
  - `--key <session_key>`
  - `--channel <name>`
  - `--conversation-id <id>`
  - optional `--thread-id <id>`
- rules:
  - must fail if the target session does not exist or is archived
  - must remove the conversation from the unassigned inbox if present

### `cag session unbind`

- `path`: `session unbind`
- `purpose`: remove one explicit binding from one session
- `feature_group`: `session-routing`
- `sunset_group`: `session-v2`
- inputs:
  - `--key <session_key>`
  - `--channel <name>`
  - `--conversation-id <id>`
  - optional `--thread-id <id>`
- rules:
  - must fail if the binding does not belong to the specified session
  - the conversation should return to the unassigned inbox after unbind

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
  - client wait timeout for `session send` must derive from the same resolved `AGENT_TIMEOUT_SEC` used by the target runtime, with a small transport buffer; it must not use an unrelated fixed default

### `cag session messages`

- `path`: `session messages`
- `purpose`: read message history for one session
- `feature_group`: `session-read-model`
- `sunset_group`: `session-v2`
- inputs:
  - `--key <session_key>`
- rules:
  - response must include persisted session messages and timeline items for the same session
  - timeline items must be available while `session send` is still running so GUI can render progressive assistant activity
  - timeline events may carry structured ACP activity metadata such as update kind, status, title, detail, and optional raw payload preview

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
- rules:
  - if the configured ACP agent command is unavailable, attach must fail with an actionable error

### `cag session detach`

- `path`: `session detach`
- `purpose`: detach the live runtime from one session while keeping metadata and history
- `feature_group`: `session-runtime`
- `sunset_group`: `session-v2`
- inputs:
  - `--key <session_key>`

### `cag runtime restart`

- `path`: `runtime restart`
- `purpose`: restart the live ACP runtime for one explicit session
- `feature_group`: `runtime-ops`
- `sunset_group`: `runtime-v2`
- inputs:
  - `--session-key <session_key>`
- rules:
  - must not infer a target session from channel metadata or `latest`
  - should stop the existing runtime if present, then attach a fresh runtime for the same session

## Channel Commands

### `cag channel list`

- `path`: `channel list`
- `purpose`: list supported channel types and configured channel backends
- `feature_group`: `channel-read-model`
- `sunset_group`: `channel-v2`
- result:
  - each row includes whether the channel is `configured`
  - each row includes whether the channel is currently `enabled`
  - manageable channels may be configured but temporarily disabled without removing config

### `cag channel enable`

- `path`: `channel enable`
- `purpose`: re-enable a configured channel ingress without removing its config
- `feature_group`: `channel-routing`
- `sunset_group`: `channel-v2`
- inputs:
  - `--channel <name>`
- rules:
  - only configured non-GUI channel backends may be enabled
  - enabling restores channel ingress processing for that channel

### `cag channel disable`

- `path`: `channel disable`
- `purpose`: pause a configured channel ingress while keeping its config
- `feature_group`: `channel-routing`
- `sunset_group`: `channel-v2`
- inputs:
  - `--channel <name>`
- rules:
  - only configured non-GUI channel backends may be disabled
  - disabling must block new channel ingress execution until re-enabled

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
- note:
  - `cag session bind` is the session-oriented equivalent and should be preferred when the operator starts from a concrete session

### `cag binding delete`

- `path`: `binding delete`
- `purpose`: remove an explicit conversation-to-session binding
- `feature_group`: `binding-routing`
- `sunset_group`: `binding-v2`
- inputs:
  - `--channel <name>`
  - `--conversation-id <id>`
  - optional `--thread-id <id>`
- note:
  - `cag session unbind` is the session-oriented equivalent and may additionally assert the expected target session

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
- `purpose`: inspect runtime diagnostics owned by `gatewayd`, not a separate top-level daemon
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
- configured channel backends may be temporarily paused via `channel disable` and resumed via `channel enable`.
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
