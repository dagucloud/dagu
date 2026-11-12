#!/usr/bin/env python3
"""Helper of run-demo.sh. It reads JSON from the REST API and checks the evidence.

usage (run with python3 -I):
  evidence.py field <dotted.path>         read JSON on stdin and print one field
  evidence.py secret-id <ref>             read the list of secrets on stdin and print the ID of <ref>
  evidence.py nodes <status file>         print the status of each step of a run
  evidence.py probes <outputs file>       print the result of each hostile probe (the outputs of
                                          the step code of agent-dev-isolation)
  evidence.py check <evidence dir> <secrets dir>
                                          print PASS or FAIL for each claim of the demo: the runs of
                                          agent-dev and agent-dev-isolation. The exit code is 1
                                          when a claim fails.
  evidence.py secret-put <API URL> <ref> <value file>
                                          make the registry secret <ref>, or write a new
                                          version of it. The value comes from the file, so
                                          no argument of a process holds it.
  evidence.py ai-put <API URL> <endpoint|mcp> <spec JSON> <secret file> [<response file>]
                                          make an AI endpoint or a named MCP server entry. The key or
                                          the token comes from the file, so no argument of a process
                                          holds it. An entry of the same name is deleted first.
  evidence.py ai-delete <API URL> <endpoint|mcp> <name>
                                          delete an AI endpoint or a named MCP server entry
  evidence.py check-endpoint <endpoint evidence dir> <secrets dir>
                                          print PASS or FAIL for each claim of the endpoint path
                                          (run-demo.sh endpoint). The exit code is 1 when a claim fails.
  evidence.py check-endpoint-real <real endpoint dir> <subscription token file> <MCP token file> <GitHub token file> <proxy credential file>
                                          print PASS or FAIL for each claim of the endpoint path with
                                          the real Claude Code (run-real-endpoint.sh check). The output
                                          holds no token.
  evidence.py stream <stream file>        print the events and the tool calls of a stream
                                          of claude --output-format stream-json
  evidence.py check-real <evidence dir> <anthropic token file> <github token file> <proxy credential file>
                                          print PASS or FAIL for each claim of the real mode
                                          (run-real.sh). The exit code is 1 when a claim fails.
  evidence.py check-loop <loop evidence dir> <secrets dir> <DAG file>
                                          print PASS or FAIL for each claim of the verify loop
                                          with the fake claude (run-demo.sh loop). The folders
                                          fixed/ and stuck/ hold the two runs.
  evidence.py check-loop-real <evidence dir> <anthropic token file> <github token file> <proxy credential file> <DAG file>
                                          print PASS or FAIL for each claim of the verify loop with
                                          the real Claude Code (run-real.sh loop). A claim fails
                                          when the real verify did not fail in round 1.
"""
import base64
import glob
import hashlib
import json
import os
import re
import sys
import urllib.error
import urllib.request


def load(path, default=None):
    try:
        with open(path) as handle:
            return json.load(handle)
    except (OSError, ValueError):
        return default


def text(path):
    try:
        with open(path, errors="replace") as handle:
            return handle.read()
    except OSError:
        return ""


def dotted(value, path):
    for part in path.split("."):
        if not isinstance(value, dict) or part not in value:
            return ""
        value = value[part]
    return value


def probes_of(outputs_path):
    """Return the probes as a dict. The step publishes each property as an output.
    The step also writes the whole result as one JSON line to stdout: the fallback."""
    outputs = load(outputs_path, {})
    for item in outputs.get("outputs", []):
        if item.get("name") == "probes":
            try:
                return json.loads(item["value"])
            except ValueError:
                break
    for line in log_text(outputs_path.replace(".outputs.json", ".log.json")).splitlines():
        try:
            probes = json.loads(line).get("probes")
        except (ValueError, AttributeError):
            continue
        if isinstance(probes, dict):
            return probes
    return {}


def output_value(outputs_path, name):
    for item in load(outputs_path, {}).get("outputs", []):
        if item.get("name") == name:
            return item.get("value", "")
    return ""


def log_text(path):
    return load(path, {}).get("content", "")


def node_of(status, step_id):
    for node in status.get("nodes", []):
        if (node.get("step") or {}).get("id") == step_id:
            return node
    return {}


def sub_runs(node):
    return (node.get("subRuns") or []) + (node.get("subRunsRepeated") or [])


def object_output(outputs_path, name):
    """Return an output that holds a JSON object, as a dict."""
    try:
        value = json.loads(output_value(outputs_path, name) or "{}")
    except ValueError:
        return {}
    return value if isinstance(value, dict) else {}


# The DAG that holds the hostile probes of the fake CLI.
ISOLATION_DAG = "agent-dev-isolation"

# The values of the fake CLI and of agent-dev.yaml for the tool lookup-ticket.
FIXED_CUSTOMER = "c-42"
AGENT_CUSTOMER = "c-666"
AGENT_QUESTION = "Is the failing test of the calculator a known issue?"


def bridge_claims(results, status, outputs_path):
    """The claims of the tools of the agent: the tool bridge and the MCP server docs."""
    code = node_of(status, "code")
    runs = sub_runs(code)
    tools = object_output(outputs_path, "tools")
    ticket_runs = [run for run in runs if run.get("dagName") == "lookup-ticket"]
    params = " ".join(str(run.get("params", "")) for run in ticket_runs)
    results.append(check("the DAG tool lookup-ticket ran as a child run with the fixed CUSTOMER_ID and the QUESTION of the agent",
                         bool(ticket_runs) and FIXED_CUSTOMER in params and AGENT_QUESTION in params and AGENT_CUSTOMER not in params,
                         "child runs %d, params %r" % (len(ticket_runs), params[:160])))
    ticket = tools.get("lookup_ticket", "")
    results.append(check("the agent got the answer of lookup-ticket for %s, the customer that the DAG fixes" % FIXED_CUSTOMER,
                         FIXED_CUSTOMER in ticket and AGENT_CUSTOMER not in ticket and not ticket.startswith("error"), ticket[:160]))
    notify_runs = [run for run in runs if "ops_notify" in str(run.get("dagName", ""))]
    notified = tools.get("ops_notify", "")
    results.append(check("the DAG tool ops_notify ran as a child run through the tool bridge",
                         bool(notify_runs) and not notified.startswith("error") and notified != "not called",
                         "child runs %s, result %r" % ([run.get("dagName") for run in notify_runs], notified[:80])))
    listing = tools.get("docs_dagu_read", "")
    results.append(check("the MCP tool dagu_read of the Dagu server answered the agent from the worker network",
                         "agent-dev" in listing and not listing.startswith("error"), listing[:120]))
    calls = output_value(outputs_path, "tool_calls")
    wanted = ["lookup-ticket", "ops_notify", "docs_dagu_read"]
    results.append(check("tool_calls lists the bridge tools with their plain names and the MCP tool as docs_dagu_read",
                         all('"%s"' % name in calls for name in wanted), calls[:160]))


def isolation_claims(results, ev, worker_host):
    """The claims of the run of agent-dev-isolation: the hostile probes, the bridge secret, the
    closed bridge and the process that the agent left. The fake CLI does the work in agent-dev."""
    steps = os.path.join(ev, "steps")
    outputs = os.path.join(steps, "%s.code.outputs.json" % ISOLATION_DAG)
    status = load(os.path.join(ev, "%s.status.json" % ISOLATION_DAG), {}).get("dagRunDetails", {})
    results.append(check("the run of agent-dev-isolation succeeded on the agent worker",
                         status.get("statusLabel") == "succeeded" and bool(worker_host) and status.get("workerId", "").startswith(worker_host),
                         "status %s, workerId %r" % (status.get("statusLabel"), status.get("workerId", ""))))
    probes = probes_of(outputs)
    allowed = [key for key, value in probes.items() if not str(value).startswith("denied:")]
    results.append(check("all hostile probes of the agent were denied", bool(probes) and not allowed,
                         "%d probes, allowed: %s" % (len(probes), ", ".join(allowed) or "none")))
    write_probe = str(probes.get("write_skills_dir", ""))
    results.append(check("the restricted user could not write the skills directory that Dagu mounted for the attempt",
                         write_probe.startswith("denied:") and "no skills directory" not in write_probe, write_probe))
    stderr_text = log_text(os.path.join(steps, "%s.code.stderr.json" % ISOLATION_DAG))
    masked = re.search(r"bridge secret: \*+", stderr_text) is not None
    clear = re.search(r"bridge secret: [A-Za-z0-9_-]{43}", stderr_text) is not None
    results.append(check("Dagu masked the bridge secret that the agent printed to its stderr", masked and not clear,
                         "see steps/%s.code.stderr.json" % ISOLATION_DAG))
    gone_text = log_text(os.path.join(steps, "%s.bridge_gone.log.json" % ISOLATION_DAG))
    results.append(check("after the attempt, the restricted user finds the bridge closed and no MCP config to read",
                         "bridge: closed" in gone_text and "MCP configs that the restricted user can read: 0" in gone_text,
                         gone_text.strip().replace("\n", "; ")[:160]))
    left = log_text(os.path.join(steps, "%s.evidence.log.json" % ISOLATION_DAG))
    results.append(check("no process of the restricted user is left after the agent that left one behind",
                         "== processes of the restricted user (user ID 10001)\nnone" in left,
                         "see steps/%s.evidence.log.json" % ISOLATION_DAG))


