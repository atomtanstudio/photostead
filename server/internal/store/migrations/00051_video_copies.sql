-- +goose Up
-- Videos every browser can play. Each video is probed (ffprobe): its container, codecs, bit depth and
-- whether it's HDR. One that isn't plain H.264 in MP4 (HEVC from phones, MKV, AVI, ProRes, MPEG-4
-- Part 2, 10-bit, HDR…) gets a copy: H.264, 8-bit SDR, at most 1920 px, made by a worker (NVENC on
-- a GPU, libx264 on a CPU) and kept with the other derived files. Browsers that can play the
-- original still get it; the rest, and every hover preview, play the copy.
ALTER TABLE worker_tasks DROP CONSTRAINT worker_tasks_kind_check;
ALTER TABLE worker_tasks ADD CONSTRAINT worker_tasks_kind_check CHECK (kind IN ('embed_image', 'ocr', 'faces', 'pets', 'adult', 'transcode'));

CREATE TABLE video_probes (
    asset_id   uuid PRIMARY KEY REFERENCES assets ON DELETE CASCADE,
    container  text NOT NULL DEFAULT '',
    codec      text NOT NULL DEFAULT '',  -- ffprobe's name: h264, hevc, vp9, av1, mpeg4, prores…
    codecs     text NOT NULL DEFAULT '',  -- for a browser's canPlayType, e.g. hvc1.2.4.L150.90
    pix_fmt    text NOT NULL DEFAULT '',
    bit_depth  smallint NOT NULL DEFAULT 8,
    transfer   text NOT NULL DEFAULT '',  -- color transfer: smpte2084 (PQ) and arib-std-b67 (HLG) are HDR
    hdr        boolean NOT NULL DEFAULT false,
    audio      text NOT NULL DEFAULT '',
    width      integer NOT NULL DEFAULT 0,
    height     integer NOT NULL DEFAULT 0,
    direct     boolean NOT NULL,          -- every browser plays the original as it is
    error      text,                      -- ffprobe couldn't read it (no copy is attempted)
    probed_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE video_copies (
    asset_id uuid PRIMARY KEY REFERENCES assets ON DELETE CASCADE,
    recipe   text NOT NULL,  -- how it was made; a new recipe makes new copies
    encoder  text NOT NULL,  -- h264_nvenc, libx264…
    width    integer NOT NULL,
    height   integer NOT NULL,
    bytes    bigint NOT NULL,
    made_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE video_copies;
DROP TABLE video_probes;
DELETE FROM worker_tasks WHERE kind = 'transcode';
ALTER TABLE worker_tasks DROP CONSTRAINT worker_tasks_kind_check;
ALTER TABLE worker_tasks ADD CONSTRAINT worker_tasks_kind_check CHECK (kind IN ('embed_image', 'ocr', 'faces', 'pets', 'adult'));
