"""photostead-worker: Photostead's AI worker.

  photostead-worker run --server URL --token-file F [--gpu] [--until-empty]
      embed thumbnails for search, pulling tasks from the server (GPU work under a Mission Control
      lease when MISSION_CONTROL_URL or --mission-control is set)
  photostead-worker builtin
      what runs inside the Photostead container: the text service for search queries, plus (unless
      PHOTOSTEAD_BUILTIN_EMBED=off) a gentle CPU worker for new items
  photostead-worker text-server [--port 4801]
  photostead-worker selftest
"""

from __future__ import annotations

import argparse
import logging
import os
import signal
import socket
import sys
import threading
import time

from . import models
from .client import Client
from .runner import Runner


def _token(args) -> str:
    if getattr(args, "token_file", None):
        return open(args.token_file).read().strip()
    t = os.environ.get("PHOTOSTEAD_WORKER_TOKEN", "")
    if not t:
        sys.exit("a worker token is needed: --token-file or PHOTOSTEAD_WORKER_TOKEN")
    return t


def selftest() -> None:
    """Checks search embeddings (a red and a blue image), text reading (a generated sign), faces, pets and adult content."""
    import io

    import numpy as np
    from PIL import Image

    from .siglip import ImageEncoder, TextEncoder

    ie, te = ImageEncoder(), TextEncoder()
    imgs = []
    for color in ((220, 30, 30), (30, 60, 220)):
        b = io.BytesIO()
        Image.new("RGB", (320, 240), color).save(b, "WEBP")
        imgs.append(ie.pixels(b.getvalue()))
    v = ie.encode(imgs)
    t = te.encode(["a plain red image", "a plain blue image"])
    s = t @ v.T
    ok = s[0, 0] > s[0, 1] and s[1, 1] > s[1, 0]
    print(f"similarities {np.round(s, 3).tolist()} -> {'ok' if ok else 'WRONG'}")

    from PIL import ImageDraw, ImageFont

    from .ocr import OCR

    page = Image.new("RGB", (900, 300), "white")
    ImageDraw.Draw(page).text((40, 110), "HELLO PHOTOSTEAD 2026", fill="black", font=ImageFont.load_default(size=64))
    b = io.BytesIO()
    page.save(b, "WEBP")
    text = OCR().read(b.getvalue())
    read = "PHOTOSTEAD" in text.upper()
    print(f"text read {text!r} -> {'ok' if read else 'WRONG'}")

    # Faces, on OpenCV's sample portrait (never shown): one face found, and its mirror image embeds
    # to the same vector, since each face is embedded with its mirror.
    import urllib.request

    from .faces import Faces

    with urllib.request.urlopen("https://raw.githubusercontent.com/opencv/opencv/4.x/samples/data/lena.jpg", timeout=60) as r:
        photo = r.read()
    found_faces = Faces().read(photo)
    mirror = Image.open(io.BytesIO(photo)).transpose(Image.FLIP_LEFT_RIGHT)
    b = io.BytesIO()
    mirror.save(b, "JPEG", quality=95)
    mirrored = Faces().read(b.getvalue())
    same = float(found_faces[0]["embedding"] @ mirrored[0]["embedding"]) if found_faces and mirrored else 0.0
    faces_ok = len(found_faces) >= 1 and found_faces[0]["embedding"].shape == (512,) and same > 0.9
    print(f"faces found {len(found_faces)}, mirror similarity {same:.3f} -> {'ok' if faces_ok else 'WRONG'}")

    # Pets, on PyTorch Hub's sample dog: one dog found, and the same dog in a tighter, smaller copy
    # of the photo embeds close to it.
    from .pets import Pets

    with urllib.request.urlopen("https://raw.githubusercontent.com/pytorch/hub/master/images/dog.jpg", timeout=60) as r:
        photo = r.read()
    pets = Pets()
    found = pets.read(photo)
    im = Image.open(io.BytesIO(photo))
    w, h = im.size
    b = io.BytesIO()
    im.crop((w // 10, h // 10, w - w // 10, h - h // 10)).resize((w * 2 // 3, h * 2 // 3)).save(b, "JPEG", quality=90)
    again = pets.read(b.getvalue())
    dogs = [p for p in found if p["species"] == "dog"]
    near = float(dogs[0]["embedding"] @ again[0]["embedding"]) if dogs and again else 0.0
    pets_ok = len(dogs) == 1 and near > 0.8
    print(f"pets found {[(p['species'], round(p['score'], 2)) for p in found]}, same dog {near:.3f} -> {'ok' if pets_ok else 'WRONG'}")
    # Adult content: the sample dog is neutral.
    from .adult import Adult

    p = Adult().scores([photo])[0]
    adult_ok = p[0] > 0.5
    print(f"adult content on the dog {[round(float(x), 3) for x in p]} -> {'ok' if adult_ok else 'WRONG'}")
    sys.exit(0 if ok and read and faces_ok and pets_ok and adult_ok else 1)


def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    p = argparse.ArgumentParser(prog="photostead-worker")
    sub = p.add_subparsers(dest="cmd", required=True)
    r = sub.add_parser("run")
    r.add_argument("--server", default=os.environ.get("PHOTOSTEAD_SERVER", "http://127.0.0.1:4800"))
    r.add_argument("--token-file")
    r.add_argument("--name", default=socket.gethostname())
    r.add_argument("--model", default=models.SIGLIP2_BASE["name"])
    r.add_argument("--gpu", action="store_true")
    r.add_argument("--batch", type=int, default=0)
    r.add_argument("--until-empty", action="store_true")
    r.add_argument("--mission-control", default=os.environ.get("MISSION_CONTROL_URL"))
    r.add_argument("--kinds", default="embed_image,faces,pets,adult,ocr,transcode",
                   help="which tasks to take: embed_image, faces, pets, adult, ocr, transcode (playable copies of videos; needs ffmpeg)")
    t = sub.add_parser("text-server")
    t.add_argument("--port", type=int, default=4801)
    t.add_argument("--model", default=models.SIGLIP2_BASE["name"])
    sub.add_parser("builtin")
    sub.add_parser("selftest")
    args = p.parse_args()
    # Stop cleanly on SIGTERM (docker stop, systemd, kill) and on Ctrl-C even when started in the
    # background (where SIGINT arrives ignored): the GPU lease and held tasks are given back.
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))
    signal.signal(signal.SIGINT, signal.default_int_handler)

    if args.cmd == "selftest":
        selftest()
    elif args.cmd == "text-server":
        from .textserver import serve

        serve(args.model, args.port, None).serve_forever()
    elif args.cmd == "run":
        batch = args.batch or (64 if args.gpu else 8)
        Runner(Client(args.server, _token(args)), args.model, args.gpu, args.name, batch, None,
               until_empty=args.until_empty, mission_control=args.mission_control,
               kinds=tuple(k.strip() for k in args.kinds.split(",") if k.strip())).run()
    elif args.cmd == "builtin":
        from .textserver import serve

        threads = int(os.environ.get("PHOTOSTEAD_WORKER_THREADS", "2"))
        model = models.SIGLIP2_BASE["name"]
        srv = serve(model, int(os.environ.get("PHOTOSTEAD_TEXT_PORT", "4801")), threads)
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        if os.environ.get("PHOTOSTEAD_BUILTIN_EMBED", "on") == "off":
            while True:
                time.sleep(3600)
        client = Client(os.environ.get("PHOTOSTEAD_SERVER", "http://127.0.0.1:4800"), _token(args))
        if os.path.isdir("/data/tmp"):  # in the container: videos' scratch copies on the data volume, not in the image
            os.environ.setdefault("PHOTOSTEAD_WORKER_TMP", "/data/tmp")
        while True:  # the server may still be starting
            try:
                Runner(client, model, False, "built-in", 8, threads).run()
            except Exception as e:
                logging.getLogger("photostead.worker").warning("worker stopped: %s; starting again", e)
                time.sleep(30)


if __name__ == "__main__":
    main()
