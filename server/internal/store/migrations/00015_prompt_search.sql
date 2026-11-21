-- +goose Up
-- Hybrid search: the words of prompts, so a search finds what a prompt asked for as well as what
-- an image looks like.
CREATE INDEX prompts_words ON prompts USING gin (to_tsvector('english', text));

-- +goose Down
DROP INDEX prompts_words;
