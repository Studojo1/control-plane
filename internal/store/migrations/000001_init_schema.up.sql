CREATE SCHEMA IF NOT EXISTS cp;

-- jobs first (no FK to idempotency_keys yet)
CREATE TABLE cp.jobs (
    id UUID PRIMARY KEY,
    user_id TEXT NOT NULL,
    type TEXT NOT NULL,
    status TEXT NOT NULL,
    idempotency_key_id UUID,
    payload JSONB,
    result JSONB,
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_jobs_user_created ON cp.jobs (user_id, created_at DESC);
CREATE INDEX idx_jobs_type_status ON cp.jobs (type, status);
CREATE INDEX idx_jobs_idempotency_key ON cp.jobs (idempotency_key_id) WHERE idempotency_key_id IS NOT NULL;

CREATE TABLE cp.job_state_transitions (
    id UUID PRIMARY KEY,
    job_id UUID NOT NULL REFERENCES cp.jobs(id) ON DELETE CASCADE,
    from_state TEXT NOT NULL,
    to_state TEXT NOT NULL,
    at TIMESTAMPTZ NOT NULL,
    metadata JSONB
);

CREATE INDEX idx_transitions_job_at ON cp.job_state_transitions (job_id, at);

CREATE TABLE cp.idempotency_keys (
    id UUID PRIMARY KEY,
    key TEXT NOT NULL,
    job_id UUID NOT NULL REFERENCES cp.jobs(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX idx_idempotency_key ON cp.idempotency_keys (key);
CREATE INDEX idx_idempotency_key_user ON cp.idempotency_keys (key, user_id);

ALTER TABLE cp.jobs
    ADD CONSTRAINT fk_jobs_idempotency_key
    FOREIGN KEY (idempotency_key_id) REFERENCES cp.idempotency_keys(id);
