-- service_results is the most written table of the product, and every index on
-- it is paid on every insert. idx_service_results_service_id_timestamp
-- (migration 000018) leads with service_id, so it answers on its own every plan
-- that filtered on service_id alone: the enrichment queries of /services and
-- /hosts switch from one bitmap index scan to the other, same rows, same heap
-- access. Measured on a 1 450 000-row reproduction: /services enrichment loop
-- 686 ms before, 649 ms after; /hosts unchanged at 0,5 ms; 9,9 MB reclaimed.
DROP INDEX IF EXISTS idx_service_results_service_id;
