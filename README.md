# cli-agent-gateway (Go, ACP-first)

一个 Go 实现的 ACP-first 网关：
- 上游通道负责收发消息。
- 执行层统一走 ACP（JSON-RPC over stdio）。
- 支持会话复用、权限请求桥接、任务报告与交互日志持久化。

## 当前通道状态

- `dingtalk`：已实现（Go 原生）。
- `command`：已实现（Go 原生）。
- `imessage`：TODO（占位，未实现）。

## 快速启动

1. 生成/更新配置：

```bash
cd src && go run ./cmd/gateway-cli config /path/to/your/workdir
```

2. 启动网关：

```bash
make run
```

3. 查看状态：

```bash
cd src && go run ./cmd/gateway-cli status
```

## CLI

```bash
cd src && go run ./cmd/gateway-cli actions
cd src && go run ./cmd/gateway-cli run
cd src && go run ./cmd/gateway-cli start
cd src && go run ./cmd/gateway-cli stop
cd src && go run ./cmd/gateway-cli restart
cd src && go run ./cmd/gateway-cli config /path/to/your/workdir
cd src && go run ./cmd/gateway-cli status
cd src && go run ./cmd/gateway-cli status --json
cd src && go run ./cmd/gateway-cli health
cd src && go run ./cmd/gateway-cli health --json
cd src && go run ./cmd/gateway-cli send --to tester --text "hello" --json
```

## DingTalk（已实现）

`CHANNEL_TYPE=dingtalk` 时：
- 入站：读取 `DINGTALK_QUEUE_FILE`（JSONL）
- 出站：
  - `DINGTALK_SEND_MODE=api`：走企业应用 API（`DINGTALK_APP_KEY/SECRET/AGENT_ID`）
  - `DINGTALK_SEND_MODE=webhook`：走机器人 webhook（`DINGTALK_BOT_WEBHOOK`）

入站 JSONL 每行示例：

```json
{"messageId":"m1","senderStaffId":"u123","text":"hello","createAt":"2026-03-05T00:00:00Z","conversationId":"cid123","chatType":"group","isAtBot":true}
```

## 存储

- `sqlite`（默认）：状态/交互/报告元数据写 SQLite，并继续输出报告 JSON 文件。
- `localfile`：状态/交互/报告写 JSON/JSONL。

## 目录

- `src/cmd/gateway-cli/main.go`：CLI 入口
- `src/internal/core/`：网关主循环
- `src/internal/agents/acp/`：ACP JSON-RPC stdio 适配
- `src/internal/channels/dingtalk/`：DingTalk 通道
- `src/internal/channels/command/`：command 通道
- `src/internal/channels/imessage/`：iMessage TODO 占位
- `src/internal/storage/`：`localfile` / `sqlite` 后端
- `docs/GO_BACKEND_ARCHITECTURE.md`：Go 后端架构方案
- `docs/CLI_SPEC.md`：CLI 命令与 JSON 合约（冻结）
