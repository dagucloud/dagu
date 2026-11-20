#!/usr/bin/env python3
"""The MCP client of the fake claude CLI (conformance tests of Spec 091).

It uses the standard library only. The fake claude starts it for a scenario that
starts with bridge_. It does what a CLI agent does with the tool bridge of Dagu:
it reads the file after --mcp-config (a copy that the fake claude kept), speaks
JSON-RPC over HTTP POST with the bearer secret, and calls the tools of its plan.
It records what it saw in the directory FAKE_CLAUDE_DIR, and prints the
stream-json lines of a canned answer.

usage: mcp_client.py <dir> <n> <plan file>

The plan is a JSON object. All keys are optional.
  calls            list of {"tool": name, "arguments": object}. Run in order.
  refuse           list of {"name": text, "authorization": value or null}. Each is
                   one request that the bridge must refuse. The fake records the
                   status code.
  bridge_status    the status of the server dagu in the event system/init.
                   The default is "connected".
  extra_tool_use   names of tool_use blocks that the stream adds after the calls.
  secret_in_stream when true, the stream carries the secret of the bridge in a line.
  stderr_secret    when true, the script writes the secret to its stderr.

Files that it writes in <dir> (n is the number of the call of the fake claude):
  url.<n>                the URL of the bridge
  tools.<n>.json         the result of tools/list
  call.<n>.<i>.json      the result of the call i, or {"error": ...}
  refused.<n>.json       {name: status code} of the refused requests
  sse.<n>                "yes" when one reply came as an event stream
"""
import json
import os
import sys
import urllib.error
import urllib.request

PROTOCOL_VERSION = "2025-06-18"

# A bridge is on the loopback address. A proxy of the environment must not
# touch the requests.
OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def read_events(text):
    """Return the JSON messages of an event stream (Server-Sent Events)."""
    messages = []
    data = []
    for line in text.splitlines():
        if line == "":
            if data:
                messages.append(json.loads("\n".join(data)))
                data = []
        elif line.startswith("data:"):
            data.append(line[len("data:"):].lstrip(" "))
    if data:
        messages.append(json.loads("\n".join(data)))
    return messages


class Reply:
    def __init__(self, status, headers, message, event_stream):
        self.status = status
        self.headers = headers
        self.message = message
        self.event_stream = event_stream


def post(url, authorization, payload, session=None, version=None):
    """Send one JSON-RPC message. A reply is JSON or an event stream; both work."""
    request = urllib.request.Request(url, data=json.dumps(payload).encode(), method="POST")
    request.add_header("Content-Type", "application/json")
    request.add_header("Accept", "application/json, text/event-stream")
    if authorization is not None:
        request.add_header("Authorization", authorization)
    if session:
        request.add_header("Mcp-Session-Id", session)
    if version:
        request.add_header("MCP-Protocol-Version", version)
    try:
        response = OPENER.open(request, timeout=60)
    except urllib.error.HTTPError as error:
        error.read()
        return Reply(error.code, error.headers, None, False)
    body = response.read().decode()
    content_type = response.headers.get("Content-Type", "")
    message = None
    event_stream = "text/event-stream" in content_type
    if event_stream:
        for candidate in read_events(body):
            if candidate.get("id") == payload.get("id"):
                message = candidate
    elif body.strip():
        message = json.loads(body)
    return Reply(response.status, response.headers, message, event_stream)


class Client:
    def __init__(self, url, authorization):
        self.url = url
        self.authorization = authorization
        self.session = None
        self.version = None
        self.next_id = 0
        self.event_stream = False

    def request(self, method, params=None):
        self.next_id += 1
        payload = {"jsonrpc": "2.0", "id": self.next_id, "method": method}
        if params is not None:
            payload["params"] = params
        reply = post(self.url, self.authorization, payload, self.session, self.version)
        if reply.status != 200 or reply.message is None:
            raise RuntimeError("%s: status %s" % (method, reply.status))
        self.event_stream = self.event_stream or reply.event_stream
        if "error" in reply.message:
            raise RuntimeError("%s: %s" % (method, json.dumps(reply.message["error"])))
        if method == "initialize":
            self.session = reply.headers.get("Mcp-Session-Id")
            self.version = reply.message["result"].get("protocolVersion", PROTOCOL_VERSION)
        return reply.message["result"]

    def notify(self, method):
        reply = post(self.url, self.authorization, {"jsonrpc": "2.0", "method": method}, self.session, self.version)
        if reply.status not in (200, 202):
            raise RuntimeError("%s: status %s" % (method, reply.status))


def write(directory, name, content):
    with open(os.path.join(directory, name), "w") as handle:
        handle.write(content)


def main():
    directory, n, plan_path = sys.argv[1], sys.argv[2], sys.argv[3]
    with open(plan_path) as handle:
        plan = json.load(handle)
    with open(os.path.join(directory, "mcp.%s.json" % n)) as handle:
        config = json.load(handle)

    server = config["mcpServers"]["dagu"]
    url = server["url"]
    authorization = server["headers"]["Authorization"]
    write(directory, "url.%s" % n, url)

    # A request that the bridge must refuse.
    refused = {}
    for item in plan.get("refuse", []):
        payload = {"jsonrpc": "2.0", "id": 1, "method": "tools/list"}
        refused[item["name"]] = post(url, item.get("authorization"), payload).status
    if refused:
        write(directory, "refused.%s.json" % n, json.dumps(refused))

    stream_calls = []
    try:
        client = Client(url, authorization)
        client.request("initialize", {
            "protocolVersion": PROTOCOL_VERSION,
            "capabilities": {},
            "clientInfo": {"name": "fake-claude", "version": "0"},
        })
        client.notify("notifications/initialized")
        write(directory, "tools.%s.json" % n, json.dumps(client.request("tools/list")))
        for index, call in enumerate(plan.get("calls", [])):
            stream_calls.append("mcp__dagu__" + call["tool"])
            try:
                result = client.request("tools/call", {"name": call["tool"], "arguments": call.get("arguments", {})})
            except RuntimeError as error:
                result = {"error": str(error)}
            write(directory, "call.%s.%d.json" % (n, index), json.dumps(result))
        if client.event_stream:
            write(directory, "sse.%s" % n, "yes")
    except RuntimeError as error:
        write(directory, "client_error.%s" % n, str(error))

    secret = authorization[len("Bearer "):]
    if plan.get("stderr_secret"):
        sys.stderr.write("the secret is %s\n" % secret)

    init = {
        "type": "system", "subtype": "init", "model": "sonnet",
        "mcp_servers": [{"name": "dagu", "status": plan.get("bridge_status", "connected")}],
    }
    if plan.get("secret_in_stream"):
        init["debug"] = secret
    blocks = [{"type": "tool_use", "name": name, "id": "t%d" % i, "input": {}}
              for i, name in enumerate(stream_calls + plan.get("extra_tool_use", []))]
    print(json.dumps(init))
    print(json.dumps({"type": "assistant", "message": {"content": blocks}}))
    print(json.dumps({
        "type": "result", "subtype": "success", "is_error": False, "num_turns": 2,
        "result": "done", "structured_output": {"verdict": "accept"},
        "modelUsage": {"sonnet": {"inputTokens": 12, "outputTokens": 3,
                                  "cacheReadInputTokens": 0, "cacheCreationInputTokens": 0}},
    }))


if __name__ == "__main__":
    main()