def cmd_field(argv):
    value = dotted(json.load(sys.stdin), argv[0])
    print(value if not isinstance(value, (dict, list)) else json.dumps(value))


def cmd_secret_id(argv):
    for secret in json.load(sys.stdin).get("secrets", []):
        if secret.get("ref") == argv[0]:
            print(secret["id"])
            return
    sys.exit(1)


def cmd_nodes(argv):
    details = load(argv[0], {}).get("dagRunDetails", {})
    print("run %s: %s (worker %s)" % (details.get("dagRunId", "?"), details.get("statusLabel", "?"), details.get("workerId", "none")))
    for node in details.get("nodes", []):
        step = node.get("step", {})
        line = "  %-12s %-10s" % (step.get("id") or step.get("name", "?"), node.get("statusLabel", "?"))
        if node.get("error"):
            line += " error: " + node["error"]
        print(line)


def cmd_probes(argv):
    probes = probes_of(argv[0])
    if not probes:
        print("no probes in the outputs of the step code")
        return
    for key, value in probes.items():
        print("  %-30s %s" % (key, value))


def check(name, ok, detail=""):
    print("%s  %s%s" % ("PASS" if ok else "FAIL", name, (": " + detail) if detail else ""))
    return ok


def cmd_check(argv):
    ev, secrets = argv[0], argv[1]
    results = []

    status = load(os.path.join(ev, "agent-dev.status.json"), {}).get("dagRunDetails", {})
    results.append(check("the agent run succeeded", status.get("statusLabel") == "succeeded", "status %s" % status.get("statusLabel")))

    worker_host = text(os.path.join(ev, "agent-worker.hostname.txt")).strip()
    worker_id = status.get("workerId", "")
    results.append(check("the agent run ran on the agent worker", bool(worker_host) and worker_id.startswith(worker_host),
                         "workerId %r, container host name %r" % (worker_id, worker_host)))

    before = load(os.path.join(ev, "status-before-agent-worker.json"), {}).get("dagRunDetails", {})
    after = load(os.path.join(ev, "status-after-attacks.json"), {}).get("dagRunDetails", {})
    unclaimed = all(not run.get("workerId") and run.get("statusLabel") in ("queued", "not_started", "running")
                    for run in (before, after) if run)
    results.append(check("no worker claimed the agent run while the attacks ran", bool(before) and bool(after) and unclaimed,
                         "before: %s %r, after: %s %r" % (before.get("statusLabel"), before.get("workerId", ""),
                                                          after.get("statusLabel"), after.get("workerId", ""))))

    rogue = text(os.path.join(ev, "attack-rogue.log"))
    results.append(check("the rogue certificate (CN in no pool) is refused",
                         "PermissionDenied" in rogue or "in no worker pool" in rogue, "see attack-rogue.log"))
    general = text(os.path.join(ev, "attack-general.log"))
    results.append(check("the certificate of the general pool cannot announce agent.provider.claude",
                         "PermissionDenied" in general or "not allowed" in general, "see attack-general.log"))
    other = text(os.path.join(ev, "attack-other-ca.log")).lower()
    results.append(check("a certificate from another CA is refused",
                         any(word in other for word in ("unavailable", "certificate", "tls", "handshake")),
                         "the worker cannot connect, see attack-other-ca.log"))

    calls = output_value(os.path.join(ev, "steps", "agent-dev.code.outputs.json"), "tool_calls")
    wanted = ["shell", "read", "edit"]
    results.append(check("the output tool_calls lists the actions of the agent", all('"%s"' % name in calls for name in wanted),
                         calls[:120]))

    steps = os.path.join(ev, "steps")
    bridge_claims(results, status, os.path.join(steps, "agent-dev.code.outputs.json"))
    agent_notes = log_text(os.path.join(steps, "agent-dev.code.stderr.json"))
    summary = output_value(os.path.join(steps, "agent-dev.code.outputs.json"), "summary")
    results.append(check("the skills project-rules and commit-message of the DAG were mounted for the attempt on the agent worker and the agent read them",
                         "Skills read: commit-message, project-rules." in summary
                         and "skills read: commit-message, project-rules" in agent_notes,
                         summary[:160]))
    results.append(check("the folder of the skill commit-message was mounted as it is: the agent read its file references/format.md",
                         "References read: commit-message/references/format.md." in summary
                         and "references read: commit-message/references/format.md" in agent_notes,
                         summary[:200]))
    results.append(check("the agent saw the tests fail, then pass after its fix",
                         "first test run: exit code 1" in agent_notes and "second test run: exit code 0" in agent_notes,
                         "see steps/agent-dev.code.stderr.json"))
    tests = log_text(os.path.join(steps, "agent-dev.verify.stderr.json"))
    results.append(check("the untrusted step verify ran the tests and they passed",
                         "Ran 2 tests" in tests and tests.rstrip().endswith("OK"), "see steps/agent-dev.verify.stderr.json"))

    inspect = load(os.path.join(ev, "agent-worker.inspect.json"), [{}])
    env = (inspect[0].get("Config") or {}).get("Env") or [] if inspect else []
    results.append(check("the agent worker has only the placeholder key", "ANTHROPIC_API_KEY=placeholder" in env,
                         "the environment of the container, see agent-worker.inspect.json"))

    key = text(os.path.join(secrets, "anthropic-key")).strip()
    placeholder = hashlib.sha256(b"placeholder").hexdigest()[:12]
    real = hashlib.sha256(key.encode()).hexdigest()[:12]
    lines = [json.loads(line) for line in text(os.path.join(ev, "fake-anthropic.requests.jsonl")).splitlines() if line.strip()]
    ok = bool(lines) and all(line.get("key_matched") and line.get("key_sha256_prefix") == real for line in lines) \
        and not any(line.get("key_sha256_prefix") == placeholder for line in lines)
    results.append(check("the fake model API saw only the real key of the endpoint, added by the coordinator", ok, "%d requests" % len(lines)))

    gitlog = text(os.path.join(ev, "git-log.txt"))
    results.append(check("the git server has the branch of the agent with calc.py", "agent/fix-calculator-" in gitlog and "calc.py" in gitlog,
                         "see git-log.txt"))
    message = log_text(os.path.join(steps, "agent-dev.message.log.json")).strip()
    results.append(check("the commit of the branch has the message of the mock LLM", bool(message) and message in gitlog,
                         "message %r" % message[:80]))
    unauth = text(os.path.join(ev, "git-server-unauthenticated.txt"))
    results.append(check("the git server refuses a request with no token or a wrong token",
                         "no token: 401" in unauth and "wrong token: 401" in unauth, unauth.strip().replace("\n", ", ")))

    summary = text(os.path.join(ev, "agent-worker.summary.txt"))
    match = re.search(r"restart count: (\d+)", summary)
    results.append(check("the agent worker exited after one run and Docker restarted it", bool(match) and int(match.group(1)) >= 1,
                         summary.strip()[:160]))

    general_status = load(os.path.join(ev, "hello-general.status.json"), {}).get("dagRunDetails", {})
    general_host = text(os.path.join(ev, "general-worker.hostname.txt")).strip()
    hostname_log = log_text(os.path.join(ev, "steps", "hello-general.hostname.log.json"))
    results.append(check("the general run succeeded on the general worker",
                         general_status.get("statusLabel") == "succeeded" and bool(general_host) and general_host in hostname_log,
                         "status %s, host %r" % (general_status.get("statusLabel"), general_host)))

    leaks = []
    for name, value in (("the git token", text(os.path.join(secrets, "git-token")).strip()), ("the real model API key", key)):
        if not value:
            continue
        for path in glob.glob(os.path.join(ev, "**", "*"), recursive=True):
            if os.path.isfile(path) and value in text(path):
                leaks.append("%s in %s" % (name, os.path.relpath(path, ev)))
    results.append(check("no secret value is in the evidence (logs, outputs, status)", not leaks, "; ".join(leaks) or "searched all files"))

    isolation_claims(results, ev, worker_host)

    failed = results.count(False)
    print()
    print("%d of %d claims hold" % (len(results) - failed, len(results)))
    sys.exit(1 if failed else 0)


