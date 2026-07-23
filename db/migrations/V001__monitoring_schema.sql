BEGIN;

SET LOCAL TIME ZONE 'UTC';

DO $roles$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'server_status_owner') THEN
        CREATE ROLE server_status_owner NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'server_status_writer') THEN
        CREATE ROLE server_status_writer NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'server_status_reader') THEN
        CREATE ROLE server_status_reader NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE;
    END IF;

    EXECUTE format('GRANT server_status_owner TO %I', session_user);
END
$roles$;

CREATE SCHEMA monitoring AUTHORIZATION server_status_owner;

SET ROLE server_status_owner;
SET search_path = monitoring, pg_catalog;

CREATE TABLE schema_migrations (
    version text PRIMARY KEY,
    description text NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    applied_by text NOT NULL DEFAULT session_user
);

CREATE TABLE nodes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id uuid NOT NULL UNIQUE,
    hostname text NOT NULL CHECK (btrim(hostname) <> ''),
    display_name text,
    os_name text NOT NULL,
    os_version text,
    kernel_version text,
    architecture text NOT NULL,
    agent_version text NOT NULL,
    inventory_fingerprint text CHECK (
        inventory_fingerprint IS NULL
        OR inventory_fingerprint ~ '^[0-9a-f]{64}$'
    ),
    labels jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(labels) = 'object'),
    first_seen_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    disabled_at timestamptz,
    CHECK (last_seen_at >= first_seen_at),
    CHECK (disabled_at IS NULL OR disabled_at >= created_at)
);

CREATE INDEX nodes_last_seen_idx
    ON nodes (last_seen_at DESC)
    WHERE disabled_at IS NULL;

CREATE TABLE node_api_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id uuid NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    token_prefix varchar(16) NOT NULL CHECK (btrim(token_prefix) <> ''),
    token_digest bytea NOT NULL UNIQUE CHECK (octet_length(token_digest) = 32),
    label text,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at timestamptz,
    revoked_at timestamptz,
    last_used_at timestamptz,
    CHECK (expires_at IS NULL OR expires_at > created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at),
    CHECK (last_used_at IS NULL OR last_used_at >= created_at)
);

CREATE INDEX node_api_tokens_node_active_idx
    ON node_api_tokens (node_id, created_at DESC)
    WHERE revoked_at IS NULL;

CREATE TABLE cpu_packages (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id uuid NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    hardware_key text NOT NULL CHECK (btrim(hardware_key) <> ''),
    package_index integer NOT NULL CHECK (package_index >= 0),
    vendor text,
    model_name text NOT NULL,
    physical_cores integer NOT NULL CHECK (physical_cores > 0),
    logical_threads integer NOT NULL CHECK (logical_threads >= physical_cores),
    performance_cores integer NOT NULL DEFAULT 0 CHECK (performance_cores >= 0),
    efficiency_cores integer NOT NULL DEFAULT 0 CHECK (efficiency_cores >= 0),
    max_frequency_mhz numeric(10, 2) CHECK (max_frequency_mhz IS NULL OR max_frequency_mhz >= 0),
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    removed_at timestamptz,
    UNIQUE (node_id, id),
    CHECK (last_seen_at >= first_seen_at),
    CHECK (removed_at IS NULL OR removed_at >= first_seen_at),
    CHECK (
        performance_cores + efficiency_cores = 0 OR
        performance_cores + efficiency_cores = physical_cores
    )
);

CREATE UNIQUE INDEX cpu_packages_active_key_uq
    ON cpu_packages (node_id, hardware_key)
    WHERE removed_at IS NULL;

CREATE TABLE memory_modules (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id uuid NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    hardware_key text NOT NULL CHECK (btrim(hardware_key) <> ''),
    slot_name text,
    manufacturer text,
    model_name text,
    part_number text,
    serial_number text,
    memory_type text,
    size_bytes bigint NOT NULL CHECK (size_bytes > 0),
    speed_mts integer CHECK (speed_mts IS NULL OR speed_mts > 0),
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    removed_at timestamptz,
    UNIQUE (node_id, id),
    CHECK (last_seen_at >= first_seen_at),
    CHECK (removed_at IS NULL OR removed_at >= first_seen_at)
);

CREATE UNIQUE INDEX memory_modules_active_key_uq
    ON memory_modules (node_id, hardware_key)
    WHERE removed_at IS NULL;

CREATE TABLE block_devices (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id uuid NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    hardware_key text NOT NULL CHECK (btrim(hardware_key) <> ''),
    device_name text NOT NULL,
    device_kind text NOT NULL CHECK (device_kind IN ('disk', 'raid', 'multipath', 'virtual')),
    vendor text,
    model_name text,
    serial_number text,
    wwn text,
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
    rotational boolean,
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    removed_at timestamptz,
    UNIQUE (node_id, id),
    CHECK (last_seen_at >= first_seen_at),
    CHECK (removed_at IS NULL OR removed_at >= first_seen_at)
);

CREATE UNIQUE INDEX block_devices_active_key_uq
    ON block_devices (node_id, hardware_key)
    WHERE removed_at IS NULL;

CREATE TABLE filesystems (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id uuid NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    filesystem_key text NOT NULL CHECK (btrim(filesystem_key) <> ''),
    filesystem_uuid text,
    device_name text NOT NULL,
    filesystem_type text NOT NULL,
    mount_point text NOT NULL CHECK (btrim(mount_point) <> ''),
    mount_options text[] NOT NULL DEFAULT ARRAY[]::text[],
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    removed_at timestamptz,
    UNIQUE (node_id, id),
    CHECK (last_seen_at >= first_seen_at),
    CHECK (removed_at IS NULL OR removed_at >= first_seen_at)
);

CREATE UNIQUE INDEX filesystems_active_key_uq
    ON filesystems (node_id, filesystem_key)
    WHERE removed_at IS NULL;

