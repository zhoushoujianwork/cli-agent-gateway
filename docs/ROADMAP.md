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

## Phase 7: Go backend migration (cross-platform)

- Introduce Go implementation of gateway runtime while keeping ACP-first architecture.
- Keep config/state/report contracts compatible for macOS app and existing scripts.
- Port modules by boundary: `infra` -> `agents` -> `core` -> `channels`.
- Validate in shadow mode against Python runtime before default cutover.
- Keep Python runtime as temporary fallback during stabilization window.

### Branch Goal (codex/feat/init-go-cli-actions)

- Step 1 complete: `gateway-cli run` now uses the Go-native gateway loop (legacy bridge removed).
- Step 2 complete: `gateway-cli config` now writes `.env` via Go-native flow (no Python bridge).
- Step 3 complete: `start/stop/restart/health` added with dashboard-friendly JSON output.

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
