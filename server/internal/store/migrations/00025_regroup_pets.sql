-- +goose Up
-- Pets are grouped again under stricter rules (a pet must be seen in two photos; weak detections
-- are left out): the suggestions made before, which nobody has named, are taken apart. Named pets
-- and animals placed by hand stay.
WITH unnamed AS (
    SELECT id FROM people WHERE kind = 'pet' AND origin = 'faces' AND name = ''
)
UPDATE faces SET person_id = NULL, assignment = NULL, grouped_at = NULL
WHERE species IS NOT NULL AND assignment = 'auto' AND person_id IN (SELECT id FROM unnamed);
DELETE FROM people p WHERE p.kind = 'pet' AND p.origin = 'faces' AND p.name = ''
  AND NOT EXISTS (SELECT 1 FROM faces f WHERE f.person_id = p.id);
UPDATE faces SET grouped_at = NULL WHERE species IS NOT NULL AND person_id IS NULL;

-- +goose Down
SELECT 1;
