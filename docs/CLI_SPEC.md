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
- `config`
- `config --global [--gatewayd-addr <addr>]`
- `status [--json]`
- `gatewayd [--listen <addr>]`
- `gatewayd-up [--json]`
- `gatewayd-down [--json]`
- `health [--json]`
- `send (--to <id> | --session-key <key>) (--text <msg> | --file <path>) [--msgtype text|markdown] [--channel <name>] [--message-id <id>] [--report-file <path>] [--dry-run] [--workdir <path>] [--json]`
- `sessions [--limit <n>] [--json]`
- `messages --session-key <key> [--json]`
- `session-new --session-key <key> --workdir <path> [--json]`
- `session-clear --session-key <key> [--json]`
- `session-delete --session-key <key> [--json]`
- `sessions-delete-all [--json]`
- `actions`
- `help`

## Global behavior

- Repository root resolution:
  - If current dir is `src/`, use parent as repo root.
  - Else prefer current dir when `.env` exists.
  - Else use parent when parent has `.env`.
- Config source precedence:
  - 1) process env
  - 2) repo `.env`
  - 3) user global `~/.cag/.env`
  - 4) built-in defaults
- Repo `.env` is optional; effective config is resolved from user global `~/.cag/.env` plus built-in defaults when repo `.env` is absent.
- `run` does not accept positional workdir arg.
- `status/start/stop/restart/health/doctor/sessions/send(--session-key)/messages/session-*` 仅通过 gRPC 控制面访问 `gatewayd`。
- CLI 会在 gRPC 调用前自动确保 `gatewayd` 在线（必要时自动拉起）；若控制面不可用则直接报错，不做本地业务回退，也不保留兼容路径。

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
  "lock_file": "/abs/path/gateway.lock",
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

Plain output:

- `status`（非 `--json`）必须打印当前 `lock` 与 `log` 路径。
- 当 `running=true` 时，`status` 额外打印最近几行运行日志，作为快速诊断视图。
- 默认当前日志文件是可见文件名：`~/.cag/gatewayd/gatewayd.log`。
- `gatewayd` 与其管理的 gateway runtime 默认共享这一份日志文件，供 GUI 展示“最新日志”。

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
  - stream ingress always requires `DINGTALK_APP_KEY` and `DINGTALK_APP_SECRET`
  - `webhook` send mode requires `DINGTALK_BOT_WEBHOOK`
  - `api` send mode additionally requires `DINGTALK_AGENT_ID`

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
- `raw_output` (`string`, optional; agent raw output, 不做空格/格式重写)
- `result_json` (`object|array|scalar`, optional; `raw_output` 可解析 JSON 时返回)
- `terminal_reason` (`string`, optional; `completed|timeout|idle_after_chunk|error|cancelled|dry_run|...`)
- `elapsed_sec` (`number`, optional; session-path execution elapsed)

Semantics:

- `ok=false` pairs with process exit code `1`.
- `message_id` is auto-generated when `--message-id` is absent.

## Command-specific flags

### `config`

- `config`: 写仓库级 `.env`（默认运行目录固定为 `~/.cag`）。
- `config --global`: 写用户级 `~/.cag/.env`（默认写入 `GATEWAYD_ADDR=127.0.0.1:58473`）。
- `config --global --gatewayd-addr <addr>`: 覆盖用户级 `GATEWAYD_ADDR`。
- 默认运行态文件（lock/state/db/reports/interactions）位于 `~/.cag/runtime/repos/<repo-id>/`。

### `start`

- `--json`: print `status` payload after start attempt.
- `--log-file`: override log output file path for background runtime.
  - Precedence: `--log-file` > `GATEWAY_LOG_FILE` > default `~/.cag/gatewayd/gatewayd.log`.

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

- For `dingtalk`, omitting `--to` requires `DINGTALK_DEFAULT_TO_USER` to be configured.
- `send --session-key` 的 `workdir` 优先级：
  1) 显式 `--workdir`
  2) 已保存的 `session metadata.workdir`（由 `session-new` 或历史执行写入）
  3) 若仍为空：自动初始化并使用 `~/.cag/workspace/default`

### `gatewayd`

- `--listen`: gRPC 监听地址（默认读取 `GATEWAYD_ADDR`；若未设置则使用 `127.0.0.1:58473`）。
- 当前开放 RPC：
  - `Status`
  - `Start`
  - `Stop`
  - `Restart`
  - `Health`
  - `Doctor`
  - `Sessions`
  - `SessionNew`
  - `SendToSession`
  - `SessionMessages`
  - `ClearSession`
  - `DeleteSession`
  - `DeleteAllSessions`

### 会话一致性约束

- GUI 依赖的会话读写命令（`sessions/messages/send --session-key/session-*`）必须在 `gatewayd` 运行时执行。
- 若 `gatewayd` 不可达，CLI 返回非 0，并在 JSON 中输出 `error.code=gateway_unreachable`。
- 当前属于开发阶段：控制面与配置行为允许按设计直接收敛，不要求保留旧 fallback 或 backward-compatibility 语义。

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

### `sessions --json`

`items[]` 在原字段基础上新增：

- `workdir` (`string`, optional)
- `updated_at` (`string`, optional, RFC3339 UTC)
- `status` (`string`, optional)

### `session-new --json`

Output object:

```json
{
  "ok": true,
  "action": "session-new",
  "session_key": "sess_xxx",
  "session_id": "optional",
  "workdir": "/abs/path",
  "updated_at": "2026-03-06T03:26:39Z",
  "status": "ready"
}
```

Semantics:

- 幂等：同一 `session_key` 重复执行不会创建重复记录。
- `--workdir` 必填；缺失时返回 `error.code=workdir_required`。

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
