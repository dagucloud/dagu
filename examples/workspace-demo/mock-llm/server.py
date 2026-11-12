#!/usr/bin/env python3
"""A small OpenAI-compatible server for the agent tools demo.

It needs no API key. When the tool load_skill is offered, it calls it first
with the name of the first available skill. On the next request that offers
tools, it calls the first other tool with placeholder arguments. Then it
answers with a summary of the tool result. Run: python3 server.py [port]
"""
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def placeholder(schema):
    kind = (schema or {}).get("type", "string")
    if kind in ("integer", "number"):
        return 1
    if kind == "boolean":
        return True
    return "demo"


def pick_call(tool, call_id="call_demo_1"):
    function = tool["function"]
    properties = (function.get("parameters") or {}).get("properties") or {}
    arguments = {name: placeholder(spec) for name, spec in properties.items()}
    return {
        "id": call_id,
        "type": "function",
        "function": {"name": function["name"], "arguments": json.dumps(arguments)},
    }


def pick_skill_call(tool):
    """Call load_skill with the first skill name that its schema lists."""
    properties = (tool["function"].get("parameters") or {}).get("properties") or {}
    names = (properties.get("name") or {}).get("enum") or ["demo"]
    return {
        "id": "call_demo_skill",
        "type": "function",
        "function": {"name": tool["function"]["name"], "arguments": json.dumps({"name": names[0]})},
    }


def called_names(messages):
    """Return the names of the tools that the model already called."""
    return [
        call["function"]["name"]
        for m in messages
        if m.get("role") == "assistant"
        for call in m.get("tool_calls") or []
    ]


def completion(message, finish):
    return {
        "id": "mock-1",
        "object": "chat.completion",
        "model": "mock",
        "choices": [{"index": 0, "message": message, "finish_reason": finish}],
        "usage": {"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20},
    }


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        request = json.loads(self.rfile.read(length) or b"{}")
        messages = request.get("messages", [])
        tools = request.get("tools") or []
        results = [m for m in messages if m.get("role") == "tool"]
        names = [t["function"]["name"] for t in tools]
        called = called_names(messages)
        skill_tool = next((t for t in tools if t["function"]["name"] == "load_skill"), None)
        other_tools = [t for t in tools if t is not skill_tool]

        if skill_tool is not None and "load_skill" not in called:
            message = {"role": "assistant", "content": "", "tool_calls": [pick_skill_call(skill_tool)]}
            body = completion(message, "tool_calls")
        elif other_tools and not [n for n in called if n != "load_skill"]:
            message = {"role": "assistant", "content": "", "tool_calls": [pick_call(other_tools[0])]}
            body = completion(message, "tool_calls")
        elif results:
            text = "I offered %d tools (%s). The tool returned: %s" % (
                len(names), ", ".join(names) or "none", results[-1].get("content", ""))
            if "load_skill" in called:
                text += " I read a skill with load_skill."
            body = completion({"role": "assistant", "content": text}, "stop")
        else:
            text = "No tool was offered to me for this request."
            body = completion({"role": "assistant", "content": text}, "stop")

        data = json.dumps(body).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, *_):
        pass


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8099
    ThreadingHTTPServer(("0.0.0.0", port), Handler).serve_forever()
