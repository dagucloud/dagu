#!/usr/bin/env python3
"""The fake claude CLI of the conformance tests of Spec 094, "The gateway".

The script claude runs this file when the file scenario exists in the directory
AITEST_DIR (or FAKE_CLAUDE_DIR). The scenario names what the CLI does with the model
API that ANTHROPIC_BASE_URL names. The CLI sends the key ANTHROPIC_API_KEY, as the real
CLI does. It uses the standard library only.

Each scenario writes its findings to files of the directory, and prints the stream-json
lines of a finished attempt. The directory is the only channel to the test, because the
CLI can run as a restricted user.

  stream    one request; the answer is a stream of events. The file chunks.log has the
            time of each chunk, and the file first_chunk exists after the first one.
  large     one request; the answer is a large stream. The file large.json has the number
            of bytes and the SHA-256 of the answer.
  many      one request; the answer is a stream of many small events.
  body      one request with a large body; the file body.json has the status.
  hostile   a list of requests that the relay must refuse; the file hostile.json has the
            status of each.
  block     one request; the answer never ends. The CLI waits until Dagu stops it.
  delete    two requests, with a wait for the file go between them.
  status    two requests; the answers have the statuses 401 and 429.
  redirect  one request; the provider answers with a redirect.
  limits    a request with a body over the limit, and a request with a response over it.
  ok        one request with a small answer.
  none      no request.
  mcp       calls the MCP servers of its --mcp-config: the named server docs through the
            relay and the inline server inline directly. The file mcp.json has the
            answers, and the file mcp_config.json has a copy of the config.
  mcp_stream  one GET on the named server; the answer is a stream of events. The file
            first_chunk exists after the first one, and the file mcp_stream.json has the end.
  mcp_block   one GET on the named server; the answer never ends.
"""

import hashlib
import http.client
import json
import os
import socket
import sys
import time
import urllib.parse

DIR = os.environ.get("AITEST_DIR") or os.environ.get("FAKE_CLAUDE_DIR") or "."
BASE = os.environ.get("ANTHROPIC_BASE_URL", "")
KEY = os.environ.get("ANTHROPIC_API_KEY", "")


def write(name, text):
    with open(os.path.join(DIR, name), "w") as handle:
        handle.write(text)


def write_json(name, value):
    write(name, json.dumps(value, sort_keys=True))


def target():
    parsed = urllib.parse.urlparse(BASE)
    return parsed.hostname, parsed.port


def post(body, path="/v1/messages", headers=None, method="POST", key=None):
    host, port = target()
    connection = http.client.HTTPConnection(host, port, timeout=120)
    merged = {"content-type": "application/json", "x-api-key": KEY if key is None else key,
              "anthropic-version": "2023-06-01"}
    merged.update(headers or {})
    data = body if isinstance(body, (bytes, type(None))) else json.dumps(body).encode()
    connection.request(method, path, body=data, headers=merged)
    return connection, connection.getresponse()


def read_all(response):
    digest = hashlib.sha256()
    total = 0
    chunks = []
    while True:
        data = response.read1(65536)
        if not data:
            break
        digest.update(data)
        total += len(data)
        chunks.append((time.monotonic_ns(), len(data)))
    return total, digest.hexdigest(), chunks


def scenario_stream():
    connection, response = post({"mode": "sse"})
    lines = []
    total = 0
    while True:
        data = response.read1(65536)
        if not data:
            break
        if total == 0:
            write("first_chunk", str(time.time_ns()))
        total += len(data)
        lines.append("%d %d" % (time.monotonic_ns(), len(data)))
        write("chunks.log", "\n".join(lines))
    connection.close()
    write_json("stream.json", {"status": response.status, "bytes": total, "chunks": len(lines),
                               "content_type": response.getheader("content-type")})
    return "stream bytes=%d" % total


def scenario_large():
    megabytes = int(open(os.path.join(DIR, "large_mib")).read().strip())
    connection, response = post({"mode": "large", "mib": megabytes})
    started = time.monotonic()
    total, digest, chunks = read_all(response)
    write_json("large.json", {"status": response.status, "bytes": total, "sha256": digest,
                              "chunks": len(chunks), "seconds": time.monotonic() - started})
    connection.close()
    return "large bytes=%d" % total


