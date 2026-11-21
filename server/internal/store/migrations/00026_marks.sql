-- +goose Up
-- Ratings, pick/reject flags and color labels: per user (asset_user_state already has the columns),
-- shown and filtered by those who turn them on (users.settings.organize). Labels are Lightroom's
-- five colors.
UPDATE asset_user_state SET color_label = NULL WHERE color_label NOT IN ('red', 'yellow', 'green', 'blue', 'purple');
UPDATE asset_user_state SET rating = NULL WHERE rating = 0;
ALTER TABLE asset_user_state ADD CONSTRAINT asset_user_state_color_label CHECK (color_label IN ('red', 'yellow', 'green', 'blue', 'purple'));
ALTER TABLE asset_user_state ADD CONSTRAINT asset_user_state_rating_stars CHECK (rating BETWEEN 1 AND 5);
CREATE INDEX asset_user_state_marked ON asset_user_state (user_id, asset_id)
    WHERE rating IS NOT NULL OR flag IS NOT NULL OR color_label IS NOT NULL;

-- +goose Down
DROP INDEX asset_user_state_marked;
ALTER TABLE asset_user_state DROP CONSTRAINT asset_user_state_rating_stars;
ALTER TABLE asset_user_state DROP CONSTRAINT asset_user_state_color_label;