CREATE TABLE network_interfaces (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id uuid NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    interface_key text NOT NULL CHECK (btrim(interface_key) <> ''),
    interface_name text NOT NULL CHECK (btrim(interface_name) <> ''),
    mac_address macaddr,
    mtu integer CHECK (mtu IS NULL OR mtu > 0),
    link_speed_mbps integer CHECK (link_speed_mbps IS NULL OR link_speed_mbps >= 0),
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    removed_at timestamptz,
    UNIQUE (node_id, id),
    CHECK (last_seen_at >= first_seen_at),
    CHECK (removed_at IS NULL OR removed_at >= first_seen_at)
);

CREATE UNIQUE INDEX network_interfaces_active_key_uq
    ON network_interfaces (node_id, interface_key)
    WHERE removed_at IS NULL;

CREATE TABLE network_addresses (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id uuid NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    interface_id uuid NOT NULL,
    address inet NOT NULL,
    address_scope text NOT NULL DEFAULT 'unknown' CHECK (
        address_scope IN ('host', 'link', 'private', 'global', 'multicast', 'unknown')
    ),
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    removed_at timestamptz,
    FOREIGN KEY (node_id, interface_id)
        REFERENCES network_interfaces (node_id, id) ON DELETE CASCADE,
    CHECK (last_seen_at >= first_seen_at),
    CHECK (removed_at IS NULL OR removed_at >= first_seen_at)
);

CREATE UNIQUE INDEX network_addresses_active_key_uq
    ON network_addresses (node_id, interface_id, address)
    WHERE removed_at IS NULL;

CREATE TABLE node_metric_samples (
    bucket_at timestamptz NOT NULL,
    node_id uuid NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    collected_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    interval_seconds smallint NOT NULL CHECK (interval_seconds BETWEEN 1 AND 3600),
    cpu_usage_percent real NOT NULL CHECK (cpu_usage_percent BETWEEN 0 AND 100),
    load_1 real CHECK (load_1 IS NULL OR load_1 >= 0),
    load_5 real CHECK (load_5 IS NULL OR load_5 >= 0),
    load_15 real CHECK (load_15 IS NULL OR load_15 >= 0),
    memory_total_bytes bigint NOT NULL CHECK (memory_total_bytes >= 0),
    memory_used_bytes bigint NOT NULL CHECK (memory_used_bytes >= 0),
    memory_available_bytes bigint NOT NULL CHECK (memory_available_bytes >= 0),
    memory_cached_bytes bigint NOT NULL CHECK (memory_cached_bytes >= 0),
    memory_buffers_bytes bigint NOT NULL CHECK (memory_buffers_bytes >= 0),
    swap_total_bytes bigint NOT NULL CHECK (swap_total_bytes >= 0),
    swap_used_bytes bigint NOT NULL CHECK (swap_used_bytes >= 0),
    uptime_seconds bigint NOT NULL CHECK (uptime_seconds >= 0),
    PRIMARY KEY (bucket_at, node_id),
    CHECK (EXTRACT(SECOND FROM bucket_at) = 0),
    CHECK (memory_used_bytes <= memory_total_bytes),
    CHECK (memory_available_bytes <= memory_total_bytes),
    CHECK (memory_cached_bytes <= memory_total_bytes),
    CHECK (memory_buffers_bytes <= memory_total_bytes),
    CHECK (swap_used_bytes <= swap_total_bytes)
) PARTITION BY RANGE (bucket_at);

CREATE INDEX node_metric_samples_node_time_idx
    ON node_metric_samples (node_id, bucket_at DESC);
CREATE INDEX node_metric_samples_time_brin_idx
    ON node_metric_samples USING brin (bucket_at) WITH (pages_per_range = 64);

CREATE TABLE filesystem_metric_samples (
    bucket_at timestamptz NOT NULL,
    node_id uuid NOT NULL,
    filesystem_id uuid NOT NULL,
    total_bytes bigint NOT NULL CHECK (total_bytes >= 0),
    used_bytes bigint NOT NULL CHECK (used_bytes >= 0),
    available_bytes bigint NOT NULL CHECK (available_bytes >= 0),
    total_inodes bigint CHECK (total_inodes IS NULL OR total_inodes >= 0),
    used_inodes bigint CHECK (used_inodes IS NULL OR used_inodes >= 0),
    used_percent real GENERATED ALWAYS AS (
        CASE
            WHEN total_bytes = 0 THEN 0::real
            ELSE ((used_bytes::numeric * 100) / total_bytes)::real
        END
    ) STORED,
    PRIMARY KEY (bucket_at, node_id, filesystem_id),
    FOREIGN KEY (bucket_at, node_id)
        REFERENCES node_metric_samples (bucket_at, node_id) ON DELETE CASCADE,
    FOREIGN KEY (node_id, filesystem_id)
        REFERENCES filesystems (node_id, id),
    CHECK (used_bytes <= total_bytes),
    CHECK (available_bytes <= total_bytes),
    CHECK (used_inodes IS NULL OR total_inodes IS NULL OR used_inodes <= total_inodes)
) PARTITION BY RANGE (bucket_at);

CREATE INDEX filesystem_metric_samples_resource_time_idx
    ON filesystem_metric_samples (node_id, filesystem_id, bucket_at DESC);
CREATE INDEX filesystem_metric_samples_time_brin_idx
    ON filesystem_metric_samples USING brin (bucket_at) WITH (pages_per_range = 64);

