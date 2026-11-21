-- +goose Up
-- Faces and the people they make (docs/architecture.md, "People, pets and faces"). Workers find
-- and embed faces (YuNet + AuraFace v1 with mirror averaging); the server groups the faces in real
-- photos into people with the streaming average-of-3 rule. Faces in AI images are kept but never
-- join People.
ALTER TABLE worker_tasks DROP CONSTRAINT worker_tasks_kind_check;
ALTER TABLE worker_tasks ADD CONSTRAINT worker_tasks_kind_check CHECK (kind IN ('embed_image', 'ocr', 'faces'));

-- Items looked at for faces (none found counts).
CREATE TABLE face_scans (
    asset_id   uuid PRIMARY KEY REFERENCES assets ON DELETE CASCADE,
    engine     text NOT NULL,
    faces      integer NOT NULL,
    scanned_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE faces (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    asset_id   uuid NOT NULL REFERENCES assets ON DELETE CASCADE,
    engine     text NOT NULL,
    box        real[] NOT NULL, -- x, y, width, height, as fractions of the picture
    landmarks  real[] NOT NULL, -- eyes, nose tip, mouth corners: x, y pairs, as fractions
    score      real NOT NULL,   -- the detector's confidence
    px         real NOT NULL,   -- the face's smaller side, in pixels of the preview
    yaw        real NOT NULL,   -- how far the nose is off the eyes' midpoint, in eye distances
    blur       real NOT NULL,   -- variance of the Laplacian of the aligned face (low is blurry)
    embedding  halfvec(512) NOT NULL,
    person_id  uuid REFERENCES people ON DELETE SET NULL,
    assignment text CHECK (assignment IN ('auto', 'confirmed')),
    grouped_at timestamptz,         -- when grouping last looked at it (left waiting, or placed)
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX faces_asset ON faces (asset_id);
CREATE INDEX faces_person ON faces (person_id) WHERE person_id IS NOT NULL;

-- "Not this person": grouping never puts the face there again.
CREATE TABLE face_rejections (
    face_id    uuid NOT NULL REFERENCES faces ON DELETE CASCADE,
    person_id  uuid NOT NULL REFERENCES people ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (face_id, person_id)
);

-- When an owner's faces were last grouped, so the job runs only when there's something new.
CREATE TABLE face_runs (
    owner_id    uuid PRIMARY KEY REFERENCES users ON DELETE CASCADE,
    ran_at      timestamptz NOT NULL,
    newest_face timestamptz,
    faces       integer NOT NULL,
    people      integer NOT NULL
);

ALTER TABLE people ADD COLUMN cover_face_id uuid REFERENCES faces ON DELETE SET NULL;
ALTER TABLE people ADD COLUMN origin text NOT NULL DEFAULT 'immich' CHECK (origin IN ('immich', 'faces', 'user'));

-- Who is in what. Faces decide for every item looked at for faces; another app's suggestions stand
-- only for items not looked at yet.
CREATE VIEW person_items AS
SELECT DISTINCT f.person_id, f.asset_id, true AS by_face FROM faces f WHERE f.person_id IS NOT NULL
UNION ALL
SELECT ps.person_id, ps.asset_id, false FROM person_suggestions ps
WHERE NOT EXISTS (SELECT 1 FROM face_scans s WHERE s.asset_id = ps.asset_id);

-- +goose Down
DROP VIEW person_items;
ALTER TABLE people DROP COLUMN origin;
ALTER TABLE people DROP COLUMN cover_face_id;
DROP TABLE face_runs;
DROP TABLE face_rejections;
DROP TABLE faces;
DROP TABLE face_scans;
DELETE FROM worker_tasks WHERE kind = 'faces';
ALTER TABLE worker_tasks DROP CONSTRAINT worker_tasks_kind_check;
ALTER TABLE worker_tasks ADD CONSTRAINT worker_tasks_kind_check CHECK (kind IN ('embed_image', 'ocr'));
