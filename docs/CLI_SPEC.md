# Gateway CLI Spec (vNext / Go-first)

本文定义 `gateway` CLI 的稳定契约，供 macOS GUI 与自动化脚本调用。

## 设计原则

- 默认人类可读输出；加 `--json` 返回机器可读结构。
- 所有错误都包含 `error.code` 与 `error.message`。
- 所有命令都返回明确退出码。
- GUI 仅依赖本文档，不依赖内部文件格式细节。

## 全局参数

- `--json`: 输出 JSON（单个对象，不输出多行日志）
- `--timeout <sec>`: 覆盖本次请求超时（可选）

## 命令清单

### 1) `gateway start`

启动网关服务进程（单实例）。

JSON 成功：

```json
{
  "ok": true,
  "action": "start",
  "status": "running",
  "pid": 12345
}
```

### 2) `gateway stop`

停止网关服务进程。

JSON 成功：

```json
{
  "ok": true,
  "action": "stop",
  "status": "stopped",
  "stopped_pids": [12345]
}
```

### 3) `gateway restart`

重启网关服务进程。

JSON 成功：

```json
{
  "ok": true,
  "action": "restart",
  "status": "running",
  "pid": 23456
}
```

### 4) `gateway status`

查询运行状态。

JSON：

```json
{
  "ok": true,
  "action": "status",
  "status": "running",
  "pid": 23456,
  "channel": "dingtalk",
  "workdir": "/abs/path",
  "lock_file": "/abs/path/.cli_agent_gateway.lock",
  "state_file": "/abs/path/logs/state.json",
  "interaction_log_file": "/abs/path/logs/interactions.jsonl"
}
```

若未运行：

```json
{
  "ok": true,
  "action": "status",
  "status": "stopped"
}
```

### 5) `gateway send`

向指定会话发送一条消息（供 GUI 聊天窗口调用）。

参数：

- `--channel <name>` 必填
- `--sender <id>` 必填
- `--thread-id <id>` 可选
- `--session-key <key>` 可选
- `--session-id <id>` 可选
- `--text <message>` 必填

JSON 成功：

```json
{
  "ok": true,
  "action": "send",
  "status": "sent",
  "summary": "assistant final summary",
  "session_key": "sess_xxx",
  "session_id": "019c...",
  "elapsed_sec": 12
}
```

JSON 失败：

```json
{
  "ok": false,
  "action": "send",
  "status": "failed",
  "error": {
    "code": "agent_error",
    "message": "session/prompt failed to deserialize response"
  }
}
```

### 6) `gateway view sessions`

读取会话列表（GUI 左侧列表数据源）。

参数：

- `--limit <n>` 可选（默认 200）

JSON：

```json
{
  "ok": true,
  "action": "view_sessions",
  "items": [
    {
      "session_key": "sess_abc",
      "session_id": "019c...",
      "channel": "dingtalk",
      "sender": "2907...",
      "thread_id": "cid...",
      "last_message": "hello",
      "last_time": "2026-03-05T10:30:00Z",
      "unread": 0
    }
  ]
}
```

### 7) `gateway view messages`

读取某个会话的消息列表（GUI 右侧聊天窗口数据源）。

参数：

- `--session-key <key>` 必填
- `--limit <n>` 可选（默认 200）

JSON：

```json
{
  "ok": true,
  "action": "view_messages",
  "session_key": "sess_abc",
  "items": [
    {
      "message_id": "msg_001",
      "role": "user",
      "text": "hello",
      "time": "2026-03-05T10:30:00Z",
      "status": "sent",
      "error_code": "",
      "error_message": ""
    },
    {
      "message_id": "msg_002",
      "role": "assistant",
      "text": "hi",
      "time": "2026-03-05T10:30:03Z",
      "status": "sent",
      "error_code": "",
      "error_message": ""
    }
  ]
}
```

`status` 允许值：

- `sending`
- `sent`
- `failed`
- `action`（系统动作消息，如 `/clear`、`/new`）

### 8) `gateway view tasks`

读取当前任务状态（可选，用于任务看板）。

JSON：

