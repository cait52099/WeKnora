CREATE SCHEMA domain_engine;

CREATE TABLE domain_engine.profile_revisions (
    tenant_id BIGINT NOT NULL,
    profile_id TEXT NOT NULL,
    revision TEXT NOT NULL,
    sha256 TEXT NOT NULL CHECK (length(sha256) = 64),
    manifest BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, profile_id, revision)
);

CREATE TABLE domain_engine.kb_bindings (
    tenant_id BIGINT NOT NULL,
    kb_id TEXT NOT NULL,
    version BIGINT NOT NULL CHECK (version > 0),
    profile_id TEXT NOT NULL,
    revision TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    PRIMARY KEY (tenant_id, kb_id),
    FOREIGN KEY (tenant_id, profile_id, revision)
        REFERENCES domain_engine.profile_revisions (tenant_id, profile_id, revision)
);

CREATE TABLE domain_engine.originals (
    tenant_id BIGINT NOT NULL,
    kb_id TEXT NOT NULL,
    document_id TEXT NOT NULL,
    sha256 TEXT NOT NULL CHECK (length(sha256) = 64),
    content BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, kb_id, document_id, sha256)
);

CREATE TABLE domain_engine.parse_snapshots (
    tenant_id BIGINT NOT NULL,
    kb_id TEXT NOT NULL,
    document_id TEXT NOT NULL,
    parse_attempt INTEGER NOT NULL CHECK (parse_attempt > 0),
    binding_version BIGINT NOT NULL CHECK (binding_version > 0),
    profile_id TEXT NOT NULL,
    profile_revision TEXT NOT NULL,
    source_sha256 TEXT NOT NULL,
    snapshot_sha256 TEXT NOT NULL CHECK (length(snapshot_sha256) = 64),
    parser_config_sha256 TEXT NOT NULL CHECK (length(parser_config_sha256) = 64),
    parsed_markdown TEXT NOT NULL,
    normalized_markdown TEXT NOT NULL,
    source_map JSON NOT NULL,
    coverage JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, kb_id, document_id, parse_attempt, binding_version),
    FOREIGN KEY (tenant_id, profile_id, profile_revision)
        REFERENCES domain_engine.profile_revisions (tenant_id, profile_id, revision),
    FOREIGN KEY (tenant_id, kb_id, document_id, source_sha256)
        REFERENCES domain_engine.originals (tenant_id, kb_id, document_id, sha256)
        ON DELETE CASCADE
);