def scenario_many():
    connection, response = post({"mode": "many", "events": 20000})
    total, digest, chunks = read_all(response)
    write_json("many.json", {"status": response.status, "bytes": total, "sha256": digest, "chunks": len(chunks)})
    connection.close()
    return "many bytes=%d" % total


def scenario_body():
    size = int(open(os.path.join(DIR, "body_mib")).read().strip()) * 1024 * 1024
    connection, response = post(b'{"mode":"size","pad":"' + b"a" * size + b'"}')
    payload = response.read().decode()
    write_json("body.json", {"status": response.status, "body": payload})
    connection.close()
    return "body status=%d" % response.status


def raw(request):
    host, port = target()
    with socket.create_connection((host, port), timeout=10) as connection:
        connection.sendall(request)
        connection.settimeout(10)
        data = b""
        try:
            while True:
                piece = connection.recv(65536)
                if not piece:
                    break
                data += piece
        except socket.timeout:
            pass
    head = data.split(b"\r\n", 1)[0].decode(errors="replace")
    parts = head.split(" ")
    return int(parts[1]) if len(parts) > 1 and parts[1].isdigit() else 0


def scenario_hostile():
    host, port = target()
    results = {}

    def status_of(name, fn):
        try:
            results[name] = fn()
        except Exception as error:  # the relay can also close the connection
            results[name] = "error:%s" % type(error).__name__

    def req(path, method="POST", headers=None, key=None, body=None):
        connection, response = post(body if body is not None else {"mode": "ok"}, path=path, method=method, headers=headers, key=key)
        response.read()
        connection.close()
        return response.status

    status_of("allowed", lambda: req("/v1/messages"))
    status_of("other_path", lambda: req("/v1/complete"))
    status_of("admin_path", lambda: req("/admin"))
    status_of("openai_path", lambda: req("/v1/chat/completions"))
    status_of("other_method", lambda: req("/v1/messages", method="GET"))
    status_of("delete_method", lambda: req("/v1/messages", method="DELETE"))
    status_of("other_query", lambda: req("/v1/messages?api_key=x"))
    status_of("no_secret", lambda: req("/v1/messages", key=""))
    status_of("wrong_secret", lambda: req("/v1/messages", key="sk-ant-not-the-secret"))
    status_of("host_header", lambda: req("/v1/messages", headers={"host": "api.anthropic.com"}))
    body = b'{"mode":"ok"}'
    head = "Content-Type: application/json\r\nContent-Length: %d\r\nX-Api-Key: %s\r\nConnection: close\r\n" % (len(body), KEY)
    status_of("absolute_form", lambda: raw(("POST http://api.anthropic.com/v1/messages HTTP/1.1\r\nHost: api.anthropic.com\r\n" + head + "\r\n").encode() + body))
    status_of("absolute_form_relay", lambda: raw(("POST %s/v1/messages HTTP/1.1\r\nHost: %s:%d\r\n" % (BASE, host, port) + head + "\r\n").encode() + body))
    status_of("connect", lambda: raw(("CONNECT api.anthropic.com:443 HTTP/1.1\r\nHost: api.anthropic.com:443\r\nX-Api-Key: %s\r\nConnection: close\r\n\r\n" % KEY).encode()))
    write_json("hostile.json", results)
    return "hostile done"


def scenario_block():
    connection, response = post({"mode": "block"})
    write("first_chunk", str(time.time_ns()))
    while True:
        data = response.read1(65536)
        if not data:
            break
    connection.close()
    return "block ended"


def scenario_delete():
    connection, response = post({"mode": "ok"})
    response.read()
    connection.close()
    write_json("first.json", {"status": response.status})
    write("first_done", "1")
    deadline = time.time() + 60
    while not os.path.exists(os.path.join(DIR, "go")) and time.time() < deadline:
        time.sleep(0.05)
    connection, response = post({"mode": "ok"})
    payload = response.read().decode()
    connection.close()
    write_json("second.json", {"status": response.status, "body": payload})
    return "delete second=%d" % response.status


