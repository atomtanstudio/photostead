-- Faces: found by workers, grouped into people by a server job (so not through the gate). Reads for
-- a viewer are in collections.sql and timeline.sql.

-- name: QueueFaces :execrows
-- Every rendered item (a video by its poster) not yet looked at for faces by the engine.
INSERT INTO worker_tasks (kind, asset_id, model, priority)
SELECT 'faces', a.id, @engine::text, CASE WHEN a.created_at > now() - interval '1 day' THEN 10 ELSE 0 END
FROM assets a
WHERE a.render_state = 'done' AND a.motion_of IS NULL AND a.purged_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM face_scans s WHERE s.asset_id = a.id AND s.engine = @engine::text)
ON CONFLICT (kind, asset_id, model) DO NOTHING;

-- name: FinishFaceTask :one
-- A faces task this worker holds is done: the item is recorded as looked at.
WITH done AS (
    DELETE FROM worker_tasks t
    WHERE t.id = @task_id AND t.lease_owner = @worker_id AND t.state = 'leased' AND t.kind = 'faces'
    RETURNING t.asset_id, t.model
)
INSERT INTO face_scans (asset_id, engine, faces)
SELECT done.asset_id, done.model, @faces::integer FROM done
ON CONFLICT (asset_id) DO UPDATE SET engine = EXCLUDED.engine, faces = EXCLUDED.faces, scanned_at = now()
RETURNING asset_id, engine;

-- name: ClearFaces :exec
-- An item looked at again: its earlier faces go, except ones the owner placed by hand.
DELETE FROM faces WHERE asset_id = @asset_id AND species IS NULL AND assignment IS DISTINCT FROM 'confirmed';

-- name: InsertFace :exec
INSERT INTO faces (asset_id, engine, box, landmarks, score, px, yaw, blur, embedding)
VALUES (@asset_id, @engine, @box::real[], @landmarks::real[], @score, @px, @yaw, @blur, CAST(@embedding::text AS halfvec(512)));

-- name: FaceProgress :one
SELECT (SELECT count(*) FROM face_scans WHERE engine = @engine::text)::integer AS scanned,
       (SELECT count(*) FROM faces WHERE engine = @engine::text AND species IS NULL)::integer AS faces,
       (SELECT count(*) FROM worker_tasks WHERE kind = 'faces' AND state IN ('queued', 'leased'))::integer AS waiting,
       (SELECT count(*) FROM worker_tasks WHERE kind = 'faces' AND state = 'failed')::integer AS failed;

-- name: FaceOwners :many
SELECT DISTINCT a.owner_id FROM faces f JOIN assets a ON a.id = f.asset_id;

-- name: ClusterFaces :many
-- An owner's faces in real photos (not AI, not trashed), in the order they were taken, with what
-- the grouping needs: the embedding (pgvector's binary halfvec), quality, and where each stands.
-- People's faces, or one species of pet (each is grouped on its own).
SELECT f.id, halfvec_send(f.embedding)::bytea AS vec, f.score, f.px, f.yaw, f.blur,
       f.person_id, f.assignment, f.asset_id, (f.grouped_at IS NOT NULL)::boolean AS seen,
       coalesce((SELECT array_agg(r.person_id) FROM face_rejections r WHERE r.face_id = f.id), '{}')::uuid[] AS rejected
FROM faces f JOIN assets a ON a.id = f.asset_id
WHERE a.owner_id = @owner_id AND a.source_type NOT IN ('ai_generated', 'ai_edited') AND a.trashed_at IS NULL
  AND f.species IS NOT DISTINCT FROM sqlc.narg(species)::text
ORDER BY a.taken_at, f.id;

-- name: MarkGrouped :exec
UPDATE faces SET grouped_at = now() WHERE id = ANY(@face_ids::uuid[]);

-- name: NewFacePerson :one
-- Someone grouping found: a person, or a pet of a species.
INSERT INTO people (owner_id, name, origin, kind, species)
VALUES (@owner_id, '', 'faces', CASE WHEN sqlc.narg(species)::text IS NULL THEN 'human' ELSE 'pet' END, sqlc.narg(species)::text)
RETURNING id;

-- name: AssignFaces :exec
UPDATE faces f SET person_id = p.person_id, assignment = 'auto'
FROM (SELECT unnest(@face_ids::uuid[]) AS face_id, unnest(@person_ids::uuid[]) AS person_id) p
WHERE f.id = p.face_id AND f.assignment IS DISTINCT FROM 'confirmed';

-- name: NamedSuggestions :many
-- Items an import said show each named person (to give a new group of faces its name).
SELECT ps.person_id, ps.asset_id FROM person_suggestions ps JOIN people p ON p.id = ps.person_id
WHERE p.owner_id = @owner_id AND p.name <> '';

-- name: SetCovers :exec
-- Each person without a cover gets their clearest face (large, frontal, sharp), never one from an
-- item in the vault: a cover there (the item went in since) is replaced.
UPDATE people p SET cover_face_id = c.id
FROM (
    SELECT DISTINCT ON (f.person_id) f.person_id, f.id
    FROM faces f JOIN assets a ON a.id = f.asset_id
    WHERE f.person_id IS NOT NULL AND a.visibility <> 'vault'
    ORDER BY f.person_id, (f.assignment = 'confirmed') DESC, (abs(f.yaw) < 0.3 AND f.score >= 0.8) DESC,
             f.px * CASE WHEN f.species IS NULL THEN least(f.blur, 400) ELSE 400 END DESC
) c
WHERE p.id = c.person_id AND p.owner_id = @owner_id AND (p.cover_face_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM faces cf JOIN assets ca ON ca.id = cf.asset_id
    WHERE cf.id = p.cover_face_id AND cf.person_id = p.id AND ca.visibility <> 'vault'));

