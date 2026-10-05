-- acknowledged_by is a user id, but an automated remediation is not a user: a
-- machine acknowledging an incident had nowhere to say so.
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS acknowledged_actor TEXT;
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS acknowledgement_note TEXT;
