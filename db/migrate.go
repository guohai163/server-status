package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/jackc/pgx/v5"
)

//go:embed migrations/V*.sql
var migrations embed.FS

const migrationLockID int64 = 0x53535645524D4947

// MigrateComposeDatabase initializes the instance managed by Compose. The
// application connects as the writer after this administrative session closes.
func MigrateComposeDatabase(ctx context.Context, databaseURL, adminPassword string) error {
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("parse database configuration: %w", err)
	}
	if config.User != "server_status_app" || len(config.Password) < 32 || len(adminPassword) < 32 || adminPassword == config.Password {
		return errors.New("Compose requires the server_status_app login and distinct PostgreSQL admin and application passwords of at least 32 characters")
	}
	appPassword := config.Password
	config.User = "postgres"
	config.Password = adminPassword
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return fmt.Errorf("connect for database migrations: %w", err)
	}
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		return fmt.Errorf("lock database migrations: %w", err)
	}
	if err := applyMigrations(ctx, conn); err != nil {
		return err
	}

	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT set_config('server_status.app_password', $1, true)", appPassword); err != nil {
			return errors.New("could not configure the database writer password")
		}
		if _, err := tx.Exec(ctx, `
			DO $bootstrap$
			BEGIN
				IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'server_status_app') THEN
					CREATE ROLE server_status_app LOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE;
				END IF;
				EXECUTE format('ALTER ROLE server_status_app PASSWORD %L', current_setting('server_status.app_password'));
				GRANT server_status_writer TO server_status_app;
				EXECUTE format('GRANT CONNECT ON DATABASE %I TO server_status_app', current_database());
			END
			$bootstrap$;
		`); err != nil {
			return errors.New("could not provision the database writer role")
		}
		return nil
	})
}

func applyMigrations(ctx context.Context, conn *pgx.Conn) error {
	files, err := fs.Glob(migrations, "migrations/V*.sql")
	if err != nil {
		return err
	}
	var initialized bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass('monitoring.schema_migrations') IS NOT NULL").Scan(&initialized); err != nil {
		return fmt.Errorf("check migration table: %w", err)
	}
	for _, file := range files {
		version := strings.SplitN(strings.TrimPrefix(file, "migrations/"), "__", 2)[0]
		if initialized {
			var applied bool
			err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM monitoring.schema_migrations WHERE version = $1)", version).Scan(&applied)
			if err != nil {
				return fmt.Errorf("check migration %s: %w", version, err)
			}
			if applied {
				continue
			}
		}
		sql, err := migrations.ReadFile(file)
		if err != nil {
			return err
		}
		if _, err := conn.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("apply migration %s: %w", version, err)
		}
		initialized = true
	}
	return nil
}
