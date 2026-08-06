BEGIN;

SET LOCAL TIME ZONE 'UTC';
SET ROLE server_status_owner;
SET search_path = monitoring, pg_catalog;

CREATE TABLE kvm_virtual_machines (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id uuid NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    hardware_key text NOT NULL CHECK (btrim(hardware_key) <> ''),
    domain_uuid text NOT NULL CHECK (btrim(domain_uuid) <> ''),
    name text NOT NULL CHECK (btrim(name) <> ''),
    state text NOT NULL CHECK (btrim(state) <> ''),
    vcpu_count integer NOT NULL CHECK (vcpu_count > 0),
    memory_bytes bigint NOT NULL CHECK (memory_bytes > 0),
    disk_bytes bigint NOT NULL CHECK (disk_bytes >= 0),
    os_name text,
    os_type text,
    architecture text,
    machine_type text,
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    removed_at timestamptz,
    UNIQUE (node_id, id),
    CHECK (last_seen_at >= first_seen_at),
    CHECK (removed_at IS NULL OR removed_at >= first_seen_at)
);

CREATE UNIQUE INDEX kvm_virtual_machines_active_key_idx
    ON kvm_virtual_machines (node_id, hardware_key)
    WHERE removed_at IS NULL;

COMMENT ON TABLE kvm_virtual_machines IS
    'Current and historical libvirt KVM domain inventory reported by Linux agents.';

INSERT INTO schema_migrations (version, description)
VALUES ('V015', 'Add libvirt KVM virtual machine inventory');

RESET ROLE;

GRANT SELECT ON TABLE monitoring.kvm_virtual_machines
TO server_status_writer, server_status_reader;
GRANT INSERT, UPDATE, DELETE ON TABLE monitoring.kvm_virtual_machines
TO server_status_writer;

COMMIT;
