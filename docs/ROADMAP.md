# ACP Gateway Roadmap

## Phase 1: Protocol hardening

- Align ACP event parsing with `codex-acp` real payload fields.
- Add strict JSON-RPC error handling and retry strategy.
- Add session lifecycle policy (idle timeout, explicit close, restore).

## Phase 2: Permission workflow

- Support interactive permission flow over channel messages.
- Add policy layers: auto-allow, auto-deny, allowlist by command/tool.
- Persist pending approvals and timeout decisions.

## Phase 3: Multi-agent support

- Add agent registry (`codex-acp`, `claude-code-acp`, etc.).
- Route tasks by sender/thread/profile.
- Add per-agent capability flags and fallback behavior.

## Phase 4: Reliability and quality

- Unit tests for router/state/jsonrpc/adapter layers.
- Integration tests with fake ACP server and command channel.
- Structured metrics and health endpoints.

## Phase 5: Channel expansion

- Add native DingTalk adapter as next channel milestone.
- Add native Telegram/Discord/Slack adapters.
- Keep message contract stable across channels.
- Add channel-specific rate limit and threading strategies.

## Phase 6: Desktop app launcher (macOS)

- Add a macOS app launcher for users who do not use CLI workflows.
- Keep Python gateway as the backend service process (no protocol rewrite).
- Add GUI controls for setup/start/stop/status and live log viewing.
- Reuse single-instance lock and existing env/config semantics.
- Package and document a `.app` distribution flow.
