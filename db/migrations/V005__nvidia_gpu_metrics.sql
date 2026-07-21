BEGIN;

SET LOCAL TIME ZONE 'UTC';
SET ROLE server_status_owner;
SET search_path = monitoring, pg_catalog;

CREATE TABLE gpu_devices (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id uuid NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    hardware_key text NOT NULL CHECK (btrim(hardware_key) <> ''),
    device_index integer NOT NULL CHECK (device_index >= 0),
    gpu_uuid text NOT NULL CHECK (btrim(gpu_uuid) <> ''),
    model_name text NOT NULL CHECK (btrim(model_name) <> ''),
    memory_total_bytes bigint NOT NULL CHECK (memory_total_bytes > 0),
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    removed_at timestamptz,
    UNIQUE (node_id, id),
    CHECK (last_seen_at >= first_seen_at),
    CHECK (removed_at IS NULL OR removed_at >= first_seen_at)
);

CREATE UNIQUE INDEX gpu_devices_active_key_idx
    ON gpu_devices (node_id, hardware_key)
    WHERE removed_at IS NULL;

CREATE TABLE gpu_current_metrics (
    node_id uuid NOT NULL,
    gpu_id uuid NOT NULL,
    bucket_at timestamptz NOT NULL,
    collected_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL,
    utilization_percent real NOT NULL CHECK (utilization_percent BETWEEN 0 AND 100),
    memory_used_bytes bigint NOT NULL CHECK (memory_used_bytes >= 0),
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (node_id, gpu_id),
    FOREIGN KEY (node_id, gpu_id)
        REFERENCES gpu_devices (node_id, id) ON DELETE CASCADE
);

CREATE INDEX gpu_current_metrics_bucket_idx
    ON gpu_current_metrics (bucket_at DESC);

COMMENT ON TABLE gpu_devices IS
    'NVIDIA GPU inventory keyed by the stable UUID reported by nvidia-smi.';
COMMENT ON TABLE gpu_current_metrics IS
    'Latest utilization and framebuffer memory usage for each active NVIDIA GPU.';

INSERT INTO schema_migrations (version, description)
VALUES ('V005', 'Add NVIDIA GPU inventory and current metrics');

RESET ROLE;

GRANT SELECT ON TABLE monitoring.gpu_devices, monitoring.gpu_current_metrics
TO server_status_writer, server_status_reader;
GRANT INSERT, UPDATE, DELETE ON TABLE monitoring.gpu_devices, monitoring.gpu_current_metrics
TO server_status_writer;

COMMIT;
