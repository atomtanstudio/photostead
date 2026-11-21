"""Embeds search text for the Photostead server it sits next to: POST /embed/text
{"texts": [...]} -> {"model": ..., "embeddings": [[...], ...]} on 127.0.0.1 only."""

from __future__ import annotations

import json
import logging
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from .siglip import TextEncoder

log = logging.getLogger("photostead.worker")


def serve(model: str, port: int, threads: int | None) -> ThreadingHTTPServer:
    enc = TextEncoder(model, threads=threads)
    lock = threading.Lock()  # one inference at a time; queries are short

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):  # quiet
            pass

        def _send(self, code: int, body: dict) -> None:
            data = json.dumps(body).encode()
            self.send_response(code)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def do_GET(self):
            if self.path == "/health":
                self._send(200, {"model": model})
            else:
                self._send(404, {"error": "not found"})

        def do_POST(self):
            if self.path != "/embed/text":
                return self._send(404, {"error": "not found"})
            try:
                n = int(self.headers.get("Content-Length") or 0)
                texts = json.loads(self.rfile.read(min(n, 1 << 20)))["texts"]
                if not isinstance(texts, list) or not texts or len(texts) > 32 or not all(isinstance(t, str) for t in texts):
                    return self._send(400, {"error": "texts: 1 to 32 strings"})
                with lock:
                    vecs = enc.encode([t[:2000] for t in texts])
                self._send(200, {"model": model, "embeddings": [[round(float(x), 6) for x in v] for v in vecs]})
            except Exception as e:
                self._send(500, {"error": str(e)[:300]})

    srv = ThreadingHTTPServer(("127.0.0.1", port), Handler)
    log.info("text embeddings for %s on 127.0.0.1:%d", model, port)
    return srv