def scenario_status():
    results = []
    for code in (401, 429):
        connection, response = post({"mode": "status", "status": code})
        response.read()
        results.append({"status": response.status, "retry_after": response.getheader("retry-after"),
                        "set_cookie": response.getheader("set-cookie")})
        connection.close()
    write_json("status.json", results)
    return "status done"


def scenario_redirect():
    connection, response = post({"mode": "redirect"})
    payload = response.read().decode()
    connection.close()
    write_json("redirect.json", {"status": response.status, "body": payload, "location": response.getheader("location")})
    return "redirect status=%d" % response.status


def scenario_limits():
    results = {}
    connection, response = post(b'{"mode":"size","pad":"' + b"a" * 4096 + b'"}')
    results["big_request"] = {"status": response.status, "body": response.read().decode()}
    connection.close()
    connection, response = post({"mode": "large", "mib": 1})
    try:
        total, _, _ = read_all(response)
        results["big_response"] = {"status": response.status, "bytes": total, "complete": False}
    except Exception as error:
        results["big_response"] = {"status": response.status, "error": type(error).__name__}
    connection.close()
    write_json("limits.json", results)
    return "limits done"


def scenario_ok():
    connection, response = post({"mode": "ok"})
    payload = response.read().decode()
    connection.close()
    write_json("ok.json", {"status": response.status, "body": payload})
    return "ok status=%d" % response.status


def scenario_none():
    return "none"


def mcp_servers():
    """The servers of the MCP config of the CLI. The CLI keeps a copy of the file, which Dagu deletes."""
    args = sys.argv[1:]
    path = args[args.index("--mcp-config") + 1]
    with open(path) as handle:
        text = handle.read()
    write("mcp_config.json", text)
    return json.loads(text)["mcpServers"]


def mcp_open(server, method, data=None, headers=None, path=None, raw_headers=None):
    parsed = urllib.parse.urlparse(server["url"])
    connection = http.client.HTTPConnection(parsed.hostname, parsed.port, timeout=120)
    merged = {"content-type": "application/json", "accept": "application/json, text/event-stream"}
    merged.update({name.lower(): value for name, value in server.get("headers", {}).items()})
    merged.update({name.lower(): value for name, value in (headers or {}).items()})
    for name in raw_headers or []:
        merged.pop(name, None)
    target_path = path if path is not None else parsed.path + ("?" + parsed.query if parsed.query else "")
    connection.request(method, target_path, body=data, headers=merged)
    return connection, connection.getresponse()


def mcp_call(server, payload, **kwargs):
    data = payload if isinstance(payload, bytes) else json.dumps(payload).encode()
    try:
        connection, response = mcp_open(server, kwargs.pop("method", "POST"), data, **kwargs)
        body = response.read().decode()
        connection.close()
        return {"status": response.status, "body": body, "session": response.getheader("mcp-session-id")}
    except Exception as error:  # the relay can also close the connection
        return {"status": 0, "error": type(error).__name__}


