-- Adding a scrape target used to mean editing a file on the machine. This holds
-- the fragment the agent fetches for itself, so an operator can add a target
-- from the platform without touching the server.
--
-- It is a YAML fragment (a list of scrape targets), the same shape the agent
-- already reads from scrape.d, so there is one format to learn and one parser
-- to trust.
ALTER TABLE hosts ADD COLUMN IF NOT EXISTS agent_scrape_config TEXT;
ALTER TABLE hosts ADD COLUMN IF NOT EXISTS agent_config_updated_at TIMESTAMPTZ;
