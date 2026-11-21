"""The worker side of Photostead's worker API (/api/worker/v1): claim tasks, fetch their inputs,
post results. A worker only ever reads the renditions of tasks it holds."""

from __future__ import annotations

import base64
import json
import logging
import os
import time
import urllib.error
import urllib.request

import numpy as np

log = logging.getLogger("photostead.worker")


class Client:
    def __init__(self, server: str, token: str, timeout: float = 60, patience: float = 600):
        self.base = server.rstrip("/") + "/api/worker/v1"
        self.token = token
        self.timeout = timeout
        self.patience = patience

    def _req(self, method: str, path: str, body: dict | None = None, raw: bool = False):
        # While the server restarts (an update, a reboot), wait for it rather than give up: up to
        # `patience` seconds, backing off. Posting results twice is harmless: the server saves only
        # tasks this worker still holds.
        data = json.dumps(body).encode() if body is not None else None
        waited, delay = 0.0, 2.0
        while True:
            req = urllib.request.Request(self.base + path, data=data, method=method)
            req.add_header("Authorization", "Bearer " + self.token)
            if data is not None:
                req.add_header("Content-Type", "application/json")
            try:
                with urllib.request.urlopen(req, timeout=self.timeout) as r:
                    payload = r.read()
                return payload if raw else (json.loads(payload) if payload else {})
            except urllib.error.HTTPError as e:
                detail = e.read()[:500].decode(errors="replace")
                if e.code not in (502, 503, 504) or waited >= self.patience:
                    raise RuntimeError(f"{method} {path}: {e.code} {detail}") from None
                why = f"{e.code}"
            except (urllib.error.URLError, ConnectionError, TimeoutError) as e:
                if waited >= self.patience:
                    raise
                why = str(getattr(e, "reason", e))
            log.info("server unavailable (%s); trying again in %.0f s", why, delay)
            time.sleep(delay)
            waited += delay
            delay = min(delay * 2, 30)

    def hello(self, name: str, version: str, capabilities: dict) -> dict:
        return self._req("POST", "/hello", {"name": name, "version": version, "capabilities": capabilities})

    def claim(self, kinds: list[str], models: list[str], max_tasks: int) -> list[dict]:
        return self._req("POST", "/claim", {"kinds": kinds, "models": models, "max": max_tasks}).get("tasks") or []

    def input(self, task_id: int, frame: int = 0) -> bytes:
        """A held task's picture; for a video's adult task, frame 1, 2… are frames through it."""
        return self._req("GET", f"/tasks/{task_id}/input" + (f"?frame={frame}" if frame else ""), raw=True)

    def original(self, task_id: int, path: str) -> None:
        """A held transcode task's original video, saved to path."""
        req = urllib.request.Request(f"{self.base}/tasks/{task_id}/original")
        req.add_header("Authorization", "Bearer " + self.token)
        with urllib.request.urlopen(req, timeout=self.timeout) as r, open(path, "wb") as f:
            while chunk := r.read(8 << 20):
                f.write(chunk)

    def extend(self, task_id: int) -> None:
        self._req("POST", f"/tasks/{task_id}/extend")

    def send_copy(self, task_id: int, path: str, encoder: str) -> dict:
        """A video's copy, sent for the transcode task this worker holds."""
        size = os.path.getsize(path)
        with open(path, "rb") as f:
            req = urllib.request.Request(f"{self.base}/tasks/{task_id}/copy?encoder={encoder}", data=f, method="PUT")
            req.add_header("Authorization", "Bearer " + self.token)
            req.add_header("Content-Type", "video/mp4")
            req.add_header("Content-Length", str(size))
            try:
                with urllib.request.urlopen(req, timeout=max(self.timeout, 600)) as r:
                    return json.loads(r.read() or b"{}")
            except urllib.error.HTTPError as e:
                raise RuntimeError(f"sending the copy: {e.code} {e.read()[:300].decode(errors='replace')}") from None

    def results(self, results: list[dict]) -> dict:
        return self._req("POST", "/results", {"results": results})

    def release(self, ids: list[int]) -> None:
        if ids:
            self._req("POST", "/release", {"ids": ids})


def pack(vec: np.ndarray) -> str:
    """An embedding as base64 little-endian float32."""
    return base64.b64encode(np.asarray(vec, dtype="<f4").tobytes()).decode()
