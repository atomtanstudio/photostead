-- +goose Up
-- Adult content, decided again (labels/adult.go): the model's middle grade turned out to be lingerie
-- and swimwear, so adult now takes its top grade (nudity, sex). A video keeps every picture's
-- probabilities, four each, since two frames must now agree; only its most adult frame was kept, so
-- videos are scanned again. Photos keep their scan and are relabelled from it: rule is the version of
-- the rule that labelled each row, and rows labelled by an older one are labelled again.
DELETE FROM adult_scans s USING assets a WHERE a.id = s.asset_id AND a.type = 'video';
ALTER TABLE adult_scans DROP CONSTRAINT adult_scans_scores_check;
ALTER TABLE adult_scans ADD CONSTRAINT adult_scans_scores_check CHECK (cardinality(scores) = 4 * frames);
ALTER TABLE adult_scans ADD COLUMN rule smallint NOT NULL DEFAULT 1;
UPDATE adult_scans SET engine = 'freepik-eva02-nsfw-v2';
CREATE INDEX adult_scans_rule ON adult_scans (rule);

-- +goose Down
DROP INDEX adult_scans_rule;
ALTER TABLE adult_scans DROP COLUMN rule;
DELETE FROM adult_scans WHERE cardinality(scores) <> 4;
ALTER TABLE adult_scans DROP CONSTRAINT adult_scans_scores_check;
ALTER TABLE adult_scans ADD CONSTRAINT adult_scans_scores_check CHECK (cardinality(scores) = 4);
UPDATE adult_scans SET engine = 'freepik-eva02-nsfw-v1';
