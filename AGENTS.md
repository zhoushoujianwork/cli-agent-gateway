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

- `src/app/main.py`: app entrypoint
- `src/core/loop.py`: gateway main loop
- `src/core/router.py`: routing logic
- `src/agents/acp_stdio_agent.py`: ACP adapter
- `src/infra/jsonrpc_stdio.py`: stdio JSON-RPC transport
- `src/channels/`: channel adapters (iMessage, DingTalk, command)
- `docs/ARCHITECTURE.md`: architecture details

## Local Setup

1. Use Python 3.9+.
2. Copy `.env.example` to `.env` and fill required values, or run interactive setup.
3. Ensure `ACP_AGENT_CMD` is executable and available.

## Common Commands

- Run gateway:
  - `make run`
- Re-run setup wizard (even if `.env` exists):
  - `make config`
- Check single-instance lock status:
  - `make status`
- Run DingTalk callback server:
  - `make run-dingtalk-callback`

## Coding Guidelines

- Keep changes small and localized.
- Preserve existing architecture boundaries:
  - channel-specific behavior in `channels/`
  - orchestration/routing in `core/`
  - ACP integration in `agents/` and `infra/`
- Prefer explicit types and dataclass/protocol patterns already used in the codebase.
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

If any sensitive value is found, remove it from code/history before commit.

## Branch Rule for New Features

- If the user asks for a new feature, start the work on a new git branch first.
- Never implement new features directly on `main`.
- Branch names must use the `codex/` prefix.
- Recommended format: `codex/feat/<short-topic>` (example: `codex/feat/add-dingtalk-retry`).
- Before opening PR, rebase or merge latest `main` and resolve conflicts.

## Multi-Agent Collaboration Rules

- One task, one branch, one owner agent.
- Split parallel work by module boundaries (`channels/`, `core/`, `agents/`, `infra/`, `docs/`) to reduce conflicts.
- Do not edit the same file from multiple agents at the same time unless explicitly coordinated.
- Each agent should publish a short plan before coding: scope, touched files, risks.
- Commit in small, reviewable units; avoid mixed-purpose commits.
- Synchronize with `main` frequently for long-running branches.
- If conflicts occur, prefer keeping behavior-compatible code and document tradeoffs in PR notes.
- Before merge, run validation checklist and secret hygiene checks again.

## Validation Checklist

Before finishing a change, run at least:

1. `python3 -m compileall src`
2. Relevant smoke run for touched flows (for example `make run`, channel scripts, or mock scripts in `scripts/testing/`).
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