-- name: DropEmptyFacePeople :exec
-- People made by grouping who have no faces left (merged away, or every face taken out).
DELETE FROM people p WHERE p.owner_id = @owner_id AND p.origin = 'faces' AND p.name = ''
  AND NOT EXISTS (SELECT 1 FROM faces f WHERE f.person_id = p.id);

-- name: FaceRunDue :one
-- Whether an owner's faces need grouping: new faces, or a correction, since the last run.
SELECT (r.owner_id IS NULL
        OR EXISTS (SELECT 1 FROM faces f JOIN assets a ON a.id = f.asset_id WHERE a.owner_id = @owner_id
                   AND f.created_at > coalesce(r.newest_face, '-infinity'))
        OR EXISTS (SELECT 1 FROM face_rejections fr JOIN faces f ON f.id = fr.face_id JOIN assets a ON a.id = f.asset_id
                   WHERE a.owner_id = @owner_id AND fr.created_at > r.ran_at)
        OR EXISTS (SELECT 1 FROM faces f JOIN assets a ON a.id = f.asset_id
                   WHERE a.owner_id = @owner_id AND a.source_type NOT IN ('ai_generated', 'ai_edited') AND a.trashed_at IS NULL
                     AND (f.grouped_at IS NULL OR (f.person_id IS NULL AND f.assignment IS NOT NULL))))::boolean AS due
FROM (SELECT @owner_id::uuid AS o) x LEFT JOIN face_runs r ON r.owner_id = x.o;

-- name: ReleaseOrphanFaces :exec
-- Faces whose person went away are placed afresh.
UPDATE faces f SET assignment = NULL, grouped_at = NULL
FROM assets a WHERE a.id = f.asset_id AND a.owner_id = @owner_id AND f.person_id IS NULL AND f.assignment IS NOT NULL;

-- name: RecordFaceRun :exec
INSERT INTO face_runs (owner_id, ran_at, newest_face, faces, people)
VALUES (@owner_id, now(), (SELECT max(f.created_at) FROM faces f JOIN assets a ON a.id = f.asset_id WHERE a.owner_id = @owner_id), @faces, @people)
ON CONFLICT (owner_id) DO UPDATE SET ran_at = EXCLUDED.ran_at, newest_face = EXCLUDED.newest_face, faces = EXCLUDED.faces, people = EXCLUDED.people;

-- name: RenamePerson :execrows
-- A name or hidden flag set by the owner (either may be left as it is). A group grouping found
-- becomes the owner's once named, so it stays when its faces go.
UPDATE people SET name = coalesce(sqlc.narg(name), name), hidden = coalesce(sqlc.narg(hidden), hidden), updated_at = now(),
    origin = CASE WHEN origin = 'faces' AND coalesce(sqlc.narg(name), '') <> '' THEN 'user' ELSE origin END
WHERE id = @id AND owner_id = @owner_id;

