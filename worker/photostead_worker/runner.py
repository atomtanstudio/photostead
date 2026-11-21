"""The worker loop: claim tasks (search embeddings, faces, pets, adult content, text in images, playable copies of videos), fetch their inputs, do them,
report back. On a GPU it works only inside a Mission Control lease (resumable, never pre-empting
anyone) and lets go when the queue is empty or another app is allowed to take the GPU."""

from __future__ import annotations

import logging
import os
import time
from concurrent.futures import ThreadPoolExecutor

from . import __version__
from .client import Client, pack

log = logging.getLogger("photostead.worker")

EMBED = "embed_image"
OCR_KIND = "ocr"
FACES_KIND = "faces"
PETS_KIND = "pets"
ADULT_KIND = "adult"
TRANSCODE_KIND = "transcode"


class Runner:
    def __init__(self, client: Client, model: str, gpu: bool, name: str, batch: int, threads: int | None,
                 until_empty: bool = False, mission_control: str | None = None, idle_s: float = 30,
                 kinds: tuple[str, ...] = (EMBED, FACES_KIND, PETS_KIND, ADULT_KIND, OCR_KIND, TRANSCODE_KIND)):
        self.client, self.model, self.gpu, self.name = client, model, gpu, name
        self.batch, self.threads, self.until_empty, self.idle_s = batch, threads, until_empty, idle_s
        self.mc_url, self.kinds = mission_control, kinds
        if TRANSCODE_KIND in kinds:
            from .transcode import ffmpeg

            if not ffmpeg():  # no ffmpeg here: leave videos' copies to another worker
                log.info("no ffmpeg on this machine, so no playable copies of videos from this worker")
                self.kinds = tuple(k for k in kinds if k != TRANSCODE_KIND)
        self._encoder = None
        self._ocr = None
        self._faces = None
        self._pets = None
        self._adult = None
        self.done = 0
        self.failed = 0

    def models(self) -> list[str]:
        from .adult import ENGINE as ADULT
        from .faces import ENGINE as FACES
        from .ocr import ENGINE
        from .pets import ENGINE as PETS

        return [m for k, m in ((EMBED, self.model), (FACES_KIND, FACES), (PETS_KIND, PETS), (ADULT_KIND, ADULT), (OCR_KIND, ENGINE))
                if k in self.kinds] + self.copy_models()

    def copy_models(self) -> list[str]:
        """Small videos' copies for any worker; long and big ones only for GPU workers."""
        if TRANSCODE_KIND not in self.kinds:
            return []
        from .transcode import LARGE, SMALL

        return [SMALL, LARGE] if self.gpu else [SMALL]

    def encoder(self):
        if self._encoder is None:
            from .siglip import ImageEncoder

            self._encoder = ImageEncoder(self.model, gpu=self.gpu, threads=self.threads)
        return self._encoder

    def faces(self):
        if self._faces is None:
            from .faces import Faces

            self._faces = Faces(gpu=self.gpu, threads=self.threads)
        return self._faces

    def pets(self):
        if self._pets is None:
            from .pets import Pets

            self._pets = Pets(gpu=self.gpu, threads=self.threads)
        return self._pets

    def adult(self):
        if self._adult is None:
            from .adult import Adult

            self._adult = Adult(gpu=self.gpu, threads=self.threads, batch=16 if self.gpu else 4)
        return self._adult

    def ocr(self):
        if self._ocr is None:
            from .ocr import OCR

            self._ocr = OCR(gpu=self.gpu)
        return self._ocr

    def _lease(self):
        if not self.gpu or not self.mc_url:
            return None
        from .mission_control import MissionControl, NotGranted

        mc = MissionControl(client="photostead-worker", url=self.mc_url)
        while True:
            try:
                return mc.reserve(vram_gb=4, purpose="Photostead · search embeddings, faces, pets, adult content and text in images", wait_s=600,
                                  ttl_s=300, resumable=True)
            except NotGranted as e:
                log.info("waiting for the GPU: %s", e)
                time.sleep(60)

    def claim(self) -> list[dict]:
        # Search embeddings first (cheap, and what search needs most), then text reading, in
        # smaller batches: OCR takes a second or more per picture on a CPU, and a lease lasts ten minutes.
        from .ocr import ENGINE

        if EMBED in self.kinds:
            tasks = self.client.claim([EMBED], [self.model], self.batch * 4)
            if tasks:
                return tasks
        if FACES_KIND in self.kinds:
            from .faces import ENGINE as FACES

            tasks = self.client.claim([FACES_KIND], [FACES], 64 if self.gpu else 16)
            if tasks:
                return tasks
        if PETS_KIND in self.kinds:
            from .pets import ENGINE as PETS

            tasks = self.client.claim([PETS_KIND], [PETS], 64 if self.gpu else 16)
            if tasks:
                return tasks
        if ADULT_KIND in self.kinds:
            from .adult import ENGINE as ADULT

            # A video is up to seven pictures (its poster and frames through it).
            tasks = self.client.claim([ADULT_KIND], [ADULT], 64 if self.gpu else 8)
            if tasks:
                return tasks
        if OCR_KIND in self.kinds:
            tasks = self.client.claim([OCR_KIND], [ENGINE], 64 if self.gpu else 16)
            if tasks:
                return tasks
        if TRANSCODE_KIND in self.kinds:  # last: one at a time, and slow
            return self.client.claim([TRANSCODE_KIND], self.copy_models(), 1)
        return []

    def run(self) -> None:
        self.client.hello(self.name, __version__, {"gpu": self.gpu, "kinds": list(self.kinds), "models": self.models()})
        log.info("worker %s ready (%s, %s)", self.name, ", ".join(self.models()), "GPU" if self.gpu else "CPU")
        while True:
            tasks = self.claim()
            if not tasks:
                if self.until_empty:
                    log.info("nothing left to do: %d done, %d failed", self.done, self.failed)
                    return
                time.sleep(self.idle_s)
                continue
            lease = self._lease()
            try:
                while tasks:
                    try:
                        self._work(tasks)
                    except BaseException:  # failed, or stopped: give them back rather than let them expire
                        self.client.release([t["id"] for t in tasks])
                        raise
                    if lease is not None:
                        lease.progress(None, f"{self.done:,} done")
                        if lease.preempted:
                            log.info("another app needs the GPU; stepping aside")
                            break
                    tasks = self.claim()
            finally:
                if lease is not None:
                    lease.release(f"{self.done:,} done")
            if lease is not None and lease.preempted:
                time.sleep(300)

    def _work(self, tasks: list[dict]) -> None:
        copies = [t for t in tasks if t["kind"] == TRANSCODE_KIND]
        if copies:
            from .transcode import make

            failed = []
            for t in copies:
                try:
                    make(self.client, t, self.gpu, self.threads, os.environ.get("PHOTOSTEAD_WORKER_TMP"))
                    self.done += 1
                except Exception as e:  # reported; tried again later, up to three times
                    log.warning("video %s: no copy: %s", t["assetId"], e)
                    failed.append({"id": t["id"], "error": str(e)[:300]})
            if failed:
                self.client.results(failed)
                self.failed += len(failed)
            tasks = [t for t in tasks if t["kind"] != TRANSCODE_KIND]
            if not tasks:
                return
        def fetch(t):
            try:
                data = self.client.input(t["id"])
            except Exception as e:  # unreadable or gone: report it, don't stop
                return t, None, str(e)[:300]
            if t["kind"] == ADULT_KIND:  # its poster, and for a video, frames through it
                data = [data]
                for n in range(1, int(t.get("frames") or 0) + 1):
                    try:
                        data.append(self.client.input(t["id"], n))
                    except Exception:  # a frame that won't read: the others still count
                        pass
            return t, data, None

        with ThreadPoolExecutor(max_workers=8) as pool:
            fetched = list(pool.map(fetch, tasks))
        results = [{"id": t["id"], "error": err} for t, data, err in fetched if data is None]
        embed = [(t, data) for t, data, _ in fetched if data is not None and t["kind"] == EMBED]
        ocr = [(t, data) for t, data, _ in fetched if data is not None and t["kind"] == OCR_KIND]
        faces = [(t, data) for t, data, _ in fetched if data is not None and t["kind"] == FACES_KIND]
        pets = [(t, data) for t, data, _ in fetched if data is not None and t["kind"] == PETS_KIND]
        adult = [(t, data) for t, data, _ in fetched if data is not None and t["kind"] == ADULT_KIND]
        if adult:
            results += self._adult_results(adult)
        for t, data in pets:
            try:
                found = self.pets().read(data)
                for f in found:
                    f["embedding"] = pack(f["embedding"])
                results.append({"id": t["id"], "pets": found})
            except Exception as e:
                results.append({"id": t["id"], "error": str(e)[:300]})
        for t, data in faces:
            try:
                found = self.faces().read(data)
                for f in found:
                    f["embedding"] = pack(f["embedding"])
                results.append({"id": t["id"], "faces": found})
            except Exception as e:
                results.append({"id": t["id"], "error": str(e)[:300]})
        if embed:
            enc = self.encoder()
            with ThreadPoolExecutor(max_workers=8) as pool:
                pixels = list(pool.map(lambda td: enc.pixels(td[1]), embed))
            for i in range(0, len(embed), self.batch):
                vecs = enc.encode(pixels[i:i + self.batch])
                results += [{"id": t["id"], "embedding": pack(v)} for (t, _), v in zip(embed[i:i + self.batch], vecs)]
        for t, data in ocr:
            try:
                results.append({"id": t["id"], "text": self.ocr().read(data)})
            except Exception as e:
                results.append({"id": t["id"], "error": str(e)[:300]})
        for i in range(0, len(results), 64):  # keep each post well under the server's body limit
            part = results[i:i + 64]
            r = self.client.results(part)
            self.done += int(r.get("saved", 0))
            self.failed += len(part) - int(r.get("saved", 0))

    def _adult_results(self, adult: list[tuple[dict, list[bytes]]]) -> list[dict]:
        """Every picture of every task rated in one go; each task sends all its pictures' scores."""
        from .adult import prepare

        pictures, owner, out = [], [], []
        for i, (t, frames) in enumerate(adult):
            readable = []
            for f in frames:
                try:
                    prepare(f)  # undecodable frames are left out, not fatal
                    readable.append(f)
                except Exception:
                    pass
            if not readable:
                out.append({"id": t["id"], "error": "none of its pictures could be decoded"})
                continue
            pictures += readable
            owner += [i] * len(readable)
        if not pictures:
            return out
        scores = self.adult().scores(pictures)
        per: dict[int, list] = {}
        for i, p in zip(owner, scores):
            per.setdefault(i, []).append(p)
        for i, ps in per.items():
            flat = [round(float(x), 6) for p in ps for x in p]
            out.append({"id": adult[i][0]["id"], "adult": {"scores": flat, "frames": len(ps)}})
        return out
