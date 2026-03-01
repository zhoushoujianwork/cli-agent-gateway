from __future__ import annotations

from cli_agent_gateway.core.contracts import TaskResult


def clip_text(text: str, limit: int) -> str:
    if limit <= 0 or len(text) <= limit:
        return text
    suffix = "\n...(truncated)"
    if limit <= len(suffix):
        return text[:limit]
    return text[: limit - len(suffix)].rstrip() + suffix


def build_user_summary(result: TaskResult, sms_limit: int) -> str:
    status_map = {
        "ok": "✅ 完成",
        "error": "❌ 失败",
        "timeout": "⏰ 超时",
        "cancelled": "⛔ 取消",
    }
    header = [
        f"[{status_map.get(result.status, 'ℹ️ 结果')}] 任务 {result.trace_id[:8]}",
        f"耗时: {result.elapsed_sec}s",
    ]
    body = result.summary.strip() or "任务已结束。"
    if result.output_text.strip():
        body = body + "\n\n输出摘录:\n" + result.output_text.strip()
    return clip_text("\n".join(header + ["", body]).strip(), sms_limit)