-- name: MergePeople :execrows
-- Everything of one person goes to another (the owner says they're the same); a name moves over
-- when the one kept has none. Faces kept out of the target are let go, for grouping to place, and
-- faces kept out of this one stay out of the target.
WITH moved AS (
    UPDATE faces f SET person_id = @into_id::uuid
    WHERE f.person_id = @from_id::uuid
      AND NOT EXISTS (SELECT 1 FROM face_rejections r WHERE r.face_id = f.id AND r.person_id = @into_id::uuid)
      AND EXISTS (SELECT 1 FROM people p WHERE p.id = @into_id::uuid AND p.owner_id = @owner_id::uuid)
    RETURNING f.id
), freed AS (
    UPDATE faces f SET person_id = NULL, assignment = NULL, grouped_at = NULL
    WHERE f.person_id = @from_id::uuid
      AND EXISTS (SELECT 1 FROM face_rejections r WHERE r.face_id = f.id AND r.person_id = @into_id::uuid)
      AND EXISTS (SELECT 1 FROM people p WHERE p.id = @into_id::uuid AND p.owner_id = @owner_id::uuid)
    RETURNING f.id
), kept_out AS (
    INSERT INTO face_rejections (face_id, person_id, created_at)
    SELECT r.face_id, @into_id::uuid, r.created_at FROM face_rejections r
    WHERE r.person_id = @from_id::uuid AND EXISTS (SELECT 1 FROM people p WHERE p.id = @into_id::uuid AND p.owner_id = @owner_id::uuid)
    ON CONFLICT DO NOTHING
    RETURNING 1
), sugg AS (
    INSERT INTO person_suggestions (person_id, asset_id, source, created_at)
    SELECT @into_id::uuid, ps.asset_id, ps.source, ps.created_at FROM person_suggestions ps
    WHERE ps.person_id = @from_id::uuid AND EXISTS (SELECT 1 FROM people p WHERE p.id = @into_id::uuid AND p.owner_id = @owner_id::uuid)
    ON CONFLICT DO NOTHING
    RETURNING 1
), named AS (
    UPDATE people t SET name = f.name, origin = 'user', updated_at = now()
    FROM people f WHERE t.id = @into_id::uuid AND f.id = @from_id::uuid AND t.owner_id = @owner_id::uuid AND f.owner_id = @owner_id::uuid
      AND t.name = '' AND f.name <> ''
    RETURNING 1
)
DELETE FROM people p WHERE p.id = @from_id::uuid AND p.owner_id = @owner_id::uuid AND p.id <> @into_id::uuid
  AND EXISTS (SELECT 1 FROM people t WHERE t.id = @into_id::uuid AND t.owner_id = @owner_id::uuid);

-- name: RemoveFromPerson :one
-- "Not this person" for these items: their faces in the person come out and are kept out, and
-- grouping looks at them afresh (they may well belong to someone else). An import's suggestions
-- for them go too. Says how many items changed.
WITH out_faces AS (
    SELECT f.id FROM faces f JOIN people p ON p.id = f.person_id
    WHERE f.person_id = @person_id::uuid AND p.owner_id = @owner_id::uuid AND f.asset_id = ANY(@asset_ids::uuid[])
), rejected AS (
    INSERT INTO face_rejections (face_id, person_id) SELECT id, @person_id::uuid FROM out_faces ON CONFLICT DO NOTHING RETURNING 1
), unsuggested AS (
    DELETE FROM person_suggestions ps USING people p
    WHERE ps.person_id = p.id AND p.id = @person_id::uuid AND p.owner_id = @owner_id::uuid AND ps.asset_id = ANY(@asset_ids::uuid[])
    RETURNING ps.asset_id
), freed AS (
    UPDATE faces SET person_id = NULL, assignment = NULL, grouped_at = NULL WHERE id IN (SELECT id FROM out_faces)
    RETURNING asset_id
)
SELECT count(*)::bigint FROM (SELECT asset_id FROM freed UNION SELECT asset_id FROM unsuggested) x;

-- name: FaceForThumb :one
-- A face's (or an animal's) item and box, if the viewer can see the item.
SELECT f.asset_id, f.box, f.species FROM faces f JOIN visible_assets(@viewer::uuid) a ON a.id = f.asset_id WHERE f.id = @id;

-- name: LinkCharacter :execrows
-- A group of faces is one of the owner's characters (or, with no character, a person again).
UPDATE people p SET character_id = sqlc.narg(character_id), updated_at = now()
WHERE p.id = @id AND p.owner_id = @owner_id
  AND (sqlc.narg(character_id)::uuid IS NULL OR EXISTS (SELECT 1 FROM characters c WHERE c.id = sqlc.narg(character_id)::uuid AND c.owner_id = @owner_id));

