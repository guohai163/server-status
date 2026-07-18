BEGIN;

SET LOCAL TIME ZONE 'UTC';
SET ROLE server_status_owner;
SET search_path = monitoring, pg_catalog;

ALTER TABLE node_metric_samples
    ADD COLUMN disk_read_bytes_total bigint NOT NULL DEFAULT 0 CHECK (disk_read_bytes_total >= 0),
    ADD COLUMN disk_write_bytes_total bigint NOT NULL DEFAULT 0 CHECK (disk_write_bytes_total >= 0),
    ADD COLUMN disk_read_bytes_delta bigint NOT NULL DEFAULT 0 CHECK (disk_read_bytes_delta >= 0),
    ADD COLUMN disk_write_bytes_delta bigint NOT NULL DEFAULT 0 CHECK (disk_write_bytes_delta >= 0),
    ADD COLUMN disk_read_ops_delta bigint NOT NULL DEFAULT 0 CHECK (disk_read_ops_delta >= 0),
    ADD COLUMN disk_write_ops_delta bigint NOT NULL DEFAULT 0 CHECK (disk_write_ops_delta >= 0);

ALTER TABLE node_current_metrics
    ADD COLUMN disk_read_bytes_total bigint NOT NULL DEFAULT 0 CHECK (disk_read_bytes_total >= 0),
    ADD COLUMN disk_write_bytes_total bigint NOT NULL DEFAULT 0 CHECK (disk_write_bytes_total >= 0),
    ADD COLUMN disk_read_bytes_delta bigint NOT NULL DEFAULT 0 CHECK (disk_read_bytes_delta >= 0),
    ADD COLUMN disk_write_bytes_delta bigint NOT NULL DEFAULT 0 CHECK (disk_write_bytes_delta >= 0),
    ADD COLUMN disk_read_ops_delta bigint NOT NULL DEFAULT 0 CHECK (disk_read_ops_delta >= 0),
    ADD COLUMN disk_write_ops_delta bigint NOT NULL DEFAULT 0 CHECK (disk_write_ops_delta >= 0),
    ADD COLUMN disk_read_bytes_per_second numeric(20, 2) GENERATED ALWAYS AS (
        disk_read_bytes_delta::numeric / interval_seconds
    ) STORED,
    ADD COLUMN disk_write_bytes_per_second numeric(20, 2) GENERATED ALWAYS AS (
        disk_write_bytes_delta::numeric / interval_seconds
    ) STORED;

ALTER TABLE node_metric_hourly
    ADD COLUMN disk_read_bytes bigint NOT NULL DEFAULT 0 CHECK (disk_read_bytes >= 0),
    ADD COLUMN disk_write_bytes bigint NOT NULL DEFAULT 0 CHECK (disk_write_bytes >= 0),
    ADD COLUMN disk_read_bytes_per_second_avg numeric(20, 2) NOT NULL DEFAULT 0 CHECK (disk_read_bytes_per_second_avg >= 0),
    ADD COLUMN disk_read_bytes_per_second_max numeric(20, 2) NOT NULL DEFAULT 0 CHECK (disk_read_bytes_per_second_max >= 0),
    ADD COLUMN disk_write_bytes_per_second_avg numeric(20, 2) NOT NULL DEFAULT 0 CHECK (disk_write_bytes_per_second_avg >= 0),
    ADD COLUMN disk_write_bytes_per_second_max numeric(20, 2) NOT NULL DEFAULT 0 CHECK (disk_write_bytes_per_second_max >= 0);

