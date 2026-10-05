-- Pro trial: a self-serve signup gets the Pro feature set for a fixed window
-- without a card. The plan column stays 'free' (it is the billing truth, written
-- only by Stripe); the effective plan is derived at read time from this deadline.
-- NULL = no trial (orgs created before this migration, seeded orgs, paying orgs).
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS trial_ends_at TIMESTAMP;

-- Custom-plan data retention, derived from the Stripe subscription's purchased
-- retention tier (60/90/365). NULL = not a custom plan (use the plan's default).
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS custom_retention_days INT;