def jsonl(path):
    lines = []
    for line in text(path).splitlines():
        try:
            lines.append(json.loads(line))
        except ValueError:
            pass
    return lines


def count_of(path):
    """Read the number of hits of a leak scan file. A scan with fewer than 3 samples, or a
    missing file, counts -1: it proves nothing."""
    found = re.search(r"samples: (\d+)\nhits: (\d+)", text(path))
    return int(found.group(2)) if found and int(found.group(1)) >= 3 else -1


def cmd_check_endpoint(argv):
    """The claims of the endpoint path (Spec 094) with the fake CLI, the fake model API and
    the fake MCP server. The folder holds the files that run-demo.sh endpoint writes."""
    ev, secrets = argv[0], argv[1]
    steps = os.path.join(ev, "steps")
    outputs = os.path.join(steps, "agent-dev-endpoint.code.outputs.json")
    results = []
    key = text(os.path.join(secrets, "anthropic-key")).strip()
    token = text(os.path.join(secrets, "mcp-token")).strip()
    key_digest = hashlib.sha256(key.encode()).hexdigest()[:12]
    token_digest = hashlib.sha256(token.encode()).hexdigest()[:12]
    placeholder = hashlib.sha256(b"placeholder").hexdigest()[:12]

    created = load(os.path.join(ev, "endpoint.create.json"), {})
    results.append(check("the API made the endpoint and answered with a fingerprint, never the key",
                         created.get("name") == "demo-upstream" and bool(created.get("keyFingerprint"))
                         and key not in text(os.path.join(ev, "endpoint.create.json")),
                         "fingerprint %s" % created.get("keyFingerprint")))
    mcp = load(os.path.join(ev, "mcp.create.json"), {})
    results.append(check("the API made the named MCP server and answered with no token",
                         mcp.get("name") == "vault" and token not in text(os.path.join(ev, "mcp.create.json")),
                         "url %s" % mcp.get("url")))

    status = load(os.path.join(ev, "status.json"), {}).get("dagRunDetails", {})
    results.append(check("the run of agent-dev-endpoint succeeded", status.get("statusLabel") == "succeeded",
                         "status %s" % status.get("statusLabel")))
    host = text(os.path.join(ev, "agent-worker.hostname.txt")).strip()
    results.append(check("the run went to the agent worker", bool(host) and status.get("workerId", "").startswith(host),
                         "workerId %r, container host name %r" % (status.get("workerId", ""), host)))

    lines = jsonl(os.path.join(ev, "fake-anthropic.requests.jsonl"))
    messages = [line for line in lines if line.get("path", "").startswith("/v1/messages")]
    results.append(check("the fake model API received the real key of the endpoint for each message of the run",
                         bool(messages) and all(line.get("key_matched") and line.get("key_sha256_prefix") == key_digest for line in messages),
                         "%d message requests" % len(messages)))
    results.append(check("the fake model API never saw the secret of the relay or the placeholder of the worker",
                         bool(lines) and all(line.get("key_matched") and line.get("key_sha256_prefix") != placeholder for line in lines),
                         "%d requests, all with the key of the endpoint" % len(lines)))
    results.append(check("the request reached the upstream with the stream flag", any(line.get("stream") for line in messages),
                         "stream flag of the request"))

    stream = object_output(outputs, "stream")
    spread = stream.get("last_ms", 0) - stream.get("first_ms", 0)
    results.append(check("streaming works: the client got each event when the upstream sent it, not in one piece at the end",
                         stream.get("text_ok") is True and stream.get("events", 0) >= 5
                         and 0 <= stream.get("first_ms", -1) < 1000 and spread >= 1200,
                         "%s events, first at %s ms, last at %s ms" % (stream.get("events"), stream.get("first_ms"), stream.get("last_ms"))))

    environment = object_output(outputs, "environment")
    agent_digest = environment.get("api_key_sha256_prefix", "")
    results.append(check("the CLI had a relay address on the loopback and a secret that is neither the real key nor the placeholder",
                         environment.get("base_url_is_loopback") is True and bool(agent_digest)
                         and agent_digest not in (key_digest, placeholder) and environment.get("oauth_token_in_environment") is False,
                         "hash of the key of the CLI %s" % agent_digest))

    leaks = {name: count_of(os.path.join(ev, "leak-scan.%s.txt" % name)) for name in ("model-key", "mcp-token")}
    results.append(check("the agent worker never held the model key: no hit in its environment, files, processes and logs during the run",
                         leaks["model-key"] == 0, "hits %d" % leaks["model-key"]))
    results.append(check("the agent worker never held the MCP token", leaks["mcp-token"] == 0, "hits %d" % leaks["mcp-token"]))
    inspect = text(os.path.join(ev, "agent-worker.inspect.json"))
    results.append(check("the container of the agent worker has the placeholder and no model key or token in its settings",
                         "ANTHROPIC_API_KEY=placeholder" in inspect and key not in inspect and token not in inspect, "docker inspect"))

    mcp_lines = jsonl(os.path.join(ev, "fake-mcp.requests.jsonl"))
    calls = [line for line in mcp_lines if "vault_lookup" in line.get("tools", "")]
    results.append(check("the named MCP server received its bearer token from the coordinator on each request",
                         bool(mcp_lines) and all(line.get("token_matched") and line.get("token_sha256_prefix") == token_digest for line in mcp_lines),
                         "%d requests" % len(mcp_lines)))
    tools = object_output(outputs, "tools")
    results.append(check("the agent called vault_lookup through the relay and got the value",
                         bool(calls) and "vault: build = 42" in tools.get("vault_lookup", ""), tools.get("vault_lookup", "")[:80]))
    results.append(check("a tool that allow does not list was refused by the coordinator and never reached the MCP server",
                         tools.get("vault_admin", "").startswith("refused:")
                         and not any("vault_admin" in line.get("tools", "") for line in mcp_lines),
                         tools.get("vault_admin", "")[:100]))
    ticket = tools.get("lookup_ticket", "")
    results.append(check("the tool bridge still works with an endpoint: lookup-ticket ran with the CUSTOMER_ID of the DAG",
                         FIXED_CUSTOMER in ticket and AGENT_CUSTOMER not in ticket and not ticket.startswith("error"), ticket[:100]))
    try:
        skills = json.loads(output_value(outputs, "skills") or "[]")
    except ValueError:
        skills = []
    results.append(check("the skills of the step were in the files of the attempt", skills == ["commit-message", "project-rules"],
                         "skills read %s" % skills))
    results.append(check("the file references/format.md of the skill commit-message was in the files of the attempt and the agent read it",
                         "References read: commit-message/references/format.md." in output_value(outputs, "summary"),
                         output_value(outputs, "summary")[:200]))

    audit = text(os.path.join(ev, "relay-audit.log")).replace('"', "")
    results.append(check("the coordinator wrote an audit line for each model request and each MCP request, with a fingerprint and no key",
                         "target_kind=endpoint" in audit and "target_kind=mcp" in audit and "key_fingerprint" in audit
                         and key not in audit and token not in audit,
                         "%d lines" % len(audit.splitlines())))

    denied = load(os.path.join(ev, "denied.status.json"), {}).get("dagRunDetails", {})
    denied_error = " ".join(str(node.get("error", "")) for node in denied.get("nodes", []))
    after = jsonl(os.path.join(ev, "fake-anthropic.requests.after-denied.jsonl"))
    results.append(check("a DAG that is not in allow.dags failed at its start check with endpoint_forbidden, and no request reached the upstream",
                         denied.get("statusLabel") == "failed" and "endpoint_forbidden" in denied_error and len(after) == len(lines),
                         denied_error[:140]))

    first = load(os.path.join(ev, "models.initial.json"), {})
    second = load(os.path.join(ev, "models.reload.json"), {})
    results.append(check("the first read of the models list after the create worked with the key of the endpoint",
                         first.get("count") == 2 and "claude-demo" in (first.get("models") or []), "%s models" % first.get("count")))
    results.append(check("a Reload read the new list from the fake upstream",
                         second.get("count") == 3 and "claude-demo-new" in (second.get("models") or []) and not second.get("error"),
                         "%s models" % second.get("count")))
    model_calls = [line for line in lines if line.get("path", "").startswith("/v1/models")]
    results.append(check("the models calls carried the real key",
                         len(model_calls) >= 2 and all(line.get("key_matched") and line.get("key_sha256_prefix") == key_digest for line in model_calls),
                         "%d calls" % len(model_calls)))

    found = []
    for name, value in (("the model key", key), ("the MCP token", token)):
        for path in glob.glob(os.path.join(ev, "**", "*"), recursive=True):
            if os.path.isfile(path) and value in text(path):
                found.append("%s in %s" % (name, os.path.relpath(path, ev)))
    results.append(check("no secret value is in the evidence of the endpoint path", not found, "; ".join(found) or "searched all files"))

    failed = results.count(False)
    print()
    print("%d of %d claims hold" % (len(results) - failed, len(results)))
    sys.exit(1 if failed else 0)


