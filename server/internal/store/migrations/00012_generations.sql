-- +goose Up
-- AI provenance: how an item was generated, read from evidence in the file (decision: evidence
-- before labels). The AI badge means assets.source_type is ai_generated or ai_edited, which is only
-- ever set together with a generations row.

-- The provenance parser version that last read an item's metadata (NULL: not yet), so a better
-- parser re-reads everything once.
ALTER TABLE assets ADD COLUMN provenance_version smallint;
-- Something that may be evidence was cut short in the index (values over 8 KB): the file itself
-- must be read again for the whole of it.
ALTER TABLE assets ADD COLUMN provenance_needs_file boolean NOT NULL DEFAULT false;

-- Checkpoints, diffusion models, services' models and LoRAs, by normalized name: the catalog
-- that "Models & LoRAs" grows from.
CREATE TABLE ai_models (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    kind       text NOT NULL CHECK (kind IN ('model', 'lora')),
    name       text NOT NULL, -- normalized: file name without folders or extension, lower case
    display    text NOT NULL, -- as first seen
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (kind, name)
);

-- Prompts, once per distinct text (by a hash of the normalized text): the prompt library's base.
CREATE TABLE prompts (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    owner_id        uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    text            text NOT NULL,
    normalized_hash bytea NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner_id, normalized_hash)
);

CREATE TABLE generations (
    asset_id       uuid PRIMARY KEY REFERENCES assets ON DELETE CASCADE,
    kind           text NOT NULL CHECK (kind IN ('generated', 'edited')),
    source         text NOT NULL CHECK (source IN ('comfyui', 'a1111', 'midjourney', 'grok', 'aigc', 'c2pa', 'iptc')),
    evidence       text NOT NULL, -- where it was found, e.g. png:prompt, exif:usercomment, mp4:comment, c2pa
    tool           text,          -- ComfyUI, Forge, Midjourney, Google AI, ChatGPT, Grok Imagine…
    tool_version   text,
    model_id       uuid REFERENCES ai_models,
    prompt_id      uuid REFERENCES prompts,
    negative       text,
    seed           text,          -- text: ComfyUI seeds go past 2^63
    sampler        text,
    scheduler      text,
    steps          integer,
    cfg            real,
    gen_width      integer,
    gen_height     integer,
    settings       jsonb NOT NULL DEFAULT '{}', -- everything else that was read
    graph          text,          -- ComfyUI's API graph ("prompt"), exactly as found
    workflow       text,          -- ComfyUI's editor workflow, exactly as found
    needs_file     boolean NOT NULL DEFAULT false, -- details were cut short in the index: read the file again
    parser_version smallint NOT NULL,
    updated_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX generations_model ON generations (model_id);
CREATE INDEX generations_prompt ON generations (prompt_id);
CREATE INDEX generations_needs_file ON generations (asset_id) WHERE needs_file;

CREATE TABLE generation_loras (
    asset_id uuid NOT NULL REFERENCES generations ON DELETE CASCADE,
    lora_id  uuid NOT NULL REFERENCES ai_models,
    weight   real,
    PRIMARY KEY (asset_id, lora_id)
);
CREATE INDEX generation_loras_lora ON generation_loras (lora_id);

-- +goose Down
DROP TABLE generation_loras;
DROP TABLE generations;
DROP TABLE prompts;
DROP TABLE ai_models;
ALTER TABLE assets DROP COLUMN provenance_needs_file;
ALTER TABLE assets DROP COLUMN provenance_version;
