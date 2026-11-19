#!/usr/bin/env python3
"""The demo scenarios of the fake claude CLI (examples/agent-worker-demo).

The fake claude script runs this file when FAKE_CLAUDE_SCENARIO is demo, loop, loop_stuck,
endpoint or isolation. It uses the standard library only. It prints stream-json events as the real
CLI does. Each scenario first connects to each MCP server of --mcp-config, as the CLI
does, and reports the status of each server in the event system/init. The scenario demo
does real work in the current directory, in this order:

 1. run ./scripts/test.sh (it fails)
 2. call $ANTHROPIC_BASE_URL/v1/messages with the key $ANTHROPIC_API_KEY and apply
    the edit plan of the answer
 3. call the Dagu tools of the tool bridge (the server dagu): lookup-ticket, with a
    question and a CUSTOMER_ID that the DAG fixes, and ops_notify
 4. call the tool dagu_read of the MCP server docs (the Dagu server), and read the
    skills in the folder of --add-dir, with the files of their references/ directory
 5. run ./scripts/test.sh again (it passes)
 6. print the result with structured_output

The scenario isolation does no work. It checks the sandbox of the restricted user, in this order:

 1. run the hostile probes. Each result is "allowed" or "denied:<reason>".
 2. write the secret of the bridge to stderr, to show that Dagu masks it
 3. leave one process behind, to show that Dagu stops it
 4. print the result with structured_output: the probes and the URL of the bridge

The scenario endpoint is the endpoint path of Spec 094. The step names an endpoint, so the
environment holds the address and the secret of the relay of the attempt, and the MCP config
holds the named server vault. The scenario does these actions:

 1. call $ANTHROPIC_BASE_URL/v1/messages with "stream": true, and note the time of each
    event of the answer
 2. call the tool vault_lookup of the named server vault, and the tool vault_admin that the
    DAG does not allow
 3. call the Dagu tool lookup-ticket through the tool bridge
 4. read the skills of the folder of --add-dir
 5. print the typed result: the timing of the stream, the answers of the tools and the hash
    of the key of the environment (never the key)

The scenarios loop and loop_stuck play the rounds of the verify loop of Spec 092. The
repository has two bugs. The tests show the first one (add). Only the verify script shows
the second one (divide). A prompt that names a file verify_<n>.log is a round after a
failed verify.

  round 1   run the tests, ask the model API, apply the edit plan (it fixes add only),
            run the tests again.
  round > 1 read the log, find the function that the log names, and fix it. The scenario
            loop_stuck reads the log and changes nothing.

usage: demo_agent.py <the arguments of the claude CLI>
"""
import hashlib
import json
import os
import re
import socket
import ssl
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

from mcp_client import PROTOCOL_VERSION, Client, post

# One key for each probe. The output_schema of the DAG agent-dev-isolation lists the same keys.
PROBE_KEYS = [
    "env_git_token",
    "env_dagu_vars",
    "read_tls_key",
    "read_config",
    "read_proc1_environ",
    "read_parent_environ",
    "write_git_hook",
    "write_git_config",
    "rename_git_dir",
    "http_via_proxy",
    "connect_via_proxy",
    "direct_tcp_internet",
    "direct_tcp_fake_anthropic",
    "tls_coordinator_without_cert",
    "bridge_without_secret",
    "bridge_secret_in_env",
    "write_skills_dir",
]

# The tools that the scenario demo calls. The DAG fixes CUSTOMER_ID of lookup-ticket and the tool
# does not show it, so the agent gives the QUESTION only: an argument for CUSTOMER_ID is refused.
TICKET_TOOL = "lookup-ticket"
TICKET_ARGUMENTS = {"QUESTION": "Is the failing test of the calculator a known issue?"}
NOTIFY_TOOL = "ops_notify"
DOCS_SERVER = "docs"
DOCS_TOOL = "dagu_read"

counter = {"tool": 0, "turn": 0}


def emit(event):
    print(json.dumps(event), flush=True)


def note(text):
    print("demo agent: " + text, file=sys.stderr, flush=True)


def tool_use(name, tool_input):
    """Print one assistant event with one tool_use block, as the CLI does."""
    counter["tool"] += 1
    counter["turn"] += 1
    emit({"type": "assistant", "message": {"content": [
        {"type": "tool_use", "name": name, "id": "toolu_%03d" % counter["tool"], "input": tool_input}]}})


