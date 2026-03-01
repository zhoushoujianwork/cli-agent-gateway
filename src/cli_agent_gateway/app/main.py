#!/usr/bin/env python3
from __future__ import annotations

import sys
from pathlib import Path

from cli_agent_gateway.agents.acp_stdio_agent import ACPStdioAgentAdapter
from cli_agent_gateway.channels.command_channel import CommandChannelAdapter
from cli_agent_gateway.core.loop import GatewayLoop
from cli_agent_gateway.infra.config import AppConfig
from cli_agent_gateway.infra.interaction_log import InteractionLog
from cli_agent_gateway.infra.state_store import JsonStateStore


def usage() -> None:
    print("Usage: python3 -m cli_agent_gateway.app.main <agent_workdir>", file=sys.stderr)


def main() -> None:
    repo_root = Path(__file__).resolve().parents[3]
    if len(sys.argv) < 2:
        usage()
        raise SystemExit(2)

    cfg = AppConfig.from_env(repo_root=repo_root, workdir_arg=sys.argv[1])
    if not cfg.workdir.exists() or not cfg.workdir.is_dir():
        print(f"[FATAL] invalid workdir: {cfg.workdir}", file=sys.stderr)
        raise SystemExit(2)

    channel = CommandChannelAdapter(fetch_cmd=cfg.fetch_cmd, send_cmd=cfg.send_cmd)
    agent = ACPStdioAgentAdapter(
        command=cfg.acp_agent_cmd,
        cwd=str(cfg.workdir),
        timeout_sec=cfg.timeout_sec,
        permission_policy=cfg.permission_policy,
    )
    state_store = JsonStateStore(cfg.state_file)
    interaction_log = InteractionLog(cfg.interaction_log_file)

    loop = GatewayLoop(
        channel=channel,
        agent=agent,
        state_store=state_store,
        interaction_log=interaction_log,
        report_dir=cfg.report_dir,
        remote_user_id=cfg.remote_user_id,
        allowed_from=cfg.allowed_from,
        process_only_latest=cfg.process_only_latest,
        poll_interval_sec=cfg.poll_interval_sec,
        progress_notify_interval_sec=cfg.progress_notify_interval_sec,
        sms_limit=cfg.sms_reply_max_chars,
    )
    loop.run_forever()


if __name__ == "__main__":
    main()