CREATE TABLE network_metric_samples (
    bucket_at timestamptz NOT NULL,
    node_id uuid NOT NULL,
    interface_id uuid NOT NULL,
    link_up boolean NOT NULL,
    rx_bytes_total bigint NOT NULL CHECK (rx_bytes_total >= 0),
    tx_bytes_total bigint NOT NULL CHECK (tx_bytes_total >= 0),
    rx_bytes_delta bigint NOT NULL CHECK (rx_bytes_delta >= 0),
    tx_bytes_delta bigint NOT NULL CHECK (tx_bytes_delta >= 0),
    rx_packets_delta bigint NOT NULL CHECK (rx_packets_delta >= 0),
    tx_packets_delta bigint NOT NULL CHECK (tx_packets_delta >= 0),
    rx_errors_delta bigint NOT NULL CHECK (rx_errors_delta >= 0),
    tx_errors_delta bigint NOT NULL CHECK (tx_errors_delta >= 0),
    rx_dropped_delta bigint NOT NULL CHECK (rx_dropped_delta >= 0),
    tx_dropped_delta bigint NOT NULL CHECK (tx_dropped_delta >= 0),
    PRIMARY KEY (bucket_at, node_id, interface_id),
    FOREIGN KEY (bucket_at, node_id)
        REFERENCES node_metric_samples (bucket_at, node_id) ON DELETE CASCADE,
    FOREIGN KEY (node_id, interface_id)
        REFERENCES network_interfaces (node_id, id)
) PARTITION BY RANGE (bucket_at);

CREATE INDEX network_metric_samples_resource_time_idx
    ON network_metric_samples (node_id, interface_id, bucket_at DESC);
CREATE INDEX network_metric_samples_time_brin_idx
    ON network_metric_samples USING brin (bucket_at) WITH (pages_per_range = 64);

CREATE TABLE node_current_metrics (
    node_id uuid PRIMARY KEY REFERENCES nodes (id) ON DELETE CASCADE,
    bucket_at timestamptz NOT NULL,
    collected_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL,
    interval_seconds smallint NOT NULL CHECK (interval_seconds BETWEEN 1 AND 3600),
    cpu_usage_percent real NOT NULL CHECK (cpu_usage_percent BETWEEN 0 AND 100),
    load_1 real,
    load_5 real,
    load_15 real,
    memory_total_bytes bigint NOT NULL CHECK (memory_total_bytes >= 0),
    memory_used_bytes bigint NOT NULL CHECK (memory_used_bytes >= 0),
    memory_available_bytes bigint NOT NULL CHECK (memory_available_bytes >= 0),
    memory_cached_bytes bigint NOT NULL CHECK (memory_cached_bytes >= 0),
    memory_buffers_bytes bigint NOT NULL CHECK (memory_buffers_bytes >= 0),
    swap_total_bytes bigint NOT NULL CHECK (swap_total_bytes >= 0),
    swap_used_bytes bigint NOT NULL CHECK (swap_used_bytes >= 0),
    uptime_seconds bigint NOT NULL CHECK (uptime_seconds >= 0),
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (memory_used_bytes <= memory_total_bytes),
    CHECK (memory_available_bytes <= memory_total_bytes),
    CHECK (swap_used_bytes <= swap_total_bytes)
);

CREATE TABLE filesystem_current_metrics (
    node_id uuid NOT NULL,
    filesystem_id uuid NOT NULL,
    bucket_at timestamptz NOT NULL,
    total_bytes bigint NOT NULL CHECK (total_bytes >= 0),
    used_bytes bigint NOT NULL CHECK (used_bytes >= 0),
    available_bytes bigint NOT NULL CHECK (available_bytes >= 0),
    total_inodes bigint CHECK (total_inodes IS NULL OR total_inodes >= 0),
    used_inodes bigint CHECK (used_inodes IS NULL OR used_inodes >= 0),
    used_percent real GENERATED ALWAYS AS (
        CASE
            WHEN total_bytes = 0 THEN 0::real
            ELSE ((used_bytes::numeric * 100) / total_bytes)::real
        END
    ) STORED,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (node_id, filesystem_id),
    FOREIGN KEY (node_id, filesystem_id)
        REFERENCES filesystems (node_id, id) ON DELETE CASCADE,
    CHECK (used_bytes <= total_bytes),
    CHECK (available_bytes <= total_bytes),
    CHECK (used_inodes IS NULL OR total_inodes IS NULL OR used_inodes <= total_inodes)
);

CREATE TABLE network_current_metrics (
    node_id uuid NOT NULL,
    interface_id uuid NOT NULL,
    bucket_at timestamptz NOT NULL,
    interval_seconds smallint NOT NULL CHECK (interval_seconds BETWEEN 1 AND 3600),
    link_up boolean NOT NULL,
    rx_bytes_total bigint NOT NULL CHECK (rx_bytes_total >= 0),
    tx_bytes_total bigint NOT NULL CHECK (tx_bytes_total >= 0),
    rx_bytes_delta bigint NOT NULL CHECK (rx_bytes_delta >= 0),
    tx_bytes_delta bigint NOT NULL CHECK (tx_bytes_delta >= 0),
    rx_packets_delta bigint NOT NULL CHECK (rx_packets_delta >= 0),
    tx_packets_delta bigint NOT NULL CHECK (tx_packets_delta >= 0),
    rx_errors_delta bigint NOT NULL CHECK (rx_errors_delta >= 0),
    tx_errors_delta bigint NOT NULL CHECK (tx_errors_delta >= 0),
    rx_dropped_delta bigint NOT NULL CHECK (rx_dropped_delta >= 0),
    tx_dropped_delta bigint NOT NULL CHECK (tx_dropped_delta >= 0),
    rx_bits_per_second numeric(20, 2) GENERATED ALWAYS AS (
        (rx_bytes_delta::numeric * 8) / interval_seconds
    ) STORED,
    tx_bits_per_second numeric(20, 2) GENERATED ALWAYS AS (
        (tx_bytes_delta::numeric * 8) / interval_seconds
    ) STORED,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (node_id, interface_id),
    FOREIGN KEY (node_id, interface_id)
        REFERENCES network_interfaces (node_id, id) ON DELETE CASCADE
);

