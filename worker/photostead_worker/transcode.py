"""Playable copies of videos: H.264 High, 8-bit 4:2:0 SDR, at most 1920 px on the long side, AAC
stereo, MP4 with its index first, so every browser plays them. On a GPU worker with an NVENC-able
ffmpeg the encoding is NVENC's; otherwise libx264, or, with an LGPL ffmpeg that has no libx264 (the
desktop app's), the system's own encoder: VideoToolbox on a Mac, Media Foundation on Windows. HDR (PQ, HLG) is tone-mapped to SDR with zscale
when this ffmpeg has it (most Linux builds do), else just converted, which looks flat but plays.

The original is downloaded to a scratch file first (a worker reads only what its tasks need),
and the copy is sent back with PUT /tasks/{id}/copy."""

from __future__ import annotations

import functools
import logging
import os
import shutil
import subprocess
import tempfile
import threading

log = logging.getLogger("photostead.worker")

RECIPE = "h264-1920-sdr-v1"  # the server's media.CopyRecipe
SMALL, LARGE = RECIPE, RECIPE + "-large"  # models: any worker, GPU workers only

TONEMAP = ("zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0,"
           "zscale=t=bt709:m=bt709:r=tv")


@functools.cache
def ffmpeg() -> str | None:
    return shutil.which("ffmpeg")


@functools.cache
def _has(listing: str, name: str) -> bool:
    try:
        out = subprocess.run([ffmpeg(), "-hide_banner", listing], capture_output=True, text=True, timeout=30).stdout
    except Exception:
        return False
    return any(line.split()[1:2] == [name] for line in out.splitlines() if line.strip())


def encoders(gpu: bool) -> list[str]:
    """The encoders to try, best first: NVENC on a GPU worker, then libx264, then the system's own
    (VideoToolbox, Media Foundation, which some Windows Server installs lack) and OpenH264."""
    found = [n for n in ("libx264", "h264_videotoolbox", "h264_mf", "libopenh264") if _has("-encoders", n)]
    if gpu and _has("-encoders", "h264_nvenc"):
        found.insert(0, "h264_nvenc")
    return found or ["libx264"]  # none of them: ffmpeg says why


def encoder(gpu: bool) -> str:
    return encoders(gpu)[0]


# What each encoder needs to make the same copy (libx264's by quality, the system encoders' by rate).
BY_RATE = ["-b:v", "8M", "-maxrate", "16M", "-bufsize", "32M"]
SETTINGS = {
    "h264_videotoolbox": ["-c:v", "h264_videotoolbox", "-allow_sw", "1", "-profile:v", "high", *BY_RATE],
    "h264_mf": ["-c:v", "h264_mf", "-rate_control", "cbr", "-b:v", "8M"],
    "libopenh264": ["-c:v", "libopenh264", *BY_RATE],
}


def command(src: str, dst: str, gpu: bool, hdr: bool, threads: int | None, keep_video: bool = False,
            enc: str | None = None) -> list[str]:
    """The ffmpeg command for a copy of src at dst. With keep_video (H.264 8-bit SDR already: only its
    sound doesn't play everywhere), the picture is copied as it is."""
    if keep_video:
        return [ffmpeg(), "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-i", src,
                "-map", "0:v:0", "-map", "0:a:0?", "-sn", "-dn", "-map_metadata", "-1", "-c:v", "copy",
                "-c:a", "aac", "-b:a", "160k", "-ac", "2", "-movflags", "+faststart", dst]
    filters = []
    if hdr:
        if _has("-filters", "zscale"):
            filters.append(TONEMAP)
    # At most 1920 on the long side, never larger, even sizes; ffmpeg turns it upright first.
    filters.append("scale=w='if(gte(iw,ih),trunc(min(1920,iw)/2)*2,-2)':h='if(gte(iw,ih),-2,trunc(min(1920,ih)/2)*2)'")
    filters.append("format=yuv420p")
    enc = enc or encoder(gpu)
    if enc == "h264_nvenc":
        video = ["-c:v", "h264_nvenc", "-preset", "p5", "-tune", "hq", "-rc", "vbr", "-cq", "24", "-b:v", "0",
                 "-maxrate", "16M", "-bufsize", "32M", "-profile:v", "high"]
    elif enc in SETTINGS:
        video = list(SETTINGS[enc])
    else:
        video = ["-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-profile:v", "high"]
        if threads:
            video += ["-threads", str(threads)]
    return [ffmpeg(), "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-i", src,
            "-map", "0:v:0", "-map", "0:a:0?", "-sn", "-dn", "-map_metadata", "-1",
            "-vf", ",".join(filters), *video,
            "-c:a", "aac", "-b:a", "128k", "-ac", "2", "-movflags", "+faststart", dst]


def make(client, task: dict, gpu: bool, threads: int | None, scratch: str | None = None) -> dict:
    """Downloads the task's original, makes its copy and sends it. Returns the server's answer;
    raises when any step fails. The task is kept (its lease extended) the whole time."""
    stop = threading.Event()

    def keep():  # a long video takes longer than a lease
        while not stop.wait(120):
            try:
                client.extend(task["id"])
            except Exception as e:
                log.warning("couldn't keep task %s: %s", task["id"], e)

    keeper = threading.Thread(target=keep, daemon=True)
    keeper.start()
    with tempfile.TemporaryDirectory(prefix="photostead-copy-", dir=scratch) as tmp:
        try:
            src, dst = os.path.join(tmp, "original"), os.path.join(tmp, "copy.mp4")
            client.original(task["id"], src)
            spec = task.get("video") or {}
            hdr, keep = bool(spec.get("hdr")), bool(spec.get("keepVideo"))
            if keep:
                p = subprocess.run(command(src, dst, gpu, hdr, threads, keep), capture_output=True, text=True)
                used = "copy"
            else:
                # NVENC can refuse (a size it doesn't take, the GPU busy), and a system encoder may be
                # missing (Media Foundation on some Windows Servers): the next one does it instead.
                for used in encoders(gpu):
                    p = subprocess.run(command(src, dst, gpu, hdr, threads, enc=used), capture_output=True, text=True)
                    if p.returncode == 0 and os.path.exists(dst):
                        break
                    log.info("%s couldn't make this copy (%s); trying the next", used, p.stderr.strip()[-200:])
            if p.returncode != 0 or not os.path.exists(dst):
                raise RuntimeError("ffmpeg: " + (p.stderr.strip().splitlines() or ["failed"])[-1][:250])
            return client.send_copy(task["id"], dst, used)
        finally:
            stop.set()
