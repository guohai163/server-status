BEGIN;

SET LOCAL TIME ZONE 'UTC';
SET ROLE server_status_owner;
SET search_path = monitoring, pg_catalog;

CREATE TABLE gpu_metric_samples (
    bucket_at timestamptz NOT NULL,
    node_id uuid NOT NULL,
    gpu_id uuid NOT NULL,
    utilization_percent real NOT NULL CHECK (utilization_percent BETWEEN 0 AND 100),
    memory_total_bytes bigint NOT NULL CHECK (memory_total_bytes > 0),
    memory_used_bytes bigint NOT NULL CHECK (memory_used_bytes >= 0),
    memory_usage_percent real GENERATED ALWAYS AS (
        ((memory_used_bytes::numeric * 100) / memory_total_bytes)::real
    ) STORED,
    PRIMARY KEY (bucket_at, node_id, gpu_id),
    FOREIGN KEY (bucket_at, node_id)
        REFERENCES node_metric_samples (bucket_at, node_id) ON DELETE CASCADE,
    FOREIGN KEY (node_id, gpu_id)
        REFERENCES gpu_devices (node_id, id),
    CHECK (EXTRACT(SECOND FROM bucket_at) = 0),
    CHECK (memory_used_bytes <= memory_total_bytes)
) PARTITION BY RANGE (bucket_at);

CREATE INDEX gpu_metric_samples_resource_time_idx
    ON gpu_metric_samples (node_id, gpu_id, bucket_at DESC);
CREATE INDEX gpu_metric_samples_time_brin_idx
    ON gpu_metric_samples USING brin (bucket_at) WITH (pages_per_range = 64);

CREATE TABLE gpu_metric_hourly (
    hour_at timestamptz NOT NULL,
    node_id uuid NOT NULL,
    gpu_id uuid NOT NULL,
    sample_count integer NOT NULL CHECK (sample_count > 0),
    utilization_avg real NOT NULL CHECK (utilization_avg BETWEEN 0 AND 100),
    utilization_max real NOT NULL CHECK (utilization_max BETWEEN 0 AND 100),
    memory_used_bytes_avg bigint NOT NULL CHECK (memory_used_bytes_avg >= 0),
    memory_used_bytes_max bigint NOT NULL CHECK (memory_used_bytes_max >= 0),
    memory_usage_avg real NOT NULL CHECK (memory_usage_avg BETWEEN 0 AND 100),
    memory_usage_max real NOT NULL CHECK (memory_usage_max BETWEEN 0 AND 100),
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (hour_at, node_id, gpu_id),
    FOREIGN KEY (node_id, gpu_id)
        REFERENCES gpu_devices (node_id, id),
    CHECK (EXTRACT(MINUTE FROM hour_at) = 0 AND EXTRACT(SECOND FROM hour_at) = 0)
) PARTITION BY RANGE (hour_at);

CREATE INDEX gpu_metric_hourly_resource_time_idx
    ON gpu_metric_hourly (node_id, gpu_id, hour_at DESC);

CREATE FUNCTION sync_gpu_current_metrics()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, monitoring
AS $function$
DECLARE
    v_collected_at timestamptz;
    v_received_at timestamptz;
BEGIN
    SELECT collected_at, received_at
      INTO v_collected_at, v_received_at
      FROM monitoring.node_metric_samples
     WHERE bucket_at = NEW.bucket_at
       AND node_id = NEW.node_id;

    INSERT INTO monitoring.gpu_current_metrics (
        node_id, gpu_id, bucket_at, collected_at, received_at,
        utilization_percent, memory_used_bytes, updated_at
    ) VALUES (
        NEW.node_id, NEW.gpu_id, NEW.bucket_at, v_collected_at, v_received_at,
        NEW.utilization_percent, NEW.memory_used_bytes, CURRENT_TIMESTAMP
    )
    ON CONFLICT (node_id, gpu_id) DO UPDATE SET
        bucket_at = EXCLUDED.bucket_at,
        collected_at = EXCLUDED.collected_at,
        received_at = EXCLUDED.received_at,
        utilization_percent = EXCLUDED.utilization_percent,
        memory_used_bytes = EXCLUDED.memory_used_bytes,
        updated_at = CURRENT_TIMESTAMP
    WHERE EXCLUDED.bucket_at >= gpu_current_metrics.bucket_at;

    RETURN NEW;
END
$function$;

CREATE TRIGGER gpu_metric_samples_sync_current
AFTER INSERT OR UPDATE ON gpu_metric_samples
FOR EACH ROW EXECUTE FUNCTION sync_gpu_current_metrics();

CREATE OR REPLACE FUNCTION ensure_raw_partitions(p_start_date date, p_end_date date)
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
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS monitoring.%I PARTITION OF monitoring.gpu_metric_samples FOR VALUES FROM (%L) TO (%L)',
            'gpu_metric_samples_p' || v_suffix, v_start, v_end
        );

        v_date := v_date + 1;
    END LOOP;
