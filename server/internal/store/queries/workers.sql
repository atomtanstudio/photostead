-- Workers and their tasks: maintenance of the library, so these may name the assets table (see
-- gate_test.go). Search reads go through the gate (search.go).

-- name: CreateWorker :one
INSERT INTO workers (name, token_hash, builtin) VALUES (@name, @token_hash, @builtin)
ON CONFLICT (name) DO UPDATE SET token_hash = EXCLUDED.token_hash, revoked_at = NULL, builtin = EXCLUDED.builtin
RETURNING id;

-- name: WorkerByToken :one
SELECT id, name FROM workers WHERE token_hash = @token_hash AND revoked_at IS NULL;

-- name: WorkerHello :exec
UPDATE workers SET last_seen_at = now(), version = @version, capabilities = @capabilities WHERE id = @id;

-- name: ListWorkers :many
SELECT w.id, w.name, w.builtin, w.version, w.capabilities, w.created_at, w.last_seen_at, w.revoked_at,
       (SELECT count(*) FROM worker_tasks t WHERE t.lease_owner = w.id AND t.state = 'leased')::integer AS leased
FROM workers w ORDER BY w.builtin DESC, w.name;

-- name: RevokeWorker :execrows
UPDATE workers SET revoked_at = now() WHERE name = @name AND revoked_at IS NULL;

