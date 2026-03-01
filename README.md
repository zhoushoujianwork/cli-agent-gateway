# cli-agent-gateway (ACP-first)

一个面向渠道网关的 ACP 驱动项目：
- 上游聊天通道只负责收发消息。
- 执行层统一走 ACP（JSON-RPC over stdio）。
- 支持会话复用、权限请求桥接、任务报告落盘。

## 当前实现

- `CommandChannelAdapter`：通过 `SMS_FETCH_CMD` / `SMS_SEND_CMD` 对接任意聊天入口。
- 内置 iMessage adapter：`src/cli_agent_gateway/channels/imessage_fetch.py`、`imessage_send.py`。
- `ACPStdioAgentAdapter`：通过 `ACP_AGENT_CMD` 对接 ACP agent（例如 `codex-acp`）。
- 主循环：去重、鉴权、session 路由、进度回包、报告与交互日志持久化。

## 快速启动

1. 复制 `.env.example` 到 `.env` 并填入实际值。
2. 确保 `ACP_AGENT_CMD` 可执行（例如已经安装 `codex-acp`）。
3. 启动：

```bash
PYTHONPATH=src python3 -m cli_agent_gateway.app.main /path/to/your/workdir
```

## iMessage 接入

1. 在 `.env` 配置 `IMSG_CHAT_ID`、`IMSG_SEND_CHAT_ID`、`REMOTE_USER_ID`。
2. 确保 `imsg` 已安装且终端有 Full Disk Access（可读取 `~/Library/Messages/chat.db`）。
3. 默认会自动使用内置 iMessage fetch/send 脚本，无需额外配置 `SMS_FETCH_CMD/SMS_SEND_CMD`。

## 目录

- `src/cli_agent_gateway/app/main.py`: 入口
- `src/cli_agent_gateway/core/loop.py`: 网关主循环
- `src/cli_agent_gateway/agents/acp_stdio_agent.py`: ACP 执行适配器
- `src/cli_agent_gateway/infra/jsonrpc_stdio.py`: JSON-RPC stdio 传输
- `docs/ARCHITECTURE.md`: 架构说明
