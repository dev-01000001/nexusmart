package db

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexusmart/core/services/chat/migrations"
)

const (
	pgCodeInvalidCatalogName = "3D000"
	pgCodeDuplicateDatabase  = "42P04"
)

var validDBNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// ValidateDatabaseName enforces a safe lowercase PostgreSQL database identifier.
func ValidateDatabaseName(name string) error {
	if !validDBNamePattern.MatchString(name) {
		return fmt.Errorf("invalid database name %q: must match %s", name, validDBNamePattern.String())
	}
	return nil
}

// EnsureDatabase connects to the target database specified in dsn; if PostgreSQL returns
// error code 3D000 (database does not exist), it validates the DB name, connects to the
// maintenance "postgres" database on the same server, and creates the target database idempotently.
func EnsureDatabase(ctx context.Context, dsn string) error {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse database dsn: %w", err)
	}

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err == nil {
		_ = conn.Close(ctx)
		return nil
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgCodeInvalidCatalogName {
		return err
	}

	targetDB := cfg.Database
	if valErr := ValidateDatabaseName(targetDB); valErr != nil {
		return valErr
	}

	adminCfg := cfg.Copy()
	adminCfg.Database = "postgres"
	adminConn, err := pgx.ConnectConfig(ctx, adminCfg)
	if err != nil {
		return fmt.Errorf("connect maintenance postgres db: %w", err)
	}
	defer func() { _ = adminConn.Close(ctx) }()

	// Safe because targetDB passed strict ^[a-z][a-z0-9_]{0,62}$ validation and pgx.Identifier quoting.
	createSQL := "CREATE DATABASE " + pgx.Identifier{targetDB}.Sanitize()
	if _, execErr := adminConn.Exec(ctx, createSQL); execErr != nil {
		var createPgErr *pgconn.PgError
		if errors.As(execErr, &createPgErr) && createPgErr.Code == pgCodeDuplicateDatabase {
			return nil
		}
		return fmt.Errorf("create database %s: %w", targetDB, execErr)
	}
	return nil
}

// Migrate applies all embedded .sql migrations to pool in lexical order.
//
// ponytail: executes idempotent DDL statements in lexical order without a schema_migrations
// ledger table. Ceiling: additive-only migrations without down-scripts or checksum drift checks.
// Upgrade path: record applied version + sha256 in a chat_schema_migrations table or adopt goose/golang-migrate.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		raw, readErr := fs.ReadFile(migrations.FS, name)
		if readErr != nil {
			return fmt.Errorf("read migration %s: %w", name, readErr)
		}
		if _, execErr := pool.Exec(ctx, string(raw)); execErr != nil {
			return fmt.Errorf("apply migration %s: %w", name, execErr)
		}
	}
	return nil
}
