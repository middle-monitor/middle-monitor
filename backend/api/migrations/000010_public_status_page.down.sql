DROP INDEX IF EXISTS idx_services_public;
ALTER TABLE services DROP COLUMN IF EXISTS public;
