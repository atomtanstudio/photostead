-- Utility labels, worked out by a server job over every owner's items (so not through the gate).
-- Reads for viewers live in collections.sql and timeline.sql.

-- name: LabelCandidates :many
-- Items the current rules haven't looked at, with their similarity to each label prompt (in the
-- order given). An item waits while its text is still to be read.
WITH p AS (
    SELECT l.label, v.vec::halfvec(768) AS v, l.ord
    FROM unnest(@prompts::text[]) WITH ORDINALITY AS l(label, ord)
    JOIN unnest(@vectors::text[]) WITH ORDINALITY AS v(vec, ord) ON v.ord = l.ord
),
items AS (
    SELECT a.id, a.type, a.source_type, coalesce(f.rel_path, '')::text AS rel_path, o.text AS ocr, e.embedding
    FROM assets a
    JOIN asset_embeddings e ON e.asset_id = a.id AND e.model = @model::text
    LEFT JOIN asset_ocr o ON o.asset_id = a.id
    LEFT JOIN files f ON f.id = a.primary_file_id
    LEFT JOIN labels_checked c ON c.asset_id = a.id
    WHERE a.motion_of IS NULL
      AND (c.asset_id IS NULL OR c.version <> @version::smallint OR (NOT c.had_text AND o.asset_id IS NOT NULL))
      AND (o.asset_id IS NOT NULL OR NOT EXISTS (
            SELECT 1 FROM worker_tasks t WHERE t.asset_id = a.id AND t.kind = 'ocr' AND t.state IN ('queued', 'leased')))
    ORDER BY a.id
    LIMIT @lim
)
SELECT i.id, i.type, i.source_type, i.rel_path, coalesce(i.ocr, '')::text AS ocr, (i.ocr IS NOT NULL)::boolean AS had_text,
       ARRAY(SELECT (1 - (i.embedding <=> p.v))::real FROM p ORDER BY p.ord)::real[] AS sims
FROM items i;

-- name: ClearAutoLabels :exec
-- (Adult and suggestive come from a worker's scan, not these rules: see ClearScanLabels.)
DELETE FROM asset_labels WHERE asset_id = ANY(@ids::uuid[]) AND basis IN ('metadata', 'visual') AND label NOT IN ('adult', 'suggestive');

-- name: AddAutoLabels :exec
-- New automatic labels; an item's own corrections win.
INSERT INTO asset_labels (asset_id, label, basis, score)
SELECT unnest(@ids::uuid[]), unnest(@labels::text[]), unnest(@bases::text[]), unnest(@scores::real[])
ON CONFLICT (asset_id, label) DO NOTHING;

-- name: MarkLabelsChecked :exec
INSERT INTO labels_checked (asset_id, version, had_text)
SELECT unnest(@ids::uuid[]), @version::smallint, unnest(@had_text::boolean[])
ON CONFLICT (asset_id) DO UPDATE SET version = EXCLUDED.version, had_text = EXCLUDED.had_text;

-- name: CorrectLabel :execrows
-- The owner says these items are (confirmed) or aren't (rejected) screenshots, receipts and so on.
INSERT INTO asset_labels (asset_id, label, basis)
SELECT a.id, @label::text, @basis::text FROM assets a WHERE a.id = ANY(@ids::uuid[]) AND a.owner_id = @owner::uuid
ON CONFLICT (asset_id, label) DO UPDATE SET basis = EXCLUDED.basis, updated_at = now();

-- name: LabelProgress :one
SELECT (SELECT count(*) FROM labels_checked WHERE version = @version::smallint)::integer AS checked,
       (SELECT count(*) FROM asset_labels WHERE basis IN ('metadata', 'visual', 'confirmed'))::integer AS labeled;

-- name: ClearScanLabels :exec
-- An item scanned again for adult content: what the last scan decided goes; the owner's corrections stay.
DELETE FROM asset_labels WHERE asset_id = @asset_id AND label IN ('adult', 'suggestive') AND basis = 'visual';

-- name: AddScanLabel :exec
-- What a scan decided (adult or suggestive); the owner's own correction of that label wins.
INSERT INTO asset_labels (asset_id, label, basis, score) VALUES (@asset_id, @label::text, 'visual', @score::real)
ON CONFLICT (asset_id, label) DO NOTHING;

-- name: ClearSuggestive :exec
-- Items their owner says are adult aren't also suggestive.
DELETE FROM asset_labels l USING assets a
WHERE l.asset_id = a.id AND a.id = ANY(@ids::uuid[]) AND a.owner_id = @owner::uuid AND l.label = 'suggestive' AND l.basis = 'visual';

-- name: AdultAmong :many
-- Which of these items are adult: by a scan or their owner, and not said otherwise.
SELECT l.asset_id FROM asset_labels l WHERE l.asset_id = ANY(@ids::uuid[]) AND l.label = 'adult' AND l.basis IN ('visual', 'confirmed');
