# CLI Spec (Go Runtime)

This document freezes the external CLI contract for `cag` (gateway-cli) used by GUI and automation.

## Version

- Spec version: `v1`
- Scope: Go runtime only (`src/cmd/gateway-cli`)
- Compatibility rule: additive changes only for `--json` outputs. Existing keys and meanings must not be broken.

## Commands

- `run`
- `start`
- `stop`
- `restart`
- `config [workdir]`
- `status [--json]`
- `gatewayd [--listen <addr>]`
- `health [--json]`
- `send (--to <id> | --session-key <key>) (--text <msg> | --file <path>) [--msgtype text|markdown] [--channel <name>] [--message-id <id>] [--report-file <path>] [--dry-run] [--json]`
- `sessions [--limit <n>] [--json]`
- `messages --session-key <key> [--json]`
- `session-clear --session-key <key> [--json]`
- `session-delete --session-key <key> [--json]`
- `sessions-delete-all [--json]`
- `actions`
- `help`

## Global behavior

- Repository root resolution:
  - If current dir is `src/`, use parent as repo root.
  - Else prefer current dir when `.env` exists.
  - Else fallback to parent when parent has `.env`.
- Missing `.env` for runtime commands (`run`, `start`, `send`) is fatal.
- `run` does not accept positional workdir arg.
- `sessions/send(--session-key)/messages/session-*` 仅通过 gRPC 控制面访问 `gatewayd`，连接失败时会自动尝试拉起 `gatewayd` 后重试一次。
- `start/stop/restart/health/doctor` 优先通过 gRPC 控制面调用 `gatewayd`；不可达时回退本地实现。
- `status` 保持 gRPC 优先，不可达时回退本地实现。

## Exit codes

- `0`: success
- `1`: runtime/validation failure
- `2`: CLI usage error (unknown action, missing required arg, parse failure)

## JSON contract

### `status --json`

Output object:

```json
{
  "running": true,
  "pid": 12345,
  "started_at": "2026-03-05T04:54:08Z",
  "lock_file": "/abs/path/.cli_agent_gateway.lock",
  "metadata": {
    "channel": "dingtalk",
    "workdir": "/abs/path"
  }
}
```

Field rules:

- `running` (`bool`, required)
- `lock_file` (`string`, required, absolute path)
- `pid` (`number`, optional)
- `started_at` (`string`, optional, RFC3339 UTC)
- `metadata` (`object`, optional, extra runtime metadata)

Semantics:

- `running=true` means lock is currently held by an active process.
- `running=false` may still include historical `metadata` from prior runs.

### `health --json`

Output object:

```json
{
  "ok": true,
  "channel": "dingtalk",
  "items": [
    {"key":"env","ok":true,"detail":".env loaded"},
    {"key":"acp","ok":true,"detail":"acp command ready: codex-acp"}
  ]
}
```

Field rules:

- `ok` (`bool`, required): aggregate of all `items[].ok`
- `channel` (`string`, optional)
- `items` (`array`, required, non-empty on normal execution)
- `items[].key` (`string`, required)
- `items[].ok` (`bool`, required)
- `items[].detail` (`string`, required)

Channel-specific checks:

- `imessage`: checks `imsg` in `PATH`
- `dingtalk`:
  - ingress is fixed to DingTalk Stream
  - `webhook` mode requires `DINGTALK_BOT_WEBHOOK`
  - `api` mode checks `DINGTALK_APP_KEY/APP_SECRET/AGENT_ID`

### `send --json`

Output object:

```json
{
  "ok": true,
  "channel": "command",
  "to": "tester",
  "message_id": "manual-1772688340592",
  "msg_type": "text",
  "dry_run": false,
  "source": "text",
  "error": ""
}
```

Field rules:

- `ok` (`bool`, required)
- `channel` (`string`, required)
- `to` (`string`, required)
- `message_id` (`string`, required)
- `msg_type` (`string`, required; `text` or `markdown`)
- `dry_run` (`bool`, required)
- `source` (`string`, required; `text` or `file`)
- `error` (`string`, optional; present on failure)
- `session_key` (`string`, optional; present when using `--session-key`)
- `session_id` (`string`, optional)
- `result` (`string`, optional; agent summary for session-path send)
- `elapsed_sec` (`number`, optional; session-path execution elapsed)

Semantics:

- `ok=false` pairs with process exit code `1`.
- `message_id` is auto-generated when `--message-id` is absent.

## Command-specific flags

### `start`

- `--json`: print `status` payload after start attempt.
- `--log-file`: override log output file path for background runtime.
  - Precedence: `--log-file` > `GATEWAY_LOG_FILE` > default `.agent_gateway.log`.

### `stop`

- `--json`: print `status` payload after stop attempt.
- `--quiet`: suppress normal output (used internally by `restart`).

### `restart`

- Supports `--json` and `--log-file` (forwarded to `start`).
- Must emit a single JSON payload when `--json` is set.

### `send`

Required:

- `--to` 或 `--session-key`（二选一，`--session-key` 用于 GUI/会话内执行）
- exactly one source: `--text` or `--file`

Optional:

- `--msgtype` (`text` default, `markdown` supported)
- `--channel`
- `--message-id`
- `--report-file`
- `--dry-run` (validate + emit result, skip real send)
- `--json`

Defaulting:

- For `dingtalk`, `--to` can fallback to `DINGTALK_DEFAULT_TO_USER`.

### `gatewayd`

- `--listen`: gRPC 监听地址（默认读取 `GATEWAYD_ADDR`，再回退 `127.0.0.1:58473`）。
- 当前开放 RPC：
  - `Status`
  - `Start`
  - `Stop`
  - `Restart`
  - `Health`
  - `Doctor`
  - `Sessions`
  - `SendToSession`
  - `SessionMessages`
  - `ClearSession`
  - `DeleteSession`
  - `DeleteAllSessions`

### 会话一致性约束

- GUI 依赖的会话读写命令（`sessions/messages/send --session-key/session-*`）必须在 `gatewayd` 运行时执行。
- 若 `gatewayd` 未运行，CLI 会先尝试自动拉起；仍不可达时返回非 0，并在 JSON 中输出 `error.code=gateway_unreachable`。

### `messages --json`

Output object:

```json
{
  "ok": true,
  "action": "messages",
  "session_key": "sess_xxx",
  "messages": [],
  "timeline": []
}
```

Field rules:

- `ok` (`bool`, required)
- `action` (`string`, required, fixed `messages`)
- `session_key` (`string`, required)
- `messages` (`array`, required)
- `timeline` (`array`, required)

### `session-clear` / `session-delete` / `sessions-delete-all --json`

Output object:

```json
{
  "ok": true,
  "action": "session-clear",
  "session_key": "sess_xxx"
}
```

Field rules:

- `ok` (`bool`, required)
- `action` (`string`, required)
- `session_key` (`string`, optional for `sessions-delete-all`)

## Compatibility policy

- Do not rename or remove existing JSON fields in v1.
- New fields must be optional.
- Existing exit code meanings are frozen.
- `actions` output remains line-based action names (no JSON requirement in v1).

## Validation

Current CI/local contract checks are implemented in:

- `src/cmd/gateway-cli/contract_integration_test.go`

This test is the executable contract for v1.
