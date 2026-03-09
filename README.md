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
cd src && go run ./cmd/gateway-cli config
cd src && go run ./cmd/gateway-cli config --global
cd src && go run ./cmd/gateway-cli config set POLL_INTERVAL_SEC 3
cd src && go run ./cmd/gateway-cli config set GATEWAYD_ADDR 127.0.0.1:58473 --global
```

2. 启动网关：

```bash
make run
```

启动成功后，网关会通过当前 channel 主动发送一条问候消息，说明自己已经上线并简要自我介绍。

3. 查看状态：

```bash
cd src && go run ./cmd/gateway-cli status
```

纯文本 `status` 会直接输出当前 lock 与运行日志路径；当网关处于运行中时，还会附带最近几行运行日志，便于快速诊断。默认控制面与 runtime 共享同一个当前日志文件：`~/.cag/gatewayd/gatewayd.log`。GUI 展示“最新日志”时应读取这一路径。

## CLI

```bash
cd src && go run ./cmd/gateway-cli actions
cd src && go run ./cmd/gateway-cli run
cd src && go run ./cmd/gateway-cli start
cd src && go run ./cmd/gateway-cli stop
cd src && go run ./cmd/gateway-cli restart
cd src && go run ./cmd/gateway-cli config
cd src && go run ./cmd/gateway-cli config --global --gatewayd-addr 127.0.0.1:58473
cd src && go run ./cmd/gateway-cli config list
cd src && go run ./cmd/gateway-cli config get POLL_INTERVAL_SEC
cd src && go run ./cmd/gateway-cli config set POLL_INTERVAL_SEC 3
cd src && go run ./cmd/gateway-cli config set GATEWAYD_ADDR 127.0.0.1:58473 --global
cd src && go run ./cmd/gateway-cli status
cd src && go run ./cmd/gateway-cli status --json
cd src && go run ./cmd/gateway-cli health
cd src && go run ./cmd/gateway-cli health --json
cd src && go run ./cmd/gateway-cli doctor --json
cd src && go run ./cmd/gateway-cli gatewayd --listen 127.0.0.1:58473
cd src && go run ./cmd/gateway-cli gatewayd-up --json
cd src && go run ./cmd/gateway-cli gatewayd-down --json
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
- CLI 在 gRPC 调用前会自动确保 `gatewayd` 在线（必要时自动拉起）；若控制面不可用或状态异常，则直接报错，不做本地业务回退，也不保留兼容分支。
- 可显式执行 `gatewayd-up` / `gatewayd-down` 管理控制面进程。
- 地址通过 `GATEWAYD_ADDR` 控制（默认 `127.0.0.1:58473`）。

### 配置收敛规则

- 仓库级 `.env` 现在只保留启动必需项。
- `~/.cag/.env` 保留用户级控制面配置，以及 GUI/全局共享的 DingTalk 配置。
- 轮询间隔、超时、回复风格、DingTalk 行为等运行期可调项统一通过 `cag config set` 落到 SQLite。
- 旧 `.env` 中的运行期键在执行 `cag config <workdir>` 时会自动迁移到 SQLite，并从 `.env` 清除。
- 旧 repo `.env` 中的 GUI DingTalk 启动键会在执行 `cag config <workdir>` 时自动迁移到 `~/.cag/.env`。
- GUI 会话使用的 workdir 不再保存在 `.env`，而是挂在 session metadata 上。
- `send --session-key` 在未显式设置 workdir 且会话 metadata 为空时，会自动初始化并使用 `~/.cag/workspace/default`。

## DingTalk（已实现）

`CHANNEL_TYPE=dingtalk` 时：
- 入站：Go 内置 DingTalk Stream（仅此模式）
- 出站：
  - `DINGTALK_SEND_MODE=api`：走企业应用 API（`DINGTALK_AGENT_ID`；同时 stream 本身要求 `DINGTALK_APP_KEY/SECRET`）
  - `DINGTALK_SEND_MODE=webhook`：走机器人 webhook（`DINGTALK_BOT_WEBHOOK`）
- 直聊鉴权：
  - `DINGTALK_DM_POLICY=allow_all`：收到即处理
  - `DINGTALK_DM_POLICY=allowlist`：未知用户先记入状态库并收到一条“待管理员在 GUI Access Requests 中批准”的提示；管理员可在 GUI 中 Allow/Block，后续消息按库内状态处理

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
