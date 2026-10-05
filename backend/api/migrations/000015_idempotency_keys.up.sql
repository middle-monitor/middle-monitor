-- Services, alert rules and notification channels have no natural key, so a
-- converging tool replaying a creation used to create a second row with no way
-- to tell. An Idempotency-Key lets the caller name the request instead, and get
-- the first answer back rather than a duplicate.
CREATE TABLE IF NOT EXISTS idempotency_keys (
    id              BIGSERIAL PRIMARY KEY,
    organization_id BIGINT      NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    key             TEXT        NOT NULL,
    method          TEXT        NOT NULL,
    path            TEXT        NOT NULL,
    status_code     INTEGER,
    response_body   TEXT,
    completed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The key is scoped to the organization and to the endpoint: the same key on a
-- different route is a different request, not a replay of this one.
CREATE UNIQUE INDEX IF NOT EXISTS idx_idempotency_keys_scope
    ON idempotency_keys(organization_id, key, method, path);

-- Keys are swept after a day; the index is what makes that cheap.
CREATE INDEX IF NOT EXISTS idx_idempotency_keys_created
    ON idempotency_keys(created_at);
