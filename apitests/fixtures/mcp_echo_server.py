import json
import os
import sys

TOOL = os.environ.get("ECHO_TOOL_NAME", "echo")


def reply(msg_id, result):
    sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": msg_id, "result": result}) + "\n")
    sys.stdout.flush()


for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    msg = json.loads(line)
    method = msg.get("method")
    msg_id = msg.get("id")
    if msg_id is None:
        continue
    if method == "initialize":
        reply(msg_id, {
            "protocolVersion": msg.get("params", {}).get("protocolVersion", "2025-06-18"),
            "capabilities": {"tools": {}},
            "serverInfo": {"name": "apitest-echo", "version": "1.0.0"},
        })
    elif method == "tools/list":
        reply(msg_id, {"tools": [{
            "name": TOOL,
            "description": "Echo the text back",
            "inputSchema": {"type": "object", "properties": {"text": {"type": "string"}}},
        }]})
    elif method == "tools/call":
        text = msg.get("params", {}).get("arguments", {}).get("text", "")
        reply(msg_id, {"content": [{"type": "text", "text": text}]})
    elif method == "ping":
        reply(msg_id, {})
    else:
        sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": msg_id, "error": {"code": -32601, "message": "method not found"}}) + "\n")
        sys.stdout.flush()
