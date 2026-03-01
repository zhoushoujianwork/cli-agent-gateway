# 新用户配置手册（iMessage）

本文档面向第一次使用 `sms-codex-loop` 的用户，目标是把配置门槛降到最低。

## 1. 如何激活 iMessage 功能

在 macOS 上完成以下步骤：

1. 打开 `Messages` 并登录 Apple ID。
2. 在 `Messages > Settings > iMessage` 中确认 iMessage 已启用。
3. 允许终端访问消息数据库：
   - `System Settings > Privacy & Security > Full Disk Access`
   - 给你实际使用的终端（Terminal / iTerm）以及 `imsg` 工具授权。
4. 验证 `imsg` 可用：

```bash
imsg chats --json | head -n 3
```

如果能看到 JSON 行输出，说明 iMessage 通道可读。

## 2. 自动配置流程（推荐）

> 目标：尽量不让用户手动改 `.env`。

### 2.a 无需手动配置（自动发现）

运行自动配置脚本：

```bash
cd /path/to/sms-codex-loop
./scripts/autoconfig_imessage.sh --cloud-id cloudx@example.com
```

脚本会：
- 生成一次性配对码
- 等待你在手机端发送该配对码
- 自动识别正确会话
- 自动写入 `.env`（`IMSG_CHAT_ID` / `IMSG_SEND_CHAT_ID` / `REMOTE_USER_ID` 等）

### 2.b 手机端发送到邮箱端 ID

按脚本提示，在手机端给 Cloud X 的邮箱端 iMessage ID（例如 `cloudx@example.com`）发送配对码。

### 2.c 系统自动匹配 Cloud X 端 / 用户端

脚本通过消息内容匹配配对码，自动确定：
- 哪个会话是 Cloud X 入口（chat id）
- 哪个 sender 是用户端 ID（`REMOTE_USER_ID`）

匹配完成后，你只需要启动：

```bash
sh start.sh
```

## 3. 启动后验证

1. 手机发一条 `你好`。
2. 预期收到两条回包：
   - `已收到消息，开始处理...`
   - `✅ 完成` 摘要消息
3. 查看本地日志：

```bash
tail -n 30 .sms_codex_interactions.jsonl
```

## 4. 常见问题

### Q1: 手机显示已发送，但 Mac 没处理

- 常见原因：发到了不同线程（手机号线程 vs 邮箱线程）。
- 处理：重新执行自动配置脚本，确保配对码匹配到你当前使用的线程。

### Q2: 会话里有别的 bot 抢回复（如 Clawbot）

- 原因：系统里还有其他 bot 进程在线。
- 处理：停掉其他 gateway，只保留 `sms-codex-loop`。

### Q3: 启动时报 `imsg history did not return JSON`

- 原因：终端没有 Full Disk Access。
- 处理：按第 1 节重新授予权限后重试。
