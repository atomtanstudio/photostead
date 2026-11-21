"""Pets: cats and dogs found with RF-DETR Nano, and each one embedded with open-noodle's pet
recognition model (DINOv2-base with a projection trained to tell individual animals apart), so the
server can suggest which photos show the same pet.

Both are Apache-2.0 and fetched only as pinned, checksummed files: RF-DETR Nano is Roboflow's COCO
model, unmodified, as converted to ONNX by Hugging Face's ONNX community (on 120 library photos it
finds the same animals as Roboflow's own export, scores and boxes within 0.001); pet-recognition-base
is open-noodle's, trained only on openly licensed data (Dogs-World, CC0; Cat Individual Images, CC
BY). On held-out animals it picks the right cat first 92% of the time, the right dog 61%: good
enough to suggest, not to decide.
"""

from __future__ import annotations

import numpy as np

from .faces import fetch
from .siglip import session

ENGINE = "rfdetr-petrec-base-v1"
DIMS = 512
MIN_SCORE = 0.3  # RF-DETR's sigmoid scores run lower than YOLO's
MIN_PX = 24      # smaller animals aren't recorded

FILES = {
    "detector": {
        "url": "https://huggingface.co/onnx-community/rfdetr_nano-ONNX/resolve/eae21cee0687a91bcf9fa071605c48d7705d2d91/onnx/model.onnx",
        "file": "rfdetr_nano_coco_onnx_community.onnx", "size": 108074865,
        "sha256": "9cbac6b11ce34a03034e4d5a24cfac5f18632fd6761d1311dd640232088d7fee",
    },
    "recognition": {
        "url": "https://huggingface.co/open-noodle/pet-recognition-base/resolve/fa30eb7785ceb764d5c2840c4585fb89821be385/recognition/model.onnx",
        "file": "pet_recognition_base.onnx", "size": 348108213,
        "sha256": "8691f5f6d0cebbd1602d21def63a34f7a58f78e08b3d4914cb81adcc2efd83f8",
    },
}

# COCO's 91-slot ids. Birds and farm animals are scored too, only so that a horse or a cow wins as
# itself rather than being taken for a dog; only cats and dogs are kept. (Bears are left out on
# purpose: with them, fluffy dog breeds come out as bears.)
CLASSES = {16: "bird", 17: "cat", 18: "dog", 19: "horse", 20: "sheep", 21: "cow"}
KEEP = {"cat", "dog"}
MEAN = np.array([0.485, 0.456, 0.406], np.float32)
STD = np.array([0.229, 0.224, 0.225], np.float32)


def _blob(rgb: np.ndarray) -> np.ndarray:
    """HxWx3 uint8 RGB at the model's size -> 1x3xHxW, ImageNet-normalized."""
    x = (rgb.astype(np.float32) / 255 - MEAN) / STD
    return x.transpose(2, 0, 1)[None]


class Pets:
    def __init__(self, gpu: bool = False, threads: int | None = None):
        import cv2

        self.cv2 = cv2
        self.det = session(fetch("detector", FILES, "pets"), gpu, threads)
        self.rec = session(fetch("recognition", FILES, "pets"), gpu, threads)
        self.ids = np.array(sorted(CLASSES))

    def detect(self, rgb: np.ndarray) -> list[tuple[str, float, np.ndarray]]:
        """(species, score, box as x, y, w, h fractions) for each cat and dog."""
        cv2 = self.cv2
        x = _blob(cv2.resize(rgb, (384, 384), interpolation=cv2.INTER_AREA))  # a plain stretch, as trained
        dets, logits = self.det.run(["pred_boxes", "logits"], {"pixel_values": x})
        scores = 1 / (1 + np.exp(-logits[0][:, self.ids]))
        best = scores.argmax(1)
        out = []
        for q in np.nonzero(scores[np.arange(len(best)), best] >= MIN_SCORE)[0]:
            species = CLASSES[int(self.ids[best[q]])]
            if species not in KEEP:
                continue
            cx, cy, w, h = (float(v) for v in dets[0][q])
            x0, y0 = max(0.0, cx - w / 2), max(0.0, cy - h / 2)
            x1, y1 = min(1.0, cx + w / 2), min(1.0, cy + h / 2)
            out.append((species, float(scores[q, best[q]]), np.array([x0, y0, x1 - x0, y1 - y0], np.float32)))
        return out

    def embed(self, crops: list[np.ndarray]) -> np.ndarray:
        """RGB crops of whole animals -> unit 512-d embeddings."""
        cv2 = self.cv2
        batch = np.vstack([_blob(cv2.resize(c, (224, 224), interpolation=cv2.INTER_AREA if min(c.shape[:2]) > 224 else cv2.INTER_LINEAR))
                           for c in crops])
        v = self.rec.run(["embedding"], {"input": batch})[0].astype(np.float32)
        return v / np.maximum(np.linalg.norm(v, axis=1, keepdims=True), 1e-12)

    def read(self, data: bytes) -> list[dict]:
        """Every cat and dog in a picture: species, where it is (fractions), detector score, size in
        pixels (the box's smaller side), and embedding."""
        cv2 = self.cv2
        img = cv2.imdecode(np.frombuffer(data, np.uint8), cv2.IMREAD_COLOR)
        if img is None:
            raise ValueError("the picture couldn't be decoded")
        rgb = cv2.cvtColor(img, cv2.COLOR_BGR2RGB)
        H, W = rgb.shape[:2]
        out, crops = [], []
        for species, score, box in self.detect(rgb):
            x0, y0 = int(box[0] * W), int(box[1] * H)
            x1, y1 = int(round((box[0] + box[2]) * W)), int(round((box[1] + box[3]) * H))
            px = min(x1 - x0, y1 - y0)
            if px < MIN_PX:
                continue
            out.append({"species": species, "box": box.round(5).tolist(), "score": score, "px": float(px)})
            crops.append(rgb[y0:y1, x0:x1])
        if crops:
            for pet, vec in zip(out, self.embed(crops)):
                pet["embedding"] = vec
        return out
