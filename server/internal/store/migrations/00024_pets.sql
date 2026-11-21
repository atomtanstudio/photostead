-- +goose Up
-- Pets (docs/architecture.md, "People, pets and faces"). Workers find cats and dogs (RF-DETR) and
-- embed each animal (open-noodle's pet recognition). An animal is stored in faces, with its species,
-- so pets share everything people have: grouping, corrections, covers, "who is in what". For an
-- animal the box is its whole body, and the face-only measures (landmarks, yaw, blur) are empty.
ALTER TABLE worker_tasks DROP CONSTRAINT worker_tasks_kind_check;
ALTER TABLE worker_tasks ADD CONSTRAINT worker_tasks_kind_check CHECK (kind IN ('embed_image', 'ocr', 'faces', 'pets'));

ALTER TABLE faces ADD COLUMN species text CHECK (species IN ('cat', 'dog')); -- NULL: a person's face

-- Items looked at for pets (none found counts).
CREATE TABLE pet_scans (
    asset_id   uuid PRIMARY KEY REFERENCES assets ON DELETE CASCADE,
    engine     text NOT NULL,
    pets       integer NOT NULL,
    scanned_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE people ADD COLUMN species text CHECK (species IN ('cat', 'dog'));

-- +goose Down
ALTER TABLE people DROP COLUMN species;
DROP TABLE pet_scans;
DELETE FROM faces WHERE species IS NOT NULL;
ALTER TABLE faces DROP COLUMN species;
DELETE FROM worker_tasks WHERE kind = 'pets';
ALTER TABLE worker_tasks DROP CONSTRAINT worker_tasks_kind_check;
ALTER TABLE worker_tasks ADD CONSTRAINT worker_tasks_kind_check CHECK (kind IN ('embed_image', 'ocr', 'faces'));