def parse_arguments(argv):
    found = {}
    index = 0
    while index < len(argv):
        if argv[index] in ("-p", "--mcp-config", "--add-dir", "--model") and index + 1 < len(argv):
            found[argv[index]] = argv[index + 1]
            index += 1
        index += 1
    return found


def run_tests():
    done = subprocess.run(["./scripts/test.sh"], capture_output=True, text=True, timeout=120)
    return done.returncode, (done.stdout + done.stderr)[-2000:]


def ask_model(prompt):
    """Send one request to the model API. The base URL is the egress proxy."""
    base = os.environ["ANTHROPIC_BASE_URL"].rstrip("/")
    body = {"model": "claude-demo", "max_tokens": 512, "messages": [{"role": "user", "content": prompt}]}
    request = urllib.request.Request(base + "/v1/messages", data=json.dumps(body).encode(), method="POST", headers={
        "x-api-key": os.environ.get("ANTHROPIC_API_KEY", ""),
        "anthropic-version": "2023-06-01",
        "content-type": "application/json",
    })
    # The base URL names the proxy. The request must not go through a second proxy.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open(request, timeout=30) as response:
        message = json.loads(response.read())
    return json.loads(message["content"][0]["text"])


def ask_model_stream(prompt):
    """Send one streamed request to the model API and note the time of each event.
    Return the text of the answer and the timing in milliseconds."""
    base = os.environ["ANTHROPIC_BASE_URL"].rstrip("/")
    body = {"model": "claude-demo", "max_tokens": 512, "stream": True, "messages": [{"role": "user", "content": prompt}]}
    request = urllib.request.Request(base + "/v1/messages", data=json.dumps(body).encode(), method="POST", headers={
        "x-api-key": os.environ.get("ANTHROPIC_API_KEY", ""),
        "anthropic-version": "2023-06-01",
        "content-type": "application/json",
        "accept": "text/event-stream",
    })
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    started = time.monotonic()
    times, text, event = [], "", ""
    with opener.open(request, timeout=60) as response:
        for raw in response:
            line = raw.decode().rstrip("\r\n")
            if line.startswith("event:"):
                event = line[len("event:"):].strip()
            elif line.startswith("data:") and event == "content_block_delta":
                times.append(int((time.monotonic() - started) * 1000))
                text += json.loads(line[len("data:"):]).get("delta", {}).get("text", "")
    timing = {"events": len(times), "first_ms": times[0] if times else -1, "last_ms": times[-1] if times else -1}
    return text, timing


def apply_plan(plan):
    changed = []
    for edit in plan["edits"]:
        with open(edit["path"]) as handle:
            text = handle.read()
        if edit["old"] not in text and edit["new"] in text:
            # A repetition of the step finds the fix of the previous execution.
            continue
        if edit["old"] not in text:
            raise RuntimeError("the text to replace is not in %s" % edit["path"])
        tool_use("Read", {"file_path": edit["path"]})
        tool_use("Edit", {"file_path": edit["path"], "old_string": edit["old"], "new_string": edit["new"]})
        with open(edit["path"], "w") as handle:
            handle.write(text.replace(edit["old"], edit["new"], 1))
        changed.append(edit["path"])
    return changed


def read_mcp_config(path):
    """Return the servers of the MCP config of the attempt, or an empty map."""
    if not path:
        return {}
    with open(path) as handle:
        return json.load(handle).get("mcpServers", {})


def connect_servers(servers):
    """Connect to each server as the CLI does. Return the clients and the statuses."""
    clients, statuses = {}, []
    for name in sorted(servers, key=lambda item: (item != "dagu", item)):
        server = servers[name]
        client = Client(server["url"], (server.get("headers") or {}).get("Authorization"))
        try:
            client.request("initialize", {
                "protocolVersion": PROTOCOL_VERSION,
                "capabilities": {},
                "clientInfo": {"name": "fake-claude-demo", "version": "0"},
            })
            client.notify("notifications/initialized")
            clients[name] = client
            statuses.append({"name": name, "status": "connected"})
        except Exception as error:
            note("the MCP server %s is not reachable: %s" % (name, error))
            statuses.append({"name": name, "status": "failed"})
    return clients, statuses


