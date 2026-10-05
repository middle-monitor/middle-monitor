-- The self-monitoring checks are seeded at every API boot with a "SELECT EXISTS
-- then INSERT", which is not atomic: two instances booting at once (a Swarm
-- rolling update runs the new task before stopping the old one) both see the
-- check missing and both create it. Production ended up with every seeded check
-- duplicated, and the status page listed each component twice.

-- Keep the oldest row per (organization_id, name) — it carries the longest
-- history — and drop the copies. Scoped to the names the product seeds itself,
-- so no operator-created check is ever deleted here.
DELETE FROM services s
 WHERE s.name IN ('self-frontend', 'self-cert-frontend', 'self-api', 'self-receiver-db', 'self-cert-api')
   AND EXISTS (
     SELECT 1 FROM services keep
      WHERE keep.organization_id = s.organization_id
        AND keep.name = s.name
        AND keep.id < s.id
   );

-- Make the race impossible for those names rather than relying on the check
-- above the insert. Partial on purpose: operators are free to name two of their
-- own checks alike, and this migration must not start refusing that.
CREATE UNIQUE INDEX IF NOT EXISTS idx_services_self_monitor_name
    ON services(organization_id, name)
 WHERE name IN ('self-frontend', 'self-cert-frontend', 'self-api', 'self-receiver-db', 'self-cert-api');
