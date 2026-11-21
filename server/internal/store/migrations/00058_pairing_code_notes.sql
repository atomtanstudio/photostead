-- +goose Up
-- A short note on a pairing code that lasts, to tell it apart in the list ("App Review",
-- "Mum's iPad"). Empty: none.
ALTER TABLE pairing_codes ADD COLUMN note text NOT NULL DEFAULT '' CHECK (length(note) <= 60);

-- +goose Down
ALTER TABLE pairing_codes DROP COLUMN note;
