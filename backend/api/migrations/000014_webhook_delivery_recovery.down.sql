DROP INDEX IF EXISTS idx_webhook_deliveries_unfinished;
ALTER TABLE webhook_deliveries DROP COLUMN IF EXISTS last_attempt_at;
