#!/usr/bin/env python3
"""A fake MCP server for the agent worker demo (streamable HTTP, JSON answers).

It needs the Python standard library only.

POST /mcp needs the header "authorization: Bearer <token>". The token is in a file. A
request with no token or a wrong token gets the status 401. A valid request gets the
JSON-RPC answer of the method:

  initialize, notifications/initialized, ping
  tools/list     the tools vault_lookup and vault_admin
  tools/call     vault_lookup answers with the text "vault: <key> = <value>"

The server writes one JSON line for each request to the log file and to stdout. The line
says if the token matched. It holds the first 12 characters of the SHA-256 of the token
that the client sent, and never the token. It also holds the JSON-RPC methods and the
tool names of the body.

usage: server.py --token-file FILE --log-file FILE [--port 8080]
"""
import argparse
import hashlib
import hmac
import json
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

TOOLS = [
    {"name": "vault_lookup", "description": "Read one value of the vault by its key.",
     "inputSchema": {"type": "object", "properties": {"key": {"type": "string"}}, "required": ["key"]}},
    {"name": "vault_admin", "description": "Change the vault. The DAG does not allow this tool.",
     "inputSchema": {"type": "object", "properties": {}}},
]
VALUES = {"build": "42", "region": "eu-west-1"}


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "fake-mcp"
    token = b""
    log_path = ""

    def log_message(self, *_):
        pass

    def sent_token(self):
        value = self.headers.get("authorization", "")
        return value[len("bearer "):] if value.lower().startswith("bearer ") else ""

    def record(self, matched, status, methods, tools):
        sent = self.sent_token()
        line = json.dumps({
            "time": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "method": self.command,
            "path": self.path,
            "token_matched": matched,
            "token_sha256_prefix": hashlib.sha256(sent.encode()).hexdigest()[:12] if sent else "",
            "rpc_methods": methods,
            "tools": tools,
            "status": status,
            "client": self.client_address[0],
        })
        print(line, flush=True)
        with open(self.log_path, "a") as handle:
            handle.write(line + "\n")

    def reply(self, status, body):
        data = json.dumps(body).encode() if body is not None else b""
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path == "/healthz":
            self.reply(200, {"status": "ok"})
            return
        self.reply(405, {"error": "no stream"})

    def do_DELETE(self):
        self.reply(200, {})

    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(length)
        try:
            message = json.loads(raw or b"{}")
        except ValueError:
            message = {}
        items = message if isinstance(message, list) else [message]
        methods = ",".join(str(item.get("method", "")) for item in items if isinstance(item, dict))
        tools = ",".join(str((item.get("params") or {}).get("name", "")) for item in items
                         if isinstance(item, dict) and item.get("method") == "tools/call")
        on_path = self.path.split("?")[0] == "/mcp"
        matched = on_path and hmac.compare_digest(self.sent_token().encode(), self.token)
        if not matched:
            self.record(False, 401 if on_path else 404, methods, tools)
            self.reply(401 if on_path else 404, {"error": "unauthorized"})
            return
        self.record(True, 200, methods, tools)
        answers = [self.answer(item) for item in items if isinstance(item, dict) and "id" in item]
        if not answers:
            self.reply(202, None)
            return
        self.reply(200, answers if isinstance(message, list) else answers[0])

    def answer(self, item):
        method = item.get("method")
        params = item.get("params") or {}
        base = {"jsonrpc": "2.0", "id": item["id"]}
        if method == "initialize":
            base["result"] = {"protocolVersion": params.get("protocolVersion", "2025-06-18"),
                              "capabilities": {"tools": {}}, "serverInfo": {"name": "fake-mcp", "version": "0"}}
        elif method == "ping":
            base["result"] = {}
        elif method == "tools/list":
            base["result"] = {"tools": TOOLS}
        elif method == "tools/call" and params.get("name") == "vault_lookup":
            key = (params.get("arguments") or {}).get("key", "")
            base["result"] = {"content": [{"type": "text", "text": "vault: %s = %s" % (key, VALUES.get(key, "unknown"))}]}
        elif method == "tools/call":
            base["result"] = {"isError": True, "content": [{"type": "text", "text": "the tool is not available"}]}
        else:
            base["error"] = {"code": -32601, "message": "method not found"}
        return base


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--token-file", required=True)
    parser.add_argument("--log-file", required=True)
    parser.add_argument("--port", type=int, default=8080)
    args = parser.parse_args()
    with open(args.token_file, "rb") as handle:
        token = handle.read().rstrip(b"\n")
    if not token:
        sys.exit("--token-file: the token file is empty")
    Handler.token = token
    Handler.log_path = args.log_file
    server = ThreadingHTTPServer(("0.0.0.0", args.port), Handler)
    print("fake mcp: listening on port %d" % args.port, file=sys.stderr, flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
