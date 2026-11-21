-- XMP sidecars: maintenance of each owner's library (it names the assets table; see gate_test.go).
-- What goes in one is its owner's: their marks, keywords, description, a date they set, the people
-- they named, how an AI item was made, a photo's edit.

-- name: SidecarItems :many
SELECT a.id, a.type, a.taken_at, a.taken_offset_min, a.date_source, a.source_type, a.visibility, (a.purged_at IS NOT NULL)::boolean AS purged,
       f.rel_path, l.id AS library_id, l.root_path, l.read_only, l.sidecar_mode,
       coalesce(us.favorite, false)::boolean AS favorite, coalesce(us.rating, 0)::smallint AS rating, us.flag, us.color_label,
       d.text AS description,
       ARRAY(SELECT k.name FROM asset_keywords ak JOIN keywords k ON k.id = ak.keyword_id WHERE ak.asset_id = a.id ORDER BY k.name)::text[] AS keywords,
       ARRAY(SELECT DISTINCT p.name FROM faces fc JOIN people p ON p.id = fc.person_id
             WHERE fc.asset_id = a.id AND p.owner_id = a.owner_id AND p.kind = 'human' AND p.name <> '' AND NOT p.hidden
             ORDER BY p.name)::text[] AS people,
       g.kind AS gen_kind, g.tool, m.display AS model, pr.text AS prompt, gp.text AS given_prompt,
       e.recipe AS edit,
       w.hash AS last_hash, q.queued_at
FROM sidecar_queue q
JOIN assets a ON a.id = q.asset_id
LEFT JOIN files f ON f.id = a.primary_file_id
LEFT JOIN libraries l ON l.id = f.library_id
LEFT JOIN asset_user_state us ON us.asset_id = a.id AND us.user_id = a.owner_id
LEFT JOIN asset_descriptions d ON d.asset_id = a.id
LEFT JOIN generations g ON g.asset_id = a.id
LEFT JOIN ai_models m ON m.id = g.model_id
LEFT JOIN prompts pr ON pr.id = g.prompt_id
LEFT JOIN given_prompts gp ON gp.asset_id = a.id
LEFT JOIN asset_edits e ON e.asset_id = a.id
LEFT JOIN sidecar_writes w ON w.asset_id = a.id
ORDER BY q.queued_at, q.asset_id
LIMIT @lim::integer;

-- name: SidecarWritten :exec
INSERT INTO sidecar_writes (asset_id, path, hash) VALUES (@asset_id, @path, @hash)
ON CONFLICT (asset_id) DO UPDATE SET path = EXCLUDED.path, hash = EXCLUDED.hash, written_at = now();

-- name: SidecarsDone :exec
-- Out of the queue, unless queued again since they were read (a change meanwhile bumps queued_at).
DELETE FROM sidecar_queue q
WHERE (q.asset_id, q.queued_at) IN (SELECT unnest(@ids::uuid[]), unnest(@queued::timestamptz[]));

-- name: SidecarProgress :one
SELECT (SELECT count(*) FROM sidecar_queue)::integer AS waiting, (SELECT count(*) FROM sidecar_writes)::integer AS written;

-- name: SetSidecarMode :execrows
UPDATE libraries SET sidecar_mode = @mode WHERE name = @name;

-- name: QueueLibrarySidecars :execrows
-- A library's sidecar mode changed: its items are looked at again (those with nothing to write are
-- passed over quickly).
INSERT INTO sidecar_queue (asset_id)
SELECT a.id FROM assets a JOIN files f ON f.id = a.primary_file_id JOIN libraries l ON l.id = f.library_id
WHERE l.name = @name AND a.purged_at IS NULL AND a.motion_of IS NULL
ON CONFLICT DO NOTHING;
