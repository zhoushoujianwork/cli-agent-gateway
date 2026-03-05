# cli-agent-gateway (Go, ACP-first)

一个 Go 实现的 ACP-first 网关：
- 上游通道负责收发消息。
- 执行层统一走 ACP（JSON-RPC over stdio）。
- 支持会话复用、权限请求桥接、任务报告与交互日志持久化。

## vNext 发布方向（Go-first）

- 新版本后端由 Go 实现并作为主线运行时。
- 控制面统一收敛到 CLI（配置、启停、状态、诊断）。
- macOS GUI 通过 CLI 管理网关生命周期。

详细设计见：
- `docs/ARCHITECTURE.md`
- `docs/ROADMAP.md`
- `docs/GO_BACKEND_ARCHITECTURE.md`

## 当前通道状态

- `dingtalk`：已实现（Go 原生）。
- `command`：已实现（Go 原生）。
- `imessage`：已实现最小 Go 通道（基于 `imsg` 命令）。

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
cd src && go run ./cmd/gateway-cli doctor --json
cd src && go run ./cmd/gateway-cli gatewayd --listen 127.0.0.1:58473
cd src && go run ./cmd/gateway-cli send --to tester --text "hello" --json
cd src && go run ./cmd/gateway-cli send --session-key sess_xxx --text "hello from gui" --json
cd src && go run ./cmd/gateway-cli messages --session-key sess_xxx --json
cd src && go run ./cmd/gateway-cli session-clear --session-key sess_xxx --json
cd src && go run ./cmd/gateway-cli session-delete --session-key sess_xxx --json
cd src && go run ./cmd/gateway-cli sessions-delete-all --json
cd src && go run ./cmd/gateway-cli send --to tester --file ./message.md --msgtype markdown --dry-run --json
```

### gRPC 控制面（阶段二进行中）

- `gatewayd` 提供控制面 gRPC 服务（当前已开放 `Status/Start/Stop/Restart`、`Health/Doctor`、`Sessions`、`SendToSession`、`SessionMessages`、`Clear/Delete`）。
- CLI 控制面命令仅通过 gRPC 访问 `gatewayd`（`status/start/stop/restart/health/doctor/sessions/messages/send --session-key/session-*`）。
- `gatewayd` 不可达时直接报错（`error.code=gateway_unreachable`），不做本地回退/自动拉起。
- 地址通过 `GATEWAYD_ADDR` 控制（默认 `127.0.0.1:58473`）。
- 可通过 `CAG_GRPC_DISABLE=1` 强制禁用 gRPC 路径。

## DingTalk（已实现）

`CHANNEL_TYPE=dingtalk` 时：
- 入站：Go 内置 DingTalk Stream（仅此模式）
- 出站：
  - `DINGTALK_SEND_MODE=api`：走企业应用 API（`DINGTALK_APP_KEY/SECRET/AGENT_ID`）
  - `DINGTALK_SEND_MODE=webhook`：走机器人 webhook（`DINGTALK_BOT_WEBHOOK`）

## 处理链路与职责

统一链路：

1. Channel（DingTalk/command/imessage/GUI）接收消息，标准化为 `InboundMessage`。
2. Gateway Loop 执行管理行为（去重、鉴权、会话路由、`/clear`、ack/final 发送策略）。
3. 普通内容请求转发给 ACP Adapter（`initialize/session/new/session/prompt`）。
4. ACP 返回结果后，Gateway 组装最终回复并回发到 Channel。
5. 全过程写入存储（state/session_map、interaction trace、report JSON）。

职责边界：

- `src/internal/channels/*`：只做通道收发与通道字段适配。
- `src/internal/core/loop.go`：统一编排、会话管理、回发策略、可观测日志。
- `src/internal/agents/acp/*`：只做 ACP 协议交互。
- `src/internal/storage/*`：状态与日志落库。

## 主程序可观测日志

`cag run` 的 stderr 会输出主链路日志，便于定位卡点。常见阶段：

- `fetch ok`
- `inbound accepted`
- `session resolved`
- `send ack ok` / `send ack failed`
- `execute start`
- `execute done` / `execute failed`
- `send final ok` / `send final failed`
- `persist done`

ACP 调试日志默认开启（`CAG_GO_DEBUG` 默认视为开启）；如需关闭可设 `CAG_GO_DEBUG=0`。

## 存储

- `sqlite`（默认）：状态/交互/报告元数据写 SQLite，并继续输出报告 JSON 文件。
- `localfile`：状态/交互/报告写 JSON/JSONL。

## 目录

- `src/cmd/gateway-cli/main.go`：CLI 入口
- `src/internal/core/`：网关主循环
- `src/internal/agents/acp/`：ACP JSON-RPC stdio 适配
- `src/internal/channels/dingtalk/`：DingTalk 通道
- `src/internal/channels/command/`：command 通道
- `src/internal/channels/imessage/`：iMessage 通道（命令适配）
- `src/internal/storage/`：`localfile` / `sqlite` 后端
- `src/proto/gateway/v1/`：gRPC 控制面 proto
- `src/internal/gen/gatewayv1/`：gRPC 生成代码
- `docs/GO_BACKEND_ARCHITECTURE.md`：Go 后端架构方案
- `docs/CLI_SPEC.md`：CLI 命令与 JSON 合约（冻结）
