BEGIN;

SET LOCAL TIME ZONE 'UTC';
SET ROLE server_status_owner;
SET search_path = monitoring, pg_catalog;

CREATE OR REPLACE VIEW v_node_hardware_summary
WITH (security_invoker = true)
AS
WITH cpu AS (
    SELECT
        node_id,
        count(*)::integer AS package_count,
        sum(physical_cores)::integer AS physical_core_count,
        sum(logical_threads)::integer AS logical_thread_count,
        array_agg(DISTINCT model_name ORDER BY model_name) AS models
      FROM monitoring.cpu_packages
     WHERE removed_at IS NULL
     GROUP BY node_id
), memory AS (
    SELECT
        node_id,
        count(*)::integer AS module_count,
        sum(size_bytes)::bigint AS total_bytes,
        array_agg(DISTINCT COALESCE(model_name, part_number) ORDER BY COALESCE(model_name, part_number))
            FILTER (WHERE COALESCE(model_name, part_number) IS NOT NULL) AS models
      FROM monitoring.memory_modules
     WHERE removed_at IS NULL
     GROUP BY node_id
), raid_physical AS (
    SELECT
        node_id,
        count(*)::integer AS disk_count,
        sum(size_bytes)::bigint AS total_bytes,
        array_agg(DISTINCT model ORDER BY model)
            FILTER (WHERE btrim(model) <> '') AS models
      FROM monitoring.raid_physical_drive_current_metrics
     GROUP BY node_id
), raid_passthrough AS (
    SELECT
        node_id,
        count(*)::integer AS disk_count,
        sum(size_bytes)::bigint AS total_bytes,
        array_agg(DISTINCT model_name ORDER BY model_name)
            FILTER (WHERE model_name IS NOT NULL) AS models
      FROM monitoring.block_devices
     WHERE removed_at IS NULL
       AND device_kind = 'disk'
       AND raid_passthrough
     GROUP BY node_id
), direct_disk AS (
    SELECT
        node_id,
        count(*)::integer AS disk_count,
        sum(size_bytes)::bigint AS total_bytes,
        array_agg(DISTINCT model_name ORDER BY model_name)
            FILTER (WHERE model_name IS NOT NULL) AS models
      FROM monitoring.block_devices
     WHERE removed_at IS NULL
       AND device_kind = 'disk'
       AND NOT raid_passthrough
     GROUP BY node_id
), disk AS (
    SELECT
        node.id AS node_id,
        CASE
            WHEN COALESCE(raid_physical.disk_count, 0) > 0 THEN raid_physical.disk_count
            WHEN COALESCE(raid_passthrough.disk_count, 0) > 0 THEN raid_passthrough.disk_count
            ELSE COALESCE(direct_disk.disk_count, 0)
        END AS disk_count,
        CASE
            WHEN COALESCE(raid_physical.disk_count, 0) > 0 THEN raid_physical.total_bytes
            WHEN COALESCE(raid_passthrough.disk_count, 0) > 0 THEN raid_passthrough.total_bytes
            ELSE direct_disk.total_bytes
        END AS total_bytes,
        CASE
            WHEN COALESCE(raid_physical.disk_count, 0) > 0 THEN raid_physical.models
            WHEN COALESCE(raid_passthrough.disk_count, 0) > 0 THEN raid_passthrough.models
            ELSE direct_disk.models
        END AS models
      FROM monitoring.nodes node
      LEFT JOIN raid_physical ON raid_physical.node_id = node.id
      LEFT JOIN raid_passthrough ON raid_passthrough.node_id = node.id
      LEFT JOIN direct_disk ON direct_disk.node_id = node.id
)
SELECT
    node.id AS node_id,
    COALESCE(cpu.package_count, 0) AS cpu_package_count,
    COALESCE(cpu.physical_core_count, 0) AS cpu_physical_core_count,
    COALESCE(cpu.logical_thread_count, 0) AS cpu_logical_thread_count,
    cpu.models AS cpu_models,
    COALESCE(memory.module_count, 0) AS memory_module_count,
    COALESCE(memory.total_bytes, 0) AS memory_total_bytes,
    memory.models AS memory_models,
    COALESCE(disk.disk_count, 0) AS disk_count,
    COALESCE(disk.total_bytes, 0) AS disk_total_bytes,
    disk.models AS disk_models
  FROM monitoring.nodes node
  LEFT JOIN cpu ON cpu.node_id = node.id
  LEFT JOIN memory ON memory.node_id = node.id
  LEFT JOIN disk ON disk.node_id = node.id;

COMMENT ON VIEW v_node_hardware_summary IS
    'Hardware summary with RAID-aware physical disk counting. Dashboard capacity is aggregated separately from writable filesystems.';

INSERT INTO schema_migrations (version, description)
VALUES ('V018', 'Use RAID-aware physical disk counts in hardware summaries');

RESET ROLE;

GRANT SELECT ON TABLE monitoring.v_node_hardware_summary
TO server_status_writer, server_status_reader;

COMMIT;
