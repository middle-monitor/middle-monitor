-- host_group has been a valid application_links.target_type at the Go and
-- correlation-engine level since host groups were introduced, but the DB
-- check constraint was never updated to match: every host_group link insert
-- has been silently failing.
ALTER TABLE application_links DROP CONSTRAINT IF EXISTS application_links_target_type_check;
ALTER TABLE application_links ADD CONSTRAINT application_links_target_type_check
    CHECK (target_type IN ('host', 'service', 'host_group'));
