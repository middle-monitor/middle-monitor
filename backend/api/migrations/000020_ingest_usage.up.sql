-- Metric points per organization and minute against the ingestion budget
-- (1 000 points/min per plan host): accepted, and over the limit (rejected
-- only once the budget is enforced). Read by Settings; purged after 7 days.
CREATE TABLE IF NOT EXISTS ingest_usage (
    organization_id BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    minute TIMESTAMPTZ NOT NULL,
    accepted_points INTEGER NOT NULL DEFAULT 0,
    over_limit_points INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (organization_id, minute)
);
