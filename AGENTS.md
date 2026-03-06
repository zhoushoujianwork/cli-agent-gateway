# AGENTS.md

This file provides practical instructions for human and AI agents working in this repository.

## Scope

- Applies to the entire repository rooted at `/Users/mikas/github/cli-agent-gateway`.
- If a deeper `AGENTS.md` is added in a subdirectory, that deeper file should take precedence for files under it.

## Project Summary

`cli-agent-gateway` is an ACP-first chat gateway:
- Channel adapters handle message ingress/egress.
- Execution is delegated to an ACP agent via JSON-RPC over stdio.
- The main loop handles deduplication, auth checks, session routing, progress replies, and report/log persistence.

## Repository Map

- `src/cmd/gateway-cli/main.go`: app entrypoint
- `src/internal/core/loop.go`: gateway main loop
- `src/internal/agents/acp/`: ACP adapter
- `src/internal/channels/`: channel adapters (DingTalk, command, iMessage TODO)
- `src/internal/storage/`: storage backends (sqlite/localfile)
- `docs/ARCHITECTURE.md`: architecture details

## Local Setup

1. Use Go 1.24+.
2. Copy `.env.example` to `.env` and fill required values, or run `cag config`.
3. Ensure `ACP_AGENT_CMD` is executable and available.

## Common Commands

- Run gateway:
  - `make run`
- Manage gateway lifecycle:
  - `cd src && go run ./cmd/gateway-cli start`
  - `cd src && go run ./cmd/gateway-cli stop`
  - `cd src && go run ./cmd/gateway-cli restart`
- Build CLI:
  - `make build`
- Run health diagnostics:
  - `cd src && go run ./cmd/gateway-cli health --json`
  - `cd src && go run ./cmd/gateway-cli doctor --json`
- Check single-instance lock status:
  - `cd src && go run ./cmd/gateway-cli status`
- Build and open macOS GUI app:
  - `make gui-open`

## Coding Guidelines

- Keep changes small and localized.
- Preserve architecture boundaries:
  - channel-specific behavior in `src/internal/channels/`
  - orchestration/routing in `src/internal/core/`
  - ACP integration in `src/internal/agents/`
  - storage/config/lock in `src/internal/infra/` and `src/internal/storage/`
- Avoid introducing heavy dependencies unless clearly justified.
- Do not hardcode secrets or credentials; use environment variables.

## Editing and Safety

- This repository is public open-source on GitHub. Treat all committed content as public.
- Do not break the single-instance lock semantics (`.cli_agent_gateway.lock` or configured `LOCK_FILE`).
- Preserve backward compatibility of env keys unless migration docs are updated.
- For channel changes, verify both fetch and send paths are still consistent.
- Keep logs and persisted reports machine-readable.
- Never commit secrets (API keys, tokens, passwords, private certificates, chat exports containing sensitive data).

## Secret Hygiene (Required Before Each Commit)

Run these checks before every commit:

1. Inspect staged changes:
   - `git diff --cached`
2. Scan staged diff for common secret patterns:
   - `git diff --cached | rg -i '(api[_-]?key|secret|token|password|passwd|private[_-]?key|BEGIN [A-Z ]+PRIVATE KEY|AKIA|ASIA|xoxb-|ghp_)'`
3. Ensure local env files and runtime artifacts are not staged:
   - `git status --short`
   - Confirm `.env` and other local secret files are ignored and unstaged.

## Validation Checklist

Before finishing a change, run at least:

1. `cd src && go test ./...`
2. Relevant smoke run for touched flows (for example `make run`, or channel mocks).
   - For control-plane/lifecycle changes, also smoke-run:
   - `cd src && go run ./cmd/gateway-cli start && go run ./cmd/gateway-cli status --json && go run ./cmd/gateway-cli stop`
3. Verify docs/env examples if behavior or config changed.

## Documentation Expectations

When behavior changes, update the relevant docs:
- `README.md` for user-facing setup/usage changes.
- `docs/ARCHITECTURE.md` for architecture changes.
- `.env.example` for new/changed environment variables.

## Closed-Loop macOS GUI Testing and Style Validation

Use this flow when a user asks for end-to-end automatic style build and GUI functional testing on macOS.

### Preconditions

- macOS host with GUI session (not pure headless shell).
- Xcode Command Line Tools installed (`xcode-select -p`).
- Accessibility + Automation permissions granted to terminal/runner process.
- Repository `.env` is configured and `ACP_AGENT_CMD` is executable.

### Step 1: Build/Rebuild the macOS GUI app

- Run:
  - `macos/CLIApp/scripts/build_macos_gui_app.sh --repo-root "$(pwd)" --output-dir "$PWD/.artifacts" --no-open`
- Expected output:
  - `.app` bundle under `.artifacts/`
  - Build logs in command output
- If build fails, stop and return concrete error lines to the user.

### Step 2: Launch and smoke-check lifecycle

- Launch app:
  - `open "$PWD/.artifacts/CLI Agent Gateway GUI.app"`
- Verify process/status:
  - `cd src && go run ./cmd/gateway-cli status`
- Validate lock semantics are preserved:
  - Must not create conflicting instances when lock already exists.

### Step 3: GUI functional test loop (minimum)

Run at least the following checks in sequence (AppleScript/System Events or UI test runner):
1. App launches and main window/menu is visible.
2. Start action works and reports running state.
3. Stop action works and clears running state.
4. Restart action works from running state.
5. Log open/view action opens readable log target.
6. Error path shows machine-readable and actionable failure signal.

For each check, record:
- action input
- observed UI/system result
- pass/fail
- relevant log snippet path

### Step 4: Automated screenshot capture for style verification

- Capture screenshots for key states (at minimum):
  - idle
  - running
  - stop/confirmation
  - error state (if reproducible)
- Prefer deterministic names:
  - `docs/screenshots/macos-gui-idle.png`
  - `docs/screenshots/macos-gui-running.png`
  - `docs/screenshots/macos-gui-stop.png`
  - `docs/screenshots/macos-gui-error.png`
- Keep README links in sync when filenames change.

### Step 5: Style validation gate

Before closing task, compare new screenshots against expected UI spec or previous baseline:
- no layout break/truncation at default scaling
- readable contrast and text hierarchy
- icon/title/action labels consistent
- no clipped dialogs/controls

If visual regressions are found, fix and re-run Steps 1-5 until all gates pass.

### Step 6: Required completion output to user

When reporting completion, always include:
1. build command + result
2. functional checks summary (pass/fail table)
3. screenshot file list
4. known limitations or non-deterministic points
5. next action if any check failed

## Notes for Agents

- Prefer minimal diffs over broad refactors.
- If you find unexpected unrelated local changes, stop and confirm before modifying those areas.
- If a requirement is unclear, choose the simplest implementation that matches current patterns.
