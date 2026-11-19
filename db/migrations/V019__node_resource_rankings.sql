BEGIN;

SET ROLE server_status_owner;
SET search_path = monitoring, pg_catalog;

CREATE TABLE node_resource_rankings (
    node_id uuid PRIMARY KEY REFERENCES nodes (id) ON DELETE CASCADE,
    collected_at timestamptz NOT NULL,
    rankings jsonb CHECK (jsonb_typeof(rankings) = 'object')
);

COMMENT ON TABLE node_resource_rankings IS
    'Latest CPU, RSS, disk I/O and socket activity rankings per node.';

INSERT INTO schema_migrations (version, description)
VALUES ('V019', 'Add latest per-node resource rankings');

RESET ROLE;

GRANT SELECT ON TABLE monitoring.node_resource_rankings
TO server_status_writer, server_status_reader;
GRANT INSERT, UPDATE ON TABLE monitoring.node_resource_rankings
TO server_status_writer;

COMMIT;