def audit_lines(path):
    """Read the audit lines of the relay (key=value pairs) as a list of dicts."""
    lines = []
    for raw in text(path).splitlines():
        found = dict(re.findall(r'(\w+)=("[^"]*"|\S+)', raw))
        if found.get("action") == "ai_relay_request":
            lines.append({key: value.strip('"') for key, value in found.items()})
    return lines


def relay_claim(results, audit_path, secrets):
    """The claim of the real mode (run-real.sh): the relay of the coordinator carried each model
    request to the endpoint claude-sub. The CLI has the secret of the relay, never the credential."""
    audit = audit_lines(audit_path)
    models = [line for line in audit if line.get("route") == "messages"]
    raw = text(audit_path)
    results.append(check("the relay carried each model request of the run to the endpoint claude-sub with the status 200, and the audit lines have one key fingerprint and no secret value",
                         bool(models) and all(line.get("status") == "200" and line.get("target") == "claude-sub" for line in models)
                         and len({line.get("key_fingerprint") for line in models}) == 1
                         and not any(value and value in raw for value in secrets.values()),
                         "%d model requests" % len(models)))


def last_status(run_dir):
    return load(os.path.join(run_dir, "status.json"), {}).get("dagRunDetails", {})


def node_errors(details):
    return " ".join(str(node.get("error", "")) for node in details.get("nodes", []))


def cmd_check_endpoint_real(argv):
    """The claims of the endpoint path with the real Claude Code (run-real-endpoint.sh). The
    folder holds main/, smoke/, refusals/ and evidence/. The token files are read to look for
    the secrets in the evidence: the output holds counts and never a value."""
    real, sub_file, mcp_file, github_file, proxy_file = argv
    results = []
    sub = secret_value(sub_file)
    secrets = {"the subscription token": sub, "the MCP token": secret_value(mcp_file),
               "the GitHub token": secret_value(github_file), "the proxy credential": secret_value(proxy_file)}
    main = os.path.join(real, "main")
    details = last_status(main)
    nodes = {(n.get("step") or {}).get("id"): n.get("statusLabel") for n in details.get("nodes", [])}
    results.append(check("the run of agent-dev-endpoint-real succeeded, with all its steps",
                         details.get("statusLabel") == "succeeded" and bool(nodes) and set(nodes.values()) == {"succeeded"},
                         "%s, steps %s" % (details.get("statusLabel"), dict(nodes))))
    host = text(os.path.join(main, "agent-worker.hostname.txt")).strip()
    results.append(check("the run went to the agent worker of the real Claude Code",
                         bool(host) and details.get("workerId", "").startswith(host), "workerId %r" % details.get("workerId", "")))
    outputs = os.path.join(main, "steps", "code.outputs.json")
    values = {name: output_value(outputs, name) for name in ("verdict", "skill_loaded", "vault_value", "changed_files", "summary")}
    results.append(check("the structured output of the step code is valid: verdict, skill, vault value and changed files",
                         values["verdict"] == "done" and values["skill_loaded"] == "project-rules" and values["vault_value"] == "42"
                         and '"calc.py"' in values["changed_files"] and bool(values["summary"]),
                         "verdict %s, skill %s, vault %s" % (values["verdict"], values["skill_loaded"], values["vault_value"])))

    stream = glob.glob(os.path.join(main, "*.stream.jsonl"))
    events = stream_events(stream[0]) if stream else []
    init = last_of(events, "system", "init")
    uses = tool_uses(events)
    names = [name for name, _ in uses]
    skill_events = [event for event in events if event.get("type") == "user" and "Base directory for this skill" in json.dumps(event)]
    results.append(check("the real CLI found the skill under --add-dir: it lists project-rules, calls its tool Skill and gets the text of SKILL.md",
                         "project-rules" in (init.get("skills") or []) and ("Skill", {"skill": "project-rules"}) in uses and bool(skill_events)
                         and bool(init.get("additional_directories")),
                         "version %s, additional directories %d" % (init.get("claude_code_version"), len(init.get("additional_directories") or []))))
    servers = {server.get("name"): server.get("status") for server in init.get("mcp_servers") or []}
    results.append(check("the real CLI connected to the tool bridge and to the named MCP server, through the relay",
                         servers.get("dagu") == "connected" and servers.get("real-vault") == "connected", str(servers)))
    mcp_lines = jsonl(os.path.join(main, "fake-mcp.requests.jsonl"))
    mcp_digest = hashlib.sha256(secrets["the MCP token"].encode()).hexdigest()[:12]
    results.append(check("the MCP server received its bearer token from the coordinator, on each request, and the agent called vault_lookup",
                         bool(mcp_lines) and all(line.get("token_matched") and line.get("token_sha256_prefix") == mcp_digest for line in mcp_lines)
                         and any("vault_lookup" in line.get("tools", "") for line in mcp_lines)
                         and "mcp__real-vault__vault_lookup" in names, "%d requests" % len(mcp_lines)))
    runs = sub_runs(node_of(details, "code"))
    tickets = [run for run in runs if run.get("dagName") == "lookup-ticket"]
    results.append(check("the Dagu tool bridge works with an endpoint: lookup-ticket ran as a child run with the CUSTOMER_ID of the DAG",
                         "mcp__dagu__lookup-ticket" in names and bool(tickets) and FIXED_CUSTOMER in str(tickets[0].get("params", "")),
                         "child runs %d" % len(tickets)))

    audit = audit_lines(os.path.join(main, "relay-audit.log"))
    models = [line for line in audit if line.get("route") == "messages"]
    results.append(check("the relay carried each model request of the run to the endpoint claude-sub with status 200",
                         bool(models) and all(line.get("status") == "200" and line.get("target") == "claude-sub" for line in models),
                         "%d model requests" % len(models)))
    results.append(check("the audit lines have one key fingerprint for the endpoint and no secret value",
                         len({line.get("key_fingerprint") for line in models}) == 1
                         and not any(value and value in text(os.path.join(main, "relay-audit.log")) for value in secrets.values()),
                         "fingerprint %s" % (models[0].get("key_fingerprint") if models else "-")))

    key_hits = count_of(os.path.join(main, "leak-scan.model-key.txt"))
    token_hits = count_of(os.path.join(main, "leak-scan.mcp-token.txt"))
    results.append(check("the agent worker never held the subscription token: no hit in its environment, files, processes and arguments during the run",
                         key_hits == 0, "hits %d" % key_hits))
    results.append(check("the agent worker never held the MCP token", token_hits == 0, "hits %d" % token_hits))
    inspect = text(os.path.join(main, "agent-worker.inspect.json"))
    env = (load(os.path.join(main, "agent-worker.inspect.json"), [{}]) or [{}])[0].get("Config", {}).get("Env") or []
    results.append(check("the container of the agent worker has no model key, no model address and no OAuth variable",
                         not any(item.split("=")[0] in ("ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN") for item in env)
                         and sub not in inspect, "%d variables" % len(env)))
    proxy = text(os.path.join(main, "docker-egress-proxy.log"))
    results.append(check("the proxy has no model route: it never allowed api.anthropic.com, and it refused the probes of the agent",
                         "allow CONNECT api.anthropic.com" not in proxy and "upstream" not in proxy.lower() and "deny api.anthropic.com:443" in proxy,
                         "see docker-egress-proxy.log"))
    probes = probe_results(os.path.join(main, "steps", "probes.log.json"))
    allowed = [name for name, value in probes.items()
               if (name.startswith(("read_", "write_", "rename_", "direct ", "http ")) or name.startswith("connect "))
               and not value.startswith("denied:")]
    results.append(check("the hostile probes after the agent are all denied", bool(probes) and not allowed,
                         "%d probes, allowed: %s" % (len(probes), ", ".join(allowed) or "none")))
    branch = load(os.path.join(main, "github-branch.json"), {})
    changed = [item.get("filename") for item in branch.get("files", [])]
    results.append(check("the trusted step pushed one commit that changes calc.py only", branch.get("exists") is True and branch.get("ahead_by") == 1
                         and changed == ["calc.py"], "files %s" % changed))

    smoke = audit_lines(os.path.join(real, "smoke", "relay-audit.log"))
    first, total = (int(smoke[0].get("first_byte_ms", "-1")), int(smoke[0].get("duration_ms", "-1"))) if smoke else (-1, -1)
    results.append(check("streaming works with the real provider: the first byte reached the client long before the end of the response",
                         bool(smoke) and 0 < first < total and total - first >= 500,
                         "first byte at %d ms, end at %d ms" % (first, total)))

    refusals = os.path.join(real, "refusals")
    denied = last_status(os.path.join(refusals, "denied"))
    results.append(check("a DAG that is not in allow.dags failed at the start check with endpoint_forbidden, with no model request",
                         denied.get("statusLabel") == "failed" and "endpoint_forbidden" in node_errors(denied)
                         and not audit_lines(os.path.join(refusals, "denied", "relay-audit.log")), node_errors(denied)[:110]))
    bad = last_status(os.path.join(refusals, "badkey"))
    bad_lines = audit_lines(os.path.join(refusals, "badkey", "relay-audit.log"))
    bad_stream = glob.glob(os.path.join(refusals, "badkey", "*.stream.jsonl"))
    bad_result = last_of(stream_events(bad_stream[0]), "result") if bad_stream else {}
    results.append(check("a key that the provider refuses: one request, the status 401 reaches the CLI, and the step fails as auth_required",
                         bad.get("statusLabel") == "failed" and "auth_required" in node_errors(bad) and len(bad_lines) == 1
                         and bad_lines[0].get("status") == "401" and "401" in str(bad_result.get("result", "")),
                         str(bad_result.get("result", ""))[:100]))
    gone = last_status(os.path.join(refusals, "gone"))
    gone_lines = audit_lines(os.path.join(refusals, "gone", "relay-audit.log"))
    gone_stream = glob.glob(os.path.join(refusals, "gone", "*.stream.jsonl"))
    gone_result = last_of(stream_events(gone_stream[0]), "result") if gone_stream else {}
    results.append(check("an endpoint deleted during the run: the next request is refused with endpoint_unknown, an audit line records it, and the step fails with that reason",
                         gone.get("statusLabel") == "failed" and "(endpoint_unknown)" in node_errors(gone)
                         and any(line.get("reason") == "endpoint_unknown" and line.get("status") == "404" for line in gone_lines),
                         "the CLI said: %s" % str(gone_result.get("result", ""))[:90]))

    state = load(os.path.join(real, "evidence", "models.claude-sub.state.json"), {})
    reload_ = load(os.path.join(real, "evidence", "models.claude-sub.reload.json"), {})
    results.append(check("the models list of claude-sub read with the subscription token: GET /v1/models accepted the OAuth token",
                         state.get("count", 0) > 0 and not state.get("error") and reload_.get("count", 0) > 0 and not reload_.get("error"),
                         "%s models after the Reload" % reload_.get("count")))

    found = []
    for name, value in secrets.items():
        if not value:
            continue
        for path in glob.glob(os.path.join(real, "**", "*"), recursive=True):
            if os.path.isfile(path) and value in text(path):
                found.append("%s in %s" % (name, os.path.relpath(path, real)))
        # The first 12 characters of a credential count too.
        for path in glob.glob(os.path.join(real, "**", "*"), recursive=True):
            if os.path.isfile(path) and len(value) >= 24 and value[:12] in text(path):
                found.append("the start of %s in %s" % (name, os.path.relpath(path, real)))
    results.append(check("no secret value, and no start of one, is in the evidence (logs, outputs, streams, status, inspect files)",
                         not found, "; ".join(found) or "searched all files"))

    failed = results.count(False)
    print()
    print("%d of %d claims hold" % (len(results) - failed, len(results)))
    sys.exit(1 if failed else 0)