```json
{
  "ok": true,
  "action": "view_tasks",
  "items": [
    {
      "task_id": "msg_xxx",
      "session_key": "sess_abc",
      "status": "running",
      "progress": "",
      "started_at": "2026-03-05T10:30:00Z",
      "last_update": "2026-03-05T10:30:05Z"
    }
  ]
}
```

### 9) `gateway session clear`

清空会话映射（动作命令，对应 GUI 输入 `/clear`）。

参数：

- `--session-key <key>` 必填

JSON：

```json
{
  "ok": true,
  "action": "session_clear",
  "session_key": "sess_abc",
  "status": "cleared"
}
```

### 10) `gateway session new`

开启新会话（动作命令，对应 GUI 输入 `/new`）。

参数：

- `--session-key <key>` 必填
- `--text <message>` 可选（等价于 `/new xxx`）

JSON：

```json
{
  "ok": true,
  "action": "session_new",
  "session_key": "sess_abc",
  "status": "created",
  "sent": false
}
```

### 11) `gateway session delete`

删除会话（用于会话列表删除动作）。

参数：

- `--session-key <key>` 必填

JSON：

```json
{
  "ok": true,
  "action": "session_delete",
  "session_key": "sess_abc",
  "status": "deleted"
}
```

### 12) `gateway config get`

读取配置项。

参数：

- `--key <name>` 可选；不传则返回全部可见配置

JSON：

```json
{
  "ok": true,
  "action": "config_get",
  "values": {
    "CHANNEL_TYPE": "dingtalk",
    "ACP_AGENT_CMD": "codex-acp"
  }
}
```

### 13) `gateway config set`

写入配置项。

参数：

- `--key <name>` 必填
- `--value <value>` 必填

### 14) `gateway doctor`

扩展诊断（`health` + 路径可写性检查，适合 GUI 启动页）。

JSON：

```json
{
  "ok": true,
  "action": "doctor",
  "status": "healthy",
  "channel": "command",
  "items": [
    { "key": "env", "ok": true, "detail": ".env loaded" },
    { "key": "acp", "ok": true, "detail": "acp command ready: codex-acp" },
    { "key": "paths.lock_file", "ok": true, "detail": "writable: /abs/path" }
  ]
}
```

JSON：

```json
{
  "ok": true,
  "action": "config_set",
  "key": "CHANNEL_TYPE",
  "value": "dingtalk"
}
```

### 14) `gateway config validate`

校验配置完整性与格式。

JSON：

```json
{
  "ok": true,
  "action": "config_validate",
  "valid": true,
  "errors": []
}
```

### 15) `gateway doctor`

环境与依赖检查（agent、channel runtime、文件权限等）。

JSON：

```json
{
  "ok": true,
  "action": "doctor",
  "checks": [
    {"id": "acp_cmd", "ok": true, "detail": "codex-acp found"},
    {"id": "lock_file_rw", "ok": true, "detail": "writable"}
  ]
}
```

## 错误码（`error.code`）

- `timeout`
- `agent_error`
- `channel_error`
- `config_error`
- `validation_error`
- `lock_error`
- `io_error`
- `internal_error`

## 退出码规范

- `0`: 成功
- `2`: 参数错误/校验失败
- `3`: 配置错误
- `4`: 运行时依赖缺失（doctor/check 失败）
- `5`: 锁冲突/实例状态错误
- `6`: 通道发送或接收错误
- `7`: ACP/Agent 错误
- `124`: 超时
- `1`: 其他未分类错误

## GUI 集成约束

- GUI 必须始终使用 `--json` 调 CLI。
- CLI 在 `--json` 模式下，`stdout` 只能输出单个 JSON 对象。
- `stderr` 仅用于调试信息，GUI 不得用 `stderr` 作为业务判断依据。
- GUI 以 `ok + status + error.code` 决定消息状态：
  - `sending` -> 调用中
  - `sent` -> `ok=true && status=sent`
  - `failed` -> `ok=false` 或非 0 退出码
- GUI 读状态统一走 `view` 子命令，不直接读取原始状态文件。
- `/clear`、`/new` 等动作命令通过 CLI session 子命令执行，不直接写状态文件。