CREATE TABLE node_metric_hourly (
    hour_at timestamptz NOT NULL,
    node_id uuid NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    sample_count integer NOT NULL CHECK (sample_count > 0),
    cpu_usage_avg real NOT NULL CHECK (cpu_usage_avg BETWEEN 0 AND 100),
    cpu_usage_max real NOT NULL CHECK (cpu_usage_max BETWEEN 0 AND 100),
    load_1_avg real,
    load_1_max real,
    memory_used_bytes_avg bigint NOT NULL CHECK (memory_used_bytes_avg >= 0),
    memory_used_bytes_max bigint NOT NULL CHECK (memory_used_bytes_max >= 0),
    memory_usage_avg real NOT NULL CHECK (memory_usage_avg BETWEEN 0 AND 100),
    memory_usage_max real NOT NULL CHECK (memory_usage_max BETWEEN 0 AND 100),
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (hour_at, node_id),
    CHECK (EXTRACT(MINUTE FROM hour_at) = 0 AND EXTRACT(SECOND FROM hour_at) = 0)
) PARTITION BY RANGE (hour_at);

CREATE INDEX node_metric_hourly_node_time_idx
    ON node_metric_hourly (node_id, hour_at DESC);

CREATE TABLE filesystem_metric_hourly (
    hour_at timestamptz NOT NULL,
    node_id uuid NOT NULL,
    filesystem_id uuid NOT NULL,
    sample_count integer NOT NULL CHECK (sample_count > 0),
    usage_avg real NOT NULL CHECK (usage_avg BETWEEN 0 AND 100),
    usage_max real NOT NULL CHECK (usage_max BETWEEN 0 AND 100),
    used_bytes_max bigint NOT NULL CHECK (used_bytes_max >= 0),
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (hour_at, node_id, filesystem_id),
    FOREIGN KEY (node_id, filesystem_id)
        REFERENCES filesystems (node_id, id)
) PARTITION BY RANGE (hour_at);

CREATE INDEX filesystem_metric_hourly_resource_time_idx
    ON filesystem_metric_hourly (node_id, filesystem_id, hour_at DESC);

CREATE TABLE network_metric_hourly (
    hour_at timestamptz NOT NULL,
    node_id uuid NOT NULL,
    interface_id uuid NOT NULL,
    sample_count integer NOT NULL CHECK (sample_count > 0),
    rx_bytes bigint NOT NULL CHECK (rx_bytes >= 0),
    tx_bytes bigint NOT NULL CHECK (tx_bytes >= 0),
    rx_bits_per_second_avg numeric(20, 2) NOT NULL CHECK (rx_bits_per_second_avg >= 0),
    rx_bits_per_second_max numeric(20, 2) NOT NULL CHECK (rx_bits_per_second_max >= 0),
    tx_bits_per_second_avg numeric(20, 2) NOT NULL CHECK (tx_bits_per_second_avg >= 0),
    tx_bits_per_second_max numeric(20, 2) NOT NULL CHECK (tx_bits_per_second_max >= 0),
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (hour_at, node_id, interface_id),
    FOREIGN KEY (node_id, interface_id)
        REFERENCES network_interfaces (node_id, id)
) PARTITION BY RANGE (hour_at);

CREATE INDEX network_metric_hourly_resource_time_idx
    ON network_metric_hourly (node_id, interface_id, hour_at DESC);

CREATE FUNCTION sync_node_current_metrics()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, monitoring
AS $function$
BEGIN
    INSERT INTO monitoring.node_current_metrics (
        node_id, bucket_at, collected_at, received_at, interval_seconds,
        cpu_usage_percent, load_1, load_5, load_15,
        memory_total_bytes, memory_used_bytes, memory_available_bytes,
        memory_cached_bytes, memory_buffers_bytes,
        swap_total_bytes, swap_used_bytes, uptime_seconds, updated_at
    ) VALUES (
        NEW.node_id, NEW.bucket_at, NEW.collected_at, NEW.received_at, NEW.interval_seconds,
        NEW.cpu_usage_percent, NEW.load_1, NEW.load_5, NEW.load_15,
        NEW.memory_total_bytes, NEW.memory_used_bytes, NEW.memory_available_bytes,
        NEW.memory_cached_bytes, NEW.memory_buffers_bytes,
        NEW.swap_total_bytes, NEW.swap_used_bytes, NEW.uptime_seconds, CURRENT_TIMESTAMP
    )
    ON CONFLICT (node_id) DO UPDATE SET
        bucket_at = EXCLUDED.bucket_at,
        collected_at = EXCLUDED.collected_at,
        received_at = EXCLUDED.received_at,
        interval_seconds = EXCLUDED.interval_seconds,
        cpu_usage_percent = EXCLUDED.cpu_usage_percent,
        load_1 = EXCLUDED.load_1,
        load_5 = EXCLUDED.load_5,
        load_15 = EXCLUDED.load_15,
        memory_total_bytes = EXCLUDED.memory_total_bytes,
        memory_used_bytes = EXCLUDED.memory_used_bytes,
        memory_available_bytes = EXCLUDED.memory_available_bytes,
        memory_cached_bytes = EXCLUDED.memory_cached_bytes,
        memory_buffers_bytes = EXCLUDED.memory_buffers_bytes,
        swap_total_bytes = EXCLUDED.swap_total_bytes,
        swap_used_bytes = EXCLUDED.swap_used_bytes,
        uptime_seconds = EXCLUDED.uptime_seconds,
        updated_at = CURRENT_TIMESTAMP
    WHERE EXCLUDED.bucket_at >= node_current_metrics.bucket_at;

    RETURN NEW;
END
$function$;

CREATE TRIGGER node_metric_samples_sync_current
AFTER INSERT OR UPDATE ON node_metric_samples
FOR EACH ROW EXECUTE FUNCTION sync_node_current_metrics();