def secret_value(path):
    """Return the content of a file without its last line end."""
    value = text(path)
    return value[:-1] if value.endswith("\n") else value


def request_json(method, url, body=None):
    """Call the REST API. Return the status and the JSON body. An error body is not
    returned, so that no message can repeat a value of the request."""
    data = json.dumps(body).encode() if body is not None else None
    request = urllib.request.Request(url, data=data, method=method, headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return response.status, json.loads(response.read() or b"{}")
    except urllib.error.HTTPError as error:
        return error.code, {}


def cmd_secret_put(argv):
    api, ref, path = argv
    value = secret_value(path)
    if not value:
        sys.exit("secret %s: the file %s is empty" % (ref, path))
    status, _ = request_json("POST", api + "/secrets", {"ref": ref, "providerType": "dagu-managed", "value": value})
    if status == 201:
        print("secret %s: created" % ref)
        return
    if status != 409:
        sys.exit("secret %s: POST /secrets: status %d" % (ref, status))
    _, listing = request_json("GET", api + "/secrets?limit=500")
    for secret in listing.get("secrets", []):
        if secret.get("ref") == ref:
            status, _ = request_json("POST", api + "/secrets/%s/versions" % secret["id"], {"value": value})
            if status >= 300:
                sys.exit("secret %s: POST /secrets/%s/versions: status %d" % (ref, secret["id"], status))
            print("secret %s: exists, wrote a new version" % ref)
            return
    sys.exit("secret %s: status 409, but the list has no such secret" % ref)


def ai_path(kind):
    return "/ai/endpoints" if kind == "endpoint" else "/ai/mcp-servers"


def cmd_ai_put(argv):
    api, kind, spec_json, secret_file = argv[:4]
    out = argv[4] if len(argv) > 4 else ""
    spec = json.loads(spec_json)
    value = secret_value(secret_file)
    if not value:
        sys.exit("the file %s is empty" % secret_file)
    spec["key" if kind == "endpoint" else "token"] = value
    request_json("DELETE", "%s%s/%s" % (api, ai_path(kind), spec["name"]))
    status, body = request_json("POST", api + ai_path(kind), spec)
    if status != 201:
        sys.exit("POST %s: status %d" % (ai_path(kind), status))
    shown = json.dumps(body, indent=2)
    if value in shown:
        sys.exit("the response of POST %s holds the secret" % ai_path(kind))
    if out:
        with open(out, "w") as handle:
            handle.write(shown + "\n")
    print("%s %s: created" % (kind, spec["name"]))


def cmd_ai_delete(argv):
    api, kind, name = argv
    status, _ = request_json("DELETE", "%s%s/%s" % (api, ai_path(kind), name))
    print("%s %s: delete status %d" % (kind, name, status))


def stream_events(path):
    """Return the JSON objects of a stream file, one for each line."""
    events = []
    for line in text(path).splitlines():
        line = line.strip()
        if line.startswith("{"):
            try:
                events.append(json.loads(line))
            except ValueError:
                pass
    return events


def tool_uses(events):
    """Return (name, input) for each tool_use block of the assistant messages."""
    uses = []
    for event in events:
        if event.get("type") != "assistant":
            continue
        for block in (event.get("message") or {}).get("content") or []:
            if isinstance(block, dict) and block.get("type") == "tool_use":
                uses.append((block.get("name", ""), block.get("input") or {}))
    return uses


def last_of(events, kind, subtype=None):
    found = None
    for event in events:
        if event.get("type") == kind and (subtype is None or event.get("subtype") == subtype):
            found = event
    return found or {}


def cmd_stream(argv):
    events = stream_events(argv[0])
    print("events: %d, types: %s" % (len(events), sorted({e.get("type", "?") for e in events})))
    init = last_of(events, "system", "init")
    print("init: " + json.dumps({k: init.get(k) for k in ("model", "claude_code_version", "apiKeySource", "permissionMode", "cwd", "tools", "mcp_servers")}))
    for name, value in tool_uses(events):
        print("  tool_use %-18s %s" % (name, json.dumps(value)[:200]))
    result = last_of(events, "result")
    print("result: " + json.dumps({k: result.get(k) for k in ("subtype", "is_error", "num_turns", "duration_ms", "duration_api_ms",
                                                            "total_cost_usd", "permission_denials", "result")})[:1500])
    print("usage: " + json.dumps(result.get("usage")))
    print("modelUsage: " + json.dumps(result.get("modelUsage")))


def probe_results(log_path):
    """Read the lines "probe <name> <result>" of the step probes."""
    probes = {}
    for line in log_text(log_path).splitlines():
        if line.startswith("probe "):
            name, _, value = line[len("probe "):].rstrip().rpartition(" ")
            probes[name.strip()] = value
    return probes


def cmd_check_real(argv):
    ev, anthropic_file, github_file, credential_file = argv
    steps = os.path.join(ev, "steps")
    results = []
    secrets = {
        "the model credential": secret_value(anthropic_file),
        "the GitHub token": secret_value(github_file),
        "the proxy credential": secret_value(credential_file),
    }
    secrets = {name: value for name, value in secrets.items() if value}

    status = load(os.path.join(ev, "agent-dev-real.status.json"), {}).get("dagRunDetails", {})
    results.append(check("the agent run succeeded", status.get("statusLabel") == "succeeded", "status %s" % status.get("statusLabel")))
    worker_host = text(os.path.join(ev, "agent-worker.hostname.txt")).strip()
    worker_id = status.get("workerId", "")
    results.append(check("the agent run ran on the agent worker", bool(worker_host) and worker_id.startswith(worker_host),
                         "workerId %r, container host name %r" % (worker_id, worker_host)))

    version = text(os.path.join(ev, "claude-version.txt")).strip()
    events = stream_events(os.path.join(ev, "agent-dev-real.code.stream.jsonl"))
    init = last_of(events, "system", "init")
    result = last_of(events, "result")
    results.append(check("the agent worker ran the real Claude Code", "(Claude Code)" in version and bool(init)
                         and init.get("claude_code_version", "") in version,
                         "%s; stream init: version %s, model %s, key source %s" % (version, init.get("claude_code_version"),
                                                                                  init.get("model"), init.get("apiKeySource"))))
    results.append(check("the stream of the real CLI is stored and ends with a success", bool(events) and result.get("subtype") == "success"
                         and not result.get("is_error"), "%d events, result %s, %s turns" % (len(events), result.get("subtype"), result.get("num_turns"))))

    servers = {server.get("name"): server.get("status") for server in init.get("mcp_servers") or []}
    results.append(check("the real CLI reached the tool bridge (dagu) and the MCP server docs: system/init shows both connected",
                         servers.get("dagu") == "connected" and servers.get("docs") == "connected", "mcp_servers %s" % servers))
    used = [name for name, _ in tool_uses(events)]
    wanted = ["mcp__dagu__lookup-ticket", "mcp__dagu__ops_notify", "mcp__docs__dagu_read"]
    results.append(check("the real CLI called the two DAG tools of the bridge, and the MCP tool dagu_read",
                         all(name in used for name in wanted), "tool calls %s" % used))
    code_node = node_of(status, "code")
    runs = sub_runs(code_node)
    ticket = " ".join(str(run.get("params", "")) for run in runs if run.get("dagName") == "lookup-ticket")
    notified = [run for run in runs if "ops_notify" in str(run.get("dagName", ""))]
    results.append(check("Dagu ran lookup-ticket with the fixed CUSTOMER_ID and a QUESTION of the agent, and ops_notify, as child runs",
                         FIXED_CUSTOMER in ticket and "QUESTION" in ticket and bool(notified),
                         "lookup-ticket params %r, ops_notify runs %d" % (ticket[:160], len(notified))))

    baseline = log_text(os.path.join(steps, "agent-dev-real.baseline.stderr.json"))
    baseline_out = log_text(os.path.join(steps, "agent-dev-real.baseline.log.json"))
    results.append(check("the tests failed before the agent ran", "FAILED" in baseline and "as expected" in baseline_out,
                         "see steps/agent-dev-real.baseline.*"))
    uses = tool_uses(events)
    test_runs = [value for name, value in uses if name == "Bash" and "./scripts/test.sh" in str(value.get("command", ""))]
    edits = [value for name, value in uses if name in ("Edit", "Write", "MultiEdit")]
    edited = sorted({os.path.basename(str(value.get("file_path", ""))) for value in edits})
    results.append(check("the agent ran the tests and changed only the source", len(test_runs) >= 1 and edited == ["calc.py"],
                         "%d test runs, edited files %s, tool calls %s" % (len(test_runs), edited, [name for name, _ in uses])))
    tests = log_text(os.path.join(steps, "agent-dev-real.verify.stderr.json"))
    results.append(check("the untrusted step verify ran the tests and they passed",
                         "Ran 2 tests" in tests and tests.rstrip().endswith("OK"), "see steps/agent-dev-real.verify.stderr.json"))
    left = log_text(os.path.join(steps, "agent-dev-real.evidence.log.json"))
    results.append(check("no process of the restricted user is left after the untrusted steps",
                         "== processes of the restricted user (user ID 10001)\nnone" in left, "see steps/agent-dev-real.evidence.log.json"))

    probes = probe_results(os.path.join(steps, "agent-dev-real.probes.log.json"))
    allowed = [name for name, value in probes.items() if name != "user" and not value.startswith("denied:")]
    results.append(check("each probe of the restricted user was denied", len(probes) >= 15 and probes.get("user") == "10001:10001" and not allowed,
                         "%d probes, user %s, allowed: %s" % (len(probes), probes.get("user"), ", ".join(allowed) or "none")))
    results.append(check("the proxy needs the proxy credential for github.com and api.anthropic.com",
                         probes.get("connect github.com:443") == "denied:407" and probes.get("connect api.anthropic.com:443") == "denied:407"
                         and probes.get("connect example.com:443") == "denied:403" and probes.get("http example.com") == "denied:403",
                         "github %s, anthropic %s, example.com %s / %s" % (probes.get("connect github.com:443"), probes.get("connect api.anthropic.com:443"),
                                                                          probes.get("connect example.com:443"), probes.get("http example.com"))))
    env_dump = log_text(os.path.join(steps, "agent-dev-real.probes.log.json")).split("== environment of the restricted user", 1)[-1]
    env_lines = [line[4:] for line in env_dump.splitlines() if line.startswith("env ")]
    bad = [line.split("=", 1)[0] for line in env_lines if "*******" in line or line.split("=", 1)[0] in ("GIT_TOKEN", "PROXY_CREDENTIAL")]
    results.append(check("the environment of the restricted user holds the placeholder and no secret",
                         "ANTHROPIC_API_KEY=placeholder" in env_lines and not bad, "%d variables, masked or secret: %s" % (len(env_lines), bad or "none")))

    inspect = load(os.path.join(ev, "agent-worker.inspect.json"), [{}])
    config = (inspect[0].get("Config") or {}) if inspect else {}
    env = config.get("Env") or []
    mounts = [m.get("Source", "") for m in (inspect[0].get("Mounts") or [])] if inspect else []
    in_env = [name for name, value in secrets.items() if any(value in item for item in env)]
    in_mounts = [path for path in mounts if os.path.realpath(path) in {os.path.realpath(p) for p in (anthropic_file, github_file, credential_file)}]
    results.append(check("the agent worker container has the placeholder, no secret in its environment and no secret file",
                         "ANTHROPIC_API_KEY=placeholder" in env and not in_env and not in_mounts,
                         "secrets in env: %s; mounts: %s" % (in_env or "none", ", ".join(mounts))))

    proxy_log = text(os.path.join(ev, "docker-egress-proxy.log"))
    relay_claim(results, os.path.join(ev, "relay-audit.log"), secrets)
    denied = re.findall(r"deny (?:CONNECT |GET |POST )?([\w.-]+:\d+)", proxy_log)
    expected = {"github.com:443", "api.anthropic.com:443", "example.com:443", "example.com:80"}
    extra = sorted(set(denied) - expected)
    results.append(check("the proxy refused only the probes: the CLI made no request that the proxy refused",
                         expected <= set(denied) and not extra, "refused %s, other refused destinations: %s" % (sorted(set(denied)), extra or "none")))
    tunnels = re.findall(r"allow CONNECT ([\w.-]+:\d+)( \(proxy credential\))?", proxy_log)
    results.append(check("only the steps clone and push opened a tunnel, to github.com with the proxy credential",
                         len(tunnels) >= 2 and all(t == ("github.com:443", " (proxy credential)") for t in tunnels),
                         "tunnels: %s" % sorted({t[0] + t[1] for t in tunnels})))

    branch = text(os.path.join(ev, "branch.txt")).strip()
    github = load(os.path.join(ev, "github-branch.json"), {})
    files = [f.get("filename") for f in github.get("files", [])]
    results.append(check("GitHub has the branch of the agent, one commit that changes calc.py only",
                         github.get("exists") is True and github.get("branch") == branch and branch.startswith("agent/fix-")
                         and files == ["calc.py"] and github.get("ahead_by") == 1,
                         "branch %s, ahead by %s, files %s" % (github.get("branch"), github.get("ahead_by"), files)))

    summary = text(os.path.join(ev, "agent-worker.summary.txt"))
    match = re.search(r"restart count: (\d+)", summary)
    results.append(check("the agent worker exited after one run and Docker restarted it", bool(match) and int(match.group(1)) >= 1,
                         summary.strip()[:160]))
    general_status = load(os.path.join(ev, "hello-general.status.json"), {}).get("dagRunDetails", {})
    general_host = text(os.path.join(ev, "general-worker.hostname.txt")).strip()
    hostname_log = log_text(os.path.join(ev, "steps", "hello-general.hostname.log.json"))
    results.append(check("the general run succeeded on the general worker",
                         general_status.get("statusLabel") == "succeeded" and bool(general_host) and general_host in hostname_log,
                         "status %s, host %r" % (general_status.get("statusLabel"), general_host)))

    leaks = []
    forms = {}
    for name, value in secrets.items():
        forms[name] = value
        forms[name + " (Basic form)"] = base64.b64encode(("git:" + value).encode()).decode()
        forms[name + " (Basic form of GitHub)"] = base64.b64encode(("x-access-token:" + value).encode()).decode()
    for path in glob.glob(os.path.join(ev, "**", "*"), recursive=True):
        if not os.path.isfile(path):
            continue
        content = text(path)
        for name, value in forms.items():
            if value in content:
                leaks.append("%s in %s" % (name, os.path.relpath(path, ev)))
    results.append(check("no secret value is in the evidence (logs, outputs, status, stream)", not leaks,
                         "; ".join(leaks) or "searched all files for %d values" % len(forms)))

    failed = results.count(False)
    print()
    print("%d of %d claims hold" % (len(results) - failed, len(results)))
    print("usage of the CLI: %s; CLI list price %s USD" % (json.dumps(result.get("modelUsage")), result.get("total_cost_usd")))
    sys.exit(1 if failed else 0)


# ---------------------------------------------------------------------------
# The verify loop (the DAGs agent-dev-loop and agent-dev-loop-real)
# ---------------------------------------------------------------------------

LOOP_STEPS = ["code_1", "verify_1", "code_2", "verify_2", "code_3", "verify_3"]


def node_labels(status_path):
    """Return {step id: status label} of the nodes of a run, and the label of the run."""
    details = load(status_path, {}).get("dagRunDetails", {})
    labels = {}
    for node in details.get("nodes", []):
        step = node.get("step", {})
        labels[step.get("id") or step.get("name", "?")] = node.get("statusLabel", "?")
    return labels, details.get("statusLabel", "?"), details


def step_text(case_dir, step, stream="log"):
    return log_text(os.path.join(case_dir, "steps", "%s.%s.json" % (step, stream)))


def verify_steps_have_no_key(dag_text):
    """True when no step verify_<n> of the DAG file has the key untrusted: they are untrusted by default."""
    blocks = re.split(r"\n  - id: ", dag_text)
    found = [block for block in blocks if re.match(r"verify_\d+\n", block)]
    return len(found) >= 3 and all("untrusted:" not in block.split("\n  # ")[0] for block in found)


def loop_claims(results, case_dir, dag_text, host, expect, real=False):
    """Claims about the run of one case: expect is "fixed" (verify_1 fails, round 2 fixes) or "stuck"
    (each verify fails)."""
    labels, run_label, details = node_labels(os.path.join(case_dir, "status.json"))
    prefix = "[%s] " % case_dir.rstrip("/").split("/")[-1]
    if expect == "fixed":
        want = {"clone": "succeeded", "code_1": "succeeded", "verify_1": "failed", "code_2": "succeeded",
                "verify_2": "succeeded", "code_3": "skipped", "verify_3": "skipped", "evidence": "succeeded",
                "check_paths": "succeeded", "push": "succeeded"}
        want_run = "partially_succeeded"
    else:
        want = {"clone": "succeeded", "code_1": "succeeded", "verify_1": "failed", "code_2": "succeeded",
                "verify_2": "failed", "code_3": "succeeded", "verify_3": "failed", "evidence": "aborted",
                "check_paths": "aborted", "push": "aborted"}
        want_run = "failed"
    got = {step: labels.get(step) for step in want}
    results.append(check(prefix + "the run ended %s and each step has the expected status" % want_run,
                         run_label == want_run and got == want,
                         "run %s; steps %s" % (run_label, json.dumps(got))))
    worker_id = details.get("workerId", "")
    results.append(check(prefix + "the whole loop was one run on the agent worker",
                         bool(host) and worker_id.startswith(host)
                         and not any(node.get("subRuns") or node.get("subRunsRepeated") for node in details.get("nodes", [])),
                         "workerId %r, container host name %r, sub runs: none" % (worker_id, host)))
    results.append(check(prefix + "the steps verify_<n> have no key untrusted in the DAG, and ran as the restricted user (user ID 10001)",
                         verify_steps_have_no_key(dag_text)
                         and "verify: running as user ID 10001" in step_text(case_dir, "verify_1"),
                         "see steps/verify_1.log.json"))
    return labels


def cmd_check_loop(argv):
    ev, secrets_dir, dag_file = argv
    dag_text = text(dag_file)
    results = []
    host = text(os.path.join(ev, "agent-worker.hostname.txt")).strip()
    token = text(os.path.join(secrets_dir, "git-token")).strip()

    fixed = os.path.join(ev, "fixed")
    loop_claims(results, fixed, dag_text, host, "fixed")
    log1 = step_text(fixed, "verify_1")
    results.append(check("[fixed] round 1 fixed the visible bug only: the tests pass in verify_1, and the verify fails on the second bug",
                         "Ran 2 tests" in log1 and "FAILED" not in log1 and "must return None" in log1,
                         "see steps/verify_1.log.json"))
    err2 = step_text(fixed, "code_2", "stderr")
    outputs2 = load(os.path.join(fixed, "steps", "code_2.outputs.json"), {})
    calls2 = next((item.get("value", "") for item in outputs2.get("outputs", []) if item.get("name") == "tool_calls"), "")
    results.append(check("[fixed] the agent of round 2 read the log of verify_1 and fixed the bug that the log names",
                         re.search(r"read the log \S*/verify_1\.log", err2) is not None and "applied the fix that the log names: divide" in err2
                         and '"read"' in calls2,
                         "see steps/code_2.stderr.json; tool_calls %s" % calls2[:80]))
    log2 = step_text(fixed, "verify_2")
    results.append(check("[fixed] verify_2 passed: the tests and the second check",
                         "OK" in log2 and "verify: ok" in log2, "see steps/verify_2.log.json"))
    branch = text(os.path.join(fixed, "branch.txt")).strip()
    code = text(os.path.join(fixed, "branch-calc.py.txt"))
    commits = text(os.path.join(fixed, "branch-commits.txt")).strip()
    results.append(check("[fixed] the git server has the branch with one commit, and the branch has both fixes",
                         commits == "1" and "return a + b" in code and "if b == 0" in code,
                         "branch %s, %s commit(s) after main" % (branch, commits)))

    stuck = os.path.join(ev, "stuck")
    loop_claims(results, stuck, dag_text, host, "stuck")
    stuck_branch = text(os.path.join(stuck, "branch.txt")).strip()
    gitlog = text(os.path.join(stuck, "git-log.txt"))
    results.append(check("[stuck] nothing was pushed: the git server has no branch of this run",
                         bool(stuck_branch) and stuck_branch not in gitlog and text(os.path.join(stuck, "branch-commits.txt")).strip() != "1",
                         "branch %s" % stuck_branch))
    err3 = step_text(stuck, "code_3", "stderr")
    results.append(check("[stuck] the agent read the log in rounds 2 and 3 and changed no file; the three verify steps failed",
                         "read the log" in step_text(stuck, "code_2", "stderr") and "read the log" in err3
                         and "this round changes no file" in err3
                         and all("must return None" in step_text(stuck, "verify_%d" % n) for n in (1, 2, 3)),
                         "see steps/code_*.stderr.json and steps/verify_*.log.json"))

    summary = text(os.path.join(ev, "agent-worker.summary.txt"))
    match = re.search(r"restart count: (\d+)", summary)
    results.append(check("the agent worker exited after each run and Docker restarted it", bool(match) and int(match.group(1)) >= 2,
                         summary.strip()[:160]))
    leaks = []
    for path in glob.glob(os.path.join(ev, "**", "*"), recursive=True):
        if os.path.isfile(path) and token and token in text(path):
            leaks.append(os.path.relpath(path, ev))
    results.append(check("the git token is not in the evidence", not leaks, "; ".join(leaks) or "searched all files"))

    failed = results.count(False)
    print()
    print("%d of %d claims hold" % (len(results) - failed, len(results)))
    sys.exit(1 if failed else 0)


def tool_results(events):
    """Return {tool_use_id: (text, is_error)} for each tool_result block of the user messages of a stream."""
    results = {}
    for event in events:
        if event.get("type") != "user":
            continue
        content = (event.get("message") or {}).get("content")
        if not isinstance(content, list):
            continue
        for block in content:
            if isinstance(block, dict) and block.get("type") == "tool_result":
                body = block.get("content")
                if isinstance(body, list):
                    body = " ".join(str(part.get("text", "")) for part in body if isinstance(part, dict))
                results[block.get("tool_use_id", "")] = (str(body), bool(block.get("is_error")))
    return results


def reads_of(events, suffix):
    """Return (file path, result text, is_error) of each Read tool call of a stream whose path ends with suffix."""
    answers = tool_results(events)
    found = []
    for event in events:
        if event.get("type") != "assistant":
            continue
        for block in (event.get("message") or {}).get("content") or []:
            if isinstance(block, dict) and block.get("type") == "tool_use" and block.get("name") == "Read":
                path = str((block.get("input") or {}).get("file_path", ""))
                if path.endswith(suffix):
                    body, is_error = answers.get(block.get("id", ""), ("", False))
                    found.append((path, body, is_error))
    return found


def cmd_check_loop_real(argv):
    ev, anthropic_file, github_file, credential_file, dag_file = argv
    dag_text = text(dag_file)
    results = []
    secrets = {
        "the model credential": secret_value(anthropic_file),
        "the GitHub token": secret_value(github_file),
        "the proxy credential": secret_value(credential_file),
    }
    secrets = {name: value for name, value in secrets.items() if value}

    labels, run_label, details = node_labels(os.path.join(ev, "status.json"))
    want = {"clone": "succeeded", "baseline": "succeeded", "code_1": "succeeded", "verify_1": "failed", "code_2": "succeeded",
            "verify_2": "succeeded", "code_3": "skipped", "verify_3": "skipped", "evidence": "succeeded",
            "check_paths": "succeeded", "push": "succeeded"}
    got = {step: labels.get(step) for step in want}
    results.append(check("the run ended partially_succeeded: verify_1 failed, round 2 passed, round 3 was skipped, the branch was pushed",
                         run_label == "partially_succeeded" and got == want, "run %s; steps %s" % (run_label, json.dumps(got))))
    worker_host = text(os.path.join(ev, "agent-worker.hostname.txt")).strip()
    worker_id = details.get("workerId", "")
    results.append(check("the whole loop was one run on the agent worker, with no sub run",
                         bool(worker_host) and worker_id.startswith(worker_host)
                         and not any(node.get("subRuns") or node.get("subRunsRepeated") for node in details.get("nodes", [])),
                         "workerId %r, container host name %r" % (worker_id, worker_host)))
    results.append(check("the steps verify_<n> have no key untrusted in the DAG, and ran as the restricted user (user ID 10001)",
                         verify_steps_have_no_key(dag_text) and "verify: running as user ID 10001" in step_text(ev, "verify_1"),
                         "see steps/verify_1.log.json"))
    block1 = dag_text.split("  - id: code_1\n", 1)[-1].split("\n  - id: ", 1)[0]
    prompt1 = re.search(r"prompt: \|\n((?:      .*\n)+)", block1 + "\n")
    prompt1 = prompt1.group(1).lower() if prompt1 else ""
    results.append(check("the prompt of round 1 names the failing test only: it does not name the verify or the second bug",
                         bool(prompt1) and "fails" in prompt1 and not any(word in prompt1 for word in ("verify", "divide", "zero", "log")),
                         "prompt of code_1: %r" % prompt1[:120]))

    version = text(os.path.join(ev, "claude-version.txt")).strip()
    streams = {n: stream_events(os.path.join(ev, "agent-dev-loop-real.code_%d.stream.jsonl" % n)) for n in (1, 2)}
    inits = [last_of(streams[n], "system", "init") for n in (1, 2)]
    finals = [last_of(streams[n], "result") for n in (1, 2)]
    results.append(check("the agent worker ran the real Claude Code in rounds 1 and 2, and each stream ends with a success",
                         "(Claude Code)" in version and all(inits) and all(i.get("claude_code_version", "") in version for i in inits)
                         and all(f.get("subtype") == "success" and not f.get("is_error") for f in finals),
                         "%s; model %s; turns %s and %s" % (version, inits[0].get("model"), finals[0].get("num_turns"), finals[1].get("num_turns"))))

    uses1 = tool_uses(streams[1])
    results.append(check("round 1 ran the tests and did not read the log of the verify",
                         any(name == "Bash" and "./scripts/test.sh" in str(value.get("command", "")) for name, value in uses1)
                         and not reads_of(streams[1], "verify_1.log"),
                         "tool calls %s" % [name for name, _ in uses1]))
    log1 = step_text(ev, "verify_1")
    results.append(check("verify_1 failed on the second bug: the tests pass, and the log names calc.divide",
                         "OK" in log1 and "FAILED" not in log1 and "calc.divide(6, 0)" in log1 and "must return None" in log1,
                         "see steps/verify_1.log.json"))
    reads = reads_of(streams[2], "verify_1.log")
    results.append(check("round 2 read the log of verify_1 with its Read tool, and the tool returned the log",
                         len(reads) >= 1 and all(not err and "calc.divide(6, 0)" in body for _, body, err in reads),
                         "read %s" % [path for path, _, _ in reads]))
    uses2 = tool_uses(streams[2])
    edits2 = [value for name, value in uses2 if name in ("Edit", "Write", "MultiEdit")]
    edited2 = sorted({os.path.basename(str(value.get("file_path", ""))) for value in edits2})
    results.append(check("round 2 edited calc.py only, and ran the tests again",
                         edited2 == ["calc.py"] and any(name == "Bash" and "./scripts/test.sh" in str(value.get("command", "")) for name, value in uses2),
                         "edited %s, tool calls %s" % (edited2, [name for name, _ in uses2])))
    log2 = step_text(ev, "verify_2")
    results.append(check("verify_2 passed: the tests and the second check", "OK" in log2 and "verify: ok" in log2, "see steps/verify_2.log.json"))

    branch = text(os.path.join(ev, "branch.txt")).strip()
    github = load(os.path.join(ev, "github-branch.json"), {})
    files = [f.get("filename") for f in github.get("files", [])]
    patch = " ".join(str(f.get("patch")) for f in github.get("files", []))
    results.append(check("GitHub has the branch: one commit that changes calc.py only, with both fixes",
                         github.get("exists") is True and github.get("branch") == branch and branch.startswith("agent/loop-")
                         and files == ["calc.py"] and github.get("ahead_by") == 1 and "+    return a + b" in patch and "return None" in patch,
                         "branch %s, ahead by %s, files %s" % (github.get("branch"), github.get("ahead_by"), files)))

    inspect = load(os.path.join(ev, "agent-worker.inspect.json"), [{}])
    config = (inspect[0].get("Config") or {}) if inspect else {}
    env = config.get("Env") or []
    in_env = [name for name, value in secrets.items() if any(value in item for item in env)]
    results.append(check("the agent worker container has the placeholder and no secret in its environment",
                         "ANTHROPIC_API_KEY=placeholder" in env and not in_env, "secrets in env: %s" % (in_env or "none")))
    proxy_log = text(os.path.join(ev, "docker-egress-proxy.log"))
    relay_claim(results, os.path.join(ev, "relay-audit.log"), secrets)
    tunnels = re.findall(r"allow CONNECT ([\w.-]+:\d+)( \(proxy credential\))?", proxy_log)
    results.append(check("only the steps clone and push opened a tunnel, to github.com with the proxy credential",
                         len(tunnels) >= 2 and all(t == ("github.com:443", " (proxy credential)") for t in tunnels),
                         "tunnels: %s" % sorted({t[0] + t[1] for t in tunnels})))
    summary = text(os.path.join(ev, "agent-worker.summary.txt"))
    match = re.search(r"restart count: (\d+)", summary)
    results.append(check("the agent worker exited after the run and Docker restarted it", bool(match) and int(match.group(1)) >= 1, summary.strip()[:160]))

    leaks = []
    forms = {}
    for name, value in secrets.items():
        forms[name] = value
        forms[name + " (Basic form)"] = base64.b64encode(("git:" + value).encode()).decode()
        forms[name + " (Basic form of GitHub)"] = base64.b64encode(("x-access-token:" + value).encode()).decode()
    for path in glob.glob(os.path.join(ev, "**", "*"), recursive=True):
        if not os.path.isfile(path):
            continue
        content = text(path)
        for name, value in forms.items():
            if value in content:
                leaks.append("%s in %s" % (name, os.path.relpath(path, ev)))
    results.append(check("no secret value is in the evidence (logs, outputs, status, streams)", not leaks,
                         "; ".join(leaks) or "searched all files for %d values" % len(forms)))

    failed = results.count(False)
    print()
    print("%d of %d claims hold" % (len(results) - failed, len(results)))
    sys.exit(1 if failed else 0)


def main():
    commands = {"field": cmd_field, "secret-id": cmd_secret_id, "nodes": cmd_nodes, "probes": cmd_probes, "check": cmd_check,
                "secret-put": cmd_secret_put, "stream": cmd_stream, "check-real": cmd_check_real,
                "check-loop": cmd_check_loop, "check-loop-real": cmd_check_loop_real,
                "ai-put": cmd_ai_put, "ai-delete": cmd_ai_delete, "check-endpoint": cmd_check_endpoint,
                "check-endpoint-real": cmd_check_endpoint_real}
    if len(sys.argv) < 2 or sys.argv[1] not in commands:
        sys.exit(__doc__)
    commands[sys.argv[1]](sys.argv[2:])


if __name__ == "__main__":
    main()
