BEGIN;

SET LOCAL TIME ZONE 'UTC';
SET ROLE server_status_owner;
SET search_path = monitoring, pg_catalog;

CREATE TABLE raid_status_current_snapshots (
    node_id uuid PRIMARY KEY REFERENCES nodes (id) ON DELETE CASCADE,
    bucket_at timestamptz NOT NULL,
    collected_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX raid_status_current_snapshots_bucket_idx
    ON raid_status_current_snapshots (bucket_at DESC);

CREATE TABLE raid_controller_current_metrics (
    node_id uuid NOT NULL,
    controller_id integer NOT NULL CHECK (controller_id >= 0),
    model text NOT NULL CHECK (btrim(model) <> ''),
    state text NOT NULL CHECK (btrim(state) <> ''),
    physical_drive_count integer NOT NULL CHECK (physical_drive_count >= 0),
    drive_group_count integer NOT NULL CHECK (drive_group_count >= 0),
    virtual_drive_count integer NOT NULL CHECK (virtual_drive_count >= 0),
    PRIMARY KEY (node_id, controller_id),
    FOREIGN KEY (node_id) REFERENCES raid_status_current_snapshots (node_id) ON DELETE CASCADE
);

CREATE TABLE raid_virtual_drive_current_metrics (
    node_id uuid NOT NULL,
    controller_id integer NOT NULL,
    drive_group_id integer NOT NULL CHECK (drive_group_id >= 0),
    virtual_drive_id integer NOT NULL CHECK (virtual_drive_id >= 0),
    raid_level text NOT NULL CHECK (btrim(raid_level) <> ''),
    state text NOT NULL CHECK (btrim(state) <> ''),
    access text,
    consistency text,
    cache text,
    size_bytes bigint NOT NULL CHECK (size_bytes > 0),
    name text,
    PRIMARY KEY (node_id, controller_id, drive_group_id, virtual_drive_id),
    FOREIGN KEY (node_id, controller_id)
        REFERENCES raid_controller_current_metrics (node_id, controller_id) ON DELETE CASCADE
);

CREATE TABLE raid_physical_drive_current_metrics (
    node_id uuid NOT NULL,
    controller_id integer NOT NULL,
    enclosure_id integer NOT NULL DEFAULT -1 CHECK (enclosure_id >= -1),
    slot integer NOT NULL CHECK (slot >= 0),
    device_id integer NOT NULL CHECK (device_id >= 0),
    state text NOT NULL CHECK (btrim(state) <> ''),
    drive_group_id integer CHECK (drive_group_id >= 0),
    size_bytes bigint NOT NULL CHECK (size_bytes > 0),
    interface text NOT NULL CHECK (btrim(interface) <> ''),
    media_type text NOT NULL CHECK (btrim(media_type) <> ''),
    sector_size_bytes bigint CHECK (sector_size_bytes > 0),
    model text NOT NULL CHECK (btrim(model) <> ''),
    spun text,
    drive_type text,
    rebuild_progress real CHECK (rebuild_progress BETWEEN 0 AND 100),
    rebuild_status text,
    estimated_time_left text,
    PRIMARY KEY (node_id, controller_id, enclosure_id, slot),
    FOREIGN KEY (node_id, controller_id)
        REFERENCES raid_controller_current_metrics (node_id, controller_id) ON DELETE CASCADE
);

COMMENT ON TABLE raid_status_current_snapshots IS
    'Freshness marker for the latest optional StorCLI or PERCCLI RAID-controller snapshot per node.';
COMMENT ON TABLE raid_controller_current_metrics IS
    'Latest RAID controller state and topology counts reported by each node.';
COMMENT ON TABLE raid_virtual_drive_current_metrics IS
    'Latest virtual-drive RAID level, state, access, consistency, cache, and capacity.';
COMMENT ON TABLE raid_physical_drive_current_metrics IS
    'Latest controller slot, disk state, interface, media, sector size, and rebuild progress.';

INSERT INTO schema_migrations (version, description)
VALUES ('V011', 'Add current RAID controller, virtual drive, and physical drive status');

RESET ROLE;

GRANT SELECT ON TABLE
    monitoring.raid_status_current_snapshots,
    monitoring.raid_controller_current_metrics,
    monitoring.raid_virtual_drive_current_metrics,
    monitoring.raid_physical_drive_current_metrics
TO server_status_writer, server_status_reader;

GRANT INSERT, UPDATE, DELETE ON TABLE
    monitoring.raid_status_current_snapshots,
    monitoring.raid_controller_current_metrics,
    monitoring.raid_virtual_drive_current_metrics,
    monitoring.raid_physical_drive_current_metrics
TO server_status_writer;

COMMIT;