CREATE FUNCTION sync_filesystem_current_metrics()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, monitoring
AS $function$
BEGIN
    INSERT INTO monitoring.filesystem_current_metrics (
        node_id, filesystem_id, bucket_at, total_bytes, used_bytes,
        available_bytes, total_inodes, used_inodes, updated_at
    ) VALUES (
        NEW.node_id, NEW.filesystem_id, NEW.bucket_at, NEW.total_bytes, NEW.used_bytes,
        NEW.available_bytes, NEW.total_inodes, NEW.used_inodes, CURRENT_TIMESTAMP
    )
    ON CONFLICT (node_id, filesystem_id) DO UPDATE SET
        bucket_at = EXCLUDED.bucket_at,
        total_bytes = EXCLUDED.total_bytes,
        used_bytes = EXCLUDED.used_bytes,
        available_bytes = EXCLUDED.available_bytes,
        total_inodes = EXCLUDED.total_inodes,
        used_inodes = EXCLUDED.used_inodes,
        updated_at = CURRENT_TIMESTAMP
    WHERE EXCLUDED.bucket_at >= filesystem_current_metrics.bucket_at;

    RETURN NEW;
END
$function$;

CREATE TRIGGER filesystem_metric_samples_sync_current
AFTER INSERT OR UPDATE ON filesystem_metric_samples
FOR EACH ROW EXECUTE FUNCTION sync_filesystem_current_metrics();

CREATE FUNCTION sync_network_current_metrics()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, monitoring
AS $function$
DECLARE
    v_interval_seconds smallint;
BEGIN
    SELECT interval_seconds
      INTO v_interval_seconds
      FROM monitoring.node_metric_samples
     WHERE bucket_at = NEW.bucket_at
       AND node_id = NEW.node_id;

    INSERT INTO monitoring.network_current_metrics (
        node_id, interface_id, bucket_at, interval_seconds, link_up,
        rx_bytes_total, tx_bytes_total, rx_bytes_delta, tx_bytes_delta,
        rx_packets_delta, tx_packets_delta, rx_errors_delta, tx_errors_delta,
        rx_dropped_delta, tx_dropped_delta, updated_at
    ) VALUES (
        NEW.node_id, NEW.interface_id, NEW.bucket_at, v_interval_seconds, NEW.link_up,
        NEW.rx_bytes_total, NEW.tx_bytes_total, NEW.rx_bytes_delta, NEW.tx_bytes_delta,
        NEW.rx_packets_delta, NEW.tx_packets_delta, NEW.rx_errors_delta, NEW.tx_errors_delta,
        NEW.rx_dropped_delta, NEW.tx_dropped_delta, CURRENT_TIMESTAMP
    )
    ON CONFLICT (node_id, interface_id) DO UPDATE SET
        bucket_at = EXCLUDED.bucket_at,
        interval_seconds = EXCLUDED.interval_seconds,
        link_up = EXCLUDED.link_up,
        rx_bytes_total = EXCLUDED.rx_bytes_total,
        tx_bytes_total = EXCLUDED.tx_bytes_total,
        rx_bytes_delta = EXCLUDED.rx_bytes_delta,
        tx_bytes_delta = EXCLUDED.tx_bytes_delta,
        rx_packets_delta = EXCLUDED.rx_packets_delta,
        tx_packets_delta = EXCLUDED.tx_packets_delta,
        rx_errors_delta = EXCLUDED.rx_errors_delta,
        tx_errors_delta = EXCLUDED.tx_errors_delta,
        rx_dropped_delta = EXCLUDED.rx_dropped_delta,
        tx_dropped_delta = EXCLUDED.tx_dropped_delta,
        updated_at = CURRENT_TIMESTAMP
    WHERE EXCLUDED.bucket_at >= network_current_metrics.bucket_at;

    RETURN NEW;
END
$function$;

CREATE TRIGGER network_metric_samples_sync_current
AFTER INSERT OR UPDATE ON network_metric_samples
FOR EACH ROW EXECUTE FUNCTION sync_network_current_metrics();

CREATE FUNCTION ensure_raw_partitions(p_start_date date, p_end_date date)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, monitoring
SET timezone = 'UTC'
AS $function$
DECLARE
    v_date date;
    v_start timestamptz;
    v_end timestamptz;
    v_suffix text;
BEGIN
    IF p_start_date IS NULL OR p_end_date IS NULL OR p_end_date < p_start_date THEN
        RAISE EXCEPTION 'invalid raw partition range: % to %', p_start_date, p_end_date;
    END IF;

    v_date := p_start_date;
    WHILE v_date <= p_end_date LOOP
        v_start := v_date::timestamp AT TIME ZONE 'UTC';
        v_end := (v_date + 1)::timestamp AT TIME ZONE 'UTC';
        v_suffix := to_char(v_date, 'YYYYMMDD');

        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS monitoring.%I PARTITION OF monitoring.node_metric_samples FOR VALUES FROM (%L) TO (%L)',
            'node_metric_samples_p' || v_suffix, v_start, v_end
        );
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS monitoring.%I PARTITION OF monitoring.filesystem_metric_samples FOR VALUES FROM (%L) TO (%L)',
            'filesystem_metric_samples_p' || v_suffix, v_start, v_end
        );
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS monitoring.%I PARTITION OF monitoring.network_metric_samples FOR VALUES FROM (%L) TO (%L)',
            'network_metric_samples_p' || v_suffix, v_start, v_end
        );

        v_date := v_date + 1;
    END LOOP;
END
$function$;

CREATE FUNCTION ensure_hourly_partitions(p_start_month date, p_end_month date)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, monitoring
SET timezone = 'UTC'
AS $function$
DECLARE
    v_month date;
    v_last_month date;
    v_start timestamptz;
    v_end timestamptz;
    v_suffix text;
