#!/usr/bin/env python3
"""A fake Anthropic Messages API for the agent worker demo.

It needs the Python standard library only.

POST /v1/messages compares the key of the request with the real key. The key is in the
header x-api-key, or in the header authorization with the scheme Bearer. The real key
is in a file that only this server can read. The coordinator sends it for the endpoint demo-upstream.
- On a match, it returns a message. The text of the message is a JSON edit plan.
  With "stream": true in the body, the answer is a stream of events that takes about
  2 seconds, with one event every 400 ms.
- Otherwise it returns the status 401 and the error authentication_error.

GET /v1/models returns the list of models, with the same key check. The list is in the
file models.json next to the log file, as a JSON array of names. The server reads the
file again for each request. The default list has two names.

The server writes one JSON line for each request to the log file and to stdout.
The line says if the key matched. It holds the first 12 characters of the SHA-256
of the key that the client sent, and never the key. It also holds the form of the
key (x-api-key or bearer), the header anthropic-beta and the flag stream.

usage: server.py --key-file FILE --log-file FILE [--port 8080]
"""
import argparse
import hashlib
import hmac
import json
import os
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PLAN = {"edits": [{"path": "calc.py", "old": "return a - b", "new": "return a + b"}]}
DEFAULT_MODELS = ["claude-demo", "claude-demo-small"]
# The stream of a message has this many text events, one every STREAM_DELAY seconds.
STREAM_PARTS = 5
STREAM_DELAY = 0.4


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "fake-anthropic"
    key = b""
    log_path = ""

    def log_message(self, *_):
        pass

    def sent_key(self):
        """Return the key of the request and the form that carries it."""
        sent = self.headers.get("x-api-key", "")
        if sent:
            return sent, "x-api-key"
        value = self.headers.get("authorization", "")
        if value.lower().startswith("bearer "):
            return value[len("bearer "):], "bearer"
        return "", ""

    def record(self, matched, status, stream=False):
        sent, form = self.sent_key()
        line = json.dumps({
            "time": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "method": self.command,
            "path": self.path,
            "key_matched": matched,
            "key_sha256_prefix": hashlib.sha256(sent.encode()).hexdigest()[:12] if sent else "",
            "auth_form": form,
            "beta": self.headers.get("anthropic-beta", ""),
            "stream": stream,
            "status": status,
            "client": self.client_address[0],
        })
        print(line, flush=True)
        with open(self.log_path, "a") as handle:
            handle.write(line + "\n")

    def reply(self, status, body):
        data = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def models(self):
        path = os.path.join(os.path.dirname(self.log_path), "models.json")
        try:
            with open(path) as handle:
                names = json.load(handle)
        except (OSError, ValueError):
            names = DEFAULT_MODELS
        return names

    def do_GET(self):
        if self.path == "/healthz":
            self.reply(200, {"status": "ok"})
            return
        if self.path.split("?")[0] == "/v1/models":
            sent, _ = self.sent_key()
            if not hmac.compare_digest(sent.encode(), self.key):
                self.record(False, 401)
                self.reply(401, {"type": "error", "error": {"type": "authentication_error", "message": "invalid key"}})
                return
            self.record(True, 200)
            data = [{"type": "model", "id": name, "display_name": name, "created_at": "2026-01-01T00:00:00Z"} for name in self.models()]
            self.reply(200, {"data": data, "has_more": False, "first_id": data[0]["id"] if data else None,
                             "last_id": data[-1]["id"] if data else None})
            return
        self.reply(404, {"type": "error", "error": {"type": "not_found_error", "message": "not found"}})

    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(length)
        try:
            wants_stream = bool(json.loads(raw or b"{}").get("stream"))
        except (ValueError, AttributeError):
            wants_stream = False
        if self.path.split("?")[0] != "/v1/messages":
            self.reply(404, {"type": "error", "error": {"type": "not_found_error", "message": "not found"}})
            return
        sent, _ = self.sent_key()
        matched = hmac.compare_digest(sent.encode(), self.key)
        if not matched:
            self.record(False, 401, wants_stream)
            self.reply(401, {"type": "error", "error": {"type": "authentication_error", "message": "invalid x-api-key"}})
            return
        self.record(True, 200, wants_stream)
        if wants_stream:
            self.stream_message()
            return
        self.reply(200, {
            "id": "msg_demo_0001",
            "type": "message",
            "role": "assistant",
            "model": "fake-model",
            "content": [{"type": "text", "text": json.dumps(PLAN)}],
            "stop_reason": "end_turn",
            "stop_sequence": None,
            "usage": {"input_tokens": 120, "output_tokens": 30},
        })


def event(handler, name, data):
    handler.wfile.write(("event: %s\ndata: %s\n\n" % (name, json.dumps(data))).encode())
    handler.wfile.flush()


def stream_message(self):
    """Send the plan as a stream of events. The first event leaves at once and the
    text goes out in parts, one every STREAM_DELAY seconds."""
    self.send_response(200)
    self.send_header("Content-Type", "text/event-stream")
    self.send_header("Cache-Control", "no-cache")
    self.send_header("Connection", "close")
    self.end_headers()
    self.close_connection = True
    text = json.dumps(PLAN)
    size = -(-len(text) // STREAM_PARTS)
    event(self, "message_start", {"type": "message_start", "message": {
        "id": "msg_demo_0002", "type": "message", "role": "assistant", "model": "fake-model", "content": [],
        "stop_reason": None, "stop_sequence": None, "usage": {"input_tokens": 120, "output_tokens": 1}}})
    event(self, "content_block_start", {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}})
    for index in range(0, len(text), size):
        time.sleep(STREAM_DELAY)
        event(self, "content_block_delta", {"type": "content_block_delta", "index": 0,
                                            "delta": {"type": "text_delta", "text": text[index:index + size]}})
    event(self, "content_block_stop", {"type": "content_block_stop", "index": 0})
    event(self, "message_delta", {"type": "message_delta", "delta": {"stop_reason": "end_turn", "stop_sequence": None},
                                  "usage": {"output_tokens": 30}})
    event(self, "message_stop", {"type": "message_stop"})


Handler.stream_message = stream_message


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--key-file", required=True)
    parser.add_argument("--log-file", required=True)
    parser.add_argument("--port", type=int, default=8080)
    args = parser.parse_args()
    with open(args.key_file, "rb") as handle:
        key = handle.read().rstrip(b"\n")
    if not key:
        sys.exit("--key-file: the key file is empty")
    Handler.key = key
    Handler.log_path = args.log_file
    server = ThreadingHTTPServer(("0.0.0.0", args.port), Handler)
    print("fake anthropic: listening on port %d" % args.port, file=sys.stderr, flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
