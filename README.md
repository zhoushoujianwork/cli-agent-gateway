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

## macOS 桌面启动器（Phase 6）

可一键生成可点击启动的 macOS `.app`：

```bash
make create-macos-launcher
```

默认会在 `~/Desktop` 生成 `CLI Agent Gateway.app`，点击后会打开 Terminal 并执行网关启动命令。

也可按需定制：

```bash
./scripts/create_macos_launcher.sh \
  --output-dir "$HOME/Applications" \
  --app-name "CLI Agent Gateway (Prod)" \
  --workdir "/path/to/your/workdir"
```

如果你要“真正桌面 GUI（非 Terminal）”版本，可构建原生 SwiftUI App：

```bash
make build-macos-gui-app
```

默认输出：`~/Desktop/CLI Agent Gateway GUI.app`
构建时会先尝试关闭旧版 GUI App，成功后自动打开新版 App。

该 GUI App 提供按钮：
- 顶部主操作区：`Start` / `Stop` / `Restart` / `Open Logs`（紧邻运行状态）
- `Config` 页面：统一修改配置（含 `Channel` 切换）与 `Health Board` 检查/修复
- `Sessions` 面板：按会话展示最近聊天信息（来自 `state.json + interactions.jsonl`）
- 支持 `Delete Selected` / `Delete All` 删除会话映射
- 点击某个会话后，下方 `Chat` 区会展示该会话的问答历史（用户提问 + 助手回复）
- 内置会话命令提醒：`/new` 开新会话，`/clear` 清空当前会话映射
- 点击助手回复气泡可查看 `AI Process Detail` 时间线（ACP 阶段、工具调用、完成/错误事件）

聊天命令（CAG 网关内置）：
- `/new`：清空当前会话映射并开启新会话
- `/new <你的问题>`：开启新会话并立刻以该问题继续
- `/clear`：清空当前会话映射（下一条消息会新建会话）

可选参数（直接调用脚本）：
- `--no-kill-old`：构建前不关闭旧 App
- `--no-open`：构建后不自动打开
- `--icon-svg /path/to/logo.svg`：用 SVG 自动生成 macOS 图标并写入 `.app`

默认 SVG 图标文件：
- `macos/CLIApp/Assets/AppLogo.svg`

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

- 依赖安装：`pip install dingtalk-stream`
- 启用方式：`.env` 里设置 `CHANNEL_TYPE=dingtalk`（或显式覆盖 `SMS_FETCH_CMD/SMS_SEND_CMD`）。
- 多用户模式：默认 `ALLOWED_FROM` 为空即不限制发送者；会按 `channel+sender+thread` 隔离会话，并回复到各自发送者。
- 用户信息透传：会把 DingTalk 的 `senderStaffId/senderName/conversationId` 等写入 `TaskRequest.metadata` 与交互日志。
- 调试开关：设置 `DINGTALK_DEBUG_USER_PROFILE=1` 可在网关日志里打印“当前说话人画像”。
- 当前模式（仅 Stream）：
  - 入站：网关进程内置 `dingtalk-stream` 监听（无需 callback server / queue 文件）；
  - 出站：`src/channels/dingtalk.py send` 默认走应用 API，webhook 可作为系统通知模式。
  - 出站消息默认使用 `markdown` 并按 `ack/progress/final/error` 渲染状态样式（`DINGTALK_SEND_MSGTYPE` 支持 `text|markdown|card`）。
  - 可开启 `RECOVER_UNFINISHED_TASKS_AS_FAILED=1`：服务重启后会把未完成任务自动标记失败并回发。
  - 默认群聊不要求 `@机器人`（`DINGTALK_REQUIRE_MENTION_IN_GROUP=0`）。
  - 默认开启 `DINGTALK_STREAM_DEBUG=1`，会打印每条原始 stream 回调与过滤原因日志。

## 回复格式策略

- 默认会向 agent 注入 iMessage 友好回复规范（短句、少格式、先结论）。
- 可通过 `.env` 控制：
  - `REPLY_STYLE_ENABLED=1|0`
  - `REPLY_STYLE_PROMPT=...`
  - `SHOW_TOOL_TRACE=1|0`（是否在最终用户回复里附加工具使用摘要）
- 无论 `SHOW_TOOL_TRACE` 是否开启，服务端日志与交互日志都会记录工具调用轨迹（用于排障与审计）。

## ACP 超时与重试（建议）

- `ACP_INITIALIZE_TIMEOUT_SEC`：`initialize` 超时（默认 `30`）
- `ACP_SESSION_NEW_TIMEOUT_SEC`：`session/new` 超时（默认 `90`）
- `ACP_SESSION_NEW_RETRIES`：`session/new` 超时重试次数（默认 `2`）
- `ACP_SESSION_NEW_RETRY_BACKOFF_SEC`：重试退避秒数（默认 `1`，指数退避）
- `ACP_PROMPT_RECOVER_RETRIES`：`session/prompt` 可恢复错误重试次数（默认 `2`）
- DingTalk 用户侧失败提示默认不回传内部异常细节；详细错误仅写入服务端日志与交互日志。

## 全链路 Debug（问题排查）

- `DEBUG_TRACE_CHAIN=1`：开启每条消息的全链路 trace（入站、鉴权、会话路由、ACP 执行、回包、错误）
- `DEBUG_ACP_EVENTS=1`：输出 ACP 行为事件（initialize/session/new/session/prompt/恢复重试/session/update 摘要）
- `DEBUG_ACP_LOG_CHUNKS=1`：输出高频 `agent_message_chunk` 事件（默认关闭，避免刷屏）
- `DEBUG_ACP_EVENT_PAYLOAD_CHARS=280`：调试字段截断长度，避免日志过大
- `TOOL_PROGRESS_NOTIFY_ENABLED=1`：每次工具调用开始/结束时，给用户推送进度消息
- `SHOW_TOOL_TRACE=1`：是否在用户最终回复中展示工具摘要（不影响服务端日志）
- 结构化调试事件会写入交互日志：`INTERACTION_LOG_FILE`（默认 `logs/interactions.jsonl`）

## 目录

- `src/app/main.py`: 入口
- `src/core/loop.py`: 网关主循环
- `src/agents/acp_stdio_agent.py`: ACP 执行适配器
- `src/infra/jsonrpc_stdio.py`: JSON-RPC stdio 传输
- `docs/ARCHITECTURE.md`: 架构说明