-- name: UnlinkCharacter :many
-- Every group of faces linked to a character becomes a person again.
UPDATE people p SET character_id = NULL, updated_at = now()
FROM characters c WHERE c.id = p.character_id AND c.id = @character_id AND c.owner_id = @owner_id
RETURNING p.id;

-- name: SyncFaceCharacters :exec
-- Characters' items from their groups of faces (basis 'faces'), kept up to date as faces are
-- grouped. An item the owner confirmed or rejected, or that matched otherwise, keeps that.
WITH want AS (
    SELECT DISTINCT p.character_id, f.asset_id FROM faces f JOIN people p ON p.id = f.person_id
    WHERE p.owner_id = @owner_id AND p.character_id IS NOT NULL
), gone AS (
    DELETE FROM character_assets ca USING characters c
    WHERE ca.character_id = c.id AND c.owner_id = @owner_id AND ca.basis = 'faces'
      AND NOT EXISTS (SELECT 1 FROM want w WHERE w.character_id = ca.character_id AND w.asset_id = ca.asset_id)
    RETURNING 1
)
INSERT INTO character_assets (character_id, asset_id, basis)
SELECT character_id, asset_id, 'faces' FROM want
ON CONFLICT (character_id, asset_id) DO NOTHING;

-- name: CharacterByName :one
SELECT id FROM characters WHERE owner_id = @owner_id AND lower(name) = lower(@name);

-- name: QueuePets :execrows
-- Every rendered real photo (a video by its poster) not yet looked at for pets by the engine. AI
-- images are skipped: their animals never become pets.
INSERT INTO worker_tasks (kind, asset_id, model, priority)
SELECT 'pets', a.id, @engine::text, CASE WHEN a.created_at > now() - interval '1 day' THEN 10 ELSE 0 END
FROM assets a
WHERE a.render_state = 'done' AND a.motion_of IS NULL AND a.purged_at IS NULL AND a.source_type NOT IN ('ai_generated', 'ai_edited')
  AND NOT EXISTS (SELECT 1 FROM pet_scans s WHERE s.asset_id = a.id AND s.engine = @engine::text)
ON CONFLICT (kind, asset_id, model) DO NOTHING;

-- name: FinishPetTask :one
-- A pets task this worker holds is done: the item is recorded as looked at.
WITH done AS (
    DELETE FROM worker_tasks t
    WHERE t.id = @task_id AND t.lease_owner = @worker_id AND t.state = 'leased' AND t.kind = 'pets'
    RETURNING t.asset_id, t.model
)
INSERT INTO pet_scans (asset_id, engine, pets)
SELECT done.asset_id, done.model, @pets::integer FROM done
ON CONFLICT (asset_id) DO UPDATE SET engine = EXCLUDED.engine, pets = EXCLUDED.pets, scanned_at = now()
RETURNING asset_id, engine;

-- name: ClearPets :exec
-- An item looked at again for pets: its earlier animals go, except ones the owner placed by hand.
DELETE FROM faces WHERE asset_id = @asset_id AND species IS NOT NULL AND assignment IS DISTINCT FROM 'confirmed';

-- name: InsertPet :exec
INSERT INTO faces (asset_id, engine, species, box, landmarks, score, px, yaw, blur, embedding)
VALUES (@asset_id, @engine, @species::text, @box::real[], '{}', @score, @px, 0, 0, CAST(@embedding::text AS halfvec(512)));

-- name: PetProgress :one
SELECT (SELECT count(*) FROM pet_scans WHERE engine = @engine::text)::integer AS scanned,
       (SELECT count(*) FROM faces WHERE engine = @engine::text AND species IS NOT NULL)::integer AS pets,
       (SELECT count(*) FROM worker_tasks WHERE kind = 'pets' AND state IN ('queued', 'leased'))::integer AS waiting,
       (SELECT count(*) FROM worker_tasks WHERE kind = 'pets' AND state = 'failed')::integer AS failed;

-- name: PersonKind :one
SELECT kind, species FROM people WHERE id = @id AND owner_id = @owner_id;

-- name: UnplaceFaces :exec
-- Faces (or animals) grouping placed that it no longer would: out of their person, to wait.
UPDATE faces SET person_id = NULL, assignment = NULL WHERE id = ANY(@face_ids::uuid[]) AND assignment = 'auto';
