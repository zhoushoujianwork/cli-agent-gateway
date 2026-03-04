#!/usr/bin/env python3
from __future__ import annotations

import os
import sys
from datetime import datetime, timezone
from pathlib import Path

from agents.acp_stdio_agent import ACPStdioAgentAdapter
from channels.command_channel import CommandChannelAdapter
from channels.dingtalk_stream_channel import DingTalkStreamChannelAdapter
from core.loop import GatewayLoop
from infra.config import AppConfig
from infra.interaction_log import InteractionLog
from infra.process_lock import ProcessLock, inspect_lock
from infra.setup_wizard import bootstrap_env_if_missing
from infra.state_store import JsonStateStore


def usage() -> None:
    print("Usage: python3 -m app.main <agent_workdir>", file=sys.stderr)


def _mask_value(value: str, *, keep_left: int = 3, keep_right: int = 2) -> str:
    raw = (value or "").strip()
    if not raw:
        return ""
    if len(raw) <= keep_left + keep_right:
        return "*" * len(raw)
    return f"{raw[:keep_left]}***{raw[-keep_right:]}"


def _log_startup(cfg: AppConfig) -> None:
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    print(f"[{now}] startup channel={cfg.channel_type} workdir={cfg.workdir}")
    print(f"[{now}] startup acp_cmd={cfg.acp_agent_cmd} permission_policy={cfg.permission_policy}")
    print(
        f"[{now}] startup acp_timeouts init={cfg.acp_initialize_timeout_sec}s "
        f"session_new={cfg.acp_session_new_timeout_sec}s retries={cfg.acp_session_new_retries} "
        f"backoff={cfg.acp_session_new_retry_backoff_sec}s"
    )
    print(f"[{now}] startup fetch_cmd={cfg.fetch_cmd}")
    print(f"[{now}] startup send_cmd={cfg.send_cmd}")
    print(f"[{now}] startup show_tool_trace={int(cfg.show_tool_trace)}")
    print(
        f"[{now}] startup debug trace_chain={int(cfg.debug_trace_chain)} "
        f"acp_events={int(cfg.debug_acp_events)} acp_log_chunks={int(cfg.debug_acp_log_chunks)} "
        f"acp_payload_chars={cfg.debug_acp_event_payload_chars} "
        f"tool_progress_notify={int(cfg.tool_progress_notify_enabled)}"
    )

    if cfg.channel_type == "dingtalk":
        send_mode = os.getenv("DINGTALK_SEND_MODE", "api").strip() or "api"
        send_msgtype = os.getenv("DINGTALK_SEND_MSGTYPE", "markdown").strip() or "markdown"
        card_fallback = os.getenv("DINGTALK_CARD_FALLBACK_MARKDOWN", "1").strip() or "1"
        app_key = _mask_value(os.getenv("DINGTALK_APP_KEY", ""))
        agent_id = os.getenv("DINGTALK_AGENT_ID", "").strip()
        print(
            f"[{now}] startup dingtalk inbound=stream send_mode={send_mode} msgtype={send_msgtype} "
            f"card_fallback={card_fallback} app_key={app_key or '(empty)'} agent_id={agent_id or '(empty)'}"
        )


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
    _log_startup(cfg)

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

    if cfg.channel_type == "dingtalk":
        channel = DingTalkStreamChannelAdapter(send_cmd=cfg.send_cmd)
    else:
        channel = CommandChannelAdapter(fetch_cmd=cfg.fetch_cmd, send_cmd=cfg.send_cmd, channel_id=cfg.channel_type)
    agent = ACPStdioAgentAdapter(
        command=cfg.acp_agent_cmd,
        cwd=str(cfg.workdir),
        timeout_sec=cfg.timeout_sec,
        permission_policy=cfg.permission_policy,
        initialize_timeout_sec=cfg.acp_initialize_timeout_sec,
        session_new_timeout_sec=cfg.acp_session_new_timeout_sec,
        session_new_retries=cfg.acp_session_new_retries,
        session_new_retry_backoff_sec=cfg.acp_session_new_retry_backoff_sec,
        debug_trace_enabled=cfg.debug_trace_chain,
        debug_acp_event_details=cfg.debug_acp_events,
        debug_acp_log_chunks=cfg.debug_acp_log_chunks,
        debug_payload_chars=cfg.debug_acp_event_payload_chars,
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
        debug_user_profile=cfg.debug_user_profile,
        show_tool_trace=cfg.show_tool_trace,
        debug_trace_chain=cfg.debug_trace_chain,
        debug_acp_events=cfg.debug_acp_events,
        tool_progress_notify_enabled=cfg.tool_progress_notify_enabled,
    )
    try:
        loop.run_forever()
    finally:
        process_lock.close()


if __name__ == "__main__":
    main()
