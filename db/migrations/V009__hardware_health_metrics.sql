BEGIN;

SET LOCAL TIME ZONE 'UTC';
SET ROLE server_status_owner;
SET search_path = monitoring, pg_catalog;

ALTER TABLE block_devices
    ADD COLUMN protocol text,
    ADD COLUMN smart_device_type text,
    ADD COLUMN raid_passthrough boolean NOT NULL DEFAULT false;

CREATE TABLE temperature_current_metrics (
    node_id uuid NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    sensor_key text NOT NULL CHECK (btrim(sensor_key) <> ''),
    component text NOT NULL CHECK (component IN ('cpu', 'motherboard', 'gpu', 'storage', 'other')),
    label text NOT NULL CHECK (btrim(label) <> ''),
    bucket_at timestamptz NOT NULL,
    collected_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL,
    temperature_celsius real NOT NULL CHECK (temperature_celsius BETWEEN -273.15 AND 1000),
    high_celsius real CHECK (high_celsius BETWEEN -273.15 AND 1000),
    critical_celsius real CHECK (critical_celsius BETWEEN -273.15 AND 1000),
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (node_id, sensor_key)
);

CREATE INDEX temperature_current_metrics_bucket_idx
    ON temperature_current_metrics (bucket_at DESC);

CREATE TABLE storage_health_current_metrics (
    node_id uuid NOT NULL,
    block_device_id uuid NOT NULL,
    bucket_at timestamptz NOT NULL,
    collected_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL,
    smart_available boolean NOT NULL,
    smart_enabled boolean NOT NULL,
    smart_status text NOT NULL CHECK (smart_status IN ('passed', 'failed', 'unknown')),
    risk_level text NOT NULL CHECK (risk_level IN ('healthy', 'warning', 'critical', 'unknown')),
    risk_reasons text[] NOT NULL DEFAULT ARRAY[]::text[],
    temperature_celsius real CHECK (temperature_celsius BETWEEN -273.15 AND 1000),
    power_on_hours bigint CHECK (power_on_hours >= 0),
    error_count bigint CHECK (error_count >= 0),
    read_error_rate_normalized bigint CHECK (read_error_rate_normalized >= 0),
    read_error_rate_raw bigint CHECK (read_error_rate_raw >= 0),
    reallocated_sectors bigint CHECK (reallocated_sectors >= 0),
    pending_sectors bigint CHECK (pending_sectors >= 0),
    uncorrectable_sectors bigint CHECK (uncorrectable_sectors >= 0),
    percentage_used real CHECK (percentage_used BETWEEN 0 AND 255),
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (node_id, block_device_id),
    FOREIGN KEY (node_id, block_device_id)
        REFERENCES block_devices (node_id, id) ON DELETE CASCADE
);

CREATE INDEX storage_health_current_metrics_bucket_idx
    ON storage_health_current_metrics (bucket_at DESC);

COMMENT ON TABLE temperature_current_metrics IS
    'Latest Linux hwmon and GPU temperature readings reported by each node.';
COMMENT ON TABLE storage_health_current_metrics IS
    'Latest SMART health snapshot for direct disks and RAID-controller passthrough members.';

INSERT INTO schema_migrations (version, description)
VALUES ('V009', 'Add current hardware temperature and SMART health metrics');

RESET ROLE;

GRANT SELECT ON TABLE
    monitoring.temperature_current_metrics,
    monitoring.storage_health_current_metrics
TO server_status_writer, server_status_reader;

GRANT INSERT, UPDATE, DELETE ON TABLE
    monitoring.temperature_current_metrics,
    monitoring.storage_health_current_metrics
TO server_status_writer;

COMMIT;