BEGIN
    IF p_start_month IS NULL OR p_end_month IS NULL THEN
        RAISE EXCEPTION 'hourly partition range cannot be null';
    END IF;

    v_month := date_trunc('month', p_start_month)::date;
    v_last_month := date_trunc('month', p_end_month)::date;
    IF v_last_month < v_month THEN
        RAISE EXCEPTION 'invalid hourly partition range: % to %', p_start_month, p_end_month;
    END IF;

    WHILE v_month <= v_last_month LOOP
        v_start := v_month::timestamp AT TIME ZONE 'UTC';
        v_end := (v_month + INTERVAL '1 month')::timestamp AT TIME ZONE 'UTC';
        v_suffix := to_char(v_month, 'YYYYMM');

        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS monitoring.%I PARTITION OF monitoring.node_metric_hourly FOR VALUES FROM (%L) TO (%L)',
            'node_metric_hourly_p' || v_suffix, v_start, v_end
        );
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS monitoring.%I PARTITION OF monitoring.filesystem_metric_hourly FOR VALUES FROM (%L) TO (%L)',
            'filesystem_metric_hourly_p' || v_suffix, v_start, v_end
        );
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS monitoring.%I PARTITION OF monitoring.network_metric_hourly FOR VALUES FROM (%L) TO (%L)',
            'network_metric_hourly_p' || v_suffix, v_start, v_end
        );

        v_month := (v_month + INTERVAL '1 month')::date;
    END LOOP;
END
$function$;

CREATE FUNCTION drop_expired_partitions(p_reference_at timestamptz DEFAULT CURRENT_TIMESTAMP)
RETURNS TABLE (dropped_partition text)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, monitoring
SET timezone = 'UTC'
AS $function$
DECLARE
    v_raw_cutoff date := (p_reference_at AT TIME ZONE 'UTC')::date - 90;
    v_hourly_cutoff date := (
        date_trunc('month', p_reference_at AT TIME ZONE 'UTC') - INTERVAL '24 months'
    )::date;
    v_record record;
    v_suffix text;
    v_partition_date date;
BEGIN
    FOR v_record IN
        SELECT child.relname AS child_name, parent.relname AS parent_name
          FROM pg_catalog.pg_inherits inheritance
          JOIN pg_catalog.pg_class child ON child.oid = inheritance.inhrelid
          JOIN pg_catalog.pg_class parent ON parent.oid = inheritance.inhparent
          JOIN pg_catalog.pg_namespace namespace ON namespace.oid = child.relnamespace
         WHERE namespace.nspname = 'monitoring'
           AND parent.relname IN (
               'filesystem_metric_samples', 'network_metric_samples', 'node_metric_samples',
               'filesystem_metric_hourly', 'network_metric_hourly', 'node_metric_hourly'
           )
         ORDER BY CASE parent.relname
             WHEN 'filesystem_metric_samples' THEN 1
             WHEN 'network_metric_samples' THEN 2
             WHEN 'node_metric_samples' THEN 3
             ELSE 4
         END
    LOOP
        IF v_record.parent_name LIKE '%_samples' THEN
            v_suffix := substring(v_record.child_name FROM 'p([0-9]{8})$');
            IF v_suffix IS NOT NULL THEN
                v_partition_date := to_date(v_suffix, 'YYYYMMDD');
                IF v_partition_date < v_raw_cutoff THEN
                    EXECUTE format(
                        'ALTER TABLE monitoring.%I DETACH PARTITION monitoring.%I',
                        v_record.parent_name, v_record.child_name
                    );
                    EXECUTE format('DROP TABLE monitoring.%I', v_record.child_name);
                    dropped_partition := v_record.child_name;
                    RETURN NEXT;
                END IF;
            END IF;
        ELSE
            v_suffix := substring(v_record.child_name FROM 'p([0-9]{6})$');
            IF v_suffix IS NOT NULL THEN
                v_partition_date := to_date(v_suffix || '01', 'YYYYMMDD');
                IF v_partition_date < v_hourly_cutoff THEN
                    EXECUTE format(
                        'ALTER TABLE monitoring.%I DETACH PARTITION monitoring.%I',
                        v_record.parent_name, v_record.child_name
                    );
                    EXECUTE format('DROP TABLE monitoring.%I', v_record.child_name);
                    dropped_partition := v_record.child_name;
                    RETURN NEXT;
                END IF;
            END IF;
        END IF;
    END LOOP;
END
$function$;

CREATE FUNCTION maintain_partitions(p_reference_at timestamptz DEFAULT CURRENT_TIMESTAMP)
RETURNS TABLE (dropped_partition text)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, monitoring
SET timezone = 'UTC'
AS $function$
DECLARE
    v_reference_date date := (p_reference_at AT TIME ZONE 'UTC')::date;
    v_reference_month date := date_trunc('month', p_reference_at AT TIME ZONE 'UTC')::date;
BEGIN
    PERFORM monitoring.ensure_raw_partitions(v_reference_date - 1, v_reference_date + 7);
    PERFORM monitoring.ensure_hourly_partitions(
        (v_reference_month - INTERVAL '1 month')::date,
        (v_reference_month + INTERVAL '2 months')::date
    );

    RETURN QUERY SELECT * FROM monitoring.drop_expired_partitions(p_reference_at);
END
$function$;