-- name: QueueEmbeddings :execrows
-- Every item that can be searched (rendered, not a Live Photo's clip) and has no embedding for the
-- model or a task for one. New items (recently created) go ahead of the backlog.
INSERT INTO worker_tasks (kind, asset_id, model, priority)
SELECT 'embed_image', a.id, @model::text, CASE WHEN a.created_at > now() - interval '1 day' THEN 10 ELSE 0 END
FROM assets a
WHERE a.render_state = 'done' AND a.motion_of IS NULL
  AND NOT EXISTS (SELECT 1 FROM asset_embeddings e WHERE e.asset_id = a.id AND e.model = @model::text)
ON CONFLICT (kind, asset_id, model) DO NOTHING;

-- name: ClaimTasks :many
-- Hands out ready tasks (queued, or leased by a worker that went quiet), most urgent first.
UPDATE worker_tasks t SET state = 'leased', lease_owner = @worker_id, lease_expires_at = now() + make_interval(secs => @lease_seconds::integer),
    attempts = t.attempts + 1
WHERE t.id IN (
    SELECT id FROM worker_tasks
    WHERE kind = ANY(@kinds::text[]) AND model = ANY(@models::text[])
      AND (state = 'queued' OR (state = 'leased' AND lease_expires_at < now()))
    ORDER BY priority DESC, id
    LIMIT @max_tasks
    FOR UPDATE SKIP LOCKED
)
RETURNING t.id, t.kind, t.asset_id, t.model;

-- name: LeasedTask :one
SELECT t.id, t.kind, t.asset_id, t.model FROM worker_tasks t
WHERE t.id = @id AND t.lease_owner = @worker_id AND t.state = 'leased' AND t.lease_expires_at > now();

-- name: SaveEmbeddings :execrows
-- Results of tasks this worker holds: the embedding saved, the task done (deleted).
WITH r AS (SELECT unnest(@task_ids::bigint[]) AS task_id, unnest(@vectors::text[]) AS vec),
done AS (
    DELETE FROM worker_tasks t
    WHERE t.id IN (SELECT task_id FROM r) AND t.lease_owner = @worker_id AND t.state = 'leased' AND t.kind = 'embed_image'
    RETURNING t.id, t.asset_id, t.model
)
INSERT INTO asset_embeddings (asset_id, model, embedding)
SELECT done.asset_id, done.model, r.vec::halfvec FROM done JOIN r ON r.task_id = done.id
ON CONFLICT (asset_id, model) DO UPDATE SET embedding = EXCLUDED.embedding, created_at = now();

-- name: FailTasks :exec
-- Tasks a worker couldn't do: tried again later, until the third failure.
UPDATE worker_tasks t SET state = CASE WHEN t.attempts >= 3 THEN 'failed' ELSE 'queued' END,
    error = r.error, lease_owner = NULL, lease_expires_at = NULL
FROM (SELECT unnest(@task_ids::bigint[]) AS task_id, unnest(@errors::text[]) AS error) r
WHERE t.id = r.task_id AND t.lease_owner = @worker_id;

-- name: ReleaseTasks :exec
UPDATE worker_tasks SET state = 'queued', lease_owner = NULL, lease_expires_at = NULL, attempts = greatest(attempts - 1, 0)
WHERE id = ANY(@task_ids::bigint[]) AND lease_owner = @worker_id AND state = 'leased';

-- name: QueueOCR :execrows
-- Every rendered item (a video by its poster frame) without text read by the engine yet.
INSERT INTO worker_tasks (kind, asset_id, model, priority)
SELECT 'ocr', a.id, @model::text, CASE WHEN a.created_at > now() - interval '1 day' THEN 10 ELSE 0 END
FROM assets a
WHERE a.render_state = 'done' AND a.motion_of IS NULL
  AND NOT EXISTS (SELECT 1 FROM asset_ocr o WHERE o.asset_id = a.id AND o.engine = @model::text)
ON CONFLICT (kind, asset_id, model) DO NOTHING;

-- name: SaveOCR :execrows
-- Text read for tasks this worker holds: saved (even when empty), the task done.
WITH r AS (SELECT unnest(@task_ids::bigint[]) AS task_id, unnest(@texts::text[]) AS txt),
done AS (
    DELETE FROM worker_tasks t
    WHERE t.id IN (SELECT task_id FROM r) AND t.lease_owner = @worker_id AND t.state = 'leased' AND t.kind = 'ocr'
    RETURNING t.id, t.asset_id, t.model
)
INSERT INTO asset_ocr (asset_id, engine, text)
SELECT done.asset_id, done.model, r.txt FROM done JOIN r ON r.task_id = done.id
ON CONFLICT (asset_id) DO UPDATE SET engine = EXCLUDED.engine, text = EXCLUDED.text, created_at = now();

-- name: OCRProgress :one
SELECT (SELECT count(*) FROM asset_ocr WHERE engine = @model::text)::integer AS done,
       (SELECT count(*) FROM asset_ocr WHERE engine = @model::text AND text <> '')::integer AS with_text,
       (SELECT count(*) FROM worker_tasks WHERE kind = 'ocr' AND state IN ('queued', 'leased'))::integer AS waiting,
       (SELECT count(*) FROM worker_tasks WHERE kind = 'ocr' AND state = 'failed')::integer AS failed;

-- name: EmbeddingProgress :one
SELECT (SELECT count(*) FROM asset_embeddings WHERE model = @model::text)::integer AS embedded,
       (SELECT count(*) FROM worker_tasks WHERE model = @model::text AND state IN ('queued', 'leased'))::integer AS waiting,
       (SELECT count(*) FROM worker_tasks WHERE model = @model::text AND state = 'failed')::integer AS failed;

-- name: TouchWorker :exec
UPDATE workers SET last_seen_at = now() WHERE id = @id;

-- name: QueueAdult :execrows
-- Every rendered item (a video by several of its frames) not yet looked at for adult content by the
-- engine. AI images too: in an AI-heavy library, most adult pictures are made with AI.
INSERT INTO worker_tasks (kind, asset_id, model, priority)
SELECT 'adult', a.id, @engine::text, CASE WHEN a.created_at > now() - interval '1 day' THEN 10 ELSE 0 END
FROM assets a
WHERE a.render_state = 'done' AND a.motion_of IS NULL AND a.purged_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM adult_scans s WHERE s.asset_id = a.id AND s.engine = @engine::text)
ON CONFLICT (kind, asset_id, model) DO NOTHING;

-- name: TaskVideos :many
-- Which of these items are videos, and how long each runs (an adult task looks at several frames).
SELECT a.id, coalesce(a.duration_ms, 0)::integer AS duration_ms FROM assets a WHERE a.id = ANY(@ids::uuid[]) AND a.type = 'video';

