#!/usr/bin/env python3
"""A smart-HTTP git server for the agent worker demo.

It serves one bare repository, app.git, with `git http-backend` as a CGI program.
It needs the Python standard library and git.

- HTTP Basic authentication. Any user name works. The password MUST be equal to
  the token in the token file. Any other request gets the status 401.
- At start, the server makes the bare repository from the seed folder when the
  repository does not exist.
- It writes one JSON line for each request to stdout. The line has no password.

usage: server.py --root DIR --seed DIR --token-file FILE [--port 8080]
"""
import argparse
import base64
import hmac
import json
import os
import subprocess
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

MAX_BODY = 64 * 1024 * 1024
REPO = "app.git"


def run(*command, cwd=None):
    subprocess.run(command, cwd=cwd, check=True, stdout=sys.stderr, stderr=sys.stderr)


def seed_repository(root, seed):
    """Make the bare repository and commit the seed folder as the first commit."""
    bare = os.path.join(root, REPO)
    if os.path.isdir(bare):
        return
    os.makedirs(root, exist_ok=True)
    run("git", "init", "--bare", "-b", "main", bare)
    run("git", "-C", bare, "config", "http.receivepack", "true")
    work = os.path.join(root, "seed-work")
    run("git", "init", "-b", "main", work)
    run("cp", "-a", seed + "/.", work)
    run("git", "-C", work, "add", "-A")
    run("git", "-C", work, "-c", "user.name=demo", "-c", "user.email=demo@localhost",
        "commit", "-m", "Add the calculator with a failing test")
    run("git", "-C", work, "push", bare, "main")
    run("rm", "-rf", work)


def read_body(handler):
    """Read the request body, with a Content-Length or with chunks."""
    if handler.headers.get("Transfer-Encoding", "").lower() == "chunked":
        data = bytearray()
        while True:
            size = int(handler.rfile.readline().split(b";")[0].strip() or b"0", 16)
            if size == 0:
                while handler.rfile.readline().strip():
                    pass
                break
            data += handler.rfile.read(size)
            handler.rfile.readline()
            if len(data) > MAX_BODY:
                raise ValueError("request body is too large")
        return bytes(data)
    length = int(handler.headers.get("Content-Length") or 0)
    if length > MAX_BODY:
        raise ValueError("request body is too large")
    return handler.rfile.read(length)


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "demo-git"
    root = ""
    token = b""

    def log_message(self, *_):
        pass

    def audit(self, user, status):
        print(json.dumps({
            "time": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "method": self.command,
            "path": self.path.split("?")[0],
            "user": user,
            "status": status,
            "client": self.client_address[0],
        }), flush=True)

    def reply(self, status, headers, body, user=""):
        self.send_response(status)
        for name, value in headers:
            self.send_header(name, value)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
        self.audit(user, status)

    def user_of_request(self):
        """Return the user name when the password is the token, else None."""
        header = self.headers.get("Authorization", "")
        if not header.startswith("Basic "):
            return None
        try:
            user, _, password = base64.b64decode(header[6:]).decode().partition(":")
        except Exception:
            return None
        if hmac.compare_digest(password.encode(), self.token):
            return user or "git"
        return None

    def handle_any(self):
        try:
            body = read_body(self)
        except ValueError as error:
            self.close_connection = True
            self.reply(413, [("Content-Type", "text/plain")], str(error).encode())
            return
        user = self.user_of_request()
        if user is None:
            self.reply(401, [("WWW-Authenticate", 'Basic realm="git"'), ("Content-Type", "text/plain")],
                       b"authentication required\n")
            return
        path, _, query = self.path.partition("?")
        env = {
            "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
            "HOME": os.environ.get("HOME", "/tmp"),
            "GIT_PROJECT_ROOT": self.root,
            "GIT_HTTP_EXPORT_ALL": "1",
            "REMOTE_USER": user,
            "REMOTE_ADDR": self.client_address[0],
            "REQUEST_METHOD": self.command,
            "PATH_INFO": path,
            "QUERY_STRING": query,
            "CONTENT_LENGTH": str(len(body)),
            "SERVER_PROTOCOL": "HTTP/1.1",
        }
        for name, key in (("Content-Type", "CONTENT_TYPE"), ("Content-Encoding", "HTTP_CONTENT_ENCODING"),
                          ("Git-Protocol", "HTTP_GIT_PROTOCOL")):
            if self.headers.get(name):
                env[key] = self.headers[name]
        done = subprocess.run(["git", "http-backend"], input=body, env=env, capture_output=True)
        raw = done.stdout
        head, separator, payload = raw.partition(b"\r\n\r\n")
        if not separator:
            head, separator, payload = raw.partition(b"\n\n")
        status = 200
        headers = []
        for line in head.decode("latin-1").splitlines():
            name, _, value = line.partition(":")
            if name.lower() == "status":
                status = int(value.strip().split()[0])
            elif name.lower() not in ("content-length", "connection", "transfer-encoding"):
                headers.append((name, value.strip()))
        if not raw:
            status, payload = 500, done.stderr or b"git http-backend gave no output\n"
        self.reply(status, headers, payload, user)

    do_GET = handle_any
    do_POST = handle_any


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", required=True)
    parser.add_argument("--seed", required=True)
    parser.add_argument("--token-file", required=True)
    parser.add_argument("--port", type=int, default=8080)
    args = parser.parse_args()
    with open(args.token_file, "rb") as handle:
        token = handle.read().rstrip(b"\n")
    if not token:
        sys.exit("--token-file: the token file is empty")
    seed_repository(args.root, args.seed)
    Handler.root = args.root
    Handler.token = token
    server = ThreadingHTTPServer(("0.0.0.0", args.port), Handler)
    print("git server: serving %s on port %d" % (REPO, args.port), file=sys.stderr, flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
