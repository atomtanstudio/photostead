"""SigLIP 2 image and text encoders on ONNX Runtime. Embeddings are L2-normalized, so cosine
similarity is a dot product."""

from __future__ import annotations

import io
import json
import os

import numpy as np
from PIL import Image

from . import models


def providers(gpu: bool) -> list[str]:
    import onnxruntime as ort

    have = ort.get_available_providers()
    if gpu and "CUDAExecutionProvider" in have:
        return ["CUDAExecutionProvider", "CPUExecutionProvider"]
    return ["CPUExecutionProvider"]


def session(path, gpu: bool, threads: int | None):
    import onnxruntime as ort

    if gpu and hasattr(ort, "preload_dlls"):
        ort.preload_dlls()  # CUDA and cuDNN from their pip packages
    opts = ort.SessionOptions()
    # Errors only: AuraFace declares a batch of one, and ONNX Runtime warns on every larger batch
    # (the results match one-at-a-time ones).
    opts.log_severity_level = 3
    if threads:
        opts.intra_op_num_threads = threads
        opts.inter_op_num_threads = 1
    s = ort.InferenceSession(str(path), sess_options=opts, providers=providers(gpu))
    if gpu and "CUDAExecutionProvider" not in s.get_providers():
        # A worker holding a GPU lease must not quietly run on the CPU (found by the face benchmark).
        raise RuntimeError("asked for the GPU, but ONNX Runtime couldn't load CUDA")
    return s


def _normalize(x: np.ndarray) -> np.ndarray:
    x = x.astype(np.float32)
    return x / np.maximum(np.linalg.norm(x, axis=1, keepdims=True), 1e-12)


class ImageEncoder:
    def __init__(self, model: str = models.SIGLIP2_BASE["name"], gpu: bool = False, threads: int | None = None):
        files = models.fetch(model, ("vision", "preprocessor"))
        pre = json.loads(files["preprocessor"].read_text())
        size = pre.get("size") or {}
        self.size = (int(size.get("width", 256)), int(size.get("height", 256)))
        self.mean = np.array(pre.get("image_mean", [0.5, 0.5, 0.5]), dtype=np.float32).reshape(1, 3, 1, 1)
        self.std = np.array(pre.get("image_std", [0.5, 0.5, 0.5]), dtype=np.float32).reshape(1, 3, 1, 1)
        self.scale = float(pre.get("rescale_factor", 1 / 255))
        self.sess = session(files["vision"], gpu, threads)
        self.input = self.sess.get_inputs()[0].name
        names = [o.name for o in self.sess.get_outputs()]
        self.output = "pooler_output" if "pooler_output" in names else names[-1]
        self.gpu = gpu

    def pixels(self, data: bytes) -> np.ndarray:
        """Decodes and resizes one image the way SigLIP's processor does (bilinear, no crop)."""
        im = Image.open(io.BytesIO(data))
        im.draft("RGB", self.size)  # cheap downscale while decoding JPEGs
        im = im.convert("RGB").resize(self.size, Image.Resampling.BILINEAR)
        return np.asarray(im, dtype=np.float32).transpose(2, 0, 1)

    def encode(self, batch: list[np.ndarray]) -> np.ndarray:
        x = np.stack(batch) * self.scale
        x = ((x - self.mean) / self.std).astype(np.float32)
        out = self.sess.run([self.output], {self.input: x})[0]
        return _normalize(out)


class TextEncoder:
    """Text the way SigLIP 2 was trained on it: lower case, padded to 64 tokens."""

    def __init__(self, model: str = models.SIGLIP2_BASE["name"], threads: int | None = None):
        from tokenizers import Tokenizer

        files = models.fetch(model, ("text", "tokenizer"))
        self.tok = Tokenizer.from_file(str(files["tokenizer"]))
        self.length = 64
        pad = self.tok.token_to_id("<pad>")
        self.tok.enable_padding(length=self.length, pad_id=pad if pad is not None else 0, pad_token="<pad>")
        self.tok.enable_truncation(max_length=self.length)
        self.sess = session(files["text"], False, threads)
        self.inputs = [i.name for i in self.sess.get_inputs()]
        names = [o.name for o in self.sess.get_outputs()]
        self.output = "pooler_output" if "pooler_output" in names else names[-1]

    def encode(self, texts: list[str]) -> np.ndarray:
        enc = self.tok.encode_batch([t.lower() for t in texts])
        feed = {"input_ids": np.array([e.ids for e in enc], dtype=np.int64)}
        if "attention_mask" in self.inputs:
            feed["attention_mask"] = np.array([e.attention_mask for e in enc], dtype=np.int64)
        out = self.sess.run([self.output], feed)[0]
        return _normalize(out)


def default_threads() -> int | None:
    v = os.environ.get("PHOTOSTEAD_WORKER_THREADS")
    return int(v) if v else None
