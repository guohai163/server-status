BEGIN;

SET LOCAL TIME ZONE 'UTC';

SELECT monitoring.ensure_raw_partitions(
    (CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date - 1,
    (CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date + 1
);
SELECT monitoring.ensure_hourly_partitions(
    (CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date - 1,
    (CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date + 1
);

-- Create deliberately expired partitions, then prove the retention routine removes them.
SELECT monitoring.ensure_raw_partitions(
    (CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date - 92,
    (CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date - 91
);
SELECT monitoring.ensure_hourly_partitions(
    (date_trunc('month', CURRENT_TIMESTAMP AT TIME ZONE 'UTC') - INTERVAL '26 months')::date,
    (date_trunc('month', CURRENT_TIMESTAMP AT TIME ZONE 'UTC') - INTERVAL '25 months')::date
);
SELECT * FROM monitoring.drop_expired_partitions(CURRENT_TIMESTAMP);

DO $retention_assertions$
DECLARE
    v_fk_count integer;
    v_old_raw_name text := format(
        'monitoring.node_metric_samples_p%s',
        to_char((CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date - 92, 'YYYYMMDD')
    );
    v_old_hourly_name text := format(
        'monitoring.node_metric_hourly_p%s',
        to_char(date_trunc('month', CURRENT_TIMESTAMP AT TIME ZONE 'UTC') - INTERVAL '26 months', 'YYYYMM')
    );
    v_current_raw_name text := format(
        'monitoring.node_metric_samples_p%s',
        to_char((CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date, 'YYYYMMDD')
    );
BEGIN
    IF to_regclass(v_old_raw_name) IS NOT NULL THEN
        RAISE EXCEPTION 'expired raw partition was not removed: %', v_old_raw_name;
    END IF;
    IF to_regclass(v_old_hourly_name) IS NOT NULL THEN
        RAISE EXCEPTION 'expired hourly partition was not removed: %', v_old_hourly_name;
    END IF;
    IF to_regclass(v_current_raw_name) IS NULL THEN
        RAISE EXCEPTION 'current raw partition was removed unexpectedly: %', v_current_raw_name;
    END IF;

    SELECT count(*)
      INTO v_fk_count
      FROM pg_constraint
     WHERE conrelid IN (
               'monitoring.filesystem_metric_samples'::regclass,
               'monitoring.network_metric_samples'::regclass
           )
       AND confrelid = 'monitoring.node_metric_samples'::regclass
       AND contype = 'f';
    IF v_fk_count <> 2 THEN
        RAISE EXCEPTION 'retention removed parent foreign keys: expected 2, got %', v_fk_count;
    END IF;
END
$retention_assertions$;

INSERT INTO monitoring.nodes (
    id, agent_id, hostname, os_name, os_version, kernel_version,
    architecture, agent_version, inventory_fingerprint, labels,
    first_seen_at, last_seen_at
) VALUES (
    '10000000-0000-0000-0000-000000000001',
    '20000000-0000-0000-0000-000000000001',
    'verify-node-1', 'Ubuntu', '24.04', '6.8.0',
    'x86_64', '0.1.0', repeat('a', 64), '{"env":"verify"}',
    CURRENT_TIMESTAMP - INTERVAL '1 day', CURRENT_TIMESTAMP
), (
    '10000000-0000-0000-0000-000000000002',
    '20000000-0000-0000-0000-000000000002',
    'verify-node-2', 'CentOS', '9', '5.14.0',
    'x86_64', '0.1.0', repeat('b', 64), '{}',
    CURRENT_TIMESTAMP - INTERVAL '1 day', CURRENT_TIMESTAMP - INTERVAL '10 minutes'
);

INSERT INTO monitoring.node_api_tokens (
    id, node_id, token_prefix, token_digest, label
) VALUES (
    '30000000-0000-0000-0000-000000000001',
    '10000000-0000-0000-0000-000000000001',
    'verify01', decode(repeat('ab', 32), 'hex'), 'verification token'
);

INSERT INTO monitoring.cpu_packages (
    id, node_id, hardware_key, package_index, vendor, model_name,
    physical_cores, logical_threads, max_frequency_mhz, first_seen_at, last_seen_at
) VALUES
    ('40000000-0000-0000-0000-000000000001', '10000000-0000-0000-0000-000000000001',
     'cpu-package-0', 0, 'GenuineIntel', 'Verification CPU', 8, 16, 4200,
     CURRENT_TIMESTAMP - INTERVAL '1 day', CURRENT_TIMESTAMP),
    ('40000000-0000-0000-0000-000000000002', '10000000-0000-0000-0000-000000000001',
     'cpu-package-1', 1, 'GenuineIntel', 'Verification CPU', 8, 16, 4200,
     CURRENT_TIMESTAMP - INTERVAL '1 day', CURRENT_TIMESTAMP);

INSERT INTO monitoring.memory_modules (
    id, node_id, hardware_key, slot_name, manufacturer, model_name,
    part_number, serial_number, memory_type, size_bytes, speed_mts,
    first_seen_at, last_seen_at
) VALUES
    ('50000000-0000-0000-0000-000000000001', '10000000-0000-0000-0000-000000000001',
     'dimm-a1', 'DIMM_A1', 'Verify Memory', 'VM-16G', 'VM16-3200', 'SERIAL-A', 'DDR4',
     17179869184, 3200, CURRENT_TIMESTAMP - INTERVAL '1 day', CURRENT_TIMESTAMP),
    ('50000000-0000-0000-0000-000000000002', '10000000-0000-0000-0000-000000000001',
     'dimm-b1', 'DIMM_B1', 'Verify Memory', 'VM-16G-OLD', 'VM16-OLD', 'SERIAL-OLD', 'DDR4',
     17179869184, 3200, CURRENT_TIMESTAMP - INTERVAL '1 day', CURRENT_TIMESTAMP - INTERVAL '1 hour');

UPDATE monitoring.memory_modules
   SET removed_at = CURRENT_TIMESTAMP - INTERVAL '30 minutes'
 WHERE id = '50000000-0000-0000-0000-000000000002';

INSERT INTO monitoring.memory_modules (
    id, node_id, hardware_key, slot_name, manufacturer, model_name,
    part_number, serial_number, memory_type, size_bytes, speed_mts,
    first_seen_at, last_seen_at
) VALUES (
    '50000000-0000-0000-0000-000000000003', '10000000-0000-0000-0000-000000000001',
    'dimm-b1', 'DIMM_B1', 'Verify Memory', 'VM-16G-NEW', 'VM16-NEW', 'SERIAL-NEW', 'DDR4',
    17179869184, 3200, CURRENT_TIMESTAMP - INTERVAL '20 minutes', CURRENT_TIMESTAMP
);

INSERT INTO monitoring.block_devices (
    id, node_id, hardware_key, device_name, device_kind, vendor,
    model_name, serial_number, wwn, size_bytes, rotational,
    first_seen_at, last_seen_at
) VALUES
    ('60000000-0000-0000-0000-000000000001', '10000000-0000-0000-0000-000000000001',
     'disk-sda', '/dev/sda', 'disk', 'Verify Disk', 'VD-1T', 'DISK-A', 'wwn-a',
     1000000000000, false, CURRENT_TIMESTAMP - INTERVAL '1 day', CURRENT_TIMESTAMP),
    ('60000000-0000-0000-0000-000000000002', '10000000-0000-0000-0000-000000000001',
     'disk-sdb', '/dev/sdb', 'disk', 'Verify Disk', 'VD-2T', 'DISK-B', 'wwn-b',
     2000000000000, true, CURRENT_TIMESTAMP - INTERVAL '1 day', CURRENT_TIMESTAMP);

INSERT INTO monitoring.filesystems (
    id, node_id, filesystem_key, filesystem_uuid, device_name,
    filesystem_type, mount_point, mount_options, first_seen_at, last_seen_at
) VALUES (
    '70000000-0000-0000-0000-000000000001', '10000000-0000-0000-0000-000000000001',
    'fs-root', 'verify-root-uuid', '/dev/mapper/vg-root', 'ext4', '/', ARRAY['rw'],
    CURRENT_TIMESTAMP - INTERVAL '1 day', CURRENT_TIMESTAMP
);

INSERT INTO monitoring.network_interfaces (
    id, node_id, interface_key, interface_name, mac_address, mtu,
    link_speed_mbps, first_seen_at, last_seen_at
) VALUES (
    '80000000-0000-0000-0000-000000000001', '10000000-0000-0000-0000-000000000001',
    'nic-eth0', 'eth0', '02:00:00:00:00:01', 1500, 1000,
    CURRENT_TIMESTAMP - INTERVAL '1 day', CURRENT_TIMESTAMP
);

INSERT INTO monitoring.network_addresses (
    id, node_id, interface_id, address, address_scope, first_seen_at, last_seen_at
) VALUES
    ('90000000-0000-0000-0000-000000000001', '10000000-0000-0000-0000-000000000001',
     '80000000-0000-0000-0000-000000000001', '10.10.0.10/24', 'private',
     CURRENT_TIMESTAMP - INTERVAL '1 day', CURRENT_TIMESTAMP),
    ('90000000-0000-0000-0000-000000000002', '10000000-0000-0000-0000-000000000001',
     '80000000-0000-0000-0000-000000000001', '2001:db8::10/64', 'global',
     CURRENT_TIMESTAMP - INTERVAL '1 day', CURRENT_TIMESTAMP);

INSERT INTO monitoring.node_network_preferences (node_id, interface_id)
VALUES (
    '10000000-0000-0000-0000-000000000001',
    '80000000-0000-0000-0000-000000000001'
);

INSERT INTO monitoring.gpu_devices (
    id, node_id, hardware_key, device_index, gpu_uuid, model_name,
    memory_total_bytes, first_seen_at, last_seen_at
) VALUES
    ('a0000000-0000-0000-0000-000000000001', '10000000-0000-0000-0000-000000000001',
     'GPU-verify-0', 0, 'GPU-verify-0', 'NVIDIA Verify GPU 0', 12884901888,
     CURRENT_TIMESTAMP - INTERVAL '1 day', CURRENT_TIMESTAMP),
    ('a0000000-0000-0000-0000-000000000002', '10000000-0000-0000-0000-000000000001',
     'GPU-verify-1', 1, 'GPU-verify-1', 'NVIDIA Verify GPU 1', 25769803776,
     CURRENT_TIMESTAMP - INTERVAL '1 day', CURRENT_TIMESTAMP);

INSERT INTO monitoring.gpu_current_metrics (
    node_id, gpu_id, bucket_at, collected_at, received_at,
    utilization_percent, memory_used_bytes
) VALUES
    ('10000000-0000-0000-0000-000000000001', 'a0000000-0000-0000-0000-000000000001',
     date_trunc('minute', CURRENT_TIMESTAMP), CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 25, 3221225472),
    ('10000000-0000-0000-0000-000000000001', 'a0000000-0000-0000-0000-000000000002',
     date_trunc('minute', CURRENT_TIMESTAMP), CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 80, 21474836480);

INSERT INTO monitoring.node_metric_samples (
    bucket_at, node_id, collected_at, interval_seconds,
    cpu_usage_percent, load_1, load_5, load_15,
    memory_total_bytes, memory_used_bytes, memory_available_bytes,
    memory_cached_bytes, memory_buffers_bytes,
    swap_total_bytes, swap_used_bytes, uptime_seconds,
    disk_read_bytes_total, disk_write_bytes_total,
    disk_read_bytes_delta, disk_write_bytes_delta,
    disk_read_ops_delta, disk_write_ops_delta
) VALUES
    (date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '50 minutes',
     '10000000-0000-0000-0000-000000000001',
     date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '50 minutes', 60,
     20, 0.5, 0.4, 0.3, 1000, 400, 500, 100, 20, 200, 20, 10000,
     100000, 200000, 6000, 3000, 10, 5),
    (date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '49 minutes',
     '10000000-0000-0000-0000-000000000001',
     date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '49 minutes', 60,
     40, 1.5, 1.0, 0.7, 1000, 600, 300, 100, 20, 200, 40, 10060,
     112000, 206000, 12000, 6000, 20, 10),
    (date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '50 minutes',
     '10000000-0000-0000-0000-000000000002',
     date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '50 minutes', 60,
     10, 0.1, 0.1, 0.1, 1000, 100, 800, 50, 10, 0, 0, 5000,
     1000, 2000, 0, 0, 0, 0);

INSERT INTO monitoring.filesystem_metric_samples (
    bucket_at, node_id, filesystem_id,
    total_bytes, used_bytes, available_bytes, total_inodes, used_inodes
) VALUES
    (date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '50 minutes',
     '10000000-0000-0000-0000-000000000001', '70000000-0000-0000-0000-000000000001',
     1000, 500, 400, 100, 50),
    (date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '49 minutes',
     '10000000-0000-0000-0000-000000000001', '70000000-0000-0000-0000-000000000001',
     1000, 750, 200, 100, 60);

INSERT INTO monitoring.network_metric_samples (
    bucket_at, node_id, interface_id, link_up,
    rx_bytes_total, tx_bytes_total, rx_bytes_delta, tx_bytes_delta,
    rx_packets_delta, tx_packets_delta, rx_errors_delta, tx_errors_delta,
    rx_dropped_delta, tx_dropped_delta
) VALUES
    (date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '50 minutes',
     '10000000-0000-0000-0000-000000000001', '80000000-0000-0000-0000-000000000001', true,
     100000, 200000, 6000, 3000, 100, 50, 0, 0, 0, 0),
    (date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '49 minutes',
     '10000000-0000-0000-0000-000000000001', '80000000-0000-0000-0000-000000000001', true,
     112000, 206000, 12000, 6000, 200, 100, 0, 0, 0, 0);

-- Retry the newest minute. The natural key updates instead of duplicating it.
INSERT INTO monitoring.node_metric_samples (
    bucket_at, node_id, collected_at, interval_seconds,
    cpu_usage_percent, load_1, load_5, load_15,
    memory_total_bytes, memory_used_bytes, memory_available_bytes,
    memory_cached_bytes, memory_buffers_bytes,
    swap_total_bytes, swap_used_bytes, uptime_seconds
) VALUES (
    date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '49 minutes',
    '10000000-0000-0000-0000-000000000001',
    date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '49 minutes', 60,
    60, 1.5, 1.0, 0.7, 1000, 600, 300, 100, 20, 200, 40, 10060
)
ON CONFLICT (bucket_at, node_id) DO UPDATE SET
    cpu_usage_percent = EXCLUDED.cpu_usage_percent,
    received_at = CURRENT_TIMESTAMP;

DO $assertions$
DECLARE
    v_count integer;
    v_number numeric;
    v_status text;
    v_bucket timestamptz;
BEGIN
    SELECT count(*) INTO v_count
      FROM monitoring.node_metric_samples
     WHERE node_id = '10000000-0000-0000-0000-000000000001';
    IF v_count <> 2 THEN
        RAISE EXCEPTION 'idempotency check failed: expected 2 node samples, got %', v_count;
    END IF;

    SELECT bucket_at INTO v_bucket
      FROM monitoring.node_current_metrics
     WHERE node_id = '10000000-0000-0000-0000-000000000001';
    IF v_bucket <> date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '49 minutes' THEN
        RAISE EXCEPTION 'older sample replaced current node metrics';
    END IF;

    SELECT cpu_usage_percent INTO v_number
      FROM monitoring.node_current_metrics
     WHERE node_id = '10000000-0000-0000-0000-000000000001';
    IF v_number <> 60 THEN
        RAISE EXCEPTION 'node current metrics were not refreshed by retry';
    END IF;

    SELECT disk_read_bytes_per_second INTO v_number
      FROM monitoring.node_current_metrics
     WHERE node_id = '10000000-0000-0000-0000-000000000001';
    IF v_number <> 200 THEN
        RAISE EXCEPTION 'disk generated read rate is incorrect: %', v_number;
    END IF;

    SELECT used_percent INTO v_number
      FROM monitoring.filesystem_current_metrics
     WHERE node_id = '10000000-0000-0000-0000-000000000001'
       AND filesystem_id = '70000000-0000-0000-0000-000000000001';
    IF v_number <> 75 THEN
        RAISE EXCEPTION 'filesystem generated usage percent is incorrect: %', v_number;
    END IF;

    SELECT rx_bits_per_second INTO v_number
      FROM monitoring.network_current_metrics
     WHERE node_id = '10000000-0000-0000-0000-000000000001'
       AND interface_id = '80000000-0000-0000-0000-000000000001';
    IF v_number <> 1600 THEN
        RAISE EXCEPTION 'network generated rate is incorrect: %', v_number;
    END IF;

    SELECT status INTO v_status
      FROM monitoring.v_node_status
     WHERE node_id = '10000000-0000-0000-0000-000000000001';
    IF v_status <> 'online' THEN
        RAISE EXCEPTION 'online status view is incorrect: %', v_status;
    END IF;

    SELECT status INTO v_status
      FROM monitoring.v_node_status
     WHERE node_id = '10000000-0000-0000-0000-000000000002';
    IF v_status <> 'offline' THEN
        RAISE EXCEPTION 'offline status view is incorrect: %', v_status;
    END IF;

    UPDATE monitoring.nodes
       SET disabled_at = CURRENT_TIMESTAMP
     WHERE id = '10000000-0000-0000-0000-000000000002';

    SELECT status INTO v_status
      FROM monitoring.v_node_status
     WHERE node_id = '10000000-0000-0000-0000-000000000002';
    IF v_status <> 'disabled' THEN
        RAISE EXCEPTION 'disabled status view is incorrect: %', v_status;
    END IF;

    SELECT memory_module_count INTO v_count
      FROM monitoring.v_node_hardware_summary
     WHERE node_id = '10000000-0000-0000-0000-000000000001';
    IF v_count <> 2 THEN
        RAISE EXCEPTION 'hardware history filtering is incorrect: % active modules', v_count;
    END IF;

    SELECT cardinality(addresses) INTO v_count
      FROM monitoring.v_network_status
     WHERE node_id = '10000000-0000-0000-0000-000000000001'
       AND interface_id = '80000000-0000-0000-0000-000000000001';
    IF v_count <> 2 THEN
        RAISE EXCEPTION 'IPv4/IPv6 aggregation is incorrect: %', v_count;
    END IF;

    IF NOT EXISTS (
        SELECT 1
          FROM monitoring.node_network_preferences
         WHERE node_id = '10000000-0000-0000-0000-000000000001'
           AND interface_id = '80000000-0000-0000-0000-000000000001'
    ) THEN
        RAISE EXCEPTION 'dashboard network preference was not persisted';
    END IF;

    SELECT count(*) INTO v_count
      FROM monitoring.gpu_devices gpu
      JOIN monitoring.gpu_current_metrics metric
        ON metric.node_id = gpu.node_id AND metric.gpu_id = gpu.id
     WHERE gpu.node_id = '10000000-0000-0000-0000-000000000001'
       AND gpu.removed_at IS NULL;
    IF v_count <> 2 THEN
        RAISE EXCEPTION 'per-device GPU metrics are incomplete: %', v_count;
    END IF;

    SELECT memory_used_bytes::numeric * 100 / memory_total_bytes INTO v_number
      FROM monitoring.gpu_devices gpu
      JOIN monitoring.gpu_current_metrics metric
        ON metric.node_id = gpu.node_id AND metric.gpu_id = gpu.id
     WHERE gpu.id = 'a0000000-0000-0000-0000-000000000002';
    IF round(v_number, 1) <> 83.3 THEN
        RAISE EXCEPTION 'GPU memory utilization is incorrect: %', v_number;
    END IF;

    IF EXISTS (
        SELECT 1 FROM monitoring.node_api_tokens
         WHERE token_digest = decode(repeat('cd', 32), 'hex')
    ) THEN
        RAISE EXCEPTION 'invalid token digest unexpectedly authenticated';
    END IF;

    BEGIN
        INSERT INTO monitoring.node_metric_samples (
            bucket_at, node_id, collected_at, interval_seconds,
            cpu_usage_percent, memory_total_bytes, memory_used_bytes,
            memory_available_bytes, memory_cached_bytes, memory_buffers_bytes,
            swap_total_bytes, swap_used_bytes, uptime_seconds
        ) VALUES (
            date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '48 minutes',
            '10000000-0000-0000-0000-000000000001', CURRENT_TIMESTAMP, 60,
            101, 1000, 100, 800, 50, 10, 0, 0, 1
        );
        RAISE EXCEPTION 'CPU percentage constraint did not reject 101';
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;

    BEGIN
        INSERT INTO monitoring.filesystem_metric_samples (
            bucket_at, node_id, filesystem_id, total_bytes, used_bytes, available_bytes
        ) VALUES (
            date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '50 minutes',
            '10000000-0000-0000-0000-000000000001',
            '70000000-0000-0000-0000-000000000001', 1000, -1, 400
        );
        RAISE EXCEPTION 'negative filesystem capacity was accepted';
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;

    BEGIN
        INSERT INTO monitoring.filesystem_metric_samples (
            bucket_at, node_id, filesystem_id, total_bytes, used_bytes, available_bytes
        ) VALUES (
            date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '50 minutes',
            '10000000-0000-0000-0000-000000000002',
            '70000000-0000-0000-0000-000000000001', 1000, 500, 400
        );
        RAISE EXCEPTION 'cross-node filesystem reference was accepted';
    EXCEPTION WHEN foreign_key_violation THEN
        NULL;
    END;

    BEGIN
        INSERT INTO monitoring.node_network_preferences (node_id, interface_id)
        VALUES (
            '10000000-0000-0000-0000-000000000002',
            '80000000-0000-0000-0000-000000000001'
        );
        RAISE EXCEPTION 'cross-node network preference was accepted';
    EXCEPTION WHEN foreign_key_violation THEN
        NULL;
    END;
END
$assertions$;

SELECT monitoring.rollup_hour(date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '1 hour');

DO $rollup_assertions$
DECLARE
    v_count integer;
    v_number numeric;
BEGIN
    SELECT sample_count
      INTO v_count
      FROM monitoring.node_metric_hourly
     WHERE hour_at = date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '1 hour'
       AND node_id = '10000000-0000-0000-0000-000000000001';

    IF v_count <> 2 THEN
        RAISE EXCEPTION 'node hourly sample count is incorrect: %', v_count;
    END IF;

    SELECT cpu_usage_avg INTO v_number
      FROM monitoring.node_metric_hourly
     WHERE hour_at = date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '1 hour'
       AND node_id = '10000000-0000-0000-0000-000000000001';
    IF v_number <> 40 THEN
        RAISE EXCEPTION 'node hourly CPU average is incorrect: %', v_number;
    END IF;

    SELECT disk_read_bytes INTO v_number
      FROM monitoring.node_metric_hourly
     WHERE hour_at = date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '1 hour'
       AND node_id = '10000000-0000-0000-0000-000000000001';
    IF v_number <> 18000 THEN
        RAISE EXCEPTION 'node hourly disk read bytes are incorrect: %', v_number;
    END IF;

    SELECT disk_read_bytes_per_second_avg INTO v_number
      FROM monitoring.node_metric_hourly
     WHERE hour_at = date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '1 hour'
       AND node_id = '10000000-0000-0000-0000-000000000001';
    IF v_number <> 150 THEN
        RAISE EXCEPTION 'node hourly disk read average is incorrect: %', v_number;
    END IF;

    SELECT usage_avg INTO v_number
      FROM monitoring.filesystem_metric_hourly
     WHERE hour_at = date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '1 hour'
       AND node_id = '10000000-0000-0000-0000-000000000001'
       AND filesystem_id = '70000000-0000-0000-0000-000000000001';
    IF v_number <> 62.5 THEN
        RAISE EXCEPTION 'filesystem hourly usage average is incorrect: %', v_number;
    END IF;

    SELECT rx_bytes INTO v_number
      FROM monitoring.network_metric_hourly
     WHERE hour_at = date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '1 hour'
       AND node_id = '10000000-0000-0000-0000-000000000001'
       AND interface_id = '80000000-0000-0000-0000-000000000001';
    IF v_number <> 18000 THEN
        RAISE EXCEPTION 'network hourly byte total is incorrect: %', v_number;
    END IF;

    SELECT rx_bits_per_second_avg INTO v_number
      FROM monitoring.network_metric_hourly
     WHERE hour_at = date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '1 hour'
       AND node_id = '10000000-0000-0000-0000-000000000001'
       AND interface_id = '80000000-0000-0000-0000-000000000001';
    IF v_number <> 1200 THEN
        RAISE EXCEPTION 'network hourly average rate is incorrect: %', v_number;
    END IF;
END
$rollup_assertions$;

DO $permission_assertions$
BEGIN
    IF NOT has_table_privilege('server_status_reader', 'monitoring.v_node_status', 'SELECT') THEN
        RAISE EXCEPTION 'reader role lacks SELECT on monitoring views';
    END IF;
    IF has_table_privilege('server_status_reader', 'monitoring.nodes', 'INSERT') THEN
        RAISE EXCEPTION 'reader role unexpectedly has INSERT on nodes';
    END IF;
    IF NOT has_table_privilege('server_status_writer', 'monitoring.node_metric_samples', 'INSERT') THEN
        RAISE EXCEPTION 'writer role lacks INSERT on raw samples';
    END IF;
    IF NOT has_table_privilege('server_status_writer', 'monitoring.node_network_preferences', 'INSERT') THEN
        RAISE EXCEPTION 'writer role lacks INSERT on dashboard network preferences';
    END IF;
    IF NOT has_table_privilege('server_status_writer', 'monitoring.gpu_current_metrics', 'INSERT') THEN
        RAISE EXCEPTION 'writer role lacks INSERT on GPU current metrics';
    END IF;
    IF has_table_privilege('server_status_reader', 'monitoring.gpu_current_metrics', 'INSERT') THEN
        RAISE EXCEPTION 'reader role can modify GPU current metrics';
    END IF;
    IF has_table_privilege('server_status_reader', 'monitoring.node_network_preferences', 'INSERT') THEN
        RAISE EXCEPTION 'reader role can modify dashboard network preferences';
    END IF;
    IF has_table_privilege('server_status_writer', 'monitoring.schema_migrations', 'INSERT') THEN
        RAISE EXCEPTION 'writer role can modify migration history';
    END IF;
END
$permission_assertions$;

SET LOCAL ROLE server_status_writer;

SELECT monitoring.maintain_partitions(CURRENT_TIMESTAMP);

INSERT INTO monitoring.nodes (
    id, agent_id, hostname, os_name, architecture, agent_version,
    first_seen_at, last_seen_at
) VALUES (
    '10000000-0000-0000-0000-000000000003',
    '20000000-0000-0000-0000-000000000003',
    'verify-writer-role', 'Ubuntu', 'x86_64', '0.1.0',
    CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
);

INSERT INTO monitoring.network_interfaces (
    id, node_id, interface_key, interface_name, first_seen_at, last_seen_at
) VALUES (
    '80000000-0000-0000-0000-000000000003',
    '10000000-0000-0000-0000-000000000003',
    'writer-eth0', 'eth0', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
);

INSERT INTO monitoring.node_network_preferences (node_id, interface_id)
VALUES (
    '10000000-0000-0000-0000-000000000003',
    '80000000-0000-0000-0000-000000000003'
);

INSERT INTO monitoring.node_metric_samples (
    bucket_at, node_id, collected_at, interval_seconds,
    cpu_usage_percent, memory_total_bytes, memory_used_bytes,
    memory_available_bytes, memory_cached_bytes, memory_buffers_bytes,
    swap_total_bytes, swap_used_bytes, uptime_seconds
) VALUES (
    date_trunc('minute', CURRENT_TIMESTAMP),
    '10000000-0000-0000-0000-000000000003', CURRENT_TIMESTAMP, 60,
    12.5, 1000, 200, 700, 50, 10, 0, 0, 1
);

RESET ROLE;

DO $writer_runtime_assertions$
BEGIN
    IF NOT EXISTS (
        SELECT 1
          FROM monitoring.node_current_metrics
         WHERE node_id = '10000000-0000-0000-0000-000000000003'
           AND cpu_usage_percent = 12.5
    ) THEN
        RAISE EXCEPTION 'writer role insert did not execute the current-metrics trigger';
    END IF;
    IF NOT EXISTS (
        SELECT 1
          FROM monitoring.node_network_preferences
         WHERE node_id = '10000000-0000-0000-0000-000000000003'
           AND interface_id = '80000000-0000-0000-0000-000000000003'
    ) THEN
        RAISE EXCEPTION 'writer role could not persist dashboard network preference';
    END IF;
END
$writer_runtime_assertions$;

EXPLAIN (COSTS OFF)
SELECT bucket_at, cpu_usage_percent
  FROM monitoring.node_metric_samples
 WHERE node_id = '10000000-0000-0000-0000-000000000001'
   AND bucket_at >= date_trunc('hour', CURRENT_TIMESTAMP) - INTERVAL '1 hour'
   AND bucket_at < date_trunc('hour', CURRENT_TIMESTAMP);

ROLLBACK;
