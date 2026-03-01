#!/usr/bin/env python3
import json
import sys
import time


def send(msg):
    sys.stdout.write(json.dumps(msg, ensure_ascii=False) + "\n")
    sys.stdout.flush()


def main():
    next_session = 1
    for raw in sys.stdin:
        raw = raw.strip()
        if not raw:
            continue
        msg = json.loads(raw)
        method = msg.get("method")
        req_id = msg.get("id")

        if method == "initialize":
            send({"jsonrpc": "2.0", "id": req_id, "result": {"server": {"name": "mock-acp", "version": "0.1"}}})
            continue

        if method == "session/new":
            sid = f"s-{next_session}"
            next_session += 1
            send({"jsonrpc": "2.0", "id": req_id, "result": {"session_id": sid}})
            continue

        if method == "session/prompt":
            params = msg.get("params", {})
            sid = params.get("session_id", "s-unknown")
            prompt = params.get("prompt", "")

            send({"jsonrpc": "2.0", "id": req_id, "result": {"accepted": True, "session_id": sid}})

            send({
                "jsonrpc": "2.0",
                "id": 9001,
                "method": "session/request_permission",
                "params": {
                    "session_id": sid,
                    "kind": "shell",
                    "command": "echo hello",
                },
            })

            time.sleep(0.2)
            send(
                {
                    "jsonrpc": "2.0",
                    "method": "session/update",
                    "params": {
                        "session_id": sid,
                        "status": "running",
                        "message": "正在分析请求",
                    },
                }
            )
            time.sleep(0.2)
            send(
                {
                    "jsonrpc": "2.0",
                    "method": "session/update",
                    "params": {
                        "session_id": sid,
                        "status": "completed",
                        "summary": f"已处理完成: {prompt[:40]}",
                        "output": "hello acp",
                        "is_terminal": True,
                    },
                }
            )
            continue

        if "id" in msg and "result" in msg:
            # response from client, ignore
            continue

        if req_id is not None:
            send({"jsonrpc": "2.0", "id": req_id, "error": {"code": -32601, "message": f"unknown method {method}"}})


if __name__ == "__main__":
    main()
