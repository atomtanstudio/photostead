"""Text in images: RapidOCR (PaddleOCR's PP-OCR models on ONNX Runtime, Apache-2.0). Only lines read
with confidence are kept; AI images often carry text-like noise."""

from __future__ import annotations

import io
import logging

import numpy as np
from PIL import Image

from . import models

log = logging.getLogger("photostead.worker")

ENGINE = "rapidocr-ppocrv6"
MIN_SCORE = 0.8
MAX_CHARS = 4000


class OCR:
    def __init__(self, gpu: bool = False):
        from rapidocr import RapidOCR

        root = models.models_dir() / "rapidocr"
        root.mkdir(parents=True, exist_ok=True)
        if gpu:
            import onnxruntime as ort

            if hasattr(ort, "preload_dlls"):
                ort.preload_dlls()
        self.engine = RapidOCR(params={
            "Global.use_cls": False,
            "Global.log_level": "error",
            "Global.model_root_dir": str(root),
            "EngineConfig.onnxruntime.use_cuda": gpu,
        })

    def read(self, data: bytes) -> str:
        """The text in an image, a line per line read, in reading order."""
        im = np.asarray(Image.open(io.BytesIO(data)).convert("RGB"))[:, :, ::-1]  # RapidOCR wants BGR
        r = self.engine(im)
        lines = [t.strip() for t, s in zip(r.txts or [], r.scores or []) if float(s) >= MIN_SCORE and t.strip()]
        return "\n".join(lines)[:MAX_CHARS]