def scenario_mcp():
    servers = mcp_servers()
    docs, inline = servers["docs"], servers["inline"]
    root = "%s://%s" % (urllib.parse.urlparse(docs["url"]).scheme, urllib.parse.urlparse(docs["url"]).netloc)

    def call(tool, ident):
        return {"jsonrpc": "2.0", "id": ident, "method": "tools/call", "params": {"name": tool, "arguments": {"query": "dagu"}}}

    results = {}
    results["initialize"] = mcp_call(docs, {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2025-06-18"}})
    results["list"] = mcp_call(docs, {"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
    results["allowed"] = mcp_call(docs, call("search", 3))
    results["refused"] = mcp_call(docs, call("drop_tables", 4))
    results["batch_refused"] = mcp_call(docs, [call("search", 5), call("drop_tables", 6)])
    results["batch_ok"] = mcp_call(docs, [call("search", 7), call("search", 8)])
    results["invalid_json"] = mcp_call(docs, b"this is not json")
    results["invalid_rpc"] = mcp_call(docs, {"id": 9, "method": "tools/call"})
    results["delete"] = mcp_call(docs, b"", method="DELETE")
    # A body that a case-sensitive server and a Go parser read in two ways: the relay
    # refuses it, and the forbidden tool never reaches the server.
    results["ambiguous_method_case"] = mcp_call(
        docs, b'{"jsonrpc":"2.0","id":21,"method":"tools/call","METHOD":"ping","params":{"name":"drop_tables"}}')
    results["ambiguous_name_case"] = mcp_call(
        docs, b'{"jsonrpc":"2.0","id":22,"method":"tools/call","params":{"name":"drop_tables","NAME":"search"}}')
    results["ambiguous_duplicate"] = mcp_call(
        docs, b'{"jsonrpc":"2.0","id":23,"method":"ping","method":"tools/call","params":{"name":"drop_tables"}}')
    results["ambiguous_batch"] = mcp_call(
        docs, b'[{"jsonrpc":"2.0","id":24,"method":"tools/call","params":{"name":"search"}},'
              b'{"jsonrpc":"2.0","id":25,"method":"tools/call","params":{"name":"drop_tables","name":"search"}}]')
    results["inline"] = mcp_call(inline, {"jsonrpc": "2.0", "id": 10, "method": "tools/call", "params": {"name": "ping", "arguments": {}}})

    # What the relay must refuse. The secret of the relay is in the config of the CLI.
    results["other_path"] = mcp_call(docs, call("search", 11), path="/mcp/docs/extra")
    results["query"] = mcp_call(docs, call("search", 12), path="/mcp/docs?token=x")
    results["other_server"] = mcp_call(docs, call("search", 13), path="/mcp/wiki")
    results["model_path"] = mcp_call(docs, call("search", 14), path="/v1/messages")
    results["put"] = mcp_call(docs, call("search", 15), method="PUT")
    results["no_secret"] = mcp_call(docs, call("search", 16), raw_headers=["authorization"])
    results["wrong_secret"] = mcp_call(docs, call("search", 17), headers={"authorization": "Bearer not-the-secret"})
    results["host_header"] = mcp_call(docs, call("search", 18), headers={"host": "docs.example.com"})
    results["x_headers"] = mcp_call(docs, call("search", 19), headers={"x-evil": "1", "cookie": "a=b"})
    results["two_secrets"] = mcp_call(docs, call("search", 20), headers={"x-api-key": "placeholder"})
    write_json("mcp.json", results)
    return "mcp done"


def scenario_mcp_stream():
    docs = mcp_servers()["docs"]
    connection, response = mcp_open(docs, "GET", None, headers={"accept": "text/event-stream"})
    total = 0
    chunks = 0
    while True:
        data = response.read1(65536)
        if not data:
            break
        if total == 0:
            write("first_chunk", str(time.time_ns()))
        total += len(data)
        chunks += 1
    connection.close()
    write_json("mcp_stream.json", {"status": response.status, "bytes": total, "chunks": chunks})
    return "mcp stream bytes=%d" % total


def scenario_mcp_block():
    docs = mcp_servers()["docs"]
    connection, response = mcp_open(docs, "GET", None, headers={"accept": "text/event-stream"})
    write("first_chunk", str(time.time_ns()))
    while True:
        data = response.read1(65536)
        if not data:
            break
    connection.close()
    return "mcp block ended"


SCENARIOS = {name[len("scenario_"):]: fn for name, fn in globals().items() if name.startswith("scenario_")}


def main():
    name = open(os.path.join(DIR, "scenario")).read().strip()
    # What the CLI sees of its own environment, for the tests that look for a key.
    write_json("cli_env.json", dict(os.environ))
    write_json("cli_argv.json", sys.argv[1:])
    text = SCENARIOS[name]()
    print(json.dumps({"type": "system", "subtype": "init", "model": "sonnet", "mcp_servers": []}))
    print(json.dumps({"type": "result", "subtype": "success", "is_error": False, "num_turns": 1, "result": text}))


if __name__ == "__main__":
    main()
