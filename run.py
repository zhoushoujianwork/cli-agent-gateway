#!/usr/bin/env python3
import hashlib
import fcntl
import json
import os
import re
import shlex
import subprocess
import sys
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Callable, Dict, List, Optional, Tuple

# 默认通讯身份（可通过 .env 覆盖）
DEFAULT_REMOTE_USER_ID = "<USER_IMESSAGE_ID>"
DEFAULT_LOCAL_UDP_PORT = 20098
STATUS_COMMANDS = {"状态", "status", "/status", "进度", "最新状态"}


def utc_now() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def usage() -> None:
    print("Usage: python3 run.py <codex_workdir>", file=sys.stderr)


def must_env(name: str) -> str:
    val = os.getenv(name, "").strip()
    if not val:
        print(f"[FATAL] missing env: {name}", file=sys.stderr)
        sys.exit(2)
    return val


def load_state(path: Path) -> Dict:
    if not path.exists():
        return {"processed_ids": []}
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except Exception:
        return {"processed_ids": []}
    if "processed_ids" not in data or not isinstance(data["processed_ids"], list):
        data["processed_ids"] = []
    return data


def save_state(path: Path, state: Dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(state, ensure_ascii=False, indent=2), encoding="utf-8")


def append_jsonl(path: Path, data: Dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("a", encoding="utf-8") as f:
        f.write(json.dumps(data, ensure_ascii=False) + "\n")


def acquire_single_instance_lock(lock_path: Path) -> Optional[object]:
    lock_path.parent.mkdir(parents=True, exist_ok=True)
    f = lock_path.open("w", encoding="utf-8")
    try:
        fcntl.flock(f.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        f.write(str(os.getpid()))
        f.flush()
        return f
    except BlockingIOError:
        f.close()
        return None


def run_shell(cmd: str, env: Dict[str, str] = None, timeout: int = 60) -> Tuple[int, str, str]:
    proc = subprocess.run(
        cmd,
        shell=True,
        text=True,
        capture_output=True,
        env=env,
        timeout=timeout,
    )
    return proc.returncode, proc.stdout, proc.stderr


def sanitize_text(value: str) -> str:
    if value is None:
        return ""
    return str(value).replace("\x00", "").strip()


def normalize_messages(raw: str) -> List[Dict]:
    raw = raw.strip()
    if not raw:
        return []

    msgs: List[Dict] = []
    try:
        parsed = json.loads(raw)
        if isinstance(parsed, list):
            msgs = parsed
        elif isinstance(parsed, dict):
            msgs = [parsed]
    except Exception:
        lines = [ln.strip() for ln in raw.splitlines() if ln.strip()]
        for ln in lines:
            msgs.append(json.loads(ln))

    normalized: List[Dict] = []
    for m in msgs:
        if not isinstance(m, dict):
            continue
        sender = sanitize_text(m.get("from", ""))
        text = sanitize_text(m.get("text", ""))
        ts = sanitize_text(m.get("ts", ""))
        msg_id = sanitize_text(m.get("id", ""))
        if not msg_id:
            sig = f"{sender}|{text}|{ts}"
            msg_id = hashlib.sha256(sig.encode("utf-8")).hexdigest()[:24]
        normalized.append({"id": msg_id, "from": sender, "text": text, "ts": ts})
    return normalized


def clip_sms(text: str, limit: int, suffix: str = "\n…（已截断，详见报告）") -> str:
    # limit <= 0 视为不截断（适合 iMessage）
    if limit <= 0:
        return text
    if len(text) <= limit:
        return text
    if len(suffix) >= limit:
        return text[:limit]
    keep = text[: max(0, limit - len(suffix))]
    # 尽量在换行边界截断，避免半行内容
    last_newline = keep.rfind("\n")
    if last_newline > max(0, int(len(keep) * 0.6)):
        keep = keep[:last_newline]
    return keep.rstrip() + suffix


def parse_csv_list(value: str) -> List[str]:
    cleaned = sanitize_text(value)
    return [x.strip() for x in cleaned.split(",") if x.strip()]


def canonical_msg_id(msg_id: str) -> str:
    s = sanitize_text(msg_id)
    if ":" in s:
        return s.split(":", 1)[1]
    return s


def message_sort_key(msg: Dict) -> Tuple[int, str]:
    msg_id = str(msg.get("id", ""))
    try:
        return (1, str(int(msg_id)))
    except Exception:
        return (0, str(msg.get("ts", "")))


ANSI_RE = re.compile(r"\x1B\[[0-?]*[ -/]*[@-~]")
NOISE_LINE_PREFIXES = (
    "OpenAI Codex ",
    "workdir:",
    "model:",
    "provider:",
    "approval:",
    "sandbox:",
    "reasoning effort:",
    "reasoning summaries:",
    "session id:",
)

DEFAULT_EXEC_POLICY_PROMPT = """请遵守以下执行要求：
1) 先完成用户任务本身，能改代码就直接改，不停留在建议层。
2) 对外回复尽可能简短、可读，优先给结论和关键结果，不要输出冗长过程。
3) 在任务处理中，若有关键进展/阶段完成/遇到阻塞，请主动发送进度更新，不要长时间静默。
4) 任务处理完成后，请明确告知“任务已处理完成”，并简要列出结果。
5) 若任务涉及开发，需同步完成必要交付：相关文档更新、必要验证/测试、代码提交，以及合并到 main 分支（仅在仓库允许、无冲突且未被用户禁止时）。
6) 你具备执行脚本能力。需要中途汇报时，优先调用通知脚本向用户发送进度消息。
7) 所有汇报尽量短句，状态明确（进行中/阻塞/完成）。
8) 若遇到阻塞，明确说明阻塞点并给出下一步最小行动方案。"""


def clean_line(line: str) -> str:
    line = ANSI_RE.sub("", line).strip()
    # 避免超长绝对路径影响可读性
    line = line.replace("/Volumes/1TB/github/", "~/")
    return line


def is_noise_line(line: str) -> bool:
    if not line:
        return True
    if line in {"---", "-", "```"}:
        return True
    if line.lower().endswith("[truncated]") or line.startswith("...[truncated]"):
        return True
    for p in NOISE_LINE_PREFIXES:
        if line.startswith(p):
            return True
    return False


def split_meaningful_lines(text: str) -> List[str]:
    lines: List[str] = []
    for raw in text.splitlines():
        line = clean_line(raw)
        if is_noise_line(line):
            continue
        lines.append(line)
    return lines


def pick_highlights(stdout: str, stderr: str, exit_code: int, max_lines: int = 8) -> List[str]:
    out_lines = split_meaningful_lines(stdout)
    err_lines = split_meaningful_lines(stderr)

    primary = out_lines
    if exit_code != 0 and err_lines:
        primary = err_lines + out_lines

    highlights: List[str] = []
    for ln in primary:
        if ln not in highlights:
            highlights.append(ln)
        if len(highlights) >= max_lines:
            break

    # 没有可读内容时提供兜底
    if not highlights and err_lines:
        highlights = err_lines[:max_lines]
    return highlights


def format_sms_summary(
    msg_id: str,
    exit_code: int,
    elapsed: int,
    stdout: str,
    stderr: str,
    report_path: Path,
    sms_limit: int,
    highlight_max_lines: int,
    timed_out: bool = False,
    timeout_sec: int = 0,
) -> str:
    short_id = msg_id[:8]
    if timed_out:
        status = "⏰ 超时"
    elif exit_code == 0:
        status = "✅ 完成"
    else:
        status = "❌ 失败"

    header = [
        f"[{status}] 任务 {short_id}",
        f"耗时: {elapsed}s" if not timed_out else f"超时: {timeout_sec}s",
    ]

    highlights = pick_highlights(stdout, stderr, exit_code, max_lines=highlight_max_lines)
    body: List[str] = []
    if highlights:
        body.append("要点:")
        body.extend(f"- {ln}" for ln in highlights)
    else:
        body.append("要点:")
        body.append("- 无可展示输出，请查看报告。")

    text = "\n".join(header + [""] + body).strip()
    return clip_sms(text, sms_limit)


def log_text(text: str, limit: int) -> str:
    if not text:
        return ""
    return clip_sms(text.strip(), limit, suffix="\n...(truncated)")


def one_line_preview(text: str, limit: int = 120) -> str:
    s = sanitize_text(text).replace("\n", " ")
    if len(s) <= limit:
        return s
    return s[: max(0, limit - 3)] + "..."


def escape_for_log(text: str) -> str:
    return sanitize_text(text).replace("\\", "\\\\").replace("\n", "\\n")


def send_text_message(
    send_cmd: str,
    to: str,
    text: str,
    message_id: str,
    report_file: str = "",
    timeout: int = 120,
) -> Tuple[int, str]:
    cmd_with_arg = f"{send_cmd} {shlex.quote(text)}"
    cmd_log = (
        f"SMS_TO={shlex.quote(to)} "
        f"MSG_ARG1={shlex.quote(escape_for_log(text))} "
        f"SMS_MESSAGE_ID={shlex.quote(message_id)} "
        f"SMS_REPORT_FILE={shlex.quote(report_file)} "
        f"{cmd_with_arg}"
    )
    print(f"[{utc_now()}] send_cmd: {cmd_log}")
    send_env = os.environ.copy()
    send_env["SMS_TO"] = to
    send_env["SMS_REPORT_FILE"] = report_file
    send_env["SMS_MESSAGE_ID"] = message_id
    send_rc, _, send_err = run_shell(cmd_with_arg, env=send_env, timeout=timeout)
    return send_rc, send_err.strip()


def build_exec_payload(
    user_text: str,
    policy_prompt: str,
    notify_script_path: str,
    notify_to: str,
    notify_log_file: str,
    remote_user_id: str,
    local_udp_port: int,
) -> str:
    user_text = sanitize_text(user_text)
    policy_prompt = sanitize_text(policy_prompt)
    if not policy_prompt:
        return user_text
    return (
        f"{policy_prompt}\n\n"
        f"通讯身份：remote_user_id={remote_user_id}, local_udp_port={local_udp_port}\n\n"
        "你可以在任务处理中主动发进度通知给用户。可用命令（会自动写交互日志）：\n"
        f"bash {shlex.quote(notify_script_path)} "
        f"--to {shlex.quote(notify_to)} "
        '--text "处理中：xxx" '
        f"--log-file {shlex.quote(notify_log_file)}\n\n"
        f"用户请求：\n{user_text}"
    )


def format_startup_message(
    codex_cwd: Path,
    state_file: Path,
    report_dir: Path,
    codex_cmd_raw: str,
    poll_interval: int,
    codex_timeout: int,
    sms_limit: int,
    process_only_latest: bool,
    remote_user_id: str,
    local_udp_port: int,
) -> str:
    mode = "只处理最新一条" if process_only_latest else "处理全部未处理消息"
    lines = [
        "[启动通知] sms-codex-loop 已启动",
        f"时间(UTC): {utc_now()}",
        f"用户端标识: {remote_user_id}",
        f"本机UDP端口: {local_udp_port}",
        f"运行目录: {codex_cwd}",
        f"状态文件: {state_file}",
        f"报告目录: {report_dir}",
        f"执行命令: {codex_cmd_raw}",
        f"轮询间隔: {poll_interval}s",
        f"执行超时: {codex_timeout}s",
        f"回复上限: {sms_limit} 字符",
        f"队列模式: {mode}",
    ]
    return "\n".join(lines)


def send_startup_notifications(
    send_cmd: str,
    targets: List[str],
    text: str,
    sms_limit: int,
) -> None:
    if not targets:
        return
    notify_text = clip_sms(text, sms_limit)
    for to in targets:
        send_rc, send_err = send_text_message(
            send_cmd=send_cmd,
            to=to,
            text=notify_text,
            message_id=f"startup-{int(time.time())}",
            report_file="",
            timeout=120,
        )
        if send_rc != 0:
            print(f"[{utc_now()}] startup notify failed to={to} rc={send_rc} err={send_err.strip()}", file=sys.stderr)
        else:
            print(f"[{utc_now()}] startup notify sent to={to}")


def run_codex_with_progress(
    cmd: List[str],
    cwd: str,
    timeout_sec: int,
    progress_interval_sec: int,
    on_progress: Optional[Callable[[int], None]] = None,
) -> Tuple[Optional[subprocess.CompletedProcess], int, bool]:
    start = time.time()
    proc = subprocess.Popen(
        cmd,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        cwd=cwd,
    )
    timed_out = False
    last_progress = start
    while True:
        rc = proc.poll()
        if rc is not None:
            break
        now = time.time()
        if now - start >= timeout_sec:
            timed_out = True
            proc.kill()
            break
        if on_progress and progress_interval_sec > 0 and (now - last_progress) >= progress_interval_sec:
            on_progress(int(now - start))
            last_progress = now
        time.sleep(1)

    elapsed = int(time.time() - start)
    if timed_out:
        try:
            proc.wait(timeout=5)
        except Exception:
            pass
        return None, elapsed, True

    stdout, stderr = proc.communicate()
    completed = subprocess.CompletedProcess(
        args=cmd,
        returncode=proc.returncode,
        stdout=stdout,
        stderr=stderr,
    )
    return completed, elapsed, False


def main() -> None:
    if len(sys.argv) < 2:
        usage()
        sys.exit(2)

    codex_cwd = Path(sys.argv[1]).expanduser().resolve()
    if not codex_cwd.exists() or not codex_cwd.is_dir():
        print(f"[FATAL] invalid codex_workdir: {codex_cwd}", file=sys.stderr)
        sys.exit(2)

    base_dir = Path(__file__).resolve().parent
    default_fetch_cmd = f"bash {shlex.quote(str(base_dir / 'adapters/imsg_fetch.sh'))}"
    default_send_cmd = f"bash {shlex.quote(str(base_dir / 'adapters/imsg_send.sh'))}"
    notify_script_path = str((base_dir / "adapters/imsg_notify.sh").resolve())

    fetch_cmd = os.getenv("SMS_FETCH_CMD", "").strip() or default_fetch_cmd
    send_cmd = os.getenv("SMS_SEND_CMD", "").strip() or default_send_cmd

    auth_token = os.getenv("AUTH_TOKEN", "").strip()
    remote_user_id = sanitize_text(os.getenv("REMOTE_USER_ID", DEFAULT_REMOTE_USER_ID))
    local_udp_port = int(os.getenv("LOCAL_UDP_PORT", str(DEFAULT_LOCAL_UDP_PORT)))
    allowed_from_list = [remote_user_id]
    allowed_from = set(allowed_from_list)
    poll_interval = int(os.getenv("POLL_INTERVAL_SEC", "5"))
    codex_cmd_raw = sanitize_text(os.getenv("CODEX_CMD", "codex exec"))
    codex_timeout = int(os.getenv("CODEX_TIMEOUT_SEC", "1800"))
    sms_limit = int(os.getenv("SMS_REPLY_MAX_CHARS", "8000"))
    highlight_max_lines = int(os.getenv("SMS_HIGHLIGHT_MAX_LINES", "20"))
    max_processed = int(os.getenv("MAX_PROCESSED_IDS", "2000"))
    process_only_latest = os.getenv("PROCESS_ONLY_LATEST", "0").strip() != "0"
    send_startup_notification = os.getenv("SEND_STARTUP_NOTIFICATION", "1").strip() != "0"
    startup_notify_to = parse_csv_list(os.getenv("STARTUP_NOTIFY_TO", "").strip())
    enable_interaction_log = os.getenv("ENABLE_INTERACTION_LOG", "1").strip() != "0"
    interaction_log_max_text = int(os.getenv("INTERACTION_LOG_MAX_TEXT", "2000"))
    progress_notify_interval = int(os.getenv("PROGRESS_NOTIFY_INTERVAL_SEC", "60"))
    enable_exec_policy_prompt = os.getenv("ENABLE_EXEC_POLICY_PROMPT", "1").strip() != "0"
    exec_policy_prompt = sanitize_text(os.getenv("EXEC_POLICY_PROMPT", DEFAULT_EXEC_POLICY_PROMPT))

    # 运行日志与消息记录默认固定在当前网关项目目录，不落到目标项目目录。
    state_default = base_dir / ".sms_codex_state.json"
    report_default = base_dir / ".sms_codex_reports"
    interaction_default = base_dir / ".sms_codex_interactions.jsonl"
    lock_default = base_dir / ".sms_codex_loop.lock"
    state_file = Path(os.getenv("STATE_FILE", str(state_default))).expanduser().resolve()
    report_dir = Path(os.getenv("REPORT_DIR", str(report_default))).expanduser().resolve()
    lock_file = Path(os.getenv("LOCK_FILE", str(lock_default))).expanduser().resolve()
    interaction_log_file = Path(
        os.getenv("INTERACTION_LOG_FILE", str(interaction_default))
    ).expanduser().resolve()

    lock_handle = acquire_single_instance_lock(lock_file)
    if lock_handle is None:
        print(f"[FATAL] another instance is running; lock_file={lock_file}", file=sys.stderr)
        sys.exit(2)

    state = load_state(state_file)
    processed_ids_list: List[str] = []
    for x in state.get("processed_ids", []):
        s = str(x)
        if s and s not in processed_ids_list:
            processed_ids_list.append(s)
    processed_ids = set(processed_ids_list)
    processed_ids_canonical = {canonical_msg_id(x) for x in processed_ids}

    def mark_processed(msg_id: str) -> None:
        cid = canonical_msg_id(msg_id)
        if msg_id in processed_ids or cid in processed_ids_canonical:
            return
        processed_ids.add(msg_id)
        processed_ids_canonical.add(cid)
        processed_ids_list.append(msg_id)

    def log_event(kind: str, **kwargs: Dict) -> None:
        if not enable_interaction_log:
            return
        record = {"time": utc_now(), "kind": kind}
        record.update(kwargs)
        try:
            append_jsonl(interaction_log_file, record)
        except Exception as log_err:
            print(f"[{utc_now()}] log write failed: {log_err}", file=sys.stderr)

    current_task = {"msg_id": "", "start_ts": 0.0, "preview": ""}

    print(f"[{utc_now()}] loop started")
    print(f"[{utc_now()}] codex_cwd={codex_cwd}")
    print(f"[{utc_now()}] remote_user_id={remote_user_id}")
    print(f"[{utc_now()}] local_udp_port={local_udp_port}")
    print(f"[{utc_now()}] state_file={state_file}")
    print(f"[{utc_now()}] report_dir={report_dir}")
    print(f"[{utc_now()}] lock_file={lock_file}")
    print(f"[{utc_now()}] startup_notify={'on' if send_startup_notification else 'off'}")
    print(f"[{utc_now()}] interaction_log={'on' if enable_interaction_log else 'off'} file={interaction_log_file}")
    log_event(
        "service_started",
        codex_cwd=str(codex_cwd),
        state_file=str(state_file),
        report_dir=str(report_dir),
        interaction_log_file=str(interaction_log_file),
        poll_interval=poll_interval,
        codex_timeout=codex_timeout,
        sms_limit=sms_limit,
        process_only_latest=process_only_latest,
        exec_policy_prompt_enabled=enable_exec_policy_prompt,
        progress_notify_interval_sec=progress_notify_interval,
        remote_user_id=remote_user_id,
        local_udp_port=local_udp_port,
    )

    if send_startup_notification:
        targets = startup_notify_to or allowed_from_list
        startup_text = format_startup_message(
            codex_cwd=codex_cwd,
            state_file=state_file,
            report_dir=report_dir,
            codex_cmd_raw=codex_cmd_raw,
            poll_interval=poll_interval,
            codex_timeout=codex_timeout,
            sms_limit=sms_limit,
            process_only_latest=process_only_latest,
            remote_user_id=remote_user_id,
            local_udp_port=local_udp_port,
        )
        send_startup_notifications(
            send_cmd=send_cmd,
            targets=targets,
            text=startup_text,
            sms_limit=sms_limit,
        )
        for to in targets:
            log_event(
                "startup_notification_sent",
                to=to,
                text=log_text(startup_text, interaction_log_max_text),
            )

    while True:
        try:
            rc, out, err = run_shell(fetch_cmd, timeout=120)
            if rc != 0:
                print(f"[{utc_now()}] fetch failed rc={rc} err={err.strip()}", file=sys.stderr)
                time.sleep(poll_interval)
                continue

            try:
                messages = normalize_messages(out)
            except Exception as parse_err:
                print(f"[{utc_now()}] parse failed: {parse_err}", file=sys.stderr)
                time.sleep(poll_interval)
                continue

            candidates = []
            for m in messages:
                msg_id = m["id"]
                text = m["text"]
                if not text or msg_id in processed_ids or canonical_msg_id(msg_id) in processed_ids_canonical:
                    continue
                candidates.append(m)

            if process_only_latest and candidates:
                candidates = sorted(candidates, key=message_sort_key)
                newest = candidates[-1]
                dropped = candidates[:-1]
                for old in dropped:
                    mark_processed(old["id"])
                if dropped:
                    print(f"[{utc_now()}] skip old backlog count={len(dropped)} keep_latest_id={newest['id']}")
                candidates = [newest]

            for m in candidates:
                msg_id = m["id"]
                sender = m["from"]
                text = m["text"]
                print(
                    f"[{utc_now()}] 收到最新消息 id={msg_id} from={sender} text={one_line_preview(text)}"
                )
                print(f"[{utc_now()}] 用户指令(id={msg_id}): {sanitize_text(text)}")
                print(f"[{utc_now()}] 开始处理消息 id={msg_id} from={sender}")
                log_event(
                    "inbound_received",
                    msg_id=msg_id,
                    sender=sender,
                    text=log_text(text, interaction_log_max_text),
                    ts=m.get("ts", ""),
                )

                if allowed_from and sender not in allowed_from:
                    print(f"[{utc_now()}] skip unauthorized sender={sender} id={msg_id}")
                    log_event("inbound_skipped", msg_id=msg_id, sender=sender, reason="unauthorized_sender")
                    mark_processed(msg_id)
                    continue

                payload = text
                if auth_token:
                    prefix = auth_token + " "
                    if not text.startswith(prefix):
                        print(f"[{utc_now()}] skip bad token sender={sender} id={msg_id}")
                        log_event("inbound_skipped", msg_id=msg_id, sender=sender, reason="bad_token")
                        mark_processed(msg_id)
                        continue
                    payload = text[len(prefix) :]
                    if not payload.strip():
                        log_event("inbound_skipped", msg_id=msg_id, sender=sender, reason="empty_payload")
                        mark_processed(msg_id)
                        continue

                if enable_exec_policy_prompt:
                    payload = build_exec_payload(
                        user_text=payload,
                        policy_prompt=exec_policy_prompt,
                        notify_script_path=notify_script_path,
                        notify_to=remote_user_id,
                        notify_log_file=str(interaction_log_file),
                        remote_user_id=remote_user_id,
                        local_udp_port=local_udp_port,
                    )
                payload = sanitize_text(payload)

                cmd_text = sanitize_text(text).lower()
                if cmd_text in STATUS_COMMANDS:
                    status_text = "当前无进行中的任务。"
                    if current_task["msg_id"]:
                        status_text = (
                            f"当前任务进行中\n"
                            f"- id: {current_task['msg_id']}\n"
                            f"- 已耗时: {int(time.time() - current_task['start_ts'])}s\n"
                            f"- 摘要: {current_task['preview']}"
                        )
                    send_rc, send_err = send_text_message(
                        send_cmd=send_cmd,
                        to=remote_user_id,
                        text=status_text,
                        message_id=f"status-{msg_id}",
                        report_file="",
                    )
                    if send_rc != 0:
                        print(f"[{utc_now()}] status send failed id={msg_id} rc={send_rc} err={send_err}", file=sys.stderr)
                        log_event(
                            "outbound_send_failed",
                            msg_id=msg_id,
                            sender=sender,
                            send_rc=send_rc,
                            send_err=log_text(send_err, interaction_log_max_text),
                            kind_detail="status",
                        )
                    else:
                        log_event(
                            "outbound_sent",
                            msg_id=msg_id,
                            sender=sender,
                            sms_text=log_text(status_text, interaction_log_max_text),
                            kind_detail="status",
                            report_file="",
                        )
                    mark_processed(msg_id)
                    continue

                print(f"[{utc_now()}] exec id={msg_id} from={sender}")
                log_event(
                    "exec_started",
                    msg_id=msg_id,
                    sender=sender,
                    payload_preview=log_text(payload, interaction_log_max_text),
                )
                ack_text = f"已收到消息，开始处理（id={msg_id}）"
                ack_rc, ack_err = send_text_message(
                    send_cmd=send_cmd,
                    to=remote_user_id,
                    text=ack_text,
                    message_id=f"ack-{msg_id}",
                    report_file="",
                )
                if ack_rc != 0:
                    print(f"[{utc_now()}] ack send failed id={msg_id} rc={ack_rc} err={ack_err}", file=sys.stderr)
                    log_event(
                        "outbound_send_failed",
                        msg_id=msg_id,
                        sender=sender,
                        send_rc=ack_rc,
                        send_err=log_text(ack_err, interaction_log_max_text),
                        kind_detail="ack",
                    )
                else:
                    log_event(
                        "outbound_sent",
                        msg_id=msg_id,
                        sender=sender,
                        sms_text=ack_text,
                        kind_detail="ack",
                        report_file="",
                    )

                try:
                    cmd = shlex.split(codex_cmd_raw) + [payload]
                except Exception as split_err:
                    print(f"[{utc_now()}] bad CODEX_CMD id={msg_id} err={split_err}", file=sys.stderr)
                    log_event(
                        "exec_finished",
                        msg_id=msg_id,
                        sender=sender,
                        status="bad_codex_cmd",
                        error=str(split_err),
                    )
                    mark_processed(msg_id)
                    continue
                current_task = {
                    "msg_id": msg_id,
                    "start_ts": time.time(),
                    "preview": one_line_preview(text, 80),
                }
                try:
                    def progress_cb(elapsed_sec: int) -> None:
                        progress_text = f"任务 {msg_id} 处理中，已耗时 {elapsed_sec}s"
                        progress_rc, progress_err = send_text_message(
                            send_cmd=send_cmd,
                            to=remote_user_id,
                            text=progress_text,
                            message_id=f"progress-{msg_id}-{elapsed_sec}",
                            report_file="",
                        )
                        if progress_rc != 0:
                            print(
                                f"[{utc_now()}] progress send failed id={msg_id} rc={progress_rc} err={progress_err}",
                                file=sys.stderr,
                            )
                            log_event(
                                "outbound_send_failed",
                                msg_id=msg_id,
                                sender=sender,
                                send_rc=progress_rc,
                                send_err=log_text(progress_err, interaction_log_max_text),
                                kind_detail="progress",
                            )
                        else:
                            log_event(
                                "outbound_sent",
                                msg_id=msg_id,
                                sender=sender,
                                sms_text=progress_text,
                                kind_detail="progress",
                                report_file="",
                            )

                    proc, elapsed, timed_out = run_codex_with_progress(
                        cmd=cmd,
                        cwd=str(codex_cwd),
                        timeout_sec=codex_timeout,
                        progress_interval_sec=progress_notify_interval,
                        on_progress=progress_cb,
                    )
                    if timed_out:
                        elapsed = codex_timeout
                        full_report = (
                            f"time={utc_now()}\n"
                            f"id={msg_id}\n"
                            f"from={sender}\n"
                            f"cwd={codex_cwd}\n"
                            f"status=timeout\n"
                            f"timeout_sec={codex_timeout}\n"
                        )
                        log_event(
                            "exec_finished",
                            msg_id=msg_id,
                            sender=sender,
                            status="timeout",
                            timeout_sec=codex_timeout,
                            elapsed_sec=elapsed,
                        )
                    else:
                        full_report = (
                            f"time={utc_now()}\n"
                            f"id={msg_id}\n"
                            f"from={sender}\n"
                            f"cwd={codex_cwd}\n"
                            f"cmd={' '.join(shlex.quote(x) for x in cmd[:-1])} <payload>\n"
                            f"exit_code={proc.returncode}\n"
                            f"elapsed_sec={elapsed}\n\n"
                            f"STDOUT:\n{proc.stdout}\n\nSTDERR:\n{proc.stderr}\n"
                        )
                        log_event(
                            "exec_finished",
                            msg_id=msg_id,
                            sender=sender,
                            status="ok" if proc.returncode == 0 else "error",
                            exit_code=proc.returncode,
                            elapsed_sec=elapsed,
                        )
                except Exception as exec_err:
                    elapsed = int(time.time() - current_task["start_ts"])
                    full_report = (
                        f"time={utc_now()}\n"
                        f"id={msg_id}\n"
                        f"from={sender}\n"
                        f"cwd={codex_cwd}\n"
                        f"status=exec_error\n"
                        f"elapsed_sec={elapsed}\n"
                        f"error={exec_err}\n"
                    )
                    proc = subprocess.CompletedProcess(
                        args=cmd,
                        returncode=1,
                        stdout="",
                        stderr=f"internal exec error: {exec_err}",
                    )
                    timed_out = False
                    log_event(
                        "exec_finished",
                        msg_id=msg_id,
                        sender=sender,
                        status="exec_error",
                        error=str(exec_err),
                        elapsed_sec=elapsed,
                    )
                finally:
                    current_task = {"msg_id": "", "start_ts": 0.0, "preview": ""}

                report_dir.mkdir(parents=True, exist_ok=True)
                report_path = report_dir / f"{datetime.now().strftime('%Y%m%d_%H%M%S')}_{msg_id}.txt"
                report_path.write_text(full_report, encoding="utf-8")

                if timed_out:
                    sms_text = format_sms_summary(
                        msg_id=msg_id,
                        exit_code=124,
                        elapsed=elapsed,
                        stdout="",
                        stderr="",
                        report_path=report_path,
                        sms_limit=sms_limit,
                        highlight_max_lines=highlight_max_lines,
                        timed_out=True,
                        timeout_sec=codex_timeout,
                    )
                else:
                    sms_text = format_sms_summary(
                        msg_id=msg_id,
                        exit_code=proc.returncode,
                        elapsed=elapsed,
                        stdout=proc.stdout,
                        stderr=proc.stderr,
                        report_path=report_path,
                        sms_limit=sms_limit,
                        highlight_max_lines=highlight_max_lines,
                    )
                send_rc, send_err = send_text_message(
                    send_cmd=send_cmd,
                    to=remote_user_id,
                    text=sms_text,
                    message_id=msg_id,
                    report_file=str(report_path),
                    timeout=120,
                )
                if send_rc != 0:
                    print(f"[{utc_now()}] send failed id={msg_id} rc={send_rc} err={send_err}", file=sys.stderr)
                    log_event(
                        "outbound_send_failed",
                        msg_id=msg_id,
                        sender=sender,
                        send_rc=send_rc,
                        send_err=log_text(send_err, interaction_log_max_text),
                        report_file=str(report_path),
                    )
                else:
                    print(f"[{utc_now()}] done id={msg_id} report={report_path}")
                    log_event(
                        "outbound_sent",
                        msg_id=msg_id,
                        sender=sender,
                        sms_text=log_text(sms_text, interaction_log_max_text),
                        report_file=str(report_path),
                    )

                mark_processed(msg_id)

            if len(processed_ids_list) > max_processed:
                processed_ids_list = processed_ids_list[-max_processed:]
                processed_ids = set(processed_ids_list)
                processed_ids_canonical = {canonical_msg_id(x) for x in processed_ids}

            state["processed_ids"] = processed_ids_list
            save_state(state_file, state)
            time.sleep(poll_interval)

        except KeyboardInterrupt:
            print(f"[{utc_now()}] loop stopped")
            log_event("service_stopped", reason="keyboard_interrupt")
            state["processed_ids"] = processed_ids_list
            save_state(state_file, state)
            return
        except Exception as fatal:
            print(f"[{utc_now()}] loop error: {fatal}", file=sys.stderr)
            time.sleep(poll_interval)


if __name__ == "__main__":
    main()
