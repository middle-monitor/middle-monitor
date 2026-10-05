-- failure_threshold is read, inserted and updated by the Go code
-- (services/service.go, services/agent.go, services/error.go, workers/worker.go)
-- and exposed by the API, the SDKs and the Terraform provider, but no migration
-- ever created it: it only existed on databases old enough to predate the
-- versioned migrations, or where an operator added it by hand. Fresh installs
-- fail every query that touches the services table.
--
-- IF NOT EXISTS keeps this a no-op on databases that already have the column,
-- whatever type it was added with.
ALTER TABLE services ADD COLUMN IF NOT EXISTS failure_threshold DOUBLE PRECISION;

-- Hand-added copies of this column used NUMERIC(10,2). Normalize them to the
-- type the code scans into (*float64) and that warning_threshold /
-- critical_threshold already use, so every database converges on one schema.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'services'
          AND column_name = 'failure_threshold'
          AND data_type <> 'double precision'
    ) THEN
        ALTER TABLE services ALTER COLUMN failure_threshold TYPE DOUBLE PRECISION;
    END IF;
END $$;
