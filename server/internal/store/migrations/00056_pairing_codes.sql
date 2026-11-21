-- +goose Up
-- Pairing codes that last: the admin makes one for someone who can't come to the home network to
-- pair (Apple's App Review, family far away). It pairs devices into its account until its days or
-- its devices run out, or it's revoked (the row goes; devices it paired stay signed in). Like a
-- sign-in under way, only the code's hash is kept: the code is shown once, when it's made.
CREATE TABLE pairing_codes (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    code_hash   bytea NOT NULL UNIQUE,
    user_id     uuid NOT NULL REFERENCES users ON DELETE CASCADE, -- the account its devices pair into
    max_devices integer NOT NULL CHECK (max_devices BETWEEN 1 AND 50),
    devices     integer NOT NULL DEFAULT 0,                       -- paired with it so far
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL
);

-- +goose Down
DROP TABLE pairing_codes;
