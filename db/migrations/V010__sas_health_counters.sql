BEGIN;

SET LOCAL TIME ZONE 'UTC';
SET ROLE server_status_owner;
SET search_path = monitoring, pg_catalog;

ALTER TABLE storage_health_current_metrics
    ADD COLUMN scsi_grown_defects bigint CHECK (scsi_grown_defects >= 0),
    ADD COLUMN scsi_ecc_corrected bigint CHECK (scsi_ecc_corrected >= 0),
    ADD COLUMN scsi_ecc_corrected_delta bigint CHECK (scsi_ecc_corrected_delta >= 0),
    ADD COLUMN scsi_non_medium_errors bigint CHECK (scsi_non_medium_errors >= 0),
    ADD COLUMN scsi_non_medium_errors_delta bigint CHECK (scsi_non_medium_errors_delta >= 0);

COMMENT ON COLUMN storage_health_current_metrics.scsi_grown_defects IS
    'Current SCSI grown defect list count; any nonzero value contributes a persistent warning.';
COMMENT ON COLUMN storage_health_current_metrics.scsi_ecc_corrected IS
    'Cumulative SCSI errors corrected by fast or delayed ECC across read, write, and verify operations.';
COMMENT ON COLUMN storage_health_current_metrics.scsi_ecc_corrected_delta IS
    'Increase in the cumulative SCSI ECC count since the previously accepted snapshot.';
COMMENT ON COLUMN storage_health_current_metrics.scsi_non_medium_errors IS
    'Cumulative SCSI non-medium error count parsed from smartctl original output.';
COMMENT ON COLUMN storage_health_current_metrics.scsi_non_medium_errors_delta IS
    'Increase in the cumulative SCSI non-medium error count since the previously accepted snapshot.';

INSERT INTO schema_migrations (version, description)
VALUES ('V010', 'Add SAS defect, ECC, and non-medium error counters');

RESET ROLE;

COMMIT;