CREATE FUNCTION rollup_hour(p_hour_at timestamptz)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, monitoring
SET timezone = 'UTC'
AS $function$
DECLARE
    v_hour timestamptz := date_trunc('hour', p_hour_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC';
    v_hour_end timestamptz := v_hour + INTERVAL '1 hour';
BEGIN
    PERFORM monitoring.ensure_hourly_partitions(
        (v_hour AT TIME ZONE 'UTC')::date,
        (v_hour AT TIME ZONE 'UTC')::date
    );

    INSERT INTO monitoring.node_metric_hourly (
        hour_at, node_id, sample_count,
        cpu_usage_avg, cpu_usage_max, load_1_avg, load_1_max,
        memory_used_bytes_avg, memory_used_bytes_max,
        memory_usage_avg, memory_usage_max, updated_at
    )
    SELECT
        v_hour,
        sample.node_id,
        count(*)::integer,
        avg(sample.cpu_usage_percent)::real,
        max(sample.cpu_usage_percent)::real,
        avg(sample.load_1)::real,
        max(sample.load_1)::real,
        round(avg(sample.memory_used_bytes))::bigint,
        max(sample.memory_used_bytes),
        avg(CASE WHEN sample.memory_total_bytes = 0 THEN 0
                 ELSE sample.memory_used_bytes::numeric * 100 / sample.memory_total_bytes END)::real,
        max(CASE WHEN sample.memory_total_bytes = 0 THEN 0
                 ELSE sample.memory_used_bytes::numeric * 100 / sample.memory_total_bytes END)::real,
        CURRENT_TIMESTAMP
      FROM monitoring.node_metric_samples sample
     WHERE sample.bucket_at >= v_hour
       AND sample.bucket_at < v_hour_end
     GROUP BY sample.node_id
    ON CONFLICT (hour_at, node_id) DO UPDATE SET
        sample_count = EXCLUDED.sample_count,
        cpu_usage_avg = EXCLUDED.cpu_usage_avg,
        cpu_usage_max = EXCLUDED.cpu_usage_max,
        load_1_avg = EXCLUDED.load_1_avg,
        load_1_max = EXCLUDED.load_1_max,
        memory_used_bytes_avg = EXCLUDED.memory_used_bytes_avg,
        memory_used_bytes_max = EXCLUDED.memory_used_bytes_max,
        memory_usage_avg = EXCLUDED.memory_usage_avg,
        memory_usage_max = EXCLUDED.memory_usage_max,
        updated_at = CURRENT_TIMESTAMP;

    DELETE FROM monitoring.node_metric_hourly hourly
     WHERE hourly.hour_at = v_hour
       AND NOT EXISTS (
           SELECT 1
             FROM monitoring.node_metric_samples sample
            WHERE sample.bucket_at >= v_hour
              AND sample.bucket_at < v_hour_end
              AND sample.node_id = hourly.node_id
       );

    INSERT INTO monitoring.filesystem_metric_hourly (
        hour_at, node_id, filesystem_id, sample_count,
        usage_avg, usage_max, used_bytes_max, updated_at
    )
    SELECT
        v_hour,
        sample.node_id,
        sample.filesystem_id,
        count(*)::integer,
        avg(sample.used_percent)::real,
        max(sample.used_percent)::real,
        max(sample.used_bytes),
        CURRENT_TIMESTAMP
      FROM monitoring.filesystem_metric_samples sample
     WHERE sample.bucket_at >= v_hour
       AND sample.bucket_at < v_hour_end
     GROUP BY sample.node_id, sample.filesystem_id
    ON CONFLICT (hour_at, node_id, filesystem_id) DO UPDATE SET
        sample_count = EXCLUDED.sample_count,
        usage_avg = EXCLUDED.usage_avg,
        usage_max = EXCLUDED.usage_max,
        used_bytes_max = EXCLUDED.used_bytes_max,
        updated_at = CURRENT_TIMESTAMP;

    DELETE FROM monitoring.filesystem_metric_hourly hourly
     WHERE hourly.hour_at = v_hour
       AND NOT EXISTS (
           SELECT 1
             FROM monitoring.filesystem_metric_samples sample
            WHERE sample.bucket_at >= v_hour
              AND sample.bucket_at < v_hour_end
              AND sample.node_id = hourly.node_id
              AND sample.filesystem_id = hourly.filesystem_id
       );

    INSERT INTO monitoring.network_metric_hourly (
        hour_at, node_id, interface_id, sample_count,
        rx_bytes, tx_bytes,
        rx_bits_per_second_avg, rx_bits_per_second_max,
        tx_bits_per_second_avg, tx_bits_per_second_max, updated_at
    )
    SELECT
        v_hour,
        network.node_id,
        network.interface_id,
        count(*)::integer,
        sum(network.rx_bytes_delta)::bigint,
        sum(network.tx_bytes_delta)::bigint,
        avg(network.rx_bytes_delta::numeric * 8 / node.interval_seconds),
        max(network.rx_bytes_delta::numeric * 8 / node.interval_seconds),
        avg(network.tx_bytes_delta::numeric * 8 / node.interval_seconds),
        max(network.tx_bytes_delta::numeric * 8 / node.interval_seconds),
        CURRENT_TIMESTAMP
      FROM monitoring.network_metric_samples network
      JOIN monitoring.node_metric_samples node
        ON node.bucket_at = network.bucket_at
       AND node.node_id = network.node_id
     WHERE network.bucket_at >= v_hour
       AND network.bucket_at < v_hour_end
     GROUP BY network.node_id, network.interface_id
    ON CONFLICT (hour_at, node_id, interface_id) DO UPDATE SET
        sample_count = EXCLUDED.sample_count,
        rx_bytes = EXCLUDED.rx_bytes,
        tx_bytes = EXCLUDED.tx_bytes,
        rx_bits_per_second_avg = EXCLUDED.rx_bits_per_second_avg,
        rx_bits_per_second_max = EXCLUDED.rx_bits_per_second_max,
        tx_bits_per_second_avg = EXCLUDED.tx_bits_per_second_avg,
        tx_bits_per_second_max = EXCLUDED.tx_bits_per_second_max,
        updated_at = CURRENT_TIMESTAMP;

    DELETE FROM monitoring.network_metric_hourly hourly
     WHERE hourly.hour_at = v_hour
       AND NOT EXISTS (
           SELECT 1
             FROM monitoring.network_metric_samples sample
            WHERE sample.bucket_at >= v_hour
              AND sample.bucket_at < v_hour_end
              AND sample.node_id = hourly.node_id
              AND sample.interface_id = hourly.interface_id
       );
END
$function$;

CREATE VIEW v_node_status
WITH (security_invoker = true)
AS
SELECT
    node.id AS node_id,
    node.agent_id,
    node.hostname,
    node.display_name,
    node.os_name,
    node.os_version,
    node.architecture,
    node.agent_version,
    node.labels,
    node.last_seen_at,
    CASE
        WHEN node.disabled_at IS NOT NULL THEN 'disabled'
        WHEN node.last_seen_at >= CURRENT_TIMESTAMP - INTERVAL '3 minutes' THEN 'online'
        ELSE 'offline'
    END AS status,
    EXTRACT(EPOCH FROM (CURRENT_TIMESTAMP - node.last_seen_at))::bigint AS seconds_since_last_seen,
    metrics.bucket_at AS latest_bucket_at,
    metrics.cpu_usage_percent,
    metrics.load_1,
    metrics.load_5,
    metrics.load_15,
    metrics.memory_total_bytes,
    metrics.memory_used_bytes,
    metrics.memory_available_bytes,
    CASE
        WHEN metrics.memory_total_bytes IS NULL OR metrics.memory_total_bytes = 0 THEN NULL
        ELSE (metrics.memory_used_bytes::numeric * 100 / metrics.memory_total_bytes)::real
    END AS memory_usage_percent,
    metrics.swap_total_bytes,
    metrics.swap_used_bytes,
    metrics.uptime_seconds
  FROM monitoring.nodes node
  LEFT JOIN monitoring.node_current_metrics metrics ON metrics.node_id = node.id;

CREATE VIEW v_node_hardware_summary
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
), disk AS (
    SELECT
        node_id,
        count(*)::integer AS disk_count,
        sum(size_bytes)::bigint AS total_bytes,
        array_agg(DISTINCT model_name ORDER BY model_name)
            FILTER (WHERE model_name IS NOT NULL) AS models
      FROM monitoring.block_devices
     WHERE removed_at IS NULL
       AND device_kind = 'disk'
     GROUP BY node_id
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

CREATE VIEW v_filesystem_status
WITH (security_invoker = true)
AS
SELECT
    filesystem.node_id,
    filesystem.id AS filesystem_id,
    filesystem.device_name,
    filesystem.filesystem_type,
    filesystem.mount_point,
    metrics.bucket_at,
    metrics.total_bytes,
    metrics.used_bytes,
    metrics.available_bytes,
    metrics.used_percent,
    metrics.total_inodes,
    metrics.used_inodes
  FROM monitoring.filesystems filesystem
  LEFT JOIN monitoring.filesystem_current_metrics metrics
    ON metrics.node_id = filesystem.node_id
   AND metrics.filesystem_id = filesystem.id
 WHERE filesystem.removed_at IS NULL;

CREATE VIEW v_network_status
WITH (security_invoker = true)
AS
SELECT
    interface.node_id,
    interface.id AS interface_id,
    interface.interface_name,
    interface.mac_address,
    interface.mtu,
    interface.link_speed_mbps,
    address.addresses,
    metrics.bucket_at,
    metrics.link_up,
    metrics.rx_bytes_total,
    metrics.tx_bytes_total,
    metrics.rx_bytes_delta,
    metrics.tx_bytes_delta,
    metrics.rx_bits_per_second,
    metrics.tx_bits_per_second,
    metrics.rx_errors_delta,
    metrics.tx_errors_delta,
    metrics.rx_dropped_delta,
    metrics.tx_dropped_delta
  FROM monitoring.network_interfaces interface
  LEFT JOIN monitoring.network_current_metrics metrics
    ON metrics.node_id = interface.node_id
   AND metrics.interface_id = interface.id
  LEFT JOIN LATERAL (
      SELECT array_agg(network_address.address::text ORDER BY network_address.address::text) AS addresses
        FROM monitoring.network_addresses network_address
       WHERE network_address.node_id = interface.node_id
         AND network_address.interface_id = interface.id
         AND network_address.removed_at IS NULL
  ) address ON true
 WHERE interface.removed_at IS NULL;

COMMENT ON SCHEMA monitoring IS 'Server inventory, authentication, raw metrics, current state, and hourly rollups.';
COMMENT ON TABLE node_metric_samples IS 'One idempotent node snapshot per UTC minute; raw data retained for 90 days.';
COMMENT ON TABLE filesystem_metric_samples IS 'Filesystem capacity samples; usage belongs to mounts, not physical disks.';
COMMENT ON TABLE node_api_tokens IS 'Only SHA-256 digests of high-entropy node bearer tokens are stored.';

SELECT monitoring.maintain_partitions(CURRENT_TIMESTAMP);

INSERT INTO schema_migrations (version, description)
VALUES ('V001', 'Initial server monitoring schema');

RESET ROLE;

GRANT USAGE ON SCHEMA monitoring TO server_status_writer, server_status_reader;

GRANT SELECT ON ALL TABLES IN SCHEMA monitoring TO server_status_writer, server_status_reader;

GRANT INSERT, UPDATE, DELETE ON TABLE
    monitoring.nodes,
    monitoring.node_api_tokens,
    monitoring.cpu_packages,
    monitoring.memory_modules,
    monitoring.block_devices,
    monitoring.filesystems,
    monitoring.network_interfaces,
    monitoring.network_addresses,
    monitoring.node_metric_samples,
    monitoring.filesystem_metric_samples,
    monitoring.network_metric_samples,
    monitoring.node_current_metrics,
    monitoring.filesystem_current_metrics,
    monitoring.network_current_metrics
TO server_status_writer;

REVOKE ALL ON ALL FUNCTIONS IN SCHEMA monitoring FROM PUBLIC;
GRANT EXECUTE ON FUNCTION monitoring.ensure_raw_partitions(date, date) TO server_status_writer;
GRANT EXECUTE ON FUNCTION monitoring.ensure_hourly_partitions(date, date) TO server_status_writer;
GRANT EXECUTE ON FUNCTION monitoring.drop_expired_partitions(timestamptz) TO server_status_writer;
GRANT EXECUTE ON FUNCTION monitoring.maintain_partitions(timestamptz) TO server_status_writer;
GRANT EXECUTE ON FUNCTION monitoring.rollup_hour(timestamptz) TO server_status_writer;

ALTER DEFAULT PRIVILEGES FOR ROLE server_status_owner IN SCHEMA monitoring
    GRANT SELECT ON TABLES TO server_status_reader, server_status_writer;
ALTER DEFAULT PRIVILEGES FOR ROLE server_status_owner IN SCHEMA monitoring
    REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC;

COMMIT;
