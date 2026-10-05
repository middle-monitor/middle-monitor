-- Allow linking an application directly to another application (e.g. "checkout
-- calls payment"), distinct from linking to a monitored host/service/host_group.
ALTER TABLE application_links DROP CONSTRAINT IF EXISTS application_links_target_type_check;
ALTER TABLE application_links ADD CONSTRAINT application_links_target_type_check
    CHECK (target_type IN ('host', 'service', 'host_group', 'app'));