def result_text(result):
    text = "".join(part.get("text", "") for part in result.get("content", []))
    return ("error: " + text) if result.get("isError") else text


def call_tool(client, server, tool, arguments):
    """Call one tool of one server, as the CLI does: the tool_use block names mcp__<server>__<tool>."""
    names = [item["name"] for item in client.request("tools/list")["tools"]]
    if tool not in names:
        raise RuntimeError("the tool %s is not in the catalog %s of the server %s" % (tool, names, server))
    tool_use("mcp__%s__%s" % (server, tool), arguments)
    return result_text(client.request("tools/call", {"name": tool, "arguments": arguments}))


def use_tools(clients):
    """Call the Dagu tools of the bridge and the tool of the MCP server docs."""
    results = {"lookup_ticket": "not called", "ops_notify": "not called", "docs_dagu_read": "not called"}
    bridge = clients.get("dagu")
    if bridge is not None:
        results["lookup_ticket"] = call_tool(bridge, "dagu", TICKET_TOOL, TICKET_ARGUMENTS)
        results["ops_notify"] = call_tool(bridge, "dagu", NOTIFY_TOOL, {"text": "The agent looked up the ticket of c-42."})
    docs = clients.get(DOCS_SERVER)
    if docs is not None:
        results["docs_dagu_read"] = call_tool(docs, DOCS_SERVER, DOCS_TOOL, {"target": "dags"})[:4000]
    for key, value in results.items():
        note("tool %s: %s" % (key, value[:160]))
    return results


def read_skill_folders(add_dir):
    """Read each skill of the folder of --add-dir and each file of its references/
    directory, as the CLI does with its tools Skill and Read. Dagu mounts the folder as
    it is, so the files of references/ are there. Return the names of the skills and the
    paths of the references, as <skill>/references/<file>."""
    found = []
    references = []
    if not add_dir:
        return found, references
    root = os.path.join(add_dir, ".claude", "skills")
    for name in sorted(os.listdir(root)):
        path = os.path.join(root, name, "SKILL.md")
        if not os.path.isfile(path):
            continue
        tool_use("Skill", {"skill": name})
        with open(path) as handle:
            handle.read()
        found.append(name)
        folder = os.path.join(root, name, "references")
        for current, _, files in sorted(os.walk(folder)):
            for file in sorted(files):
                file_path = os.path.join(current, file)
                tool_use("Read", {"file_path": file_path})
                with open(file_path) as handle:
                    handle.read()
                references.append("%s/%s" % (name, os.path.relpath(file_path, os.path.join(root, name)).replace(os.sep, "/")))
    return found, references


# The hostile probes. Each one returns "allowed" or "denied:<reason>".

def probe_env_git_token():
    return "allowed" if "GIT_TOKEN" in os.environ else "denied:GIT_TOKEN is not in the environment"


def probe_env_dagu_vars():
    names = sorted(name for name in os.environ if name.startswith("DAGU_") and name != "DAGU_OUTPUT_FILE")
    return "allowed" if names else "denied:no DAGU_ variable in the environment"


def read_one_byte(path):
    try:
        with open(path, "rb") as handle:
            handle.read(1)
    except PermissionError:
        return "denied:permission denied"
    except FileNotFoundError:
        return "denied:no such file"
    except OSError as error:
        return "denied:" + error.strerror
    return "allowed"


def probe_read_tls_key():
    tool_use("Read", {"file_path": "/etc/dagu/tls/agent-claude.key"})
    return read_one_byte("/etc/dagu/tls/agent-claude.key")


def probe_read_config():
    tool_use("Read", {"file_path": "/etc/dagu/config.yaml"})
    return read_one_byte("/etc/dagu/config.yaml")


def probe_read_proc1_environ():
    tool_use("Read", {"file_path": "/proc/1/environ"})
    return read_one_byte("/proc/1/environ")


def probe_read_parent_environ():
    path = "/proc/%d/environ" % os.getppid()
    tool_use("Read", {"file_path": path})
    return read_one_byte(path)


