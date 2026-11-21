"""Model files: downloaded once from Hugging Face into a models folder, and checked by size."""

from __future__ import annotations

import json
import logging
import os
import shutil
import urllib.request
from pathlib import Path

log = logging.getLogger("photostead.worker")

# The default search model: SigLIP 2 base, 16px patches at 256px. Small enough to embed query text
# and new uploads on a modest CPU; the same weights everywhere, so every worker's embeddings agree.
SIGLIP2_BASE = {
    "name": "siglip2-base-patch16-256",
    "repo": "onnx-community/siglip2-base-patch16-256-ONNX",
    "files": {
        "vision": "onnx/vision_model.onnx",
        "text": "onnx/text_model.onnx",
        "tokenizer": "tokenizer.json",
        "preprocessor": "preprocessor_config.json",
        "config": "config.json",
    },
    "dims": 768,
}

MODELS = {SIGLIP2_BASE["name"]: SIGLIP2_BASE}


def models_dir() -> Path:
    return Path(os.environ.get("PHOTOSTEAD_MODELS_DIR", Path.home() / ".cache" / "photostead" / "models"))


def _expected_size(repo: str, path: str) -> int | None:
    url = f"https://huggingface.co/api/models/{repo}/tree/main/{os.path.dirname(path)}"
    try:
        with urllib.request.urlopen(url, timeout=30) as r:
            for entry in json.load(r):
                if entry.get("path") == path:
                    return int(entry.get("size") or 0) or None
    except Exception:  # offline: trust what's on disk
        return None
    return None


def fetch(model: str, parts: tuple[str, ...]) -> dict[str, Path]:
    """Makes sure the model's files for these parts are on disk, downloading what's missing."""
    spec = MODELS[model]
    base = models_dir() / model
    out: dict[str, Path] = {}
    for part in parts:
        rel = spec["files"][part]
        dst = base / rel
        out[part] = dst
        if dst.exists() and dst.stat().st_size > 0:
            continue
        dst.parent.mkdir(parents=True, exist_ok=True)
        url = f"https://huggingface.co/{spec['repo']}/resolve/main/{rel}"
        tmp = dst.with_suffix(dst.suffix + ".part")
        log.info("downloading %s %s", model, rel)
        with urllib.request.urlopen(url, timeout=60) as r, open(tmp, "wb") as f:
            shutil.copyfileobj(r, f, length=8 << 20)
        want = _expected_size(spec["repo"], rel)
        if want and tmp.stat().st_size != want:
            tmp.unlink(missing_ok=True)
            raise RuntimeError(f"{rel}: got {tmp.stat().st_size} bytes, expected {want}")
        tmp.rename(dst)
    return out
