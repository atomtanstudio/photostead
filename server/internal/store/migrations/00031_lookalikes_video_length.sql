-- +goose Up
-- Two videos are look-alikes only within half a second or 1% of each other's length (it was a
-- second or 2%, which chained 4- and 5-second clips together): unanswered suggestions are made again.
DELETE FROM stacks WHERE kind = 'near_duplicate' AND state = 'suggested';
DELETE FROM near_duplicate_checks;

-- +goose Down
SELECT 1;
