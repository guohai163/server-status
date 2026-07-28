BEGIN;

SET LOCAL TIME ZONE 'UTC';
SET ROLE server_status_owner;
SET search_path = monitoring, pg_catalog;

ALTER TABLE filesystem_metric_samples
    DROP CONSTRAINT filesystem_metric_samples_node_id_filesystem_id_fkey,
    ADD CONSTRAINT filesystem_metric_samples_node_id_filesystem_id_fkey
        FOREIGN KEY (node_id, filesystem_id)
        REFERENCES filesystems (node_id, id) ON DELETE CASCADE;

ALTER TABLE network_metric_samples
    DROP CONSTRAINT network_metric_samples_node_id_interface_id_fkey,
    ADD CONSTRAINT network_metric_samples_node_id_interface_id_fkey
        FOREIGN KEY (node_id, interface_id)
        REFERENCES network_interfaces (node_id, id) ON DELETE CASCADE;

ALTER TABLE filesystem_metric_hourly
    DROP CONSTRAINT filesystem_metric_hourly_node_id_filesystem_id_fkey,
    ADD CONSTRAINT filesystem_metric_hourly_node_id_filesystem_id_fkey
        FOREIGN KEY (node_id, filesystem_id)
        REFERENCES filesystems (node_id, id) ON DELETE CASCADE;

ALTER TABLE network_metric_hourly
    DROP CONSTRAINT network_metric_hourly_node_id_interface_id_fkey,
    ADD CONSTRAINT network_metric_hourly_node_id_interface_id_fkey
        FOREIGN KEY (node_id, interface_id)
        REFERENCES network_interfaces (node_id, id) ON DELETE CASCADE;

ALTER TABLE gpu_metric_samples
    DROP CONSTRAINT gpu_metric_samples_node_id_gpu_id_fkey,
    ADD CONSTRAINT gpu_metric_samples_node_id_gpu_id_fkey
        FOREIGN KEY (node_id, gpu_id)
        REFERENCES gpu_devices (node_id, id) ON DELETE CASCADE;

ALTER TABLE gpu_metric_hourly
    DROP CONSTRAINT gpu_metric_hourly_node_id_gpu_id_fkey,
    ADD CONSTRAINT gpu_metric_hourly_node_id_gpu_id_fkey
        FOREIGN KEY (node_id, gpu_id)
        REFERENCES gpu_devices (node_id, id) ON DELETE CASCADE;

INSERT INTO schema_migrations (version, description)
VALUES ('V013', 'Allow complete node deletion through cascading resource history');

RESET ROLE;

COMMIT;
