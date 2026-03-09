# GUI <-> CLI Flow (vNext)

## 目标

macOS GUI 不直接读写网关内部文件，只通过 `gateway` CLI 获取数据与执行动作。

> 注：GUI 相关命令仅经 `gatewayd` gRPC；CLI 不走本地业务回退。若控制面不可用，则直接返回错误。

## 读模型（Read）

GUI 周期轮询：

1. `gateway status --json`
2. `gateway sessions --json`
3. `gateway messages --session-key <key> --json`（选中会话时）
4. `gateway view tasks --json`（可选）

日志展示规则：

- GUI 当前日志路径必须来自 `status --json` 返回的 `log_file`。
- GUI 不应硬编码 repo 内日志路径或自行推导 `gatewayd.log` 路径作为业务真源。
- GUI “Open Current Log” 仅打开该 `log_file` 指向的文件或其父目录，不自行推导替代路径。

GUI 生命周期：

1. 启动时执行 `gatewayd-up --json`
2. 退出时执行 `gatewayd-down --json`

## 动作模型（Write）

GUI 用户操作映射：

- 创建会话：
  - `gateway session-new --session-key <generated_key> --workdir <path> --json`
- 更新已选会话 workdir：
  - `gateway session-new --session-key <existing_key> --workdir <path> --json`
- 点击 Send：
  - `gateway send --session-key <key> --text "<text>" --json`
- 输入 `/clear`：
  - `gateway session-clear --session-key <key> --json`
- 输入 `/new`：
  - `gateway session-clear --session-key <key> --json`
- 输入 `/new hello`：
  - `gateway session-clear --session-key <key> --json`
  - `gateway send --session-key <key> --text "hello" --json`
- 删除会话：
  - `gateway session-delete --session-key <key> --json`

明确约束：

- GUI session 是 gateway 自己的会话条目，不是 ACP agent 的长期会话句柄。
- `session-new` 只管理 gateway session 元数据，不创建 ACP live session。
- `send --session-key` 每次都是新的 ACP 执行；GUI 不应把 `session_id` 理解为可长期复用的 agent 对话上下文。
- 若用户需要长期复用 agent 对话，应离开 GUI/gateway 流程，改走 agent 自身的 session CLI。

## 消息状态映射

- GUI 发送前：本地显示 `sending`
- 消息写入系统（`messages` 可见 `status=sent`）：更新为 `sent`
- 服务端处理中（`messages` 可见 `status=processing`）：更新为 `processing`
- CLI 闭环完成且 `ok=true`：保留 `sent`，并刷新会话/消息
- CLI 返回 `ok=false` 或非 0 退出码：更新为 `failed`
- `failed` 时展示 `error.code` 与 `error.message`

## 约束

- GUI 所有调用都必须加 `--json`
- `stdout` 只解析一个 JSON 对象
- 不解析 `stderr` 作为业务结果
