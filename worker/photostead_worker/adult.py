"""Adult content: Freepik's EVA-based NSFW classifier (EVA-02 base at 448 px, MIT) rates a picture
with four probabilities, which Freepik calls neutral, low, medium and high. Freepik trained it on
100,000 images, AI-made ones included, and on AI images it beats the other open models by a wide
margin. The worker sends every picture's probabilities (a video's poster and frames, each); the
server turns them into labels (labels/adult.go).

The weights are Freepik's, run on ONNX Runtime from an ONNX conversion fetched as a pinned,
checksummed file. On 40 pictures from a real library it gives the same probabilities as Freepik's
own PyTorch weights (to five decimal places), with the preparation below matching timm's: a plain
stretch to 448 x 448 (bicubic), then OpenAI CLIP's mean and spread.
"""

from __future__ import annotations

import io

import numpy as np
from PIL import Image

from .faces import fetch
from .siglip import session

ENGINE = "freepik-eva02-nsfw-v2"  # the server's labels.AdultEngine: every picture's probabilities
SIZE = 448
MEAN = np.array([0.48145466, 0.4578275, 0.40821073], np.float32)
STD = np.array([0.26862954, 0.26130258, 0.27577711], np.float32)

FILES = {
    "classifier": {
        # Freepik/nsfw_image_detector (revision 15b8547) converted to ONNX, fp32.
        "url": "https://huggingface.co/doppeltilde/nsfw_image_detector-ONNX/resolve/ee48dc9aac238b7496f5e5f41a5a726022e249ed/onnx/model.onnx",
        "file": "freepik_nsfw_eva02_base_448.onnx", "size": 346154840,
        "sha256": "8c2deab24cc63c4614ec631dbf63d77c70c01f6fb9e14c33ebd78e09c0da6a1a",
    },
}


def prepare(data: bytes) -> np.ndarray:
    """A picture's bytes -> 3x448x448, as the model was trained."""
    im = Image.open(io.BytesIO(data)).convert("RGB").resize((SIZE, SIZE), Image.BICUBIC)
    x = (np.asarray(im, np.float32) / 255 - MEAN) / STD
    return x.transpose(2, 0, 1)


class Adult:
    def __init__(self, gpu: bool = False, threads: int | None = None, batch: int = 8):
        self.sess = session(fetch("classifier", FILES, "adult"), gpu, threads)
        self.input = self.sess.get_inputs()[0].name
        self.batch = batch

    def scores(self, pictures: list[bytes]) -> list[np.ndarray]:
        """Each picture's probabilities of neutral, low, medium and high. A picture that can't be
        decoded raises."""
        x = np.stack([prepare(p) for p in pictures])
        out = []
        for i in range(0, len(x), self.batch):
            logits = self.sess.run(None, {self.input: x[i:i + self.batch]})[0].astype(np.float64)
            p = np.exp(logits - logits.max(1, keepdims=True))
            out += list((p / p.sum(1, keepdims=True)).astype(np.float32))
        return out
