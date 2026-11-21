-- +goose Up
-- Personal links: sharing albums with someone by name, with no account for them to make. One
-- link per guest, for every album shared with them: the first few devices that open it are
-- signed in as them (a session marked via_link), and the rest are turned away. Whoever made the
-- link can copy it again, sign its devices out, or replace it.
CREATE TABLE personal_links (
    user_id     uuid PRIMARY KEY REFERENCES users ON DELETE CASCADE, -- the guest it signs in
    token       text NOT NULL UNIQUE,
    created_by  uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    max_devices int  NOT NULL DEFAULT 3,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX personal_links_created_by ON personal_links (created_by);

ALTER TABLE sessions ADD COLUMN via_link boolean NOT NULL DEFAULT false; -- opened from a personal link: one of its devices

-- +goose Down
ALTER TABLE sessions DROP COLUMN via_link;
DROP TABLE personal_links;
