BEGIN;

SET LOCAL TIME ZONE 'UTC';
SET ROLE server_status_owner;
SET search_path = monitoring, pg_catalog;

ALTER TABLE nodes
    ADD COLUMN tags text[] NOT NULL DEFAULT ARRAY[]::text[],
    ADD CONSTRAINT nodes_tags_count_check CHECK (cardinality(tags) <= 5),
    ADD CONSTRAINT nodes_tags_null_check CHECK (array_position(tags, NULL) IS NULL);

COMMENT ON COLUMN nodes.tags IS
    'Up to five administrator-managed tags displayed on dashboard cards.';

INSERT INTO schema_migrations (version, description)
VALUES ('V006', 'Add administrator-managed node tags');

RESET ROLE;

COMMIT;
