-- Playing videos: how each was probed, and its copy that every browser plays (see 00051).

-- name: CopiesAmong :many
-- Which of these items have a playable copy.
SELECT c.asset_id FROM video_copies c WHERE c.asset_id = ANY(@ids::uuid[]);

-- name: VideoPlayback :one
-- An item's probe and copy, for the viewer to choose what to play.
SELECT p.codec, p.codecs, p.hdr, p.direct, p.error IS NOT NULL AS unreadable,
       (c.asset_id IS NOT NULL)::boolean AS has_copy,
       EXISTS (SELECT 1 FROM worker_tasks t WHERE t.kind = 'transcode' AND t.asset_id = p.asset_id AND t.state IN ('queued', 'leased'))::boolean AS waiting
FROM video_probes p LEFT JOIN video_copies c ON c.asset_id = p.asset_id
WHERE p.asset_id = @asset_id;
