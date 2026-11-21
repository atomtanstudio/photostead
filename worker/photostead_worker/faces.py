"""Faces: found with YuNet, aligned to ArcFace's five-point template, and embedded with AuraFace v1
(the face and its mirror image, averaged), along with the quality signals the clustering gate uses
(size, detector score, yaw, blur). This is the setup Photostead's face benchmark chose, on a real
library's labelled faces, AI-made ones included.

Both models are redistributable: YuNet 2023mar (OpenCV Zoo, MIT) and AuraFace v1 glintr100 (fal,
Apache-2.0). Only their checksummed files are fetched.
"""

from __future__ import annotations

import hashlib
import logging
import shutil
import urllib.request

import numpy as np

from . import models
from .siglip import session

log = logging.getLogger("photostead.worker")

ENGINE = "yunet-auraface-v1"
DIMS = 512
MIN_PX = 20  # smaller faces aren't recorded at all (the gate holds back anything under 64 px anyway)

FILES = {
    "yunet": {
        "url": "https://github.com/opencv/opencv_zoo/raw/main/models/face_detection_yunet/face_detection_yunet_2023mar.onnx",
        "file": "yunet_2023mar.onnx", "size": 232589,
        "sha256": "8f2383e4dd3cfbb4553ea8718107fc0423210dc964f9f4280604804ed2552fa4",
    },
    "auraface": {
        "url": "https://huggingface.co/fal/AuraFace-v1/resolve/main/glintr100.onnx",
        "file": "auraface_glintr100.onnx", "size": 260694151,
        "sha256": "a7933ea5330113b01c9b60351d8f4c33003f145d8470ac5f0e52ee2effe25c60",
    },
}

# ArcFace's 112x112 template: left eye, right eye, nose, left and right mouth corners.
ARC_DST = np.array([[38.2946, 51.6963], [73.5318, 51.5014], [56.0252, 71.7366],
                    [41.5493, 92.3655], [70.7299, 92.2041]], dtype=np.float32)


def fetch(name: str, files: dict = FILES, folder: str = "faces"):
    """A pinned model file, downloaded once and checked against its size and checksum."""
    spec = files[name]
    dst = models.models_dir() / folder / spec["file"]
    if dst.exists() and dst.stat().st_size == spec["size"]:
        return dst
    dst.parent.mkdir(parents=True, exist_ok=True)
    tmp = dst.with_suffix(".part")
    log.info("downloading %s", spec["file"])
    digest = hashlib.sha256()
    with urllib.request.urlopen(spec["url"], timeout=60) as r, open(tmp, "wb") as f:
        while chunk := r.read(8 << 20):
            digest.update(chunk)
            f.write(chunk)
    if tmp.stat().st_size != spec["size"] or digest.hexdigest() != spec["sha256"]:
        tmp.unlink(missing_ok=True)
        raise RuntimeError(f"{spec['file']}: the download doesn't match its checksum")
    shutil.move(tmp, dst)
    return dst


def umeyama(src: np.ndarray, dst: np.ndarray) -> np.ndarray:
    """Similarity transform src->dst (2x3), as in insightface's face_align."""
    n = src.shape[0]
    mu_s, mu_d = src.mean(0), dst.mean(0)
    s_c, d_c = src - mu_s, dst - mu_d
    cov = d_c.T @ s_c / n
    U, S, Vt = np.linalg.svd(cov)
    d = np.ones(2)
    if np.linalg.det(cov) < 0:
        d[-1] = -1
    R = U @ np.diag(d) @ Vt
    var_s = (s_c ** 2).sum() / n
    scale = (S * d).sum() / var_s
    t = mu_d - scale * R @ mu_s
    return np.hstack([scale * R, t[:, None]]).astype(np.float32)


def _normalize(x: np.ndarray) -> np.ndarray:
    x = x.astype(np.float32)
    return x / np.maximum(np.linalg.norm(x, axis=1, keepdims=True), 1e-12)


class Faces:
    def __init__(self, gpu: bool = False, threads: int | None = None):
        import cv2

        self.cv2 = cv2
        # The benchmark's settings: score threshold 0.5, NMS 0.3, at most 50 faces.
        self.det = cv2.FaceDetectorYN.create(str(fetch("yunet")), "", (320, 320), 0.5, 0.3, 50)
        self.sess = session(fetch("auraface"), gpu, threads)
        inp = self.sess.get_inputs()[0]
        self.input = inp.name
        self.fixed = inp.shape[0] if isinstance(inp.shape[0], int) else None
        self.output = self.sess.get_outputs()[0].name

    def detect(self, img: np.ndarray):
        h, w = img.shape[:2]
        self.det.setInputSize((w, h))
        _, found = self.det.detect(img)
        return [] if found is None else found

    def embed(self, crops: np.ndarray) -> np.ndarray:
        """Aligned 112x112 BGR crops -> unit 512-d embeddings, the face and its mirror averaged."""
        def run(batch: np.ndarray) -> np.ndarray:
            # AuraFace takes RGB scaled to (x - 127.5) / 127.5 (insightface's ArcFaceONNX rule).
            blob = self.cv2.dnn.blobFromImages(list(batch), 1 / 127.5, (112, 112), (127.5, 127.5, 127.5), swapRB=True)
            if self.fixed and len(batch) != self.fixed:
                return np.vstack([self.sess.run([self.output], {self.input: blob[i:i + 1]})[0] for i in range(len(batch))])
            return self.sess.run([self.output], {self.input: blob})[0]

        f = run(crops).reshape(len(crops), -1)
        fl = run(np.ascontiguousarray(crops[:, :, ::-1])).reshape(len(crops), -1)
        return _normalize(_normalize(f) + _normalize(fl))

    def read(self, data: bytes) -> list[dict]:
        """Every face in a picture: where it is (as fractions of the picture), its landmarks, quality
        signals and embedding."""
        cv2 = self.cv2
        img = cv2.imdecode(np.frombuffer(data, np.uint8), cv2.IMREAD_COLOR)
        if img is None:
            raise ValueError("the picture couldn't be decoded")
        H, W = img.shape[:2]
        out, crops = [], []
        for f in self.detect(img):
            x, y, bw, bh = (float(v) for v in f[:4])
            px = min(bw, bh)
            if px < MIN_PX:
                continue
            kps = f[4:14].reshape(5, 2).astype(np.float32).copy()
            # Eyes ordered left-to-right in the picture, as the template expects.
            if kps[0, 0] > kps[1, 0]:
                kps[[0, 1]] = kps[[1, 0]]
                kps[[3, 4]] = kps[[4, 3]]
            aligned = cv2.warpAffine(img, umeyama(kps, ARC_DST), (112, 112), borderValue=0)
            eye_mid = (kps[0] + kps[1]) / 2
            eye_d = max(1e-3, float(np.linalg.norm(kps[1] - kps[0])))
            gray = cv2.cvtColor(aligned, cv2.COLOR_BGR2GRAY)
            out.append({
                "box": [x / W, y / H, bw / W, bh / H],
                "landmarks": (kps / np.array([W, H], np.float32)).ravel().round(5).tolist(),
                "score": float(f[14]),
                "px": px,
                "yaw": float((kps[2, 0] - eye_mid[0]) / eye_d),
                "blur": float(cv2.Laplacian(gray[16:100, 16:96], cv2.CV_64F).var()),
            })
            crops.append(aligned)
        if crops:
            for face, vec in zip(out, self.embed(np.stack(crops))):
                face["embedding"] = vec
        return out
