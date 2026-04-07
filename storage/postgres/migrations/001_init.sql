CREATE SCHEMA IF NOT EXISTS director;

CREATE TABLE IF NOT EXISTS director.config_snapshots (
    collection_name TEXT        NOT NULL,
    version         TEXT        NOT NULL,
    content         JSONB       NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'pending',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (collection_name, version)
);

CREATE INDEX IF NOT EXISTS idx_config_snapshots_active
    ON director.config_snapshots (collection_name) WHERE status = 'active';

CREATE TABLE IF NOT EXISTS director.config_apply_log (
    instance_id     TEXT        NOT NULL,
    collection_name TEXT        NOT NULL,
    version         TEXT        NOT NULL,
    status          TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (instance_id, collection_name, version)
);

CREATE TABLE IF NOT EXISTS director.config_instances (
    instance_id     TEXT        PRIMARY KEY,
    service_name    TEXT        NOT NULL,
    last_heartbeat  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
