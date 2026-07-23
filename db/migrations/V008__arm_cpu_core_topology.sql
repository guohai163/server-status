BEGIN;

SET LOCAL TIME ZONE 'UTC';
SET ROLE server_status_owner;
SET search_path = monitoring, pg_catalog;

ALTER TABLE cpu_packages
    ADD COLUMN performance_cores integer NOT NULL DEFAULT 0 CHECK (performance_cores >= 0),
    ADD COLUMN efficiency_cores integer NOT NULL DEFAULT 0 CHECK (efficiency_cores >= 0),
    ADD CONSTRAINT cpu_packages_core_topology_check CHECK (
        performance_cores + efficiency_cores = 0 OR
        performance_cores + efficiency_cores = physical_cores
    );

COMMENT ON COLUMN cpu_packages.performance_cores IS
    'Performance-class physical cores reported for heterogeneous ARM packages.';
COMMENT ON COLUMN cpu_packages.efficiency_cores IS
    'Efficiency-class physical cores reported for heterogeneous ARM packages.';

INSERT INTO schema_migrations (version, description)
VALUES ('V008', 'Add heterogeneous ARM CPU core topology');

RESET ROLE;

COMMIT;
