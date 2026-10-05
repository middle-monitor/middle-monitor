-- Alert on a custom metric series instead of one of the built-in signals.
-- NULL = a built-in rule, which keeps reading service_results as before: the
-- evaluator only takes the OpenSearch path when this column is set.
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS custom_metric VARCHAR(255);

-- Label equality constraints for that metric, as a JSON array of {key,value}.
-- Empty array = the metric across every label combination.
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS custom_labels JSONB NOT NULL DEFAULT '[]'::jsonb;
