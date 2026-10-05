-- Public status page: Middle Monitor publishes its own availability at /status,
-- fed by the checks it already runs against itself (see api/self_monitor.go).
--
-- The self-monitoring org also holds internal checks and SDK error services, so
-- exposure is opt-in per service and defaults to false: nothing an org monitors
-- becomes public on deploy.
ALTER TABLE services ADD COLUMN IF NOT EXISTS public BOOLEAN NOT NULL DEFAULT false;

-- The page reads only public services, so a partial index keeps it off the much
-- larger full services scan.
CREATE INDEX IF NOT EXISTS idx_services_public ON services(organization_id) WHERE public;

-- One-shot backfill for the seeded self-monitoring checks: they probe endpoints
-- that are already public (the dashboard, the API, the ingestion pipeline), so
-- they are what the page is meant to show. The certificate checks stay out: an
-- expiry date is an operational concern we alert on, not something a visitor
-- asking "is it up?" needs. New installs get the flag at creation time; after
-- this migration the flag is the operator's to change.
UPDATE services SET public = true
 WHERE name IN ('self-frontend', 'self-api', 'self-receiver-db');

-- Those checks predate the status page and were seeded without a label, so the
-- page would show their technical names. Only fill in the blanks: an operator
-- who already renamed one keeps their wording.
UPDATE services SET display_name = v.label
  FROM (VALUES
    ('self-frontend', 'Dashboard'),
    ('self-cert-frontend', 'Dashboard TLS certificate'),
    ('self-api', 'API'),
    ('self-receiver-db', 'Ingestion pipeline'),
    ('self-cert-api', 'API TLS certificate')
  ) AS v(name, label)
 WHERE services.name = v.name AND COALESCE(services.display_name, '') = '';
