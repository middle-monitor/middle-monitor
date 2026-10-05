-- Opting out has to survive a redeploy and a change of prospecting tool, so the
-- suppression list lives with the data rather than in a file next to a script.
CREATE TABLE IF NOT EXISTS email_suppressions (
    email      TEXT PRIMARY KEY,
    source     TEXT NOT NULL DEFAULT 'unsubscribe_link',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
