BEGIN;

SET LOCAL TIME ZONE 'UTC';
SET ROLE server_status_owner;
SET search_path = monitoring, pg_catalog;

CREATE OR REPLACE FUNCTION drop_expired_partitions(p_reference_at timestamptz DEFAULT CURRENT_TIMESTAMP)
RETURNS TABLE (dropped_partition text)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, monitoring
SET timezone = 'UTC'
AS $function$
DECLARE
    v_raw_cutoff date := (p_reference_at AT TIME ZONE 'UTC')::date - 7;
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

COMMENT ON TABLE node_metric_samples IS
    'One idempotent node snapshot per UTC minute; raw data retained for 7 days.';
COMMENT ON TABLE filesystem_metric_samples IS
    'Filesystem capacity samples; usage belongs to mounts, not physical disks; raw data retained for 7 days.';
COMMENT ON TABLE network_metric_samples IS
    'Per-interface network traffic samples retained for 7 days.';
COMMENT ON TABLE gpu_metric_samples IS
    'Per-GPU minute utilization and framebuffer memory samples retained for 7 days.';

SELECT monitoring.maintain_partitions(CURRENT_TIMESTAMP);

INSERT INTO schema_migrations (version, description)
VALUES ('V016', 'Reduce raw metric partition retention to seven days');

RESET ROLE;

REVOKE ALL ON FUNCTION monitoring.drop_expired_partitions(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION monitoring.drop_expired_partitions(timestamptz) TO server_status_writer;

COMMIT;
