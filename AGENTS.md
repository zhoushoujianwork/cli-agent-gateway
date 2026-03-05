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
- Build CLI:
  - `make build`
- Check single-instance lock status:
  - `cd src && go run ./cmd/gateway-cli status`

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
3. Verify docs/env examples if behavior or config changed.

## Documentation Expectations

When behavior changes, update the relevant docs:
- `README.md` for user-facing setup/usage changes.
- `docs/ARCHITECTURE.md` for architecture changes.
- `.env.example` for new/changed environment variables.

## Notes for Agents

- Prefer minimal diffs over broad refactors.
- If you find unexpected unrelated local changes, stop and confirm before modifying those areas.
- If a requirement is unclear, choose the simplest implementation that matches current patterns.