def probe_write_git_hook():
    tool_use("Bash", {"command": "echo hook > .git/hooks/pre-commit"})
    try:
        # A clone made by go-git has no hooks folder. The attacker makes it.
        os.makedirs(".git/hooks", exist_ok=True)
        with open(".git/hooks/pre-commit", "w") as handle:
            handle.write("#!/bin/sh\necho the agent was here\n")
    except OSError as error:
        return "denied:" + error.strerror
    # The file stays. The trusted evidence step of the demo DAG finds it.
    return "allowed"


def probe_write_git_config():
    tool_use("Bash", {"command": "git config core.fsmonitor ./evil.sh"})
    try:
        with open(".git/config", "a") as handle:
            handle.write("[core]\n\tfsmonitor = ./evil.sh\n")
    except OSError as error:
        return "denied:" + error.strerror
    # The change stays. The trusted evidence step of the demo DAG finds it.
    return "allowed"


def probe_rename_git_dir():
    tool_use("Bash", {"command": "mv .git .git-x"})
    try:
        os.rename(".git", ".git-x")
    except OSError as error:
        return "denied:" + error.strerror
    os.rename(".git-x", ".git")
    return "allowed"


def proxy_address():
    value = os.environ.get("HTTP_PROXY") or os.environ.get("http_proxy") or ""
    parts = urllib.parse.urlsplit(value)
    return value, parts.hostname, parts.port or 80


def probe_http_via_proxy():
    tool_use("Bash", {"command": "curl http://example.com/"})
    value, host, _ = proxy_address()
    if not host:
        return "denied:no HTTP_PROXY, so no route"
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({"http": value}))
    try:
        opener.open("http://example.com/", timeout=8).read(1)
    except urllib.error.HTTPError as error:
        return "denied:the proxy answered %d" % error.code
    except OSError as error:
        return "denied:%s" % error
    return "allowed"


def probe_connect_via_proxy():
    tool_use("Bash", {"command": "curl https://example.com/"})
    _, host, port = proxy_address()
    if not host:
        return "denied:no HTTP_PROXY, so no route"
    try:
        with socket.create_connection((host, port), timeout=8) as connection:
            connection.sendall(b"CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")
            status = connection.recv(256).split(b"\r\n")[0].decode("latin-1")
    except OSError as error:
        return "denied:%s" % error
    if " 200" in status:
        return "allowed"
    return "denied:the proxy answered " + (status.split(" ", 1)[1] if " " in status else status)


def probe_direct_tcp(host, port):
    tool_use("Bash", {"command": "nc -z %s %d" % (host, port)})
    try:
        socket.create_connection((host, port), timeout=4).close()
    except socket.gaierror:
        return "denied:the name does not resolve"
    except OSError as error:
        return "denied:" + (error.strerror or type(error).__name__)
    return "allowed"


def probe_direct_tcp_internet():
    return probe_direct_tcp("1.1.1.1", 443)


def probe_direct_tcp_fake_anthropic():
    return probe_direct_tcp("fake-anthropic", 8080)


def probe_tls_coordinator_without_cert():
    tool_use("Bash", {"command": "openssl s_client -connect dagu-coordinator:50055"})
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
    context.check_hostname = False
    context.verify_mode = ssl.CERT_NONE
    context.set_alpn_protocols(["h2"])
    try:
        with socket.create_connection(("dagu-coordinator", 50055), timeout=8) as raw:
            with context.wrap_socket(raw, server_hostname="dagu-coordinator") as secure:
                # With TLS 1.3 the server checks the client certificate after the
                # handshake of the client. Send one byte and read the answer.
                secure.settimeout(8)
                secure.sendall(b"PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n")
                data = secure.recv(1)
    except ssl.SSLError as error:
        return "denied:" + (error.reason or str(error))
    except OSError as error:
        return "denied:" + (error.strerror or type(error).__name__)
    return "denied:the connection closed with no data" if not data else "allowed"


