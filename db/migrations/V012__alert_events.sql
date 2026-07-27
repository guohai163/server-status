BEGIN;

SET LOCAL TIME ZONE 'UTC';
SET ROLE server_status_owner;
SET search_path = monitoring, pg_catalog;

CREATE TABLE alert_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id uuid NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    category text NOT NULL CHECK (category IN (
        'offline', 'filesystem', 'load', 'smart', 'raid', 'temperature'
    )),
    resource_key text NOT NULL CHECK (btrim(resource_key) <> ''),
    severity text NOT NULL CHECK (severity IN ('warning', 'critical')),
    title text NOT NULL CHECK (btrim(title) <> ''),
    details jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details) = 'object'),
    opened_at timestamptz NOT NULL,
    last_observed_at timestamptz NOT NULL,
    resolved_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (last_observed_at >= opened_at),
    CHECK (resolved_at IS NULL OR resolved_at >= opened_at)
);

CREATE UNIQUE INDEX alert_events_open_resource_uq
    ON alert_events (node_id, category, resource_key)
    WHERE resolved_at IS NULL;

CREATE INDEX alert_events_open_severity_idx
    ON alert_events (severity, opened_at DESC)
    WHERE resolved_at IS NULL;

CREATE INDEX alert_events_history_idx
    ON alert_events (opened_at DESC, id DESC);

CREATE INDEX alert_events_node_history_idx
    ON alert_events (node_id, opened_at DESC);

CREATE OR REPLACE FUNCTION prune_resolved_alert_events(
    p_reference_at timestamptz DEFAULT CURRENT_TIMESTAMP
)
RETURNS bigint
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, monitoring
SET timezone = 'UTC'
AS $function$
DECLARE
    v_deleted bigint;
BEGIN
    DELETE FROM monitoring.alert_events
     WHERE resolved_at IS NOT NULL
       AND resolved_at < p_reference_at - INTERVAL '365 days';
    GET DIAGNOSTICS v_deleted = ROW_COUNT;
    RETURN v_deleted;
END
$function$;

COMMENT ON TABLE alert_events IS
    'Persistent alert lifecycle events derived from the latest node and hardware-health snapshots.';
COMMENT ON COLUMN alert_events.severity IS
    'Highest severity observed during this alert occurrence; severity never decreases before resolution.';

INSERT INTO schema_migrations (version, description)
VALUES ('V012', 'Add persistent alert lifecycle events');

RESET ROLE;

GRANT SELECT ON TABLE monitoring.alert_events TO server_status_writer, server_status_reader;
GRANT INSERT, UPDATE, DELETE ON TABLE monitoring.alert_events TO server_status_writer;
REVOKE ALL ON FUNCTION monitoring.prune_resolved_alert_events(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION monitoring.prune_resolved_alert_events(timestamptz) TO server_status_writer;

COMMIT;
