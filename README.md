# cli-agent-gateway (ACP-first)

一个面向渠道网关的 ACP 驱动项目：
- 上游聊天通道只负责收发消息。
- 执行层统一走 ACP（JSON-RPC over stdio）。
- 支持会话复用、权限请求桥接、任务报告落盘。

## 当前实现

- `CommandChannelAdapter`：通过 `SMS_FETCH_CMD` / `SMS_SEND_CMD` 对接任意聊天入口。
- 内置 iMessage adapter：`src/channels/imessage.py`（单文件，含 fetch/send 子命令）。
- `ACPStdioAgentAdapter`：通过 `ACP_AGENT_CMD` 对接 ACP agent（例如 `codex-acp`）。
- 主循环：去重、鉴权、session 路由、进度回包、报告与交互日志持久化。

## 快速启动

1. 复制 `.env.example` 到 `.env` 并填入实际值（可选）。
   - 用 `CHANNEL_TYPE=imessage|dingtalk` 选择通道；
   - 默认会按通道自动选择内置 fetch/send 脚本；
   - 如需自定义可继续覆盖 `SMS_FETCH_CMD` / `SMS_SEND_CMD`。
2. 确保 `ACP_AGENT_CMD` 可执行（例如已经安装 `codex-acp`）。
3. 启动：

```bash
PYTHONPATH=src python3 -m app.main /path/to/your/workdir
```

或使用快捷命令：

```bash
make run
```

单实例保护：
- 启动时会抢占 `LOCK_FILE`（默认 `.cli_agent_gateway.lock`）；
- 若已有实例运行，新进程会直接退出并打印已运行实例的 PID/启动时间；
- 可执行 `make status` 查看当前运行状态。

如果 `.env` 缺失，程序会自动进入交互式初始化：
- 默认只询问必要项（例如 iMessage 的手机号/AppleID/Chat ID）；
- 其余配置自动探测并自动填充；
- 选择项使用方向键（↑/↓ 或 ←/→）切换，回车确认；
- 输入 `-` 可暂时跳过当前项；
- 如需细项调整，可进入“高级配置”；
- 完成后会自动写入 `.env` 并继续启动。

如需在 `.env` 已存在时重新进入配置页面并修改已有值，可执行：

```bash
make config
```

## iMessage 接入

1. 在 `.env` 配置 `IMSG_CHAT_ID`、`IMSG_SEND_CHAT_ID`、`REMOTE_USER_ID`。
2. 确保 `imsg` 已安装且终端有 Full Disk Access（可读取 `~/Library/Messages/chat.db`）。
3. 设置 `CHANNEL_TYPE=imessage` 时，默认会自动使用内置 iMessage fetch/send 脚本，无需额外配置 `SMS_FETCH_CMD/SMS_SEND_CMD`。

## DingTalk 接入（MVP）

- 启用方式：`.env` 里设置 `CHANNEL_TYPE=dingtalk`（或显式覆盖 `SMS_FETCH_CMD/SMS_SEND_CMD`）。
- 多用户模式：默认 `ALLOWED_FROM` 为空即不限制发送者；会按 `channel+sender+thread` 隔离会话，并回复到各自发送者。
- 已提供：
  - `src/channels/dingtalk.py`
  - （同文件内含 `fetch` / `send` / `callback-server` 子命令）
- 当前模式：
  - fetch 从 `DINGTALK_QUEUE_FILE` 读取 JSONL 入站队列；
  - callback server 接收钉钉回调并写入队列（双向入站）；
  - send 默认走应用 API（双向主链路），webhook 可作为系统通知模式。

## 回复格式策略

- 默认会向 agent 注入 iMessage 友好回复规范（短句、少格式、先结论）。
- 可通过 `.env` 控制：
  - `REPLY_STYLE_ENABLED=1|0`
  - `REPLY_STYLE_PROMPT=...`

## 目录

- `src/app/main.py`: 入口
- `src/core/loop.py`: 网关主循环
- `src/agents/acp_stdio_agent.py`: ACP 执行适配器
- `src/infra/jsonrpc_stdio.py`: JSON-RPC stdio 传输
- `docs/ARCHITECTURE.md`: 架构说明