-- name: FinishAdultTask :one
-- An adult task this worker holds is done: what the model saw is kept (replacing an earlier scan),
-- with the version of the rule that labels it.
WITH done AS (
    DELETE FROM worker_tasks t
    WHERE t.id = @task_id AND t.lease_owner = @worker_id AND t.state = 'leased' AND t.kind = 'adult'
    RETURNING t.asset_id, t.model
)
INSERT INTO adult_scans (asset_id, engine, scores, frames, rule)
SELECT done.asset_id, done.model, @scores::real[], @frames::smallint, @rule::smallint FROM done
ON CONFLICT (asset_id) DO UPDATE SET engine = EXCLUDED.engine, scores = EXCLUDED.scores, frames = EXCLUDED.frames,
    rule = EXCLUDED.rule, scanned_at = now()
RETURNING asset_id;

-- name: AdultToRelabel :many
-- Scans labelled by an older rule than this one.
SELECT asset_id, scores FROM adult_scans WHERE rule < @rule::smallint ORDER BY asset_id LIMIT @lim;

-- name: ClearScanLabelsAmong :exec
-- Items labelled again: what their last scan decided goes; their owner's corrections stay.
DELETE FROM asset_labels WHERE asset_id = ANY(@ids::uuid[]) AND label IN ('adult', 'suggestive') AND basis = 'visual';

