-- The in-process retry is fast (30s, 2min, 8min). A process that dies inside
-- that window takes the pending retries with it, and the delivery is lost with
-- nothing left to say it should be re-sent. last_attempt_at is what lets a
-- sweeper recognise a delivery nobody is retrying any more.
ALTER TABLE webhook_deliveries ADD COLUMN IF NOT EXISTS last_attempt_at TIMESTAMPTZ;
UPDATE webhook_deliveries SET last_attempt_at = created_at WHERE last_attempt_at IS NULL;

-- The sweeper reads the unfinished ones, oldest first.
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_unfinished
    ON webhook_deliveries(last_attempt_at)
 WHERE completed_at IS NULL;
