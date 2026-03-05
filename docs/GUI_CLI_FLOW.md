# GUI <-> CLI Flow (vNext)

## 目标

macOS GUI 不直接读写网关内部文件，只通过 `gateway` CLI 获取数据与执行动作。

## 读模型（Read）

GUI 周期轮询：

1. `gateway status --json`
2. `gateway view sessions --json`
3. `gateway view messages --session-key <key> --json`（选中会话时）
4. `gateway view tasks --json`（可选）

## 动作模型（Write）

GUI 用户操作映射：

- 点击 Send：
  - `gateway send --session-key <key> --text "<text>" --json`
- 输入 `/clear`：
  - `gateway session clear --session-key <key> --json`
- 输入 `/new`：
  - `gateway session new --session-key <key> --json`
- 输入 `/new hello`：
  - `gateway session new --session-key <key> --text "hello" --json`
- 删除会话：
  - `gateway session delete --session-key <key> --json`

## 消息状态映射

- GUI 发送前：本地显示 `sending`
- CLI 返回 `ok=true`：更新为 `sent`
- CLI 返回 `ok=false` 或非 0 退出码：更新为 `failed`
- `failed` 时展示 `error.code` 与 `error.message`

## 约束

- GUI 所有调用都必须加 `--json`
- `stdout` 只解析一个 JSON 对象
- 不解析 `stderr` 作为业务结果