END
$function$;

CREATE OR REPLACE FUNCTION ensure_hourly_partitions(p_start_month date, p_end_month date)
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
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS monitoring.%I PARTITION OF monitoring.gpu_metric_hourly FOR VALUES FROM (%L) TO (%L)',
            'gpu_metric_hourly_p' || v_suffix, v_start, v_end
        );

        v_month := (v_month + INTERVAL '1 month')::date;
    END LOOP;
END
$function$;

CREATE OR REPLACE FUNCTION drop_expired_partitions(p_reference_at timestamptz DEFAULT CURRENT_TIMESTAMP)
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
               'gpu_metric_samples', 'filesystem_metric_samples',
               'network_metric_samples', 'node_metric_samples',
               'gpu_metric_hourly', 'filesystem_metric_hourly',
               'network_metric_hourly', 'node_metric_hourly'
           )
         ORDER BY CASE parent.relname
             WHEN 'gpu_metric_samples' THEN 1
             WHEN 'filesystem_metric_samples' THEN 2
             WHEN 'network_metric_samples' THEN 3
             WHEN 'node_metric_samples' THEN 4
             ELSE 5
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

    INSERT INTO monitoring.gpu_metric_hourly (
        hour_at, node_id, gpu_id, sample_count,
        utilization_avg, utilization_max,
        memory_used_bytes_avg, memory_used_bytes_max,
        memory_usage_avg, memory_usage_max, updated_at
    )
    SELECT v_hour, sample.node_id, sample.gpu_id, count(*)::integer,
           avg(sample.utilization_percent)::real,
           max(sample.utilization_percent)::real,
           round(avg(sample.memory_used_bytes))::bigint,
           max(sample.memory_used_bytes),
           avg(sample.memory_usage_percent)::real,
           max(sample.memory_usage_percent)::real,
           CURRENT_TIMESTAMP
      FROM monitoring.gpu_metric_samples sample
     WHERE sample.bucket_at >= v_hour AND sample.bucket_at < v_hour_end
     GROUP BY sample.node_id, sample.gpu_id
    ON CONFLICT (hour_at, node_id, gpu_id) DO UPDATE SET
        sample_count = EXCLUDED.sample_count,
        utilization_avg = EXCLUDED.utilization_avg,
        utilization_max = EXCLUDED.utilization_max,
        memory_used_bytes_avg = EXCLUDED.memory_used_bytes_avg,
        memory_used_bytes_max = EXCLUDED.memory_used_bytes_max,
        memory_usage_avg = EXCLUDED.memory_usage_avg,
        memory_usage_max = EXCLUDED.memory_usage_max,
        updated_at = CURRENT_TIMESTAMP;

    DELETE FROM monitoring.gpu_metric_hourly hourly
     WHERE hourly.hour_at = v_hour
       AND NOT EXISTS (
           SELECT 1 FROM monitoring.gpu_metric_samples sample
            WHERE sample.bucket_at >= v_hour AND sample.bucket_at < v_hour_end
              AND sample.node_id = hourly.node_id
              AND sample.gpu_id = hourly.gpu_id
       );
END
$function$;

COMMENT ON TABLE gpu_metric_samples IS
    'Per-GPU minute utilization and framebuffer memory samples retained for 90 days.';
COMMENT ON TABLE gpu_metric_hourly IS
    'Per-GPU hourly rollups retained for 24 months.';

SELECT monitoring.maintain_partitions(CURRENT_TIMESTAMP);

INSERT INTO schema_migrations (version, description)
VALUES ('V007', 'Add per-GPU raw and hourly history metrics');

RESET ROLE;

GRANT SELECT ON TABLE monitoring.gpu_metric_samples, monitoring.gpu_metric_hourly
TO server_status_writer, server_status_reader;
GRANT INSERT, UPDATE, DELETE ON TABLE monitoring.gpu_metric_samples
TO server_status_writer;

REVOKE ALL ON FUNCTION monitoring.sync_gpu_current_metrics() FROM PUBLIC;
REVOKE ALL ON FUNCTION monitoring.ensure_raw_partitions(date, date) FROM PUBLIC;
REVOKE ALL ON FUNCTION monitoring.ensure_hourly_partitions(date, date) FROM PUBLIC;
REVOKE ALL ON FUNCTION monitoring.drop_expired_partitions(timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION monitoring.rollup_hour(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION monitoring.ensure_raw_partitions(date, date) TO server_status_writer;
GRANT EXECUTE ON FUNCTION monitoring.ensure_hourly_partitions(date, date) TO server_status_writer;
GRANT EXECUTE ON FUNCTION monitoring.drop_expired_partitions(timestamptz) TO server_status_writer;
GRANT EXECUTE ON FUNCTION monitoring.rollup_hour(timestamptz) TO server_status_writer;

COMMIT;
