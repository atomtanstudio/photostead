-- +goose Up
-- Look-alikes are suggested at 0.99 alike rather than 0.97, which
-- took in similar but different shots: suggestions nobody has answered yet are made again.
DELETE FROM stacks WHERE kind = 'near_duplicate' AND state = 'suggested';
DELETE FROM near_duplicate_checks;

-- +goose Down
SELECT 1;
