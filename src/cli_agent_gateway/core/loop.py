from __future__ import annotations

import json
import time
from pathlib import Path

from cli_agent_gateway.agents.base import AgentAdapter
from cli_agent_gateway.channels.base import ChannelAdapter
from cli_agent_gateway.core.contracts import TaskRequest, utc_now
from cli_agent_gateway.core.router import build_session_key
from cli_agent_gateway.infra.interaction_log import InteractionLog
from cli_agent_gateway.infra.state_store import GatewayState, JsonStateStore
from cli_agent_gateway.services.summarizer import build_user_summary


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

        self.state: GatewayState = self.state_store.load()
        self.processed_ids = set(self.state.processed_ids)

    def run_forever(self) -> None:
        print(f"[{utc_now()}] ACP gateway loop started")
        while True:
            try:
                self._tick()
            except KeyboardInterrupt:
                self._persist()
                self.agent.close()
                print(f"[{utc_now()}] loop stopped")
                return
            except Exception as exc:
                print(f"[{utc_now()}] loop error: {exc}")
            time.sleep(self.poll_interval_sec)

    def _tick(self) -> None:
        messages = [m for m in self.channel.fetch() if m.id not in self.processed_ids]
        if not messages:
            return

        if self.process_only_latest and len(messages) > 1:
            messages = sorted(messages, key=lambda x: x.ts)[-1:]

        for msg in messages:
            self.interaction_log.append(
                "inbound_received",
                msg_id=msg.id,
                sender=msg.sender,
                text=msg.text,
                ts=msg.ts,
            )
            if self.allowed_from and msg.sender not in self.allowed_from:
                self._mark_processed(msg.id)
                self.interaction_log.append("inbound_skipped", msg_id=msg.id, sender=msg.sender, reason="unauthorized")
                continue

            self.channel.send(
                f"已收到消息，开始处理（id={msg.id}）",
                to=self.remote_user_id,
                message_id=f"ack-{msg.id}",
            )

            session_key = build_session_key(msg.channel, msg.sender, msg.thread_id)
            session_id = self.state.session_map.get(session_key)
            last_progress_ts = 0.0

            def on_progress(update_text: str) -> None:
                nonlocal last_progress_ts
                now = time.time()
                if now - last_progress_ts < self.progress_notify_interval_sec:
                    return
                progress = f"任务 {msg.id} 处理中: {update_text[:160]}"
                self.channel.send(progress, to=self.remote_user_id, message_id=f"progress-{msg.id}-{int(now)}")
                last_progress_ts = now

            started = time.time()
            req = TaskRequest(
                trace_id=msg.id,
                session_key=session_key,
                user_text=msg.text,
                sender=msg.sender,
                channel=msg.channel,
                thread_id=msg.thread_id,
                session_id=session_id,
                metadata={"received_ts": msg.ts},
            )
            result = self.agent.execute(req, on_progress=on_progress)

            if result.session_id:
                self.state.session_map[session_key] = result.session_id

            report_path = self._write_report(msg.id, req.user_text, result)
            summary = build_user_summary(result, self.sms_limit)
            self.channel.send(summary, to=self.remote_user_id, message_id=msg.id, report_file=str(report_path))

            self.interaction_log.append(
                "exec_finished",
                msg_id=msg.id,
                sender=msg.sender,
                status=result.status,
                elapsed_sec=int(time.time() - started),
                summary=result.summary,
                report_file=str(report_path),
            )
            self._mark_processed(msg.id)

        self._persist()

    def _mark_processed(self, msg_id: str) -> None:
        if msg_id not in self.processed_ids:
            self.processed_ids.add(msg_id)
            self.state.processed_ids.append(msg_id)

    def _persist(self) -> None:
        self.state_store.save(self.state)

    def _write_report(self, msg_id: str, prompt: str, result) -> Path:
        self.report_dir.mkdir(parents=True, exist_ok=True)
        path = self.report_dir / f"{time.strftime('%Y%m%d_%H%M%S')}_{msg_id}.json"
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
