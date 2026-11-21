# ComfyUI → Photostead sync

Copies finished ComfyUI images and videos from a machine's ComfyUI output folders into Photostead,
through its upload API. It keeps its state (what it has sent) in `PHOTOSTEAD_STATE_DIR`.

How it works:
- Each settled file is checked as a whole image or video (Pillow, ffprobe) and hashed (SHA-256).
- `POST /api/v1/uploads/check` says which files Photostead already has. Those are marked done
  without being sent.
- The rest go to `POST /api/v1/uploads` with the file's time as its date and the album's name.
  Photostead files each by day in the photostead share. The sync checks that the SHA-256
  Photostead received matches.
- Source files are never changed.

## Install on the Legion

```
mkdir -p /srv/ai/comfy-photostead
cp photostead_sync.py /srv/ai/comfy-photostead/
cp comfy-photostead.service ~/.config/systemd/user/
```

`/srv/ai/comfy-photostead/.env` (mode 600):

```
PHOTOSTEAD_URL=http://192.168.1.10:4800
PHOTOSTEAD_API_KEY=psk_…            # docker exec photostead photostead apikey add -name "ComfyUI sync"
PHOTOSTEAD_ALBUM=ComfyUI Outputs
PHOTOSTEAD_STATE_DIR=/srv/ai/comfy-photostead/state
COMFY_OUTPUT_GLOBS=[...]            # JSON list of output folders (globs)
COMFY_SKIP_DIRS=...
PHOTOSTEAD_SETTLE_SECONDS=60
PHOTOSTEAD_POLL_SECONDS=20
PHOTOSTEAD_SINCE=...                # optional: outputs older than this aren't sent
```

Then `systemctl --user daemon-reload && systemctl --user enable --now comfy-photostead`.
`python3 photostead_sync.py --env-file .env --status` shows progress without calling Photostead.
