# cli-agent-gateway

An ACP-first gateway for local task sessions.

The current design direction is session-first:

- one `session` owns task context
- multiple entrypoints may attach to that same session
- `channel` is only an ingress/egress endpoint
- `binding` explicitly maps a channel conversation to a session
- `runtime` holds the live ACP process for a session

## Design Docs

The old flat-command documentation has been replaced by a new vNext design set:

- `docs/ARCHITECTURE.md`
- `docs/CLI_SPEC.md`
- `docs/GUI_CLI_FLOW.md`
- `docs/GO_BACKEND_ARCHITECTURE.md`
- `AGENTS.md`

## vNext Command Preview

The command tree is expected to converge to:

```text
cag session ...
cag channel ...
cag binding ...
cag runtime ...
```

Examples:

```bash
cag session create --key sess_build_web --workdir /path/to/repo
cag session send --key sess_build_web --text "continue"
cag channel inbox --json
cag binding create --channel dingtalk --conversation-id conv_123 --session-key sess_build_web
cag runtime ps --json
```

See `docs/CLI_SPEC.md` for the latest preview and command annotations.

## Current Repository Layout

- `src/cmd/gateway-cli/main.go`
- `src/internal/core/`
- `src/internal/agents/acp/`
- `src/internal/channels/`
- `src/internal/storage/`
- `macos/CLIApp/`
- `docs/`

## Local Validation Baseline

```bash
cd src && go test ./...
cd src && go run ./cmd/gateway-cli start --json
cd src && go run ./cmd/gateway-cli status --json
cd src && go run ./cmd/gateway-cli stop --json
```

## Notes

- The new docs describe the intended target model.
- Existing implementation may still contain legacy flat commands during migration.
- New behavior should be designed against the grouped command families, not the legacy flat surface.
