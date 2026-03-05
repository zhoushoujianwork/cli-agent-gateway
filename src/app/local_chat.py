#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path

from agents.acp_stdio_agent import ACPStdioAgentAdapter
from core.contracts import TaskRequest
from core.router import build_session_key
from infra.config import AppConfig
from infra.state_store import JsonStateStore


def _build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(description="Run one local ACP chat turn and persist session mapping.")
    p.add_argument("--workdir", required=True)
    p.add_argument("--channel", required=True)
    p.add_argument("--sender", required=True)
    p.add_argument("--thread-id", default="")
    p.add_argument("--session-key", default="")
    p.add_argument("--session-id", default="")
    p.add_argument("--text", required=True)
    return p


def _human_mode_prompt(channel: str, text: str, sender_name: str = "") -> str:
    if channel != "dingtalk":
        return text
    if os.getenv("DINGTALK_HUMAN_REPLY_ONLY", "1").strip() == "0":
        return text
    base = os.getenv(
        "DINGTALK_HUMAN_STYLE_PROMPT",
        (
            "Reply like a real human in chat: concise, natural, a little funny, no status labels, "
            "no task IDs, no markdown headers."
        ),
    ).strip()
    sender_name = sender_name.strip()
    greeting = text.strip().lower()
    is_greeting = greeting in {"hi", "hello", "hey", "yo", "你好", "嗨"}
    if is_greeting and sender_name:
        base = f"{base} Greeting detected: reply with the user's name '{sender_name}' in the first sentence."
    return f"{base}\n\nUser message:\n{text}"


def main() -> None:
    args = _build_parser().parse_args()
    repo_root = Path(__file__).resolve().parents[2]
    cfg = AppConfig.from_env(repo_root=repo_root, workdir_arg=args.workdir)
    text = (args.text or "").strip()
    if not text:
        print(json.dumps({"ok": False, "error": "empty_text"}, ensure_ascii=False))
        raise SystemExit(2)

    session_key = (args.session_key or "").strip()
    if not session_key:
        session_key = build_session_key(args.channel, args.sender, args.thread_id or None)
    state_store = JsonStateStore(cfg.state_file)
    state = state_store.load()
    session_id = (args.session_id or "").strip() or state.session_map.get(session_key)

    agent = ACPStdioAgentAdapter(
        command=cfg.acp_agent_cmd,
        cwd=str(cfg.workdir),
        timeout_sec=cfg.timeout_sec,
        permission_policy=cfg.permission_policy,
        initialize_timeout_sec=cfg.acp_initialize_timeout_sec,
        session_new_timeout_sec=cfg.acp_session_new_timeout_sec,
        session_new_retries=cfg.acp_session_new_retries,
        session_new_retry_backoff_sec=cfg.acp_session_new_retry_backoff_sec,
        debug_trace_enabled=False,
        debug_acp_event_details=False,
        debug_acp_log_chunks=False,
        debug_payload_chars=cfg.debug_acp_event_payload_chars,
        prompt_recover_retries=cfg.acp_prompt_recover_retries,
    )
    try:
        user_text = _human_mode_prompt(args.channel, text)
        req = TaskRequest(
            trace_id=f"local-{os.getpid()}",
            session_key=session_key,
            user_text=user_text,
            sender=args.sender,
            channel=args.channel,
            thread_id=args.thread_id or None,
            session_id=session_id,
            metadata={"source": "gui_local_chat"},
        )
        result = agent.execute(req)
    finally:
        agent.close()

    if result.session_id:
        state.session_map[session_key] = result.session_id
        state_store.save(state)

    summary = (result.summary or "").strip() or (result.output_text or "").strip() or "..."
    payload = {
        "ok": result.status == "ok",
        "status": result.status,
        "summary": summary,
        "session_key": session_key,
        "session_id": result.session_id or "",
        "elapsed_sec": result.elapsed_sec,
        "error_text": result.error_text or "",
    }
    print(json.dumps(payload, ensure_ascii=False))
    if result.status != "ok":
        raise SystemExit(1)


if __name__ == "__main__":
    main()
