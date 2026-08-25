BEGIN;

SET LOCAL TIME ZONE 'UTC';
SET ROLE server_status_owner;
SET search_path = monitoring, pg_catalog;

ALTER TABLE storage_health_current_metrics
    ADD COLUMN host_writes_bytes bigint CHECK (host_writes_bytes >= 0);

COMMENT ON COLUMN storage_health_current_metrics.host_writes_bytes IS
    'SMART-reported cumulative host bytes written, used to display SSD TBW. Only stored when its source unit is known.';

INSERT INTO schema_migrations (version, description)
VALUES ('V017', 'Add SMART cumulative host writes for SSD TBW display');

RESET ROLE;

COMMIT;
