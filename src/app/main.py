#!/usr/bin/env python3
from __future__ import annotations

import os
import sys
from datetime import datetime, timezone
from pathlib import Path

from agents.acp_stdio_agent import ACPStdioAgentAdapter
from channels.command_channel import CommandChannelAdapter
from core.loop import GatewayLoop
from infra.config import AppConfig
from infra.interaction_log import InteractionLog
from infra.process_lock import ProcessLock, inspect_lock
from infra.setup_wizard import bootstrap_env_if_missing
from infra.state_store import JsonStateStore


def usage() -> None:
    print("Usage: python3 -m app.main <agent_workdir>", file=sys.stderr)


def main() -> None:
    repo_root = Path(__file__).resolve().parents[2]
    if len(sys.argv) < 2:
        usage()
        raise SystemExit(2)

    bootstrap_env_if_missing(repo_root=repo_root, workdir_arg=sys.argv[1])
    cfg = AppConfig.from_env(repo_root=repo_root, workdir_arg=sys.argv[1])
    if not cfg.workdir.exists() or not cfg.workdir.is_dir():
        print(f"[FATAL] invalid workdir: {cfg.workdir}", file=sys.stderr)
        raise SystemExit(2)

    process_lock = ProcessLock(cfg.lock_file)
    if not process_lock.acquire():
        status = inspect_lock(cfg.lock_file)
        pid_text = str(status.owner_pid) if status.owner_pid is not None else "unknown"
        started_text = status.owner_started_at or "unknown"
        print(
            (
                f"[FATAL] 检测到上一个网关实例未结束，请先关闭后再启动。"
                f" lock={cfg.lock_file} pid={pid_text} started_at={started_text}\n"
                f"[HINT] 先执行: make status\n"
                f"[HINT] 若确认要关闭该实例: kill {pid_text}"
            ),
            file=sys.stderr,
        )
        raise SystemExit(2)
    process_lock.write_metadata(
        {
            "pid": os.getpid(),
            "started_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
            "workdir": str(cfg.workdir),
            "lock_file": str(cfg.lock_file),
        }
    )

    channel = CommandChannelAdapter(fetch_cmd=cfg.fetch_cmd, send_cmd=cfg.send_cmd, channel_id=cfg.channel_type)
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
        reply_style_enabled=cfg.reply_style_enabled,
        reply_style_prompt=cfg.reply_style_prompt,
    )
    try:
        loop.run_forever()
    finally:
        process_lock.close()


if __name__ == "__main__":
    main()
