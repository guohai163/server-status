BEGIN;

SET LOCAL TIME ZONE 'UTC';
SET ROLE server_status_owner;
SET search_path = monitoring, pg_catalog;

CREATE TABLE hardware_sensor_current_snapshots (
    node_id uuid PRIMARY KEY REFERENCES nodes (id) ON DELETE CASCADE,
    bucket_at timestamptz NOT NULL,
    collected_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE hardware_sensor_current_metrics (
    node_id uuid NOT NULL REFERENCES hardware_sensor_current_snapshots (node_id) ON DELETE CASCADE,
    sensor_key text NOT NULL CHECK (btrim(sensor_key) <> ''),
    sensor_type text NOT NULL CHECK (sensor_type IN ('fan', 'voltage', 'current', 'power')),
    label text NOT NULL CHECK (btrim(label) <> ''),
    value double precision NOT NULL CHECK (value BETWEEN -1000000000000 AND 1000000000000),
    min_value double precision CHECK (min_value BETWEEN -1000000000000 AND 1000000000000),
    max_value double precision CHECK (max_value BETWEEN -1000000000000 AND 1000000000000),
    critical_value double precision CHECK (critical_value BETWEEN -1000000000000 AND 1000000000000),
    alarm boolean NOT NULL DEFAULT false,
    fault boolean NOT NULL DEFAULT false,
    PRIMARY KEY (node_id, sensor_key),
    CHECK (sensor_type <> 'fan' OR value >= 0),
    CHECK (sensor_type <> 'fan' OR min_value IS NULL OR min_value >= 0),
    CHECK (sensor_type <> 'fan' OR max_value IS NULL OR max_value >= 0),
    CHECK (sensor_type <> 'fan' OR critical_value IS NULL OR critical_value >= 0)
);

CREATE INDEX hardware_sensor_current_snapshots_bucket_idx
    ON hardware_sensor_current_snapshots (bucket_at DESC);

COMMENT ON TABLE hardware_sensor_current_snapshots IS
    'Latest accepted hardware-sensor snapshot, including snapshots with no readings.';
COMMENT ON TABLE hardware_sensor_current_metrics IS
    'Latest Linux hwmon fan, voltage, current, and power readings for physical nodes.';

INSERT INTO schema_migrations (version, description)
VALUES ('V014', 'Add current physical-node hardware sensor metrics');

RESET ROLE;

GRANT SELECT ON TABLE
    monitoring.hardware_sensor_current_snapshots,
    monitoring.hardware_sensor_current_metrics
TO server_status_writer, server_status_reader;

GRANT INSERT, UPDATE, DELETE ON TABLE
    monitoring.hardware_sensor_current_snapshots,
    monitoring.hardware_sensor_current_metrics
TO server_status_writer;

COMMIT;