-- name: AddScanLabels :exec
-- What a rule decided for each of these items (their owner's correction of a label wins).
INSERT INTO asset_labels (asset_id, label, basis, score)
SELECT unnest(@ids::uuid[]), unnest(@labels::text[]), 'visual', unnest(@scores::real[])
ON CONFLICT (asset_id, label) DO NOTHING;

-- name: SetAdultRule :exec
UPDATE adult_scans SET rule = @rule::smallint WHERE asset_id = ANY(@ids::uuid[]);

-- name: AdultProgress :one
SELECT (SELECT count(*) FROM adult_scans WHERE engine = @engine::text)::integer AS scanned,
       (SELECT count(*) FROM asset_labels WHERE label = 'adult' AND basis IN ('visual', 'confirmed'))::integer AS adult,
       (SELECT count(*) FROM worker_tasks WHERE kind = 'adult' AND state IN ('queued', 'leased'))::integer AS waiting,
       (SELECT count(*) FROM worker_tasks WHERE kind = 'adult' AND state = 'failed')::integer AS failed;

-- name: AdultWaiting :one
SELECT count(*)::integer FROM worker_tasks WHERE kind = 'adult' AND state IN ('queued', 'leased');

-- name: VideosToProbe :many
-- Videos not probed yet (their original reachable), oldest first.
SELECT a.id, f.rel_path, l.root_path FROM assets a
JOIN files f ON f.id = a.primary_file_id JOIN libraries l ON l.id = f.library_id
WHERE a.type = 'video' AND a.purged_at IS NULL AND a.motion_of IS NULL
  AND NOT EXISTS (SELECT 1 FROM video_probes p WHERE p.asset_id = a.id)
ORDER BY a.id
LIMIT @lim::integer;

-- name: SaveVideoProbe :exec
INSERT INTO video_probes (asset_id, container, codec, codecs, pix_fmt, bit_depth, transfer, hdr, audio, width, height, direct, error)
VALUES (@asset_id, @container, @codec, @codecs, @pix_fmt, @bit_depth, @transfer, @hdr, @audio, @width, @height, @direct, sqlc.narg('error'))
ON CONFLICT (asset_id) DO UPDATE SET container = EXCLUDED.container, codec = EXCLUDED.codec, codecs = EXCLUDED.codecs,
    pix_fmt = EXCLUDED.pix_fmt, bit_depth = EXCLUDED.bit_depth, transfer = EXCLUDED.transfer, hdr = EXCLUDED.hdr, audio = EXCLUDED.audio,
    width = EXCLUDED.width, height = EXCLUDED.height, direct = EXCLUDED.direct, error = EXCLUDED.error, probed_at = now();

-- name: GPUWorkerSeen :one
-- Whether a GPU worker has been around this week (long videos then wait for it, rather than tie up the
-- server's CPU for hours).
SELECT EXISTS (SELECT 1 FROM workers WHERE revoked_at IS NULL AND NOT builtin AND (capabilities->>'gpu')::boolean
               AND last_seen_at > now() - interval '7 days')::boolean;

-- name: QueueCopies :execrows
-- A copy for every video browsers can't all play, not made yet with this recipe. Long or big ones go
-- to GPU workers (model @large) when there are any; the rest to any worker (model @small).
INSERT INTO worker_tasks (kind, asset_id, model, priority)
SELECT 'transcode', a.id,
       CASE WHEN @gpu::boolean AND (coalesce(a.duration_ms, 0) > 300000 OR f.size > 500000000) THEN @large::text ELSE @small::text END,
       CASE WHEN a.created_at > now() - interval '1 day' THEN 10 ELSE 0 END
FROM assets a JOIN video_probes p ON p.asset_id = a.id JOIN files f ON f.id = a.primary_file_id
WHERE NOT p.direct AND p.error IS NULL AND a.purged_at IS NULL AND a.trashed_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM video_copies c WHERE c.asset_id = a.id AND c.recipe = @recipe::text)
  AND NOT EXISTS (SELECT 1 FROM worker_tasks t WHERE t.kind = 'transcode' AND t.asset_id = a.id)
ON CONFLICT (kind, asset_id, model) DO NOTHING;

-- name: TranscodeInputs :many
-- What a worker needs to know to make the copies of the videos it holds.
SELECT p.asset_id, p.hdr, p.width, p.height, coalesce(a.duration_ms, 0)::integer AS duration_ms,
       (p.codec = 'h264' AND p.pix_fmt IN ('yuv420p', 'yuvj420p') AND NOT p.hdr)::boolean AS picture_plays
FROM video_probes p JOIN assets a ON a.id = p.asset_id WHERE p.asset_id = ANY(@ids::uuid[]);

-- name: ExtendTask :execrows
-- A worker still at a long task (making a video's copy) keeps it ten more minutes.
UPDATE worker_tasks SET lease_expires_at = now() + interval '10 minutes'
WHERE id = @id AND lease_owner = @worker_id AND state = 'leased';

-- name: FinishCopy :one
-- A transcode task this worker holds is done: its copy is recorded.
WITH done AS (
    DELETE FROM worker_tasks t
    WHERE t.id = @task_id AND t.lease_owner = @worker_id AND t.state = 'leased' AND t.kind = 'transcode'
    RETURNING t.asset_id
)
INSERT INTO video_copies (asset_id, recipe, encoder, width, height, bytes)
SELECT done.asset_id, @recipe::text, @encoder::text, @width::integer, @height::integer, @bytes::bigint FROM done
ON CONFLICT (asset_id) DO UPDATE SET recipe = EXCLUDED.recipe, encoder = EXCLUDED.encoder, width = EXCLUDED.width,
    height = EXCLUDED.height, bytes = EXCLUDED.bytes, made_at = now()
RETURNING asset_id;

-- name: VideoProgress :one
SELECT (SELECT count(*) FROM video_probes)::integer AS probed,
       (SELECT count(*) FROM video_probes WHERE NOT direct AND error IS NULL)::integer AS need_copies,
       (SELECT count(*) FROM video_copies)::integer AS copies,
       (SELECT count(*) FROM worker_tasks WHERE kind = 'transcode' AND state IN ('queued', 'leased'))::integer AS waiting,
       (SELECT count(*) FROM worker_tasks WHERE kind = 'transcode' AND state = 'failed')::integer AS failed;

-- name: WorkerTaskCounts :one
-- AI work for the health check: tasks waiting for a worker, and the latest a worker checked in.
SELECT count(*) FILTER (WHERE state = 'queued')::bigint AS waiting,
       coalesce((SELECT max(last_seen_at) FROM workers WHERE revoked_at IS NULL), 'epoch')::timestamptz AS last_seen -- epoch: never
FROM worker_tasks;

-- name: WorkNoWorkerTakes :many
-- Queued AI work that no worker seen in the last ten minutes can do (by model), and how much: it
-- waits for one that can, such as a GPU machine run on demand.
SELECT t.model, count(*)::bigint AS waiting
FROM worker_tasks t
WHERE t.state = 'queued' AND NOT EXISTS (
    SELECT 1 FROM workers w WHERE w.revoked_at IS NULL AND w.last_seen_at > now() - interval '10 minutes'
      AND w.capabilities->'models' ? t.model)
GROUP BY t.model ORDER BY t.model;
