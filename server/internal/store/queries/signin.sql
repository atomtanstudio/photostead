-- Sign-in beyond a password: credentials of other kinds, and sign-ins under way (auth_flows).

-- name: StartFlow :one
WITH old AS (DELETE FROM auth_flows WHERE expires_at < now()) -- sweep the ones that ran out
INSERT INTO auth_flows (token_hash, kind, user_id, data, expires_at)
VALUES (@token_hash, @kind, sqlc.narg(user_id), @data, now() + make_interval(secs => @seconds::integer))
RETURNING id;

-- name: TakeFlow :one
-- A sign-in under way, while it lasts; each look counts as a try.
UPDATE auth_flows SET attempts = attempts + 1
WHERE token_hash = @token_hash AND kind = @kind AND expires_at > now()
RETURNING id, user_id, data, attempts;

-- name: EndFlow :exec
DELETE FROM auth_flows WHERE id = @id;

-- name: Credentials :many
-- A user's credentials of one kind, oldest first.
SELECT id, name, data, created_at, last_used_at FROM credentials WHERE user_id = @user_id AND kind = @kind ORDER BY created_at;

-- name: CredentialKinds :many
-- What a user can sign in with: each kind and how many.
SELECT kind, count(*)::integer AS n FROM credentials WHERE user_id = @user_id GROUP BY kind;

-- name: AddCredential :one
INSERT INTO credentials (user_id, kind, name, data) VALUES (@user_id, @kind, @name, @data) RETURNING id, created_at;

-- name: SetTOTP :exec
-- The user's authenticator-app secret (one at most), confirmed or waiting to be.
INSERT INTO credentials (user_id, kind, data) VALUES (@user_id, 'totp', @data)
ON CONFLICT (user_id) WHERE kind = 'totp' DO UPDATE SET data = EXCLUDED.data, created_at = now(), last_used_at = NULL;

-- name: UpdateCredential :exec
-- Its data after a use (a passkey's counter, a code's time step, a recovery code spent).
UPDATE credentials SET data = @data, last_used_at = now() WHERE id = @id;

-- name: DeleteCredential :execrows
DELETE FROM credentials WHERE id = @id AND user_id = @user_id AND kind = @kind;

-- name: DeleteCredentials :execrows
DELETE FROM credentials WHERE user_id = @user_id AND kind = @kind;

-- name: OIDCUser :one
-- Whose account a sign-on provider's subject is linked to.
SELECT c.id, c.user_id FROM credentials c
WHERE c.kind = 'oidc' AND c.data->>'issuer' = @issuer::text AND c.data->>'subject' = @subject::text;

-- name: CreateMember :one
-- A new account made by single sign-on (no password).
INSERT INTO users (email, username, display_name, role) VALUES (@email, @username, @display_name, 'member')
RETURNING *;
