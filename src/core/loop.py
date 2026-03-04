from __future__ import annotations

import json
import os
import re
import time
from pathlib import Path
from typing import Any

from agents.base import AgentAdapter
from channels.base import ChannelAdapter
from core.contracts import TaskRequest, utc_now
from core.router import build_session_key
from infra.interaction_log import InteractionLog
from infra.state_store import GatewayState, JsonStateStore
from services.summarizer import build_user_summary


class GatewayLoop:
    def __init__(
        self,
        *,
        channel: ChannelAdapter,
        agent: AgentAdapter,
        state_store: JsonStateStore,
        interaction_log: InteractionLog,
        report_dir: Path,
        remote_user_id: str,
        allowed_from: set[str],
        process_only_latest: bool,
        poll_interval_sec: int,
        progress_notify_interval_sec: int,
        sms_limit: int,
        reply_style_enabled: bool,
        reply_style_prompt: str,
        debug_user_profile: bool,
        show_tool_trace: bool,
        debug_trace_chain: bool,
        debug_acp_events: bool,
        tool_progress_notify_enabled: bool,
    ):
        self.channel = channel
        self.agent = agent
        self.state_store = state_store
        self.interaction_log = interaction_log
        self.report_dir = report_dir
        self.remote_user_id = remote_user_id
        self.allowed_from = allowed_from
        self.process_only_latest = process_only_latest
        self.poll_interval_sec = poll_interval_sec
        self.progress_notify_interval_sec = progress_notify_interval_sec
        self.sms_limit = sms_limit
        self.reply_style_enabled = reply_style_enabled
        self.reply_style_prompt = reply_style_prompt.strip()
        self.debug_user_profile = debug_user_profile
        self.show_tool_trace = show_tool_trace
        self.debug_trace_chain = debug_trace_chain
        self.debug_acp_events = debug_acp_events
        self.tool_progress_notify_enabled = tool_progress_notify_enabled

        self.state: GatewayState = self.state_store.load()
        self.processed_ids = set(self.state.processed_ids)

    def _resolve_reply_target(self, sender: str) -> str:
        target = (sender or "").strip()
        if target:
            return target
        return self.remote_user_id

    def _extract_user_profile(self, msg) -> dict[str, Any]:
        profile: dict[str, Any] = {}
        if isinstance(msg.metadata, dict):
            raw_profile = msg.metadata.get("user_profile")
            if isinstance(raw_profile, dict):
                profile.update({k: v for k, v in raw_profile.items() if v not in ("", None)})
        if "sender_id" not in profile and msg.sender:
            profile["sender_id"] = msg.sender
        if msg.channel:
            profile.setdefault("channel", msg.channel)
        if msg.thread_id:
            profile.setdefault("thread_id", msg.thread_id)
        return profile

    def _log(self, message: str) -> None:
        print(f"[{utc_now()}] {message}")

    def _trace(self, stage: str, *, msg_id: str, **data: Any) -> None:
        if not self.debug_trace_chain:
            return
        parts: list[str] = []
        for key, value in data.items():
            if isinstance(value, (dict, list)):
                text = json.dumps(value, ensure_ascii=False)
            else:
                text = str(value)
            parts.append(f"{key}={self._preview(text, 180)}")
        tail = f" {' '.join(parts)}" if parts else ""
        self._log(f"trace stage={stage} msg_id={msg_id}{tail}")
        self.interaction_log.append("trace", msg_id=msg_id, stage=stage, **data)

    def _tool_progress_text(self, *, channel: str, title: str, status: str) -> str:
        tool_title = title.strip() or "tool"
        st = status.strip().lower()
        if st in {"completed", "ok", "success"}:
            return f"工具已完成：{tool_title}"
        if st in {"failed", "error"}:
            return f"工具执行失败：{tool_title}"
        if st in {"in_progress", "running", "started"}:
            return f"工具处理中：{tool_title}"
        return f"工具状态更新：{tool_title}（{st or 'unknown'}）"

    def _preview(self, text: str, limit: int = 120) -> str:
        compact = " ".join((text or "").split())
        if len(compact) <= limit:
            return compact
        return compact[: max(0, limit - 3)] + "..."

    def _dingtalk_human_mode(self, channel: str) -> bool:
        return channel == "dingtalk" and os.getenv("DINGTALK_HUMAN_REPLY_ONLY", "1").strip() != "0"

    def _wants_step_by_step(self, text: str) -> bool:
        t = (text or "").strip().lower()
        if not t:
            return False
        keys = ("step by step", "step-by-step", "一步一步", "逐步", "分步", "进度")
        return any(k in t for k in keys)

    def _is_task_board_query(self, text: str) -> bool:
        t = (text or "").strip().lower()
        if not t:
            return False
        keys = (
            "task running",
            "running task",
            "task status",
            "status",
            "still running",
            "还在跑",
            "还在运行",
            "任务状态",
            "任务进度",
            "正在处理吗",
        )
        return any(k in t for k in keys)

    def _format_task_board(self, sender: str) -> str:
        rows: list[str] = []
        for msg_id, info in self.state.inflight_tasks.items():
            if str(info.get("sender", "")).strip() != (sender or "").strip():
                continue
            status = str(info.get("status", "running")).strip() or "running"
            started = str(info.get("started_at", "unknown")).strip() or "unknown"
            last_update = str(info.get("last_update", started)).strip() or started
            progress = str(info.get("progress", "")).strip()
            short_id = msg_id[:10]
            if progress:
                rows.append(f"- {short_id} | {status} | {progress}")
            else:
                rows.append(f"- {short_id} | {status} | started {started} | last {last_update}")
        if not rows:
            return "No running tasks for you right now."
        return "Your running tasks:\n" + "\n".join(rows)

    def _is_greeting(self, text: str) -> bool:
        t = (text or "").strip().lower()
        if not t:
            return False
        greeting_words = (
            "hi",
            "hello",
            "hey",
            "yo",
            "good morning",
            "good afternoon",
            "good evening",
            "thanks",
            "thank you",
            "你好",
            "嗨",
            "早上好",
            "下午好",
            "晚上好",
            "谢谢",
        )
        return any(t == w or t.startswith(w + " ") for w in greeting_words)

    def _dingtalk_human_style_prompt(self, *, msg_text: str, user_profile: dict[str, Any]) -> str:
        base = os.getenv(
            "DINGTALK_HUMAN_STYLE_PROMPT",
            (
                "Reply like a real human in chat: concise, natural, a little funny, no status labels, "
                "no task IDs, no markdown headers."
            ),
        ).strip()
        sender_name = str(user_profile.get("sender_name", "")).strip()
        if sender_name and self._is_greeting(msg_text):
            return f"{base} Greeting detected: reply with the user's name '{sender_name}' in the first sentence."
        return base

    def _sanitize_message_metadata(self, metadata: Any) -> dict[str, Any]:
        if not isinstance(metadata, dict):
            return {}
        sanitized: dict[str, Any] = {}
        for key, value in metadata.items():
            if key == "raw_callback":
                continue
            if isinstance(value, dict):
                sanitized[key] = self._sanitize_message_metadata(value)
            elif isinstance(value, list):
                sanitized[key] = [x for x in value if isinstance(x, (str, int, float, bool, dict, list, type(None)))]
            elif isinstance(value, (str, int, float, bool)) or value is None:
                sanitized[key] = value
            else:
                sanitized[key] = str(value)
        return sanitized

    def _extract_tool_trace(self, raw_events: list[dict[str, Any]]) -> list[str]:
        tool_names: list[str] = []
        for event in raw_events:
            if not isinstance(event, dict):
                continue
            params = event.get("params")
            if not isinstance(params, dict):
                continue
            update = params.get("update")
            if not isinstance(update, dict):
                continue
            su = str(update.get("sessionUpdate", "")).lower().strip()
            if su not in {"tool_call_update", "tool_call"}:
                continue
            name_candidates = (
                update.get("toolName"),
                update.get("tool_name"),
                update.get("name"),
                update.get("title"),
                update.get("tool"),
            )
            name = ""
            for candidate in name_candidates:
                if isinstance(candidate, str) and candidate.strip():
                    name = candidate.strip()
                    break
            if not name:
                tool_obj = update.get("tool")
                if isinstance(tool_obj, dict):
                    for key in ("name", "toolName", "tool_name", "id"):
                        value = tool_obj.get(key)
                        if isinstance(value, str) and value.strip():
                            name = value.strip()
                            break
            if not name:
                raw_input = update.get("rawInput")
                if isinstance(raw_input, dict):
                    command = raw_input.get("command")
                    if isinstance(command, list) and command:
                        name = "exec:" + " ".join(str(x) for x in command[-2:])
            if name and name not in tool_names:
                tool_names.append(name)
        return tool_names

    def _extract_tool_calls(self, raw_events: list[dict[str, Any]]) -> list[dict[str, Any]]:
        calls: list[dict[str, Any]] = []
        for event in raw_events:
            if not isinstance(event, dict):
                continue
            params = event.get("params")
            if not isinstance(params, dict):
                continue
            update = params.get("update")
            if not isinstance(update, dict):
                continue
            su = str(update.get("sessionUpdate", "")).lower().strip()
            if su not in {"tool_call", "tool_call_update"}:
                continue
            item: dict[str, Any] = {
                "session_update": su,
                "tool_call_id": str(update.get("toolCallId", "")).strip(),
                "status": str(update.get("status", "")).strip(),
                "title": str(update.get("title", "")).strip(),
            }
            raw_input = update.get("rawInput")
            if isinstance(raw_input, dict):
                command = raw_input.get("command")
                if isinstance(command, list):
                    item["command"] = [str(x) for x in command]
            raw_output = update.get("rawOutput")
            if isinstance(raw_output, dict):
                item["exit_code"] = raw_output.get("exit_code")
                stderr = raw_output.get("stderr")
                if isinstance(stderr, str) and stderr.strip():
                    item["stderr"] = self._preview(stderr, 180)
            calls.append(item)
        return calls

    def _summarize_session_updates(self, raw_events: list[dict[str, Any]]) -> dict[str, int]:
        counts: dict[str, int] = {}
        for event in raw_events:
            if not isinstance(event, dict):
                continue
            params = event.get("params")
            if not isinstance(params, dict):
                continue
            update = params.get("update")
            if not isinstance(update, dict):
                continue
            su = str(update.get("sessionUpdate", "")).strip() or "unknown"
            counts[su] = counts.get(su, 0) + 1
        return counts

    def _build_tool_trace_suffix(self, tool_names: list[str]) -> str:
        if not tool_names:
            return ""
        shown = tool_names[:6]
        suffix = "Tools used: " + ", ".join(shown)
        if len(tool_names) > len(shown):
            suffix += f", +{len(tool_names) - len(shown)} more"
        return suffix

    def run_forever(self) -> None:
        self._recover_inflight_tasks()
        self._log("ACP gateway loop started")
        while True:
            try:
                self._tick()
            except KeyboardInterrupt:
                self._persist()
                self.agent.close()
                self._log("loop stopped")
                return
            except Exception as exc:
                self._log(f"loop error: {exc}")
            time.sleep(self.poll_interval_sec)

    def _tick(self) -> None:
        fetched = self.channel.fetch()
        duplicate_count = sum(1 for m in fetched if m.id in self.processed_ids)
        if duplicate_count:
            self._log(f"skip duplicated messages count={duplicate_count}")
        messages = [m for m in fetched if m.id not in self.processed_ids]
        if not messages:
            return

        if self.process_only_latest and len(messages) > 1:
            self._log(f"process_only_latest enabled: dropping {len(messages) - 1} older messages")
            messages = sorted(messages, key=lambda x: x.ts)[-1:]

        for msg in messages:
            reply_to = self._resolve_reply_target(msg.sender)
            user_profile = self._extract_user_profile(msg)
            session_key: str | None = None
            try:
                if self._is_task_board_query(msg.text):
                    board = self._format_task_board(msg.sender)
                    self.channel.send(board, to=reply_to, message_id=msg.id)
                    self._log(f"task board sent id={msg.id} to={reply_to}")
                    self._mark_processed(msg.id)
                    continue

                self._register_inflight(msg_id=msg.id, sender=msg.sender, reply_to=reply_to, channel=msg.channel)
                self._log(f"inbound id={msg.id} from={msg.sender} text={self._preview(msg.text)}")
                self._log(f"user_sent id={msg.id} raw={msg.text}")
                self._trace(
                    "inbound_start",
                    msg_id=msg.id,
                    sender=msg.sender,
                    channel=msg.channel,
                    thread_id=msg.thread_id or "",
                )
                if self.debug_user_profile and user_profile:
                    self._log(f"user profile id={msg.id} data={json.dumps(user_profile, ensure_ascii=False)}")
                self.interaction_log.append(
                    "inbound_received",
                    msg_id=msg.id,
                    sender=msg.sender,
                    text=msg.text,
                    ts=msg.ts,
                    user_profile=user_profile,
                    message_metadata=msg.metadata,
                )
                if self.allowed_from and msg.sender not in self.allowed_from:
                    self._log(f"skip unauthorized sender={msg.sender} id={msg.id}")
                    self._trace("auth_rejected", msg_id=msg.id, sender=msg.sender)
                    self._mark_processed(msg.id)
                    self.interaction_log.append("inbound_skipped", msg_id=msg.id, sender=msg.sender, reason="unauthorized")
                    continue

                human_mode = self._dingtalk_human_mode(msg.channel)
                self._trace("routing_mode", msg_id=msg.id, human_mode=int(human_mode))
                if not human_mode:
                    self.channel.send(
                        "已收到，正在处理",
                        to=reply_to,
                        message_id=f"ack-{msg.id}",
                    )
                    self._log(f"ack sent id={msg.id} to={reply_to}")

                session_key = build_session_key(msg.channel, msg.sender, msg.thread_id)
                session_id = self.state.session_map.get(session_key)
                self._trace(
                    "session_resolved",
                    msg_id=msg.id,
                    session_key=session_key,
                    has_session_id=int(bool(session_id)),
                    session_id=session_id or "",
                )
                last_progress_ts = 0.0
                wants_step = self._wants_step_by_step(msg.text)

                def on_progress(update_text: str) -> None:
                    nonlocal last_progress_ts
                    self._update_inflight_progress(msg.id, update_text)
                    if human_mode and not wants_step:
                        return
                    now = time.time()
                    if now - last_progress_ts < self.progress_notify_interval_sec:
                        return
                    if msg.channel == "dingtalk":
                        progress = f"处理中：{update_text[:80]}"
                    else:
                        progress = f"任务 {msg.id} 处理中: {update_text[:80]}"
                    self.channel.send(progress, to=reply_to, message_id=f"progress-{msg.id}-{int(now)}")
                    self._log(f"progress sent id={msg.id} text={self._preview(progress, 100)}")
                    last_progress_ts = now

                started = time.time()
                user_text = msg.text
                if self._dingtalk_human_mode(msg.channel):
                    style = self._dingtalk_human_style_prompt(msg_text=msg.text, user_profile=user_profile)
                    user_text = f"{style}\n\nUser message:\n{msg.text}"
                elif self.reply_style_enabled and self.reply_style_prompt:
                    user_text = f"{self.reply_style_prompt}\n\n用户请求：\n{msg.text}"
                req = TaskRequest(
                    trace_id=msg.id,
                    session_key=session_key,
                    user_text=user_text,
                    sender=msg.sender,
                    channel=msg.channel,
                    thread_id=msg.thread_id,
                    session_id=session_id,
                    metadata={
                        "received_ts": msg.ts,
                        "user_profile": user_profile,
                        "message_metadata": self._sanitize_message_metadata(msg.metadata),
                    },
                )
                self._trace("acp_execute_start", msg_id=msg.id, timeout_sec=self.agent.timeout_sec if hasattr(self.agent, "timeout_sec") else "")
                seen_tool_updates: set[tuple[str, str]] = set()

                def on_acp_debug(event: str, data: dict[str, Any]) -> None:
                    if self.debug_acp_events:
                        self._trace(f"acp.{event}", msg_id=msg.id, **data)
                    if not self.tool_progress_notify_enabled:
                        return
                    if event != "session_update":
                        return
                    session_update = str(data.get("session_update", "")).strip().lower()
                    if session_update not in {"tool_call", "tool_call_update"}:
                        return
                    tool_call_id = str(data.get("tool_call_id", "")).strip()
                    status = str(data.get("status", "")).strip() or "in_progress"
                    dedup_key = (tool_call_id or "-", status.lower())
                    if dedup_key in seen_tool_updates:
                        return
                    seen_tool_updates.add(dedup_key)
                    title = str(data.get("title", "")).strip()
                    tool_msg = self._tool_progress_text(channel=msg.channel, title=title, status=status)
                    try:
                        mid = f"tool-{msg.id}-{(tool_call_id or 'x')[:8]}-{status.lower()}"
                        self.channel.send(tool_msg, to=reply_to, message_id=mid)
                        self.interaction_log.append(
                            "tool_progress_notify",
                            msg_id=msg.id,
                            sender=msg.sender,
                            tool_call_id=tool_call_id,
                            status=status,
                            title=title,
                            text=tool_msg,
                        )
                    except Exception as notify_exc:
                        self._log(f"tool progress notify failed id={msg.id} err={notify_exc}")

                result = self.agent.execute(req, on_progress=on_progress, on_debug=on_acp_debug)
                tool_names = self._extract_tool_trace(result.raw_events)
                tool_calls = self._extract_tool_calls(result.raw_events)
                session_update_counts = self._summarize_session_updates(result.raw_events)
                tool_trace_suffix = self._build_tool_trace_suffix(tool_names)
                if tool_trace_suffix:
                    self._log(f"tool_trace id={msg.id} sender={msg.sender} tools={tool_trace_suffix}")
                self._trace(
                    "acp_execute_done",
                    msg_id=msg.id,
                    status=result.status,
                    elapsed_sec=result.elapsed_sec,
                    raw_events=len(result.raw_events),
                    session_update_counts=session_update_counts,
                    tool_calls=tool_calls,
                )
                self.interaction_log.append(
                    "tool_trace",
                    msg_id=msg.id,
                    sender=msg.sender,
                    tools=tool_names,
                    tool_count=len(tool_names),
                    tool_calls=tool_calls,
                    session_update_counts=session_update_counts,
                )

                if result.session_id:
                    self.state.session_map[session_key] = result.session_id

                report_path = self._write_report(msg.id, req.user_text, result)
                if human_mode:
                    summary = (result.summary or "").strip() or (result.output_text or "").strip() or "..."
                else:
                    summary = build_user_summary(result, self.sms_limit)
                if self.show_tool_trace and tool_trace_suffix:
                    summary = f"{summary}\n\n{tool_trace_suffix}"
                self.channel.send(summary, to=reply_to, message_id=msg.id, report_file=str(report_path))
                self._log(
                    f"final sent id={msg.id} to={reply_to} status={result.status} elapsed={result.elapsed_sec}s "
                    f"report={report_path.name}"
                )

                self.interaction_log.append(
                    "exec_finished",
                    msg_id=msg.id,
                    sender=msg.sender,
                    status=result.status,
                    elapsed_sec=int(time.time() - started),
                    summary=result.summary,
                    report_file=str(report_path),
                )
                self._clear_inflight(msg.id)
                self._mark_processed(msg.id)
            except Exception as exc:
                self._log(f"message error id={msg.id} sender={msg.sender} err={exc}")
                self._trace("exec_error", msg_id=msg.id, error=str(exc))
                err_text = str(exc).lower()
                if session_key and (
                    "failed to deserialize response" in err_text
                    or "resource not found" in err_text
                ):
                    self.state.session_map.pop(session_key, None)
                    self._persist()
                    self._log(f"session dropped id={msg.id} session_key={session_key} reason=acp_recoverable_error")
                self.interaction_log.append(
                    "exec_error",
                    msg_id=msg.id,
                    sender=msg.sender,
                    text=msg.text,
                    error=str(exc),
                )
                try:
                    error_text = f"处理失败（id={msg.id}）：{str(exc)[:180]}"
                    if self._dingtalk_human_mode(msg.channel):
                        error_text = "抱歉，处理失败了，请稍后重试。"
                    elif msg.channel == "dingtalk":
                        error_text = "处理失败，请稍后重试。"
                    self.channel.send(
                        error_text,
                        to=reply_to,
                        message_id=f"error-{msg.id}",
                    )
                except Exception as send_exc:
                    self._log(f"error-notify failed id={msg.id} err={send_exc}")
                self._clear_inflight(msg.id)
                self._mark_processed(msg.id)

        self._persist()
        self._log(f"state persisted processed_ids={len(self.state.processed_ids)} sessions={len(self.state.session_map)}")

    def _mark_processed(self, msg_id: str) -> None:
        if msg_id not in self.processed_ids:
            self.processed_ids.add(msg_id)
            self.state.processed_ids.append(msg_id)

    def _persist(self) -> None:
        self.state_store.save(self.state)

    def _register_inflight(self, *, msg_id: str, sender: str, reply_to: str, channel: str) -> None:
        self.state.inflight_tasks[msg_id] = {
            "sender": sender or "",
            "reply_to": reply_to or "",
            "channel": channel or "",
            "status": "running",
            "progress": "",
            "started_at": utc_now(),
            "last_update": utc_now(),
        }
        self._persist()

    def _update_inflight_progress(self, msg_id: str, progress_text: str) -> None:
        info = self.state.inflight_tasks.get(msg_id)
        if not info:
            return
        info["status"] = "running"
        info["progress"] = self._preview(progress_text, 80)
        info["last_update"] = utc_now()
        self.state.inflight_tasks[msg_id] = info
        self._persist()

    def _clear_inflight(self, msg_id: str) -> None:
        if msg_id in self.state.inflight_tasks:
            self.state.inflight_tasks.pop(msg_id, None)
            self._persist()

    def _recover_inflight_tasks(self) -> None:
        if os.getenv("RECOVER_UNFINISHED_TASKS_AS_FAILED", "1").strip() == "0":
            return
        if not self.state.inflight_tasks:
            return
        pending = list(self.state.inflight_tasks.items())
        self._log(f"recover inflight tasks count={len(pending)}")
        for msg_id, info in pending:
            reply_to = str(info.get("reply_to", "")).strip() or str(info.get("sender", "")).strip() or self.remote_user_id
            started_at = str(info.get("started_at", "")).strip() or "unknown"
            fail_text = f"任务因网关重启中断，已标记失败（id={msg_id}，started_at={started_at}）"
            try:
                self.channel.send(fail_text, to=reply_to, message_id=f"error-{msg_id}")
                self._log(f"recovered failed id={msg_id} to={reply_to}")
            except Exception as exc:
                self._log(f"recover failed id={msg_id} err={exc}")
            self.interaction_log.append(
                "exec_recovered_failed",
                msg_id=msg_id,
                sender=str(info.get("sender", "")),
                reply_to=reply_to,
                started_at=started_at,
            )
            self.state.inflight_tasks.pop(msg_id, None)
            self._mark_processed(msg_id)
        self._persist()

    def _write_report(self, msg_id: str, prompt: str, result) -> Path:
        self.report_dir.mkdir(parents=True, exist_ok=True)
        safe_msg_id = re.sub(r"[^A-Za-z0-9._-]+", "_", msg_id).strip("._-") or "msg"
        path = self.report_dir / f"{time.strftime('%Y%m%d_%H%M%S')}_{safe_msg_id}.json"
        payload = {
            "id": msg_id,
            "prompt": prompt,
            "status": result.status,
            "summary": result.summary,
            "elapsed_sec": result.elapsed_sec,
            "session_id": result.session_id,
            "output_text": result.output_text,
            "error_text": result.error_text,
            "raw_events": result.raw_events,
        }
        path.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
        return path
