BEGIN;

SET LOCAL TIME ZONE 'UTC';
SET ROLE server_status_owner;
SET search_path = monitoring, pg_catalog;

CREATE TABLE node_network_preferences (
    node_id uuid PRIMARY KEY,
    interface_id uuid NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (node_id, interface_id)
        REFERENCES network_interfaces (node_id, id) ON DELETE CASCADE
);

COMMENT ON TABLE node_network_preferences IS
    'Per-node network interface preference used for dashboard identity and IP ordering.';
COMMENT ON COLUMN node_network_preferences.node_id IS
    'Node whose dashboard network preference is configured.';
COMMENT ON COLUMN node_network_preferences.interface_id IS
    'Active or historical interface selected as the preferred dashboard IP source.';
COMMENT ON COLUMN node_network_preferences.updated_at IS
    'Time at which the interface preference was last changed.';

INSERT INTO schema_migrations (version, description)
VALUES ('V004', 'Add preferred dashboard network interface');

RESET ROLE;

GRANT SELECT ON TABLE monitoring.node_network_preferences
TO server_status_writer, server_status_reader;
GRANT INSERT, UPDATE, DELETE ON TABLE monitoring.node_network_preferences
TO server_status_writer;

COMMIT;
