# Issue: 长任务在 channel -> gatewayd 路径上约 150s 被误判失败

日期: 2026-03-09
状态: open
严重级别: high

## 现象

当 DingTalk/外部 channel 进入的任务执行时间较长时，用户侧先持续收到进度消息：

- `处理中，已等待 120s`
- `处理中，已等待 140s`

随后收到失败消息：

- `执行失败: rpc error: code = DeadlineExceeded desc = context deadline exceeded`

但从 ACP 日志看，底层 agent 并未在此时真正达到任务超时上限，仍在继续执行、输出 chunk，甚至继续发起工具调用。

## 直接结论

这不是单纯的“Codex/ACP 任务超时”，而是两层问题叠加：

1. 主问题: channel 进程调用 `gatewayd` 的 gRPC deadline 大约只有 150 秒，和运行时配置里的 `AGENT_TIMEOUT_SEC=1800` 脱节。
2. 次问题: 任务后续触发 `session/request_permission` 时，网关返回的响应结构可能不符合上游 ACP 预期，导致上游报 `failed to deserialize response message:Internal error`。

用户当前看到的 `DeadlineExceeded`，优先是第 1 个问题暴露出来的结果。

## 证据链

### 1. 外层报错发生在约 140s-150s，不符合默认 agent 1800s 超时

日志中持续出现：

- `execute waiting ... elapsed=141s remaining=1658s`
- `execute waiting ... elapsed=146s remaining=1653s`
- `execute waiting ... elapsed=151s remaining=1648s`

说明 ACP 内层执行 deadline 仍然剩余约 27 分钟，不是 agent 自身超时。

与此同时，channel loop 给用户发出：

- `处理中，已等待 120s`
- `处理中，已等待 140s`

随后直接进入：

- `send error reply ok ...`
- `execute failed ... err=rpc error: code = DeadlineExceeded desc = context deadline exceeded`

这更像是 channel 进程等待 `gatewayd` 返回结果时，自己的 gRPC 调用先超时了。

### 2. gRPC timeout 代码路径与现象一致

`src/cmd/gateway-cli/session.go` 中，channel 侧通过 `gatewaySessionProxyAgent.Execute()` 调 `tryActionViaGRPC(... Action: "session.send" ...)` 把请求发给 `gatewayd`。

`src/cmd/gateway-cli/grpc_actions.go` / `src/cmd/gateway-cli/grpc_control.go` 中，`tryActionViaGRPC()` 使用 `sendViaSessionGRPCTimeout()` 创建 context timeout。

`sendViaSessionGRPCTimeout()` 当前逻辑：

- 默认 `timeoutSec := 120`
- 只尝试从进程环境变量 `AGENT_TIMEOUT_SEC` 读取
- 最终返回 `time.Duration(timeoutSec+30) * time.Second`

因此如果 channel 进程环境里没有显式注入 `AGENT_TIMEOUT_SEC`，实际 gRPC timeout 就是：

- `120 + 30 = 150s`

这与现场“120s/140s 后很快失败”的现象完全吻合。

而仓库配置定义里，`AGENT_TIMEOUT_SEC` 是 `ScopeRuntimeDB`，默认值是 `1800`，见：

- `src/internal/config/keys.go`

也就是说：

- `gatewayd` 侧真正执行任务时知道 runtime DB 里的 1800s
- channel 侧等待 `gatewayd` 返回时，却只按本地环境变量推导 150s

两边超时预算不一致。

## 次要问题: permission 请求响应可能不兼容 ACP 协议

在外层 deadline 之后，ACP 日志还出现：

- `server request method=session/request_permission id=0`
- `prompt response id=3 error=map[code:-32603 data:failed to deserialize response message:Internal error]`

当前实现位于 `src/internal/agents/acp/adapter.go`：

- 对任何包含 `request_permission` 的方法，直接返回
  - `{"decision":"allow|deny","reason":"policy:..."}`

这里有两个风险点：

1. `session/request_permission` 的返回结构可能不是上游 ACP 预期字段。
2. 请求 `id=0` 也值得警惕，部分 JSON-RPC/ACP 实现对该 id 或响应格式有额外约束。

从日志顺序看，这个问题不是用户本次看到 `140s 后失败` 的首因，但它会导致：

- 任务一旦进入需要提权/联网授权的分支，可能被 ACP 自身打成 internal error。

## 影响范围

受影响场景：

- 通过 DingTalk / 外部 channel 进入
- 实际执行时间超过约 150 秒
- 或执行中触发需要 permission approval 的工具/网络操作

不明显受影响场景：

- GUI 直连本地会话，且不经过这条 channel -> gatewayd -> session.send 的等待链路
- 150 秒内完成的短任务

## 根因判断

主根因:

- channel 侧 gRPC deadline 不是从统一配置加载，而是从进程环境变量兜底推导，导致和 `gatewayd`/runtime 实际 timeout 配置脱节。

次根因:

- ACP `session/request_permission` 的响应 payload 可能与实际上游协议不兼容。

## 建议修复方向

1. 统一 `session.send` 的 deadline 来源。
   - `tryActionViaGRPC()` 不应只读进程环境。
   - 应改为读取和 `gatewayd` 相同的最终配置值，或直接由 server 端返回/协商 timeout。

2. 区分“外层 RPC 超时”和“内层 agent 仍在运行”。
   - 如果外层超时，用户提示不应直接显示成任务失败。
   - 至少应标明是 `gatewayd RPC timeout`，避免误判为 agent 自身失败。

3. 核对 ACP `session/request_permission` 的正式返回 schema。
   - 按上游协议返回完整字段。
   - 为该分支补测试，覆盖 allow / deny / id=0 / malformed request。

## 最小复现思路

1. 从 DingTalk 发起一个会持续 3 分钟以上的任务。
2. 保持 `gatewayd` 默认配置，且不要在 channel 进程环境里单独注入 `AGENT_TIMEOUT_SEC=1800`。
3. 观察到约 150 秒时 channel 侧先报：
   - `rpc error: code = DeadlineExceeded desc = context deadline exceeded`
4. 如果任务还尝试联网/申请权限，进一步可能看到：
   - `session/request_permission`
   - `failed to deserialize response message:Internal error`

## 相关代码

- `src/cmd/gateway-cli/session.go`
- `src/cmd/gateway-cli/grpc_actions.go`
- `src/cmd/gateway-cli/grpc_control.go`
- `src/internal/config/keys.go`
- `src/internal/agents/acp/adapter.go`