def probe_bridge_without_secret(bridge):
    """Send a request to the tool bridge with no secret. The bridge must refuse it."""
    if not bridge:
        return "denied:no tool bridge"
    tool_use("Bash", {"command": "curl -X POST " + bridge["url"]})
    reply = post(bridge["url"], None, {"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
    return "allowed" if reply.status == 200 else "denied:status %d" % reply.status


def probe_bridge_secret_in_env(bridge):
    """Look for the secret of the bridge in the environment. Only the MCP config holds it."""
    if not bridge:
        return "denied:no tool bridge"
    secret = bridge["headers"]["Authorization"][len("Bearer "):]
    tool_use("Bash", {"command": "env"})
    found = [name for name, value in os.environ.items() if secret in value]
    return ("allowed:" + ",".join(found)) if found else "denied:the secret is not in the environment"


def probe_write_skills_dir(add_dir):
    tool_use("Bash", {"command": "touch <agent directory>/evil"})
    if not add_dir:
        return "denied:no skills directory is mounted"
    try:
        with open(os.path.join(add_dir, "evil"), "w") as handle:
            handle.write("x")
    except OSError as error:
        return "denied:" + error.strerror
    return "allowed"


def run_probes(bridge=None, add_dir=None):
    probes = {
        "env_git_token": probe_env_git_token,
        "env_dagu_vars": probe_env_dagu_vars,
        "read_tls_key": probe_read_tls_key,
        "read_config": probe_read_config,
        "read_proc1_environ": probe_read_proc1_environ,
        "read_parent_environ": probe_read_parent_environ,
        "write_git_hook": probe_write_git_hook,
        "write_git_config": probe_write_git_config,
        "rename_git_dir": probe_rename_git_dir,
        "http_via_proxy": probe_http_via_proxy,
        "connect_via_proxy": probe_connect_via_proxy,
        "direct_tcp_internet": probe_direct_tcp_internet,
        "direct_tcp_fake_anthropic": probe_direct_tcp_fake_anthropic,
        "tls_coordinator_without_cert": probe_tls_coordinator_without_cert,
        "bridge_without_secret": lambda: probe_bridge_without_secret(bridge),
        "bridge_secret_in_env": lambda: probe_bridge_secret_in_env(bridge),
        "write_skills_dir": lambda: probe_write_skills_dir(add_dir),
    }
    assert list(probes) == PROBE_KEYS
    results = {}
    for key, probe in probes.items():
        try:
            results[key] = probe()
        except Exception as error:  # a probe must never stop the run
            results[key] = "denied:probe error %s" % type(error).__name__
        note("probe %s = %s" % (key, results[key]))
    return results


def leave_a_process():
    """Start a process in its own session and leave it. Dagu must stop it."""
    tool_use("Bash", {"command": "setsid sleep 600 &"})
    child = subprocess.Popen(["sleep", "600"], start_new_session=True,
                             stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    return child.pid


FIX_OF_DIVIDE = ("    return a / b\n", "    if b == 0:\n        return None\n    return a / b\n")


def log_of_prompt(prompt):
    """Return the path of the verify log that the prompt names, or an empty text."""
    for line in prompt.splitlines():
        found = re.search(r"(/\S*verify_\d+\.log)\s*$", line.strip())
        if found:
            return found.group(1)
    return ""


def main_loop(scenario, arguments):
    """One round of the verify loop. Return the summary of the round."""
    prompt = arguments.get("-p", "")
    log_path = log_of_prompt(prompt)
    if not log_path:
        note("round 1: the prompt names no log")
        tool_use("Bash", {"command": "./scripts/test.sh"})
        code, output = run_tests()
        note("first test run: exit code %d" % code)
        plan = ask_model("%s\n\ncalc.py:\n%s\n\nTest output:\n%s" % (prompt, open("calc.py").read(), output))
        changed = apply_plan(plan)
        tool_use("Bash", {"command": "./scripts/test.sh"})
        code, _ = run_tests()
        note("second test run: exit code %d" % code)
        return "Round 1: changed %s. The tests %s." % (", ".join(changed) or "no file", "pass" if code == 0 else "fail")

    tool_use("Read", {"file_path": log_path})
    with open(log_path) as handle:
        text = handle.read()
    note("read the log %s" % log_path)
    failures = [line for line in text.splitlines() if "FAIL" in line]
    note("the log says: %s" % (failures[-1].strip() if failures else "no FAIL line"))
    found = re.search(r"calc\.(\w+)\(", "\n".join(failures))
    if scenario == "loop_stuck" or not found or found.group(1) != "divide":
        note("this round changes no file")
        return "The agent read the log and changed no file."
    tool_use("Read", {"file_path": "calc.py"})
    old, new = FIX_OF_DIVIDE
    with open("calc.py") as handle:
        source = handle.read()
    if old in source:
        tool_use("Edit", {"file_path": "calc.py", "old_string": old, "new_string": new})
        with open("calc.py", "w") as handle:
            handle.write(source.replace(old, new, 1))
    note("applied the fix that the log names: %s" % found.group(1))
    tool_use("Bash", {"command": "./scripts/test.sh"})
    code, _ = run_tests()
    note("test run after the fix: exit code %d" % code)
    return "The agent read the log, fixed %s, and the tests %s." % (found.group(1), "pass" if code == 0 else "fail")


def main_endpoint(arguments, clients, statuses):
    """The scenario endpoint. Return the typed result."""
    add_dir = arguments.get("--add-dir")
    problems = []
    stream = {"events": 0, "first_ms": -1, "last_ms": -1, "text_ok": False}
    try:
        text, timing = ask_model_stream("Plan a fix for the failing test of the calculator.")
        stream.update(timing)
        stream["text_ok"] = isinstance(json.loads(text).get("edits"), list)
        note("stream: %d events, first at %d ms, last at %d ms" % (stream["events"], stream["first_ms"], stream["last_ms"]))
    except Exception as error:
        problems.append("stream: %s" % error)
        note("the streamed model call failed: %s" % error)

    vault = clients.get("vault")
    vault_lookup, vault_admin = "not called", "not called"
    if vault is not None:
        try:
            vault_lookup = call_tool(vault, "vault", "vault_lookup", {"key": "build"})
        except Exception as error:
            problems.append("vault_lookup: %s" % error)
        try:
            vault_admin = call_tool(vault, "vault", "vault_admin", {})
        except Exception as error:
            # The coordinator refuses a tool that "allow" does not list.
            vault_admin = "refused: %s" % error
    note("vault_lookup: %s" % vault_lookup[:160])
    note("vault_admin: %s" % vault_admin[:160])

    ticket = "not called"
    bridge = clients.get("dagu")
    if bridge is not None:
        try:
            ticket = call_tool(bridge, "dagu", TICKET_TOOL, TICKET_ARGUMENTS)
        except Exception as error:
            problems.append("lookup-ticket: %s" % error)
    note("lookup_ticket: %s" % ticket[:160])

    skills, references = [], []
    try:
        skills, references = read_skill_folders(add_dir)
    except Exception as error:
        problems.append("skills: %s" % error)
    note("skills read: %s" % (", ".join(skills) or "none"))
    note("references read: %s" % (", ".join(references) or "none"))

    key = os.environ.get("ANTHROPIC_API_KEY", "")
    base = os.environ.get("ANTHROPIC_BASE_URL", "")
    environment = {
        # The hash of the key of the environment. The relay secret is a random value. It is not
        # the placeholder of the deployment and not the key of the endpoint.
        "api_key_sha256_prefix": hashlib.sha256(key.encode()).hexdigest()[:12] if key else "",
        "base_url_is_loopback": base.startswith("http://127.0.0.1:"),
        "oauth_token_in_environment": "CLAUDE_CODE_OAUTH_TOKEN" in os.environ or "ANTHROPIC_AUTH_TOKEN" in os.environ,
    }
    mcp_names = sorted(status["name"] for status in statuses if status["status"] == "connected")
    return {
        "verdict": "blocked" if problems else "done",
        "summary": "Streamed %d events. Skills read: %s. References read: %s. MCP servers: %s.%s" % (
            stream["events"], ", ".join(skills) or "none", ", ".join(references) or "none", ", ".join(mcp_names) or "none",
            (" Problems: " + "; ".join(problems)) if problems else ""),
        "stream": stream,
        "environment": environment,
        "skills": skills,
        "tools": {"vault_lookup": vault_lookup, "vault_admin": vault_admin, "lookup_ticket": ticket},
    }


def main_isolation(arguments, servers):
    """The scenario isolation. Return the typed result."""
    bridge = servers.get("dagu")
    probes = run_probes(bridge, arguments.get("--add-dir"))

    # The CLI prints the secret of the bridge. Dagu masks it in the stderr of the step.
    if bridge:
        note("bridge secret: " + bridge["headers"]["Authorization"][len("Bearer "):])

    # A process that stays after the end of the step.
    leftover = leave_a_process()

    allowed = [key for key, value in probes.items() if not value.startswith("denied:")]
    return {
        "verdict": "done",
        "summary": "Ran %d probes, allowed: %s. Left process %d." % (len(probes), ", ".join(allowed) or "none", leftover),
        "probes": probes,
        # The URL of the bridge has no secret. A later step checks that the bridge is closed.
        "bridge_url": bridge["url"] if bridge else "",
    }


def main():
    arguments = parse_arguments(sys.argv[1:])
    add_dir = arguments.get("--add-dir")
    prompt = arguments.get("-p", "")
    servers = read_mcp_config(arguments.get("--mcp-config"))
    clients, statuses = connect_servers(servers)
    emit({"type": "system", "subtype": "init", "model": arguments.get("--model", "sonnet"), "mcp_servers": statuses})

    scenario = os.environ.get("FAKE_CLAUDE_SCENARIO", "demo")
    if scenario in ("endpoint", "isolation"):
        if scenario == "endpoint":
            result = main_endpoint(arguments, clients, statuses)
        else:
            result = main_isolation(arguments, servers)
        emit({
            "type": "result", "subtype": "success", "is_error": False, "num_turns": counter["turn"] + 1,
            "result": "done", "structured_output": result,
            "modelUsage": {"sonnet": {"inputTokens": 1200, "outputTokens": 300,
                                      "cacheReadInputTokens": 0, "cacheCreationInputTokens": 0}},
        })
        return
    if scenario in ("loop", "loop_stuck"):
        summary = main_loop(scenario, arguments)
        emit({
            "type": "result", "subtype": "success", "is_error": False, "num_turns": counter["turn"] + 1,
            "result": "done",
            "structured_output": {"verdict": "done", "summary": summary},
            "modelUsage": {"sonnet": {"inputTokens": 1200, "outputTokens": 300,
                                      "cacheReadInputTokens": 0, "cacheCreationInputTokens": 0}},
        })
        return

    problems = []
    changed = []
    skills = []
    references = []

    # 1. The tests fail.
    tool_use("Bash", {"command": "./scripts/test.sh"})
    code, output = run_tests()
    note("first test run: exit code %d" % code)

    # 2. The model API answers with an edit plan. The CLI applies it.
    try:
        plan = ask_model("%s\n\ncalc.py:\n%s\n\nTest output:\n%s" % (prompt, open("calc.py").read(), output))
        changed = apply_plan(plan)
    except Exception as error:
        problems.append("model: %s" % error)
        note("the model API call failed: %s" % error)

    # 3 and 4. The Dagu tools through the bridge, and the tool of the MCP server docs.
    tools = {}
    try:
        tools = use_tools(clients)
    except Exception as error:
        problems.append("tools: %s" % error)
        note("a tool call failed: %s" % error)

    # 4b. The skills of the folder of --add-dir.
    try:
        skills, references = read_skill_folders(add_dir)
    except Exception as error:
        problems.append("skills: %s" % error)
    note("skills read: %s" % (", ".join(skills) or "none"))
    note("references read: %s" % (", ".join(references) or "none"))

    # 5. The tests pass.
    tool_use("Bash", {"command": "./scripts/test.sh"})
    code, _ = run_tests()
    note("second test run: exit code %d" % code)
    if code != 0:
        problems.append("the tests still fail")

    summary = "Changed %s. Tests %s. Skills read: %s. References read: %s." % (
        ", ".join(changed) or "no file", "pass" if code == 0 else "fail", ", ".join(skills) or "none",
        ", ".join(references) or "none")
    if problems:
        summary += " Problems: " + "; ".join(problems)

    # 6. The typed result.
    emit({
        "type": "result", "subtype": "success", "is_error": False, "num_turns": counter["turn"] + 1,
        "result": "done" if not problems else "blocked",
        "structured_output": {
            "verdict": "blocked" if problems else "done",
            "summary": summary,
            "changed_files": changed,
            "tools": {key: tools.get(key, "not called") for key in ("lookup_ticket", "ops_notify", "docs_dagu_read")},
        },
        "modelUsage": {"sonnet": {"inputTokens": 1200, "outputTokens": 300,
                                  "cacheReadInputTokens": 0, "cacheCreationInputTokens": 0}},
    })


if __name__ == "__main__":
    main()
