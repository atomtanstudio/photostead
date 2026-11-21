-- The server's own settings, and the sessions people are signed in with.

-- name: ServerSetting :one
SELECT value FROM server_settings WHERE key = @key;

-- name: SetServerSetting :exec
INSERT INTO server_settings (key, value) VALUES (@key, @value)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now();

-- name: CreateDeviceSession :one
INSERT INTO sessions (token_hash, user_id, expires_at, device, platform)
VALUES (@token_hash, @user_id, @expires_at, @device, @platform)
RETURNING id;

-- name: UserSessions :many
-- Where someone is signed in: browsers and paired devices, most recently used first.
SELECT id, device, platform, user_agent, created_at, last_seen_at FROM sessions
WHERE user_id = @user_id AND expires_at > now()
ORDER BY last_seen_at DESC;

-- name: EndUserSession :execrows
DELETE FROM sessions WHERE id = @id AND user_id = @user_id;

-- name: SetSessionUserAgent :exec
UPDATE sessions SET user_agent = @user_agent WHERE id = @id;

-- name: CreatePairingCode :one
-- A pairing code that lasts; the ones that ran out go.
WITH old AS (DELETE FROM pairing_codes WHERE expires_at < now())
INSERT INTO pairing_codes (code_hash, user_id, max_devices, note, expires_at)
VALUES (@code_hash, @user_id, @max_devices, @note, now() + make_interval(days => @days::integer))
RETURNING id, created_at, expires_at;

-- name: PairingCodes :many
-- The pairing codes that last, while they do, newest first, with whose account they pair into.
SELECT c.id, c.user_id, u.display_name, u.email, c.max_devices, c.devices, c.note, c.created_at, c.expires_at
FROM pairing_codes c JOIN users u ON u.id = c.user_id
WHERE c.expires_at > now()
ORDER BY c.created_at DESC;

-- name: UsePairingCode :one
-- One more device pairs with a lasting code, while it has days and devices left.
UPDATE pairing_codes SET devices = devices + 1
WHERE code_hash = @code_hash AND expires_at > now() AND devices < max_devices
RETURNING id, user_id;

-- name: RevokePairingCode :execrows
DELETE FROM pairing_codes WHERE id = @id;
