"""A local stand-in for the System One endpoint, for the eval suite.

Prints its port on stdout, then serves one fixed probability set by --prob.
A request whose body contains HANG_SENTINEL is accepted but never answered,
which is what the hook's deadline is for. Every request is appended to the
log file named by --log, so a case can assert that no request was made at
all.
"""
import argparse
import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

p = argparse.ArgumentParser()
p.add_argument("--prob", type=float, default=0.0)
p.add_argument("--log", default="")
p.add_argument("--port-file", default="")
args = p.parse_args()

# Hanging is decided per REQUEST, not per process: the eval suite owns one
# stub for the whole run, and a mode fixed at startup could not serve both
# the answering cases and the one that must never be answered.
HANG_SENTINEL = "HANG-THIS-REQUEST"


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length") or 0))
        text = body.decode("utf-8", "replace")
        if args.log:
            with open(args.log, "a") as fh:
                fh.write(text + "\n")
        if HANG_SENTINEL in text:
            import time
            time.sleep(60)
            return
        payload = json.dumps({
            "model": "jev-1.13.0",
            "answers": {"kind": {"type": "choice", "choice": "change_history",
                                 "probabilities": {"change_history": args.prob,
                                                   "compatibility_contract": 0.0,
                                                   "present_behaviour": 1 - args.prob},
                                 "confidence": 0.9}},
            "usage": {"input_tokens": 1, "output_tokens": 1}}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, *a):
        pass


server = HTTPServer(("127.0.0.1", 0), Handler)
# The port goes to a file when one is named, because a background job's
# stdout is not readable by the shell that started it; a caller holding the
# pipe (the unit tests) reads it from stdout instead.
if args.port_file:
    with open(args.port_file, "w") as fh:
        fh.write("%d\n" % server.server_port)
sys.stdout.write("%d\n" % server.server_port)
sys.stdout.flush()
server.serve_forever()
