-- +goose Up
-- The image an image-to-video clip was made from ("Made from"), worked out with characters: the
-- clip's graph names it, and when the name recurs, the look settles which one it was.
CREATE TABLE generation_starts (
    video_id uuid PRIMARY KEY REFERENCES assets ON DELETE CASCADE,
    image_id uuid NOT NULL REFERENCES assets ON DELETE CASCADE,
    look     real -- how alike they look (search embeddings); missing when its name alone was enough
);
CREATE INDEX generation_starts_image ON generation_starts (image_id);

-- +goose Down
DROP TABLE generation_starts;
