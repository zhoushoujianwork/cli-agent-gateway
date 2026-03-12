# AGENTS.md

This file defines the current working contract for human and AI agents in this repository.

## Scope

- Applies to the entire repository rooted at `/Users/mikas/github/cli-agent-gateway`.
- A deeper `AGENTS.md` may override this file for its subtree.

## Product Direction

This repository is moving to a **session-first local build workflow**:

- `session` is the only user-facing context owner.
- `channel` is only an ingress/egress endpoint.
- `binding` explicitly connects a channel conversation to a session.
- `runtime` manages the live ACP/agent process behind a session.

Target user story:

- The same person can talk to the same task session from multiple entrypoints.
- Example: GUI on desktop and DingTalk on phone both attach to one session and share one task context.

## Canonical Domain Model

- `session`
  - owns task context, workdir, history, runtime lifecycle
- `channel conversation`
  - represents one concrete external entrypoint conversation
- `binding`
  - maps one channel conversation to one session
- `runtime`
  - holds the live ACP session/process for a session

## Routing Rules

- Never derive a target session from `sender`, `thread`, or `channel` heuristics.
- Never route writes to a global `latest` session implicitly.
- GUI writes must target an explicit session selected by the user.
- Channel ingress must resolve through an explicit binding.
- If no binding exists, the message must not execute. Record it as unassigned instead.

## CLI Direction

New work must converge to grouped commands:

- `cag session ...`
- `cag channel ...`
- `cag binding ...`
- `cag runtime ...`

Old flat commands such as `session-new`, `session-clear`, `session-delete`, `sessions`, and `messages` are legacy surface area.

Rules:

- Do not add new product behavior to old flat commands.
- If a change touches command design, prefer the grouped command tree.
- When removing old commands, remove them by command family, not piecemeal.
- Every new command should be documented with:
  - command path
  - purpose
  - feature group
  - sunset group

## Documentation-First Workflow

For any change affecting command behavior, session model, channel routing, runtime ownership, GUI flow, or control-plane semantics:

1. Write or update the design docs first.
2. Make the target behavior explicit.
3. Only then modify implementation.

If current code and docs disagree:

- Treat the docs as the source of intended direction for the next change.
- Prefer deleting obsolete behavior over adding compatibility branches.

## Repository Map

- `src/cmd/gateway-cli/main.go`: current CLI entrypoint
- `src/internal/core/loop.go`: current gateway loop
- `src/internal/agents/acp/`: ACP adapter
- `src/internal/channels/`: channel adapters
- `src/internal/storage/`: storage backends
- `docs/ARCHITECTURE.md`: product/system architecture
- `docs/CLI_SPEC.md`: command tree and command contracts
- `docs/GUI_CLI_FLOW.md`: GUI behavior and control flow
- `docs/GO_BACKEND_ARCHITECTURE.md`: backend runtime/session manager design

## Engineering Constraints

- Keep diffs small and localized.
- Preserve architecture boundaries:
  - channel-specific code in `src/internal/channels/`
  - orchestration/routing in `src/internal/core/`
  - ACP protocol logic in `src/internal/agents/`
  - storage/config/lock in `src/internal/infra/` and `src/internal/storage/`
- Avoid hidden fallback paths.
- Prefer explicit breakage with actionable errors.
- Do not hardcode secrets or credentials.

## Control Plane Rules

- `gatewayd` remains the single source of truth for control-plane state.
- `~/.cag/` remains the canonical user-scoped runtime home.
- Do not treat a repo checkout as a control-plane identity.
- Do not add or preserve per-request `repo_root` routing in grouped command flows.
- Task working directory belongs to `session.workdir`, not to `gatewayd`.
- GUI must go through CLI / `gatewayd`; it must not mutate runtime files directly as product behavior.

## Command Group Annotation Requirement

When adding or changing a command family, document each command with a stable group tag.

Recommended fields:

- `path`
- `purpose`
- `feature_group`
- `sunset_group`

Example:

- `path=session create`
- `purpose=create a new task session`
- `feature_group=session-lifecycle`
- `sunset_group=session-v2`

This requirement exists so an obsolete family can be removed in one pass.

## Validation

Before finishing a change, run at least:

1. `cd src && go test ./...`
2. If control-plane behavior changed:
   - `cd src && go run ./cmd/gateway-cli start --json`
   - `cd src && go run ./cmd/gateway-cli status --json`
   - `cd src && go run ./cmd/gateway-cli stop --json`
3. If docs changed, verify the docs do not reference removed commands or deleted files.

## Secret Hygiene

Before any commit:

1. `git diff --cached`
2. `git diff --cached | rg -i '(api[_-]?key|secret|token|password|passwd|private[_-]?key|BEGIN [A-Z ]+PRIVATE KEY|AKIA|ASIA|xoxb-|ghp_)'`
3. `git status --short`

## Editing Safety

- Treat all committed content as public.
- Never commit secrets.
- Do not break lock semantics or runtime ownership invariants.
- If unrelated local changes conflict with the task, stop and ask before overwriting them.

## Newly Observed Workflows (2026-03-12)

- GUI local dev loop (repo-verified): `make gui-dev`
  - runs `gui-close`, rebuilds `./bin/cag`, attempts `CAG_GRPC_DISABLE=1 ./bin/cag restart --json`, attempts `./bin/cag gatewayd-up --json`, then rebuilds/opens the macOS app.
- Runtime log workflow:
  - `cd src && go run ./cmd/gateway-cli runtime logs`
  - `cd src && go run ./cmd/gateway-cli runtime logs --follow`
- Runtime/session inspection flags now used in repo workflows:
  - `cd src && go run ./cmd/gateway-cli runtime ps --include-detached --json`
  - `cd src && go run ./cmd/gateway-cli session list --include-archived --json`
- Session send workflow supports file-backed input:
  - `cd src && go run ./cmd/gateway-cli session send --key <session_key> --file <path> --json`
