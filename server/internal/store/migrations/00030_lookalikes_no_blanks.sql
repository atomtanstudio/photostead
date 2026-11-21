-- +goose Up
-- Blank pictures (black frames, plain title cards) are never look-alikes, photos go only with
-- photos, and videos only with videos of the same length: suggestions nobody has answered yet are
-- made again under those rules.
DELETE FROM stacks WHERE kind = 'near_duplicate' AND state = 'suggested';
DELETE FROM near_duplicate_checks;

-- +goose Down
SELECT 1;
