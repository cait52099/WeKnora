-- Phase 2B could leave uncommitted reprocess placeholders behind. They
-- contain no successful stage results. The original job can be resubmitted.
DELETE FROM domain_engine.jobs
 WHERE status='preparing' AND parse_attempt IS NULL
   AND NOT EXISTS (SELECT 1 FROM domain_engine.stage_runs r WHERE r.job_id=jobs.id);

CREATE INDEX domain_jobs_stable_page ON domain_engine.jobs (tenant_id,kb_id,created_at DESC,id DESC);
CREATE TABLE domain_engine.profile_packages (
    tenant_id bigint NOT NULL,
    profile_id text NOT NULL,
    revision text NOT NULL,
    archive bytea NOT NULL,
    sha256 text NOT NULL,
    description jsonb NOT NULL,
    PRIMARY KEY (tenant_id,profile_id,revision),
    FOREIGN KEY (tenant_id,profile_id,revision)
        REFERENCES domain_engine.profile_revisions(tenant_id,profile_id,revision) ON DELETE CASCADE
);
ALTER TABLE domain_engine.stage_runs ADD COLUMN model_calls integer,
    ADD COLUMN reported_calls integer;
