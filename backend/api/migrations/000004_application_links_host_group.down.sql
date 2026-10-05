ALTER TABLE application_links DROP CONSTRAINT IF EXISTS application_links_target_type_check;
ALTER TABLE application_links ADD CONSTRAINT application_links_target_type_check
    CHECK (target_type IN ('host', 'service'));