CREATE OR REPLACE FUNCTION sync_node_current_metrics()
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
        swap_total_bytes, swap_used_bytes, uptime_seconds,
        disk_read_bytes_total, disk_write_bytes_total,
        disk_read_bytes_delta, disk_write_bytes_delta,
        disk_read_ops_delta, disk_write_ops_delta, updated_at
    ) VALUES (
        NEW.node_id, NEW.bucket_at, NEW.collected_at, NEW.received_at, NEW.interval_seconds,
        NEW.cpu_usage_percent, NEW.load_1, NEW.load_5, NEW.load_15,
        NEW.memory_total_bytes, NEW.memory_used_bytes, NEW.memory_available_bytes,
        NEW.memory_cached_bytes, NEW.memory_buffers_bytes,
        NEW.swap_total_bytes, NEW.swap_used_bytes, NEW.uptime_seconds,
        NEW.disk_read_bytes_total, NEW.disk_write_bytes_total,
        NEW.disk_read_bytes_delta, NEW.disk_write_bytes_delta,
        NEW.disk_read_ops_delta, NEW.disk_write_ops_delta, CURRENT_TIMESTAMP
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
        disk_read_bytes_total = EXCLUDED.disk_read_bytes_total,
        disk_write_bytes_total = EXCLUDED.disk_write_bytes_total,
        disk_read_bytes_delta = EXCLUDED.disk_read_bytes_delta,
        disk_write_bytes_delta = EXCLUDED.disk_write_bytes_delta,
        disk_read_ops_delta = EXCLUDED.disk_read_ops_delta,
        disk_write_ops_delta = EXCLUDED.disk_write_ops_delta,
        updated_at = CURRENT_TIMESTAMP
    WHERE EXCLUDED.bucket_at >= node_current_metrics.bucket_at;

    RETURN NEW;
END
$function$;

CREATE OR REPLACE FUNCTION rollup_hour(p_hour_at timestamptz)
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
        memory_usage_avg, memory_usage_max,
        disk_read_bytes, disk_write_bytes,
        disk_read_bytes_per_second_avg, disk_read_bytes_per_second_max,
        disk_write_bytes_per_second_avg, disk_write_bytes_per_second_max,
        updated_at
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
        sum(sample.disk_read_bytes_delta)::bigint,
        sum(sample.disk_write_bytes_delta)::bigint,
        avg(sample.disk_read_bytes_delta::numeric / sample.interval_seconds),
        max(sample.disk_read_bytes_delta::numeric / sample.interval_seconds),
        avg(sample.disk_write_bytes_delta::numeric / sample.interval_seconds),
        max(sample.disk_write_bytes_delta::numeric / sample.interval_seconds),
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
        disk_read_bytes = EXCLUDED.disk_read_bytes,
        disk_write_bytes = EXCLUDED.disk_write_bytes,
        disk_read_bytes_per_second_avg = EXCLUDED.disk_read_bytes_per_second_avg,
        disk_read_bytes_per_second_max = EXCLUDED.disk_read_bytes_per_second_max,
        disk_write_bytes_per_second_avg = EXCLUDED.disk_write_bytes_per_second_avg,
        disk_write_bytes_per_second_max = EXCLUDED.disk_write_bytes_per_second_max,
        updated_at = CURRENT_TIMESTAMP;

    DELETE FROM monitoring.node_metric_hourly hourly
     WHERE hourly.hour_at = v_hour
       AND NOT EXISTS (
           SELECT 1 FROM monitoring.node_metric_samples sample
            WHERE sample.bucket_at >= v_hour AND sample.bucket_at < v_hour_end
              AND sample.node_id = hourly.node_id
       );

    INSERT INTO monitoring.filesystem_metric_hourly (
        hour_at, node_id, filesystem_id, sample_count,
        usage_avg, usage_max, used_bytes_max, updated_at
    )
    SELECT v_hour, sample.node_id, sample.filesystem_id, count(*)::integer,
           avg(sample.used_percent)::real, max(sample.used_percent)::real,
           max(sample.used_bytes), CURRENT_TIMESTAMP
      FROM monitoring.filesystem_metric_samples sample
     WHERE sample.bucket_at >= v_hour AND sample.bucket_at < v_hour_end
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
           SELECT 1 FROM monitoring.filesystem_metric_samples sample
            WHERE sample.bucket_at >= v_hour AND sample.bucket_at < v_hour_end
              AND sample.node_id = hourly.node_id
              AND sample.filesystem_id = hourly.filesystem_id
       );

    INSERT INTO monitoring.network_metric_hourly (
        hour_at, node_id, interface_id, sample_count,
        rx_bytes, tx_bytes,
        rx_bits_per_second_avg, rx_bits_per_second_max,
        tx_bits_per_second_avg, tx_bits_per_second_max, updated_at
    )
    SELECT v_hour, network.node_id, network.interface_id, count(*)::integer,
           sum(network.rx_bytes_delta)::bigint, sum(network.tx_bytes_delta)::bigint,
           avg(network.rx_bytes_delta::numeric * 8 / node.interval_seconds),
           max(network.rx_bytes_delta::numeric * 8 / node.interval_seconds),
           avg(network.tx_bytes_delta::numeric * 8 / node.interval_seconds),
           max(network.tx_bytes_delta::numeric * 8 / node.interval_seconds),
           CURRENT_TIMESTAMP
      FROM monitoring.network_metric_samples network
      JOIN monitoring.node_metric_samples node
        ON node.bucket_at = network.bucket_at AND node.node_id = network.node_id
     WHERE network.bucket_at >= v_hour AND network.bucket_at < v_hour_end
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
           SELECT 1 FROM monitoring.network_metric_samples sample
            WHERE sample.bucket_at >= v_hour AND sample.bucket_at < v_hour_end
              AND sample.node_id = hourly.node_id
              AND sample.interface_id = hourly.interface_id
       );
END
$function$;

INSERT INTO schema_migrations (version, description)
VALUES ('V003', 'Add node disk IO metrics and hourly rollups');

RESET ROLE;

REVOKE ALL ON FUNCTION monitoring.sync_node_current_metrics() FROM PUBLIC;
REVOKE ALL ON FUNCTION monitoring.rollup_hour(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION monitoring.rollup_hour(timestamptz) TO server_status_writer;

COMMIT;
