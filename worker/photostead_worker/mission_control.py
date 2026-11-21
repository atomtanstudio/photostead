"""Mission Control client for Legion apps. Standard library only; drop this file in.

    from mission_control import MissionControl

    mc = MissionControl(client="h3lix")
    print(mc.status()["summary"])

    fit = mc.fit(vram_gb=22)                    # no side effects
    if fit["verdict"] == "busy":
        print("Waiting on:", fit["busy"])

    with mc.reserve(vram_gb=22, purpose="video · shot 03", workload="comfy-lab", wait_s=300) as lease:
        ...                                     # run the job
        lease.progress(0.5, "sampling 15/30")   # optional; heartbeats are automatic
        if lease.preempted:                     # another app was allowed to stop this job
            ...                                 # requeue it
        lease.keep_warm(300)                    # more work for comfy-lab is queued
    # released on exit, even on error

    # With `workload`, vram_gb is that workload's total footprint: if the model is
    # already loaded, almost nothing extra is needed and nothing gets unloaded.

    # The user chose "stop it": interrupt those jobs and go next, in one step.
    mc.reserve(vram_gb=22, workload="comfy-live", priority="next", preempt=["comfy-live"])

Download: curl -O http://<mission-control>:7777/client/mission_control.py
"""

from __future__ import annotations

import json
import os
import threading
import urllib.error
import urllib.parse
import urllib.request

DEFAULT_URL = os.environ.get("MISSION_CONTROL_URL", "http://127.0.0.1:7777")


class NotGranted(Exception):
    """The memory couldn't be freed. `.info` says why (verdict, busy, blocking)."""

    def __init__(self, info: dict):
        self.info = info
        busy = "; ".join(f"{b['name']}: {b['detail']}" for b in info.get("busy") or [])
        super().__init__(f"not granted ({info.get('verdict', '?')}){': ' + busy if busy else ''}")


class MissionControl:
    def __init__(self, client: str, url: str = DEFAULT_URL, timeout: float = 10):
        self.client, self.url, self.timeout = client, url.rstrip("/"), timeout

    def _req(self, method: str, path: str, body: dict | None = None, timeout: float | None = None):
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(self.url + path, data=data, method=method,
                                     headers={"Content-Type": "application/json"} if data else {})
        try:
            with urllib.request.urlopen(req, timeout=timeout or self.timeout) as r:
                return r.status, json.loads(r.read() or b"null")
        except urllib.error.HTTPError as e:
            try:
                return e.code, json.loads(e.read() or b"null")
            except ValueError:
                return e.code, {"error": str(e)}

    def status(self) -> dict:
        return self._req("GET", "/api/v1/status")[1]

    def fit(self, vram_gb: float = 0, ram_gb: float = 0, workload: str | None = None) -> dict:
        """verdict: fits_now | fits_after_reclaim | busy | needs_you. blocking[] has id, owner, resumable."""
        q = {"vram_gb": vram_gb, "ram_gb": ram_gb, "client": self.client}
        if workload:
            q["workload"] = workload
        return self._req("GET", "/api/v1/fit?" + urllib.parse.urlencode(q))[1]

    def interrupt(self, workload: str, reason: str = "") -> dict:
        """Stop the job a ComfyUI workload is running (only when the user asked). Its queue is left alone."""
        return self._req("POST", f"/api/v1/workloads/{workload}/interrupt", {"client": self.client, "reason": reason})[1]

    def workloads(self, which: str = "running") -> list[dict]:
        return self._req("GET", f"/api/v1/workloads?which={which}")[1]

    def reserve(self, vram_gb: float = 0, ram_gb: float = 0, purpose: str = "", workload: str | None = None,
                wait_s: float = 0, ttl_s: float = 600, priority: str | None = None,
                preempt: list[str] | None = None, resumable: bool = False) -> "Lease":
        """Claim memory. Raises NotGranted if it can't be freed within wait_s.

        priority="next" jumps the line. preempt=[workload ids] (only with the user's OK)
        interrupts those jobs and unloads them for you. resumable=True tells others your
        job can be requeued if it's preempted."""
        body = {"client": self.client, "vram_gb": vram_gb, "ram_gb": ram_gb, "purpose": purpose,
                "workload": workload, "wait_s": wait_s, "ttl_s": ttl_s, "resumable": resumable}
        if priority:
            body["priority"] = priority
        if preempt:
            body["preempt"] = list(preempt)
        code, body = self._req("POST", "/api/v1/reserve", body, timeout=wait_s + 120)
        if code != 200 or not body.get("granted"):
            raise NotGranted(body or {})
        return Lease(self, body["lease"], body)


class Lease:
    """A granted reservation. Heartbeats in the background until released."""

    def __init__(self, mc: MissionControl, lease: dict, grant: dict):
        self.mc, self.id, self.info, self.grant = mc, lease["id"], lease, grant
        self.linger_s = 0.0
        self._stop = threading.Event()
        self._beat = threading.Thread(target=self._loop, daemon=True)
        self._beat.start()

    def _beat_once(self, body: dict) -> None:
        code, info = self.mc._req("POST", f"/api/v1/leases/{self.id}/heartbeat", body)
        if code == 200 and isinstance(info, dict):
            self.info = info

    def _loop(self) -> None:
        every = max(10.0, float(self.info.get("ttl_s", 600)) / 3)
        while not self._stop.wait(every):
            self._beat_once({})

    @property
    def preempted(self) -> dict | None:
        """{by, reason, at} once another app was allowed to stop this job (as of the last heartbeat)."""
        return self.info.get("preempted")

    def progress(self, fraction: float | None = None, message: str | None = None) -> None:
        self._beat_once({"progress": fraction, "message": message})

    def keep_warm(self, seconds: float) -> None:
        """On release, keep this lease's workload safe from other apps' reclaim (max 900 s)."""
        self.linger_s = seconds

    def release(self, message: str = "", linger_s: float | None = None) -> None:
        if not self._stop.is_set():
            self._stop.set()
            self.mc._req("POST", f"/api/v1/leases/{self.id}/release",
                         {"message": message, "linger_s": self.linger_s if linger_s is None else linger_s})

    def __enter__(self) -> "Lease":
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        self.release("failed: " + type(exc).__name__ if exc else "")
