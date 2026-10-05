-- Baseline migration: full schema as previously built inline by RunMigrations.
-- Every statement is idempotent (IF NOT EXISTS / guarded DO blocks) so this
-- file applies cleanly both on a fresh database and on a database that was
-- created by the legacy inline migrations (which golang-migrate then stamps
-- as version 1).

-- Organizations
CREATE TABLE IF NOT EXISTS organizations (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    slug VARCHAR(255) NOT NULL UNIQUE,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_organizations_slug ON organizations(slug);

-- Users
CREATE TABLE IF NOT EXISTS users (
    id SERIAL PRIMARY KEY,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    role VARCHAR(50) NOT NULL DEFAULT 'member',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    last_login_at TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
CREATE INDEX IF NOT EXISTS idx_users_organization_id ON users(organization_id);

-- Email verification: self-serve registrations must confirm their email
-- before accessing the dashboard. Opaque random token (not a JWT) so a
-- verification link can never be replayed as a bearer token.
-- Column kept NULLABLE on purpose: a NOT NULL DEFAULT false would lock out
-- every pre-existing account. The backfill below verifies all rows that
-- predate this migration (NULL); new registrations INSERT false explicitly.
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified BOOLEAN;
ALTER TABLE users ADD COLUMN IF NOT EXISTS verification_token VARCHAR(64);
ALTER TABLE users ADD COLUMN IF NOT EXISTS verification_sent_at TIMESTAMP;
UPDATE users SET email_verified = true WHERE email_verified IS NULL;
CREATE INDEX IF NOT EXISTS idx_users_verification_token ON users(verification_token);

-- Per-user roles: read_only, read_write, admin. Migrate the legacy 'member'
-- role to 'read_write' and move the column default accordingly.
UPDATE users SET role = 'read_write' WHERE role = 'member';
ALTER TABLE users ALTER COLUMN role SET DEFAULT 'read_write';

-- User invitations: opaque single-use invite_token (separate from
-- verification_token so an invite link can never be consumed by the
-- email-verification flow, which wouldn't set a password).
ALTER TABLE users ADD COLUMN IF NOT EXISTS invite_token VARCHAR(64);
ALTER TABLE users ADD COLUMN IF NOT EXISTS invited_at TIMESTAMP;
CREATE INDEX IF NOT EXISTS idx_users_invite_token ON users(invite_token);

-- Memberships: one human identity can belong to several organizations, each
-- with its own role. users.organization_id is kept as the identity's
-- home/default org for back-compat. Backfill one membership per existing user.
CREATE TABLE IF NOT EXISTS memberships (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    role VARCHAR(50) NOT NULL DEFAULT 'read_write',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, organization_id)
);
CREATE INDEX IF NOT EXISTS idx_memberships_user_id ON memberships(user_id);
CREATE INDEX IF NOT EXISTS idx_memberships_organization_id ON memberships(organization_id);
INSERT INTO memberships (user_id, organization_id, role, created_at)
    SELECT id, organization_id, role, created_at FROM users
    ON CONFLICT (user_id, organization_id) DO NOTHING;

-- Password reset: opaque single-use reset_token (separate from
-- verification/invite tokens so a reset link can only set a new password).
ALTER TABLE users ADD COLUMN IF NOT EXISTS reset_token VARCHAR(64);
ALTER TABLE users ADD COLUMN IF NOT EXISTS reset_sent_at TIMESTAMP;
CREATE INDEX IF NOT EXISTS idx_users_reset_token ON users(reset_token);

-- Two-factor authentication (TOTP, RFC 6238). totp_secret may exist while
-- totp_enabled is still false (pending enrollment).
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_secret VARCHAR(255);
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_enabled BOOLEAN NOT NULL DEFAULT false;

-- Single-use recovery codes shown once at enrollment, stored bcrypt-hashed.
CREATE TABLE IF NOT EXISTS user_recovery_codes (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash VARCHAR(255) NOT NULL,
    used_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_user_recovery_codes_user_id ON user_recovery_codes(user_id);

-- Default organization for existing data migration
INSERT INTO organizations (id, name, slug, created_at, updated_at)
VALUES (1, 'Default Organization', 'default', NOW(), NOW())
ON CONFLICT (slug) DO NOTHING;

-- The explicit id above does not consume the sequence: without this setval the
-- next INSERT would take nextval = 1 and hit a primary-key conflict.
SELECT setval('organizations_id_seq', GREATEST((SELECT COALESCE(MAX(id), 1) FROM organizations), 1), true);

-- Dedicated internal org: the dashboard reports its OWN UI/API errors here
-- (see handleFrontendError). Slug is referenced as internalOrgSlug in code.
INSERT INTO organizations (name, slug, created_at, updated_at)
VALUES ('Middle Monitor (internal)', 'middle-monitor', NOW(), NOW())
ON CONFLICT (slug) DO NOTHING;

-- Plan: free | pro (free = limits enforced)
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS plan VARCHAR(50) NOT NULL DEFAULT 'free';

-- When true, every member of the org must enroll TOTP before the dashboard
-- unlocks (enforced by RequireMFAEnrollment middleware + the frontend gate).
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS mfa_required BOOLEAN NOT NULL DEFAULT false;

-- Application errors
CREATE TABLE IF NOT EXISTS application_errors (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    message TEXT NOT NULL,
    file VARCHAR(500) NOT NULL,
    line INTEGER NOT NULL,
    timestamp TIMESTAMP NOT NULL DEFAULT NOW(),
    environment VARCHAR(50) NOT NULL,
    service VARCHAR(255) NOT NULL
);
ALTER TABLE application_errors ADD COLUMN IF NOT EXISTS http_method VARCHAR(10);
ALTER TABLE application_errors ADD COLUMN IF NOT EXISTS http_url TEXT;
ALTER TABLE application_errors ADD COLUMN IF NOT EXISTS http_headers TEXT;
ALTER TABLE application_errors ADD COLUMN IF NOT EXISTS http_body TEXT;
-- Distributed trace correlation: links an error to its OpenTelemetry trace.
ALTER TABLE application_errors ADD COLUMN IF NOT EXISTS trace_id VARCHAR(64);
-- Stable grouping hash (normalized name+message+file) for recurrence detection.
ALTER TABLE application_errors ADD COLUMN IF NOT EXISTS fingerprint VARCHAR(64);
CREATE INDEX IF NOT EXISTS idx_errors_trace_id ON application_errors(trace_id);
CREATE INDEX IF NOT EXISTS idx_errors_timestamp ON application_errors(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_errors_service ON application_errors(service);
CREATE INDEX IF NOT EXISTS idx_errors_environment ON application_errors(environment);
ALTER TABLE application_errors ADD COLUMN IF NOT EXISTS organization_id INTEGER REFERENCES organizations(id) ON DELETE CASCADE;
UPDATE application_errors SET organization_id = 1 WHERE organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_errors_organization_id ON application_errors(organization_id);
CREATE INDEX IF NOT EXISTS idx_errors_fingerprint ON application_errors(organization_id, fingerprint);

-- System metrics
CREATE TABLE IF NOT EXISTS system_metrics (
    id SERIAL PRIMARY KEY,
    service VARCHAR(255) NOT NULL,
    environment VARCHAR(50) NOT NULL,
    cpu_perc DECIMAL(5,2) NOT NULL,
    ram_perc DECIMAL(5,2) NOT NULL,
    http_latency DECIMAL(10,2),
    endpoint VARCHAR(500),
    timestamp TIMESTAMP NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_metrics_timestamp ON system_metrics(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_metrics_service ON system_metrics(service);
ALTER TABLE system_metrics ADD COLUMN IF NOT EXISTS organization_id INTEGER REFERENCES organizations(id) ON DELETE CASCADE;
UPDATE system_metrics SET organization_id = 1 WHERE organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_metrics_organization_id ON system_metrics(organization_id);

-- Migration: rename targets to hosts
CREATE TABLE IF NOT EXISTS hosts (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    host VARCHAR(500) NOT NULL,
    service VARCHAR(255) NOT NULL,
    environment VARCHAR(50) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'targets') THEN
        INSERT INTO hosts (id, name, host, service, environment, created_at)
        SELECT id, name, host, service, environment, created_at FROM targets
        WHERE NOT EXISTS (SELECT 1 FROM hosts WHERE hosts.id = targets.id);
    END IF;
END $$;
SELECT setval('hosts_id_seq', COALESCE((SELECT MAX(id) FROM hosts), 1), true);
DROP TABLE IF EXISTS targets CASCADE;
CREATE INDEX IF NOT EXISTS idx_hosts_host ON hosts(host);
CREATE INDEX IF NOT EXISTS idx_hosts_service ON hosts(service);
ALTER TABLE hosts ADD COLUMN IF NOT EXISTS organization_id INTEGER REFERENCES organizations(id) ON DELETE CASCADE;
UPDATE hosts SET organization_id = 1 WHERE organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_hosts_organization_id ON hosts(organization_id);
-- Host name unique per organization (not globally)
CREATE UNIQUE INDEX IF NOT EXISTS idx_hosts_org_name ON hosts(organization_id, name);
-- Status derived from latest service_results; kept in sync by trigger below.
ALTER TABLE hosts ADD COLUMN IF NOT EXISTS status VARCHAR(50) NOT NULL DEFAULT 'unknown';
CREATE INDEX IF NOT EXISTS idx_hosts_status ON hosts(status);
-- Display name (editable in UI; name stays the technical identifier)
ALTER TABLE hosts ADD COLUMN IF NOT EXISTS display_name VARCHAR(255);

-- Host groups: group hosts that share the same logical role. Correlation is
-- scoped to a host + its group, never across the whole environment.
CREATE TABLE IF NOT EXISTS host_groups (
    id SERIAL PRIMARY KEY,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_host_groups_org ON host_groups(organization_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_host_groups_org_name ON host_groups(organization_id, name);
-- At most one default group per organization.
CREATE UNIQUE INDEX IF NOT EXISTS idx_host_groups_one_default ON host_groups(organization_id) WHERE is_default;
ALTER TABLE hosts ADD COLUMN IF NOT EXISTS host_group_id INTEGER REFERENCES host_groups(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_hosts_host_group_id ON hosts(host_group_id);
-- Every organization gets a default group; backfill for existing orgs.
INSERT INTO host_groups (organization_id, name, is_default)
    SELECT id, 'Default', true FROM organizations o
    WHERE NOT EXISTS (SELECT 1 FROM host_groups hg WHERE hg.organization_id = o.id AND hg.is_default);
UPDATE hosts SET host_group_id = (
    SELECT hg.id FROM host_groups hg WHERE hg.organization_id = hosts.organization_id AND hg.is_default LIMIT 1
) WHERE host_group_id IS NULL;

-- Migration: rename checks to services
CREATE TABLE IF NOT EXISTS services (
    id SERIAL PRIMARY KEY,
    host_id INTEGER REFERENCES hosts(id) ON DELETE SET NULL,
    name VARCHAR(255) NOT NULL,
    type VARCHAR(50) NOT NULL,
    host VARCHAR(500) NOT NULL,
    path VARCHAR(500),
    credentials TEXT,
    service VARCHAR(255) NOT NULL,
    environment VARCHAR(50) NOT NULL,
    service_interval INTEGER NOT NULL DEFAULT 60,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    token VARCHAR(255) UNIQUE,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);
ALTER TABLE services ADD COLUMN IF NOT EXISTS token VARCHAR(255) UNIQUE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'checks') THEN
        INSERT INTO services (id, host_id, name, type, host, path, credentials, service, environment, service_interval, max_attempts, created_at)
        SELECT c.id, c.target_id, c.name, c.type, c.host, c.path, c.credentials, c.service, c.environment, c.check_interval, c.max_attempts, c.created_at
        FROM checks c
        WHERE NOT EXISTS (SELECT 1 FROM services WHERE services.id = c.id);
    END IF;
END $$;
SELECT setval('services_id_seq', COALESCE((SELECT MAX(id) FROM services), 1), true);
ALTER TABLE services ADD COLUMN IF NOT EXISTS host_id INTEGER REFERENCES hosts(id) ON DELETE SET NULL;
ALTER TABLE services ADD COLUMN IF NOT EXISTS service_interval INTEGER NOT NULL DEFAULT 60;
ALTER TABLE services ADD COLUMN IF NOT EXISTS max_attempts INTEGER NOT NULL DEFAULT 3;
ALTER TABLE services ADD COLUMN IF NOT EXISTS organization_id INTEGER REFERENCES organizations(id) ON DELETE CASCADE;
UPDATE services SET organization_id = 1 WHERE organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_services_organization_id ON services(organization_id);
-- Display name (editable in UI; name stays the technical identifier)
ALTER TABLE services ADD COLUMN IF NOT EXISTS display_name VARCHAR(255);

-- Install tokens: link agent install to organization (user generates token,
-- agent uses it for registration)
CREATE TABLE IF NOT EXISTS install_tokens (
    id SERIAL PRIMARY KEY,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    token VARCHAR(64) NOT NULL UNIQUE,
    name VARCHAR(255),
    created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_install_tokens_token ON install_tokens(token);
CREATE INDEX IF NOT EXISTS idx_install_tokens_organization_id ON install_tokens(organization_id);
DROP TABLE IF EXISTS checks CASCADE;

-- Migration: rename check_results to service_results
CREATE TABLE IF NOT EXISTS service_results (
    id SERIAL PRIMARY KEY,
    service_id INTEGER NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    status VARCHAR(50) NOT NULL,
    latency DECIMAL(10,2),
    message TEXT,
    timestamp TIMESTAMP NOT NULL DEFAULT NOW()
);
-- Map check_id to service_id (check IDs were preserved during migration).
-- Result IDs are not preserved to avoid sequence conflicts.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'check_results') THEN
        INSERT INTO service_results (service_id, status, latency, message, timestamp)
        SELECT cr.check_id, cr.status, cr.latency, cr.message, cr.timestamp
        FROM check_results cr
        WHERE EXISTS (SELECT 1 FROM services s WHERE s.id = cr.check_id)
        AND NOT EXISTS (
            SELECT 1 FROM service_results sr
            WHERE sr.service_id = cr.check_id
            AND sr.status = cr.status
            AND ABS(EXTRACT(EPOCH FROM (sr.timestamp - cr.timestamp))) < 1
        );
    END IF;
END $$;
SELECT setval('service_results_id_seq', COALESCE((SELECT MAX(id) FROM service_results), 1), true);
DROP TABLE IF EXISTS check_results CASCADE;
CREATE INDEX IF NOT EXISTS idx_service_results_timestamp ON service_results(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_service_results_service_id ON service_results(service_id);
-- Backfill hosts.status from latest service_results
UPDATE hosts h SET status = COALESCE((
    SELECT CASE
        WHEN bool_or(latest = 'failure') THEN 'failure'
        WHEN bool_or(latest = 'warning') THEN 'warning'
        WHEN count(*) > 0 AND count(*) = count(*) FILTER (WHERE latest = 'success') THEN 'success'
        ELSE 'unknown'
    END
    FROM (
        SELECT (SELECT sr.status FROM service_results sr WHERE sr.service_id = s.id ORDER BY sr.timestamp DESC LIMIT 1) AS latest
        FROM services s
        WHERE s.host_id = h.id
    ) t
), 'unknown')
WHERE EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'hosts' AND column_name = 'status');
-- Trigger to keep hosts.status in sync when service_results change
CREATE OR REPLACE FUNCTION update_host_status_on_result()
RETURNS TRIGGER AS $$
DECLARE
    v_host_id INTEGER;
    v_status VARCHAR(50);
BEGIN
    SELECT host_id INTO v_host_id FROM services WHERE id = NEW.service_id;
    IF v_host_id IS NULL THEN RETURN NEW; END IF;
    SELECT COALESCE((
        SELECT CASE
            WHEN bool_or(latest = 'failure') THEN 'failure'
            WHEN bool_or(latest = 'warning') THEN 'warning'
            WHEN count(*) > 0 AND count(*) = count(*) FILTER (WHERE latest = 'success') THEN 'success'
            ELSE 'unknown'
        END
        FROM (
            SELECT (SELECT sr.status FROM service_results sr WHERE sr.service_id = s.id ORDER BY sr.timestamp DESC LIMIT 1) AS latest
            FROM services s
            WHERE s.host_id = v_host_id
        ) t
    ), 'unknown') INTO v_status;
    UPDATE hosts SET status = v_status WHERE id = v_host_id;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS trg_service_results_update_host_status ON service_results;
CREATE TRIGGER trg_service_results_update_host_status
AFTER INSERT OR UPDATE OF status ON service_results
FOR EACH ROW EXECUTE FUNCTION update_host_status_on_result();
-- Ensure sequences are properly set (run even if migration already happened)
SELECT setval('hosts_id_seq', COALESCE((SELECT MAX(id) FROM hosts), 1), true);
SELECT setval('services_id_seq', COALESCE((SELECT MAX(id) FROM services), 1), true);
SELECT setval('service_results_id_seq', COALESCE((SELECT MAX(id) FROM service_results), 1), true);

-- Migration: merge agent_metrics into service_results
ALTER TABLE service_results ADD COLUMN IF NOT EXISTS metric_type VARCHAR(50);
ALTER TABLE service_results ADD COLUMN IF NOT EXISTS metric_value DECIMAL(10,2);
ALTER TABLE service_results ADD COLUMN IF NOT EXISTS metadata TEXT;
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'service_results'
        AND column_name = 'timestamp'
        AND data_type = 'timestamp without time zone'
    ) THEN
        ALTER TABLE service_results ALTER COLUMN timestamp TYPE TIMESTAMP WITH TIME ZONE USING timestamp AT TIME ZONE 'UTC';
    END IF;
END $$;
-- Guarded: agent_metrics only exists here on legacy databases (it is created
-- further down for fresh ones), in which case its rows are migrated.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'agent_metrics') THEN
        INSERT INTO service_results (service_id, status, message, timestamp, metric_type, metric_value, metadata)
        SELECT
            s.id as service_id,
            CASE
                WHEN am.value > 90.0 THEN 'failure'
                ELSE 'success'
            END as status,
            CASE
                WHEN am.value > 90.0 THEN
                    CASE am.metric_type
                        WHEN 'cpu' THEN 'CPU usage is ' || am.value::text || '%'
                        WHEN 'ram' THEN 'RAM usage is ' || am.value::text || '%'
                        WHEN 'disk' THEN 'Disk usage is ' || am.value::text || '%'
                        ELSE NULL
                    END
                ELSE NULL
            END as message,
            am.timestamp,
            am.metric_type,
            am.value as metric_value,
            am.metadata
        FROM agent_metrics am
        JOIN services s ON s.host = am.hostname
            AND s.service = am.service
            AND s.environment = am.environment
            AND s.type = 'agent_' || am.metric_type
        WHERE NOT EXISTS (
            SELECT 1 FROM service_results sr
            WHERE sr.service_id = s.id
            AND sr.metric_type = am.metric_type
            AND ABS(EXTRACT(EPOCH FROM (sr.timestamp - am.timestamp))) < 1
        );
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS idx_service_results_metric_type ON service_results(metric_type) WHERE metric_type IS NOT NULL;

-- Events
CREATE TABLE IF NOT EXISTS events (
    id SERIAL PRIMARY KEY,
    type VARCHAR(50) NOT NULL,
    service VARCHAR(255) NOT NULL,
    environment VARCHAR(50) NOT NULL,
    message TEXT NOT NULL,
    metadata TEXT,
    timestamp TIMESTAMP NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_events_type ON events(type);

-- Application links: link an app (service name + environment) to hosts or
-- services for correlation
CREATE TABLE IF NOT EXISTS application_links (
    id SERIAL PRIMARY KEY,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    app_service_name VARCHAR(255) NOT NULL,
    app_environment VARCHAR(50) NOT NULL,
    target_type VARCHAR(20) NOT NULL CHECK (target_type IN ('host', 'service')),
    target_id BIGINT NOT NULL,
    UNIQUE(organization_id, app_service_name, app_environment, target_type, target_id)
);
CREATE INDEX IF NOT EXISTS idx_application_links_org_app ON application_links(organization_id, app_service_name, app_environment);
CREATE INDEX IF NOT EXISTS idx_events_service ON events(service);
ALTER TABLE events ADD COLUMN IF NOT EXISTS organization_id INTEGER REFERENCES organizations(id) ON DELETE CASCADE;
UPDATE events SET organization_id = 1 WHERE organization_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_events_organization_id ON events(organization_id);

-- Agent metrics
CREATE TABLE IF NOT EXISTS agent_metrics (
    id SERIAL PRIMARY KEY,
    hostname VARCHAR(500) NOT NULL,
    service VARCHAR(255) NOT NULL,
    environment VARCHAR(50) NOT NULL,
    metric_type VARCHAR(50) NOT NULL,
    value DECIMAL(10,2) NOT NULL,
    metadata TEXT,
    timestamp TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
ALTER TABLE agent_metrics ADD COLUMN IF NOT EXISTS metadata TEXT;
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'agent_metrics'
        AND column_name = 'timestamp'
        AND data_type = 'timestamp without time zone'
    ) THEN
        ALTER TABLE agent_metrics ALTER COLUMN timestamp TYPE TIMESTAMP WITH TIME ZONE USING timestamp AT TIME ZONE 'UTC';
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS idx_agent_metrics_timestamp ON agent_metrics(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_agent_metrics_hostname ON agent_metrics(hostname);
CREATE INDEX IF NOT EXISTS idx_agent_metrics_type ON agent_metrics(metric_type);

-- Alert rules
CREATE TABLE IF NOT EXISTS alert_rules (
    id SERIAL PRIMARY KEY,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    type VARCHAR(50) NOT NULL DEFAULT 'threshold',
    target_type VARCHAR(50) NOT NULL DEFAULT 'service',
    target_id INTEGER,
    metric VARCHAR(100) NOT NULL,
    operator VARCHAR(10) NOT NULL DEFAULT 'gt',
    threshold DECIMAL(10,2) NOT NULL,
    duration INTEGER NOT NULL DEFAULT 60,
    severity VARCHAR(20) NOT NULL DEFAULT 'warning',
    enabled BOOLEAN NOT NULL DEFAULT true,
    channels JSONB DEFAULT '[]'::jsonb,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS channels JSONB DEFAULT '[]'::jsonb;
CREATE INDEX IF NOT EXISTS idx_alert_rules_organization_id ON alert_rules(organization_id);

-- Notification channels
CREATE TABLE IF NOT EXISTS notification_channels (
    id SERIAL PRIMARY KEY,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    type VARCHAR(50) NOT NULL,
    config JSONB NOT NULL DEFAULT '{}'::jsonb,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_notification_channels_org_id ON notification_channels(organization_id);

-- Incidents
CREATE TABLE IF NOT EXISTS incidents (
    id SERIAL PRIMARY KEY,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    alert_rule_id INTEGER REFERENCES alert_rules(id) ON DELETE SET NULL,
    title VARCHAR(500) NOT NULL,
    description TEXT,
    severity VARCHAR(20) NOT NULL DEFAULT 'warning',
    status VARCHAR(20) NOT NULL DEFAULT 'open',
    service VARCHAR(255),
    environment VARCHAR(50),
    started_at TIMESTAMP NOT NULL DEFAULT NOW(),
    resolved_at TIMESTAMP,
    acknowledged_at TIMESTAMP,
    acknowledged_by INTEGER REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_incidents_organization_id ON incidents(organization_id);
CREATE INDEX IF NOT EXISTS idx_incidents_status ON incidents(status);

-- API keys
CREATE TABLE IF NOT EXISTS api_keys (
    id SERIAL PRIMARY KEY,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    key_hash VARCHAR(255) NOT NULL,
    key_prefix VARCHAR(10) NOT NULL,
    scopes TEXT NOT NULL DEFAULT 'read',
    last_used_at TIMESTAMP,
    expires_at TIMESTAMP,
    created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_api_keys_organization_id ON api_keys(organization_id);

-- Personal API tokens: when user_id is set, the key is owned by that user and
-- authenticates AS them (carrying their role). NULL = org token (legacy).
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS user_id INTEGER REFERENCES users(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_api_keys_user_id ON api_keys(user_id);

-- pprof profile captures (SDK uploads, UI lists/downloads)
CREATE TABLE IF NOT EXISTS profile_captures (
    id SERIAL PRIMARY KEY,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    service VARCHAR(255) NOT NULL DEFAULT '',
    environment VARCHAR(50) NOT NULL DEFAULT '',
    profile_type VARCHAR(50) NOT NULL,
    duration_seconds INTEGER,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    data BYTEA NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_profile_captures_organization_id ON profile_captures(organization_id);
CREATE INDEX IF NOT EXISTS idx_profile_captures_created_at ON profile_captures(created_at DESC);
ALTER TABLE profile_captures ADD COLUMN IF NOT EXISTS memory_mb DECIMAL(10,2);

-- Keep services.environment aligned with hosts.environment when host_id is set
UPDATE services s SET environment = h.environment FROM hosts h WHERE s.host_id = h.id AND s.environment IS DISTINCT FROM h.environment;
CREATE OR REPLACE FUNCTION sync_services_environment_from_host()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND (OLD.environment IS DISTINCT FROM NEW.environment) THEN
        UPDATE services SET environment = NEW.environment WHERE host_id = NEW.id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS trg_hosts_sync_service_env ON hosts;
CREATE TRIGGER trg_hosts_sync_service_env
AFTER UPDATE OF environment ON hosts
FOR EACH ROW EXECUTE FUNCTION sync_services_environment_from_host();

-- Explanations cache: root-cause explanations for errors and service_results
CREATE TABLE IF NOT EXISTS explanations (
    id SERIAL PRIMARY KEY,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    subject_type VARCHAR(32) NOT NULL CHECK (subject_type IN ('error', 'service_result')),
    subject_id BIGINT NOT NULL,
    content TEXT NOT NULL,
    model VARCHAR(128),
    duration_ms INTEGER,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (subject_type, subject_id)
);
CREATE INDEX IF NOT EXISTS idx_explanations_org ON explanations(organization_id);
CREATE INDEX IF NOT EXISTS idx_explanations_subject ON explanations(subject_type, subject_id);

-- Stripe subscriptions (one row per organization)
CREATE TABLE IF NOT EXISTS subscriptions (
    id SERIAL PRIMARY KEY,
    organization_id INTEGER NOT NULL UNIQUE REFERENCES organizations(id) ON DELETE CASCADE,
    organization_slug VARCHAR(255) NOT NULL,
    stripe_customer_id VARCHAR(255),
    stripe_subscription_id VARCHAR(255),
    plan VARCHAR(50) NOT NULL DEFAULT 'free',
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    current_period_end TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_subscriptions_stripe_sub ON subscriptions(stripe_subscription_id);

-- Dual alert thresholds on checks (warning + critical). failure_threshold
-- remains for backward compatibility; new checks use the two-level thresholds.
ALTER TABLE services ADD COLUMN IF NOT EXISTS warning_threshold DOUBLE PRECISION;
ALTER TABLE services ADD COLUMN IF NOT EXISTS critical_threshold DOUBLE PRECISION;
-- Guarded: failure_threshold is a legacy column that no migration creates; it
-- only exists on old databases. Referencing it unguarded breaks fresh installs.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'services' AND column_name = 'failure_threshold'
    ) THEN
        UPDATE services SET critical_threshold = failure_threshold
        WHERE critical_threshold IS NULL AND failure_threshold IS NOT NULL;
    END IF;
END $$;

-- HTTP checks: optional exact expected status code (NULL = any 2xx)
ALTER TABLE services ADD COLUMN IF NOT EXISTS expected_status_code INTEGER;

-- HTTP checks: optional response-body assertion (NULL = not checked)
ALTER TABLE services ADD COLUMN IF NOT EXISTS expected_body_contains TEXT;
-- Body assertion mode: 'contains' (substring, default) or 'json_path' (path=value)
ALTER TABLE services ADD COLUMN IF NOT EXISTS expected_body_mode TEXT;

-- Per-org severity opt-in: choose to receive warning / critical alerts
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS alert_warning_enabled BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS alert_critical_enabled BOOLEAN NOT NULL DEFAULT true;

-- Per-org SMTP: email alert channels send through the org's own SMTP server.
-- smtp_pass_enc holds an AES-GCM encrypted blob (JSON); never stored in clear.
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS smtp_host VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS smtp_port VARCHAR(8) NOT NULL DEFAULT '';
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS smtp_user VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS smtp_pass_enc TEXT NOT NULL DEFAULT '';
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS smtp_from VARCHAR(255) NOT NULL DEFAULT '';

-- Custom-plan resource caps, derived from the Stripe subscription's purchased
-- quantities. NULL = not a custom plan (use the plan's static limits).
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS custom_max_hosts INT;
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS custom_max_monitored_services INT;
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS custom_max_error_services INT;

-- Maintenance / downtime windows: suppress alerts for a service or host
CREATE TABLE IF NOT EXISTS maintenance_windows (
    id SERIAL PRIMARY KEY,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    target_type VARCHAR(20) NOT NULL,
    target_id INTEGER NOT NULL,
    starts_at TIMESTAMP WITH TIME ZONE NOT NULL,
    ends_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_maint_org_time ON maintenance_windows(organization_id, starts_at, ends_at);

-- Link incidents to the originating check so service-threshold alerts can be
-- de-duplicated and auto-resolved per check (rule-based ones use alert_rule_id).
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS service_id INTEGER;
-- Link host-scoped alert-rule incidents to their host so the UI can deep-link.
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS host_id INTEGER;

-- Alert policies: percentile/statistic aggregation, two-level thresholds,
-- and recovery (hysteresis). aggregation: avg|min|max|sum|p50|p75|p90|p95|p99
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS aggregation VARCHAR(10) NOT NULL DEFAULT 'avg';
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS warning_threshold DOUBLE PRECISION;
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS critical_threshold DOUBLE PRECISION;
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS recovery_threshold DOUBLE PRECISION;
-- Backfill the two-level thresholds from the legacy single threshold so
-- existing rules keep firing under the new evaluator.
UPDATE alert_rules SET critical_threshold = threshold
   WHERE critical_threshold IS NULL AND severity = 'critical';
UPDATE alert_rules SET warning_threshold = threshold
   WHERE warning_threshold IS NULL AND severity = 'warning';

-- Alert rules as notification ROUTING policies. Thresholds now live on the
-- service; a rule routes a fired severity to channels with optional tags.
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS tags TEXT;
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS notify_warning BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS notify_critical BOOLEAN NOT NULL DEFAULT true;
-- metric is NOT NULL with no default; make routing rules insertable.
ALTER TABLE alert_rules ALTER COLUMN metric SET DEFAULT 'routing';
ALTER TABLE alert_rules ALTER COLUMN threshold SET DEFAULT 0;

-- Custom dashboards: user-built dashboards scoped per organization (shared
-- across the org's members). Widgets are stored as an opaque JSONB blob.
CREATE TABLE IF NOT EXISTS custom_dashboards (
    id SERIAL PRIMARY KEY,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    widgets JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_custom_dashboards_organization_id ON custom_dashboards(organization_id);

-- Environment is being removed from the product. As a first, non-destructive
-- step, drop the NOT NULL constraint everywhere so the UI and SDKs can stop
-- providing it without breaking inserts. The columns themselves are dropped
-- only later, once nothing depends on them.
ALTER TABLE application_errors ALTER COLUMN environment DROP NOT NULL;
ALTER TABLE system_metrics ALTER COLUMN environment DROP NOT NULL;
ALTER TABLE hosts ALTER COLUMN environment DROP NOT NULL;
ALTER TABLE services ALTER COLUMN environment DROP NOT NULL;
ALTER TABLE events ALTER COLUMN environment DROP NOT NULL;
ALTER TABLE agent_metrics ALTER COLUMN environment DROP NOT NULL;
ALTER TABLE application_links ALTER COLUMN app_environment DROP NOT NULL;
