-- +goose Up
-- Memories: "on this day" and named people over the years, made when asked for from the viewer's
-- items (through the gate). Each person can keep people, pets and stretches of time out of theirs.
ALTER TABLE people ADD COLUMN in_memories boolean NOT NULL DEFAULT true;

CREATE TABLE memory_hidden_days (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    from_date  date NOT NULL,
    to_date    date NOT NULL CHECK (to_date >= from_date),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX memory_hidden_days_user ON memory_hidden_days (user_id, from_date);

-- +goose Down
DROP TABLE memory_hidden_days;
ALTER TABLE people DROP COLUMN in_memories;
