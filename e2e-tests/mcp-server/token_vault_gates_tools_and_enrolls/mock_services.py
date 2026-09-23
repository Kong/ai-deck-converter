#!/usr/bin/env python3
"""Mock services for the Token Vault e2e test.

Runs two HTTP servers:

  :18080  mock Kong Identity service (the Token Vault's token endpoint).
          POST /{directory}/v2/vault/token answers the RFC 8693 exchange:
            - 400 {"error": "x_kong_enrollment_required",
                   "x_kong_enrollment_url": "https://auth.example.com/..."}
              until $STATE_DIR/enrolled exists,
            - 200 {"access_token": "upstream-cred-123", ...} afterwards.
          This is the exact shape kong-ee's token_vault.lua expects (see
          is_enrollment_required / apply_credential there).

  :18081  mock upstream MCP server (Streamable HTTP / JSON-RPC 2.0). Answers
          initialize / tools/list / tools/call; every POST records the
          Authorization header it received into $STATE_DIR/upstream-auth.txt
          so the test can assert the exchanged credential was applied.

Both take the state directory as argv[1]. Stdlib only.
"""

import json
import os
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs

STATE_DIR = sys.argv[1] if len(sys.argv) > 1 else "."

IDENTITY_PORT = 18080
UPSTREAM_PORT = 18081


def enrolled() -> bool:
    return os.path.exists(os.path.join(STATE_DIR, "enrolled"))


class IdentityHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length).decode("utf-8", "replace")
        form = parse_qs(body)
        subject = form.get("subject_token", [""])[0]
        resource = form.get("resource", [""])[0]

        with open(os.path.join(STATE_DIR, "identity-requests.log"), "a") as f:
            f.write(json.dumps({
                "path": self.path,
                "subject_token": subject,
                "resource": resource,
            }) + "\n")

        if not enrolled():
            payload = {
                "error": "x_kong_enrollment_required",
                "x_kong_enrollment_url": "https://auth.example.com/authorize?state=mock-123",
            }
            status = 400
        else:
            payload = {
                "access_token": "upstream-cred-123",
                "expires_in": 300,
                "x-kong-bearer-methods-supported": ["header"],
            }
            status = 200

        encoded = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)

    def log_message(self, fmt, *args):
        pass


class UpstreamMCPHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        raw = self.rfile.read(length).decode("utf-8", "replace")
        auth = self.headers.get("Authorization", "")

        # Record the Authorization header the plugin applied to the proxied
        # request: after enrollment it must be the exchanged credential.
        with open(os.path.join(STATE_DIR, "upstream-auth.txt"), "a") as f:
            f.write(f"{self.path} {auth}\n")

        try:
            req = json.loads(raw)
        except json.JSONDecodeError:
            self._json({"jsonrpc": "2.0", "id": None,
                        "error": {"code": -32700, "message": "parse error"}}, 400)
            return

        method = req.get("method", "")
        rid = req.get("id")
        if method == "initialize":
            result = {
                "protocolVersion": "2025-06-18",
                "capabilities": {"tools": {"listChanged": True}},
                "serverInfo": {"name": "mock-upstream", "version": "1.0.0"},
            }
            self._json({"jsonrpc": "2.0", "id": rid, "result": result},
                       200, session="mock-upstream-session-1")
        elif method.startswith("notifications/"):
            self._raw(b"", 202)
        elif method == "tools/list":
            result = {
                "tools": [{
                    "name": "flights-search",
                    "description": "Search flights (served by the mock upstream)",
                    "inputSchema": {"type": "object", "properties": {}},
                }],
            }
            self._json({"jsonrpc": "2.0", "id": rid, "result": result}, 200)
        elif method == "tools/call":
            result = {
                "content": [{"type": "text",
                             "text": f"flights-search ok (upstream saw Authorization: {auth})"}],
            }
            self._json({"jsonrpc": "2.0", "id": rid, "result": result}, 200)
        else:
            self._json({"jsonrpc": "2.0", "id": rid,
                        "error": {"code": -32601, "message": f"method not found: {method}"}}, 200)

    def _json(self, payload, status, session=None):
        encoded = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        if session:
            self.send_header("Mcp-Session-Id", session)
        self.end_headers()
        self.wfile.write(encoded)

    def _raw(self, payload, status):
        self.send_response(status)
        self.send_header("Content-Length", "0")
        self.end_headers()

    def log_message(self, fmt, *args):
        pass


def serve(port, handler):
    server = ThreadingHTTPServer(("0.0.0.0", port), handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server


if __name__ == "__main__":
    os.makedirs(STATE_DIR, exist_ok=True)
    identity = serve(IDENTITY_PORT, IdentityHandler)
    upstream = serve(UPSTREAM_PORT, UpstreamMCPHandler)
    print(f"mock identity on :{IDENTITY_PORT}, mock upstream on :{UPSTREAM_PORT}", flush=True)
    try:
        threading.Event().wait()
    except KeyboardInterrupt:
        identity.shutdown()
        upstream.shutdown()
