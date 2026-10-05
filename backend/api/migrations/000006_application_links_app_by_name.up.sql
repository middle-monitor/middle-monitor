-- App->app correlation links become name-based, like the source side already
-- is (app_service_name): an application is identified by the service tag its
-- SDK reports, whether or not it was ever registered as an error service row.
-- target_id stays 0 for app links; legacy id-based app links are migrated.
ALTER TABLE application_links ADD COLUMN IF NOT EXISTS target_app_name VARCHAR(255);
UPDATE application_links al SET target_app_name = s.name
    FROM services s
    WHERE al.target_type = 'app' AND al.target_id = s.id AND al.target_app_name IS NULL;

-- The old unique constraint keyed on target_id, which is now always 0 for app
-- links: replace it with one that includes the target app name. It also drops
-- app_environment from the key (the per-environment scope is being removed
-- product-wide and the column is already nullable).
ALTER TABLE application_links DROP CONSTRAINT IF EXISTS application_links_organization_id_app_service_name_app_envi_key;
CREATE UNIQUE INDEX IF NOT EXISTS idx_application_links_unique
    ON application_links(organization_id, app_service_name, target_type, target_id, COALESCE(target_app_name, ''));
