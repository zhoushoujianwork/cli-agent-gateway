# ACP Gateway Roadmap (Go-first)

## R0: CLI Contract Freeze

- 定义并冻结 CLI 命令与 JSON 输出协议：
  - `run/start/stop/restart/status/health`
  - `config`
- 约定统一退出码与错误码（timeout/agent_error/channel_error/config_error）。
- 明确日志与状态文件路径规范。

## R1: Go Gateway Core

- 实现 Go 版 gateway core（去重、鉴权、会话路由、进度通知）。
- 实现 JSON-RPC stdio 客户端与 ACP adapter。
- 实现单实例锁、状态持久化、交互日志、报告落盘。

## R2: Channel Implementations

- 先交付 command channel（最小可运行链路）。
- 再交付 DingTalk channel（stream + send）。
- iMessage 作为后续扩展项。

## R3: CLI-first Operations

- CLI 作为唯一控制面入口：
  - 进程控制、配置管理、健康检查。
- GUI 不再直接读写关键流程逻辑，只消费 CLI。

## R4: macOS GUI Integration

- GUI 对接 CLI 协议完成全功能管理：
  - 启停重启、状态展示、日志打开、会话发送。
- 聊天窗口支持消息发送状态（sending/sent/failed）与本地动作命令（`/clear`、`/new`）。

## R5: Release

- 发布 Go-first 新版本（不以 Python 兼容为发布门槛）。
- 提供迁移说明（安装、命令、配置变化）。
- 建立回归测试与 smoke 检查清单。

## R6: gRPC Control Plane (GoDo)

- 新增 `gatewayd` gRPC 服务，承载控制面与可观测接口：
  - lifecycle: start/stop/restart/status
  - health/doctor
  - sessions/log tail
- `cag` 作为 gRPC 客户端（保留本地直连 fallback，渐进迁移）。
- GUI 改为优先调用 gRPC，避免文件锁元数据轮询。
- 数据面仍保留 ACP stdio，后续评估统一传输层。

## Branch Goal (codex/feat/init-go-cli-actions)

- Step 1 complete: `gateway-cli run` now uses the Go-native gateway loop (legacy bridge removed).
- Step 2 complete: `gateway-cli config` now writes `.env` via Go-native flow (no Python bridge).
- Step 3 complete: `start/stop/restart/health` added with dashboard-friendly JSON output.
- Next: complete iMessage adapter and wire GUI controls fully to CLI actions.

### Next focus (Go core hardening)

1. ACP error model
- Introduce structured ACP error kinds: `timeout`, `transport`, `protocol`.
- Keep method context in errors to improve diagnostics and GUI display.

2. Reliability tests
- Add integration tests for CLI control flow and lock semantics.
- Add smoke tests for dedup/session routing and restart behavior.

3. Storage hardening
- Make sqlite backend production defaults explicit (`WAL`, `busy_timeout`, migration hooks).
- Add schema version checks for future evolutions.

4. Observability and contracts
- Freeze JSON contract for `status --json` and `health --json`.
- Add basic metrics/log conventions for operational dashboards.
