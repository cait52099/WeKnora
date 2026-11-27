ALTER TABLE domain_engine.kb_bindings ADD COLUMN access_epoch bigint NOT NULL DEFAULT 1;

CREATE TABLE domain_engine.jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id bigint NOT NULL,
    kb_id text NOT NULL,
    document_id text NOT NULL,
    source_path text NOT NULL,
    native_hash text NOT NULL,
    profile_id text NOT NULL,
    revision text NOT NULL,
    binding_version bigint NOT NULL,
    access_epoch bigint NOT NULL,
    model_pins jsonb NOT NULL,
    optional_stages jsonb NOT NULL,
    native_payload bytea,
    native_wake_at timestamptz NOT NULL DEFAULT now() + interval '2 minutes',
    native_wakes integer NOT NULL DEFAULT 0,
    status text NOT NULL DEFAULT 'waiting_parse',
    error_code text NOT NULL DEFAULT '',
    stage_index integer NOT NULL DEFAULT 0,
    retries integer NOT NULL DEFAULT 0,
    fence bigint NOT NULL DEFAULT 0,
    parse_attempt integer,
    snapshot_binding_version bigint,
    due_at timestamptz NOT NULL DEFAULT now(),
    wake_at timestamptz NOT NULL DEFAULT now(),
    lease_until timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(tenant_id,kb_id,document_id,source_path,native_hash,binding_version),
    FOREIGN KEY (tenant_id, profile_id, revision)
        REFERENCES domain_engine.profile_revisions (tenant_id, profile_id, revision)
);
CREATE INDEX domain_jobs_dispatch ON domain_engine.jobs (wake_at,due_at)
    WHERE status IN ('queued','retry_wait','running');
CREATE INDEX domain_jobs_scope ON domain_engine.jobs (tenant_id,kb_id,created_at DESC);
CREATE INDEX domain_jobs_waiting ON domain_engine.jobs (created_at)
    WHERE status='waiting_parse';

CREATE TABLE domain_engine.stage_runs (
    job_id uuid NOT NULL REFERENCES domain_engine.jobs(id) ON DELETE CASCADE,
    fence bigint NOT NULL,
    stage_index integer NOT NULL,
    stage_id text NOT NULL,
    raw_response bytea,
    valid_json bytea,
    prompt_tokens integer NOT NULL,
    completion_tokens integer NOT NULL,
    error_code text NOT NULL,
    input_coverage jsonb NOT NULL DEFAULT '{}',
    reused boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(job_id,fence)
);
CREATE INDEX domain_stage_checkpoint ON domain_engine.stage_runs(job_id,stage_index)
    WHERE valid_json IS NOT NULL;

CREATE FUNCTION domain_engine.binding_epoch() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.enabled AND NOT NEW.enabled THEN
        NEW.access_epoch := OLD.access_epoch + 1;
        UPDATE domain_engine.jobs SET status='cancelled', error_code='binding_revoked', fence=fence+1
        WHERE tenant_id=OLD.tenant_id AND kb_id=OLD.kb_id
          AND status IN ('preparing','waiting_parse','capture_failed','queued','running','retry_wait','failed','needs_review');
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER domain_binding_epoch BEFORE UPDATE ON domain_engine.kb_bindings
    FOR EACH ROW EXECUTE FUNCTION domain_engine.binding_epoch();

CREATE FUNCTION domain_engine.purge_native_document() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='UPDATE' THEN
        IF NEW.deleted_at IS NULL AND NEW.parse_status IS DISTINCT FROM 'deleting'
           AND NEW.tenant_id IS NOT DISTINCT FROM OLD.tenant_id
           AND NEW.knowledge_base_id IS NOT DISTINCT FROM OLD.knowledge_base_id
           AND NEW.file_path IS NOT DISTINCT FROM OLD.file_path
           AND NEW.file_hash IS NOT DISTINCT FROM OLD.file_hash THEN
            IF NEW.parse_status = 'cancelled' AND OLD.parse_status IS DISTINCT FROM 'cancelled' THEN
                UPDATE domain_engine.jobs SET status='cancelled', error_code='source_cancelled', fence=fence+1
                WHERE tenant_id=OLD.tenant_id AND kb_id=OLD.knowledge_base_id AND document_id=OLD.id
                  AND status IN ('preparing','waiting_parse','capture_failed','queued','running','retry_wait','failed','needs_review');
            END IF;
            RETURN NEW;
        END IF;
    END IF;
    DELETE FROM domain_engine.jobs
      WHERE tenant_id=OLD.tenant_id AND kb_id=OLD.knowledge_base_id AND document_id=OLD.id;
    DELETE FROM domain_engine.originals
      WHERE tenant_id=OLD.tenant_id AND kb_id=OLD.knowledge_base_id AND document_id=OLD.id;
    RETURN NULL;
END;
$$;
CREATE TRIGGER domain_purge_document AFTER DELETE OR UPDATE OF deleted_at, parse_status, tenant_id, knowledge_base_id, file_path, file_hash
    ON knowledges FOR EACH ROW EXECUTE FUNCTION domain_engine.purge_native_document();

CREATE FUNCTION domain_engine.purge_native_scope() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='UPDATE' THEN
        IF NEW.deleted_at IS NULL THEN RETURN NULL; END IF;
    END IF;
    IF TG_TABLE_NAME='tenants' THEN
        DELETE FROM domain_engine.jobs WHERE tenant_id=OLD.id;
        DELETE FROM domain_engine.originals WHERE tenant_id=OLD.id;
        DELETE FROM domain_engine.kb_bindings WHERE tenant_id=OLD.id;
        DELETE FROM domain_engine.profile_revisions WHERE tenant_id=OLD.id;
    ELSE
        DELETE FROM domain_engine.jobs WHERE tenant_id=OLD.tenant_id AND kb_id=OLD.id;
        DELETE FROM domain_engine.originals WHERE tenant_id=OLD.tenant_id AND kb_id=OLD.id;
        DELETE FROM domain_engine.kb_bindings WHERE tenant_id=OLD.tenant_id AND kb_id=OLD.id;
    END IF;
    RETURN NULL;
END;
$$;
CREATE TRIGGER domain_purge_kb AFTER DELETE OR UPDATE OF deleted_at
    ON knowledge_bases FOR EACH ROW EXECUTE FUNCTION domain_engine.purge_native_scope();
CREATE TRIGGER domain_purge_tenant AFTER DELETE OR UPDATE OF deleted_at
    ON tenants FOR EACH ROW EXECUTE FUNCTION domain_engine.purge_native_scope();
