-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: CreateUser :one
INSERT INTO users (email, username, display_name, role, time_zone)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: CreateFirstUser :one
-- First-run setup: creates the admin only while the server has no users at all.
INSERT INTO users (email, username, display_name, role, time_zone)
SELECT @email, @username, @display_name, 'admin', @time_zone
WHERE NOT EXISTS (SELECT 1 FROM users)
RETURNING *;

-- name: SetUserTimeZone :exec
UPDATE users SET time_zone = @time_zone WHERE id = @id;

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE lower(email) = lower($1);

-- name: SetPassword :exec
INSERT INTO credentials (user_id, kind, data) VALUES ($1, 'password', $2)
ON CONFLICT (user_id) WHERE kind = 'password' DO UPDATE SET data = EXCLUDED.data;

-- name: GetPassword :one
SELECT data FROM credentials WHERE user_id = $1 AND kind = 'password';

-- name: CreateSession :one
INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)
RETURNING id;

-- name: GetSessionUser :one
SELECT u.*, s.id AS session_id, s.last_seen_at AS session_seen_at, s.vault_until, s.device AS session_device, s.user_agent AS session_user_agent,
       s.via_link AS session_via_link
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1 AND s.expires_at > now();

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = now(), expires_at = $2 WHERE id = $1;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = $1;

-- name: SetUserSetting :exec
-- One of a user's settings (users.settings), by name.
UPDATE users SET settings = settings || jsonb_build_object(@name::text, @value::jsonb) WHERE id = @id;
