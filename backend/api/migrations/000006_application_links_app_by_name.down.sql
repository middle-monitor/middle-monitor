DROP INDEX IF EXISTS idx_application_links_unique;
ALTER TABLE application_links ADD CONSTRAINT application_links_organization_id_app_service_name_app_envi_key
    UNIQUE (organization_id, app_service_name, app_environment, target_type, target_id);
ALTER TABLE application_links DROP COLUMN IF EXISTS target_app_name;
