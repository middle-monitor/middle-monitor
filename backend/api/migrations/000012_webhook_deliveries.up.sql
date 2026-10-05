-- Debugging a webhook integration without a delivery log means asking the
-- customer to reproduce the alert. This table records every attempt so the
-- receiver's answer can be read back, and a failed delivery replayed.
CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id              BIGSERIAL PRIMARY KEY,
    organization_id BIGINT      NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    channel_id      BIGINT      NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    event_id        TEXT        NOT NULL,
    event_type      TEXT        NOT NULL,
    dedup_key       TEXT,
    url             TEXT        NOT NULL,
    request_body    TEXT        NOT NULL,
    status_code     INTEGER,
    attempts        INTEGER     NOT NULL DEFAULT 0,
    -- Truncated on write: a receiver answering with a megabyte of HTML must not
    -- fill the table.
    response_body   TEXT,
    error           TEXT,
    succeeded       BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ
);

-- The list endpoint reads the recent deliveries of one channel, newest first.
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_channel
    ON webhook_deliveries(channel_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_org
    ON webhook_deliveries(organization_id, created_at DESC);
