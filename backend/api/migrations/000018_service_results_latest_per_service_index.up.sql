-- "The last n results of this service" is served today by the global timestamp
-- index: PostgreSQL walks it from the head and stops as soon as it has found n
-- rows for the service. That only works while every service writes at the same
-- cadence. The day a service stops emitting, its rows sink away from the head
-- and the same plan reads and discards everything written since — 188 431 rows
-- and 66 ms after three days of silence in production, degrading further until
-- retention finally drops them. Two services are already in that state.
--
-- The column order and the DESC are what makes the index answer the query:
-- (timestamp, service_id) or an ASC timestamp would not let the scan start at
-- the service's newest row and stop after n.
CREATE INDEX IF NOT EXISTS idx_service_results_service_id_timestamp
    ON service_results(service_id, timestamp DESC);

-- getHostStatus filters services by host_id and only organization_id and the
-- public flag were indexed. Hygiene, not a measured gain at today's volume.
CREATE INDEX IF NOT EXISTS idx_services_host_id ON services(host_id);
