-- Zone collectors: agents in collector mode that run the "command" checks of a
-- zone. One row per machine and zone, refreshed on every sync; the zones an
-- organization has are the zones its collectors announce.
CREATE TABLE IF NOT EXISTS collectors (
    id BIGSERIAL PRIMARY KEY,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    zone VARCHAR(255) NOT NULL,
    hostname VARCHAR(255) NOT NULL,
    version VARCHAR(64),
    -- Command names declared in the collector's command files.
    commands JSONB NOT NULL DEFAULT '[]',
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (organization_id, zone, hostname)
);

-- Current state of each command check, updated in place. A command check does
-- not write service_results: at tens of thousands of checks every 30 seconds
-- that table would take a billion rows a month. Its history goes to the series
-- indices instead.
CREATE TABLE IF NOT EXISTS command_check_states (
    service_id INTEGER PRIMARY KEY REFERENCES services(id) ON DELETE CASCADE,
    state VARCHAR(16) NOT NULL,
    -- soft until the check failed max_attempts times in a row, as in Icinga.
    state_type VARCHAR(8) NOT NULL,
    attempt INTEGER NOT NULL,
    hard_state VARCHAR(16) NOT NULL,
    exit_code INTEGER NOT NULL,
    output TEXT NOT NULL,
    long_output TEXT,
    perfdata JSONB,
    duration_ms DOUBLE PRECISION,
    executed_at TIMESTAMPTZ NOT NULL,
    last_state_change TIMESTAMPTZ NOT NULL,
    last_hard_state_change TIMESTAMPTZ NOT NULL,
    collector VARCHAR(255)
);

-- A collector asks for the checks of its zone on every sync. The zone lives in
-- the check's config (services.credentials, as for dns and heartbeat checks),
-- so the lookup needs an expression index to avoid parsing every row. The CASE
-- is part of the expression on purpose: PostgreSQL does not promise to test
-- type first, and other types store non-JSON credentials the cast would fail
-- on. Queries must repeat this exact expression to use the index.
CREATE INDEX IF NOT EXISTS idx_services_command_zone
    ON services (organization_id, (CASE WHEN type = 'command' THEN (credentials::jsonb)->>'zone' END))
    WHERE type = 'command';
