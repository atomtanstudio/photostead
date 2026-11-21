-- +goose Up
-- Workers and search by meaning (milestone 2). Workers pull tasks over /api/worker/v1 with a
-- revocable token, fetch only the renditions of tasks they hold, and post results back.
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE workers (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    name         text NOT NULL UNIQUE,
    token_hash   bytea NOT NULL UNIQUE, -- SHA-256 of the token; the token itself is shown once
    builtin      boolean NOT NULL DEFAULT false,
    capabilities jsonb NOT NULL DEFAULT '{}',
    version      text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz,
    revoked_at   timestamptz
);

-- Work for workers. A finished task is deleted; a failed one is retried twice, then kept as failed.
CREATE TABLE worker_tasks (
    id               bigserial PRIMARY KEY,
    kind             text NOT NULL CHECK (kind IN ('embed_image')),
    asset_id         uuid NOT NULL REFERENCES assets ON DELETE CASCADE,
    model            text NOT NULL,
    priority         smallint NOT NULL DEFAULT 0, -- new items ahead of the backlog
    state            text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'leased', 'failed')),
    lease_owner      uuid REFERENCES workers ON DELETE SET NULL,
    lease_expires_at timestamptz,
    attempts         smallint NOT NULL DEFAULT 0,
    error            text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (kind, asset_id, model)
);
CREATE INDEX worker_tasks_ready ON worker_tasks (kind, model, priority DESC, id) WHERE state IN ('queued', 'leased');

-- Search embeddings, one row per item and model, so a better model can be added beside the old
-- one and take over when it's done. Each model has its own index at its own size.
CREATE TABLE asset_embeddings (
    asset_id   uuid NOT NULL REFERENCES assets ON DELETE CASCADE,
    model      text NOT NULL,
    embedding  halfvec NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (asset_id, model)
);
CREATE INDEX asset_embeddings_siglip2_base ON asset_embeddings
    USING hnsw ((embedding::halfvec(768)) halfvec_cosine_ops) WHERE model = 'siglip2-base-patch16-256';

-- +goose Down
DROP TABLE asset_embeddings;
DROP TABLE worker_tasks;
DROP TABLE workers;
