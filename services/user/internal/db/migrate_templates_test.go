package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexusmart/core/services/user/internal/db"
	"github.com/nexusmart/core/services/user/internal/site"
)

func TestRunMigrations_SiteTemplatesSeed(t *testing.T) {
	dsn := os.Getenv("TEST_USER_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_USER_DATABASE_URL not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("RunMigrations (1st run): %v", err)
	}
	// Idempotent re-run must succeed cleanly.
	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("RunMigrations (2nd run): %v", err)
	}

	store := db.NewSQLStore(pool)
	rows, err := store.ListActiveSiteTemplates(ctx)
	if err != nil {
		t.Fatalf("ListActiveSiteTemplates: %v", err)
	}
	if len(rows) != 8 {
		t.Fatalf("expected 8 active site_templates rows after 0004 migration, got %d", len(rows))
	}

	for _, row := range rows {
		v2, _, err := site.ValidateAndCanonicalizeConfigV2(row.Config)
		if err != nil {
			t.Fatalf("row %q config invalid v2: %v", row.ID, err)
		}
		if v2.TemplateID != row.ID {
			t.Errorf("row %q template_id mismatch: %q", row.ID, v2.TemplateID)
		}
	}

	got, err := store.GetSiteTemplateByID(ctx, "editorial")
	if err != nil {
		t.Fatalf("GetSiteTemplateByID(editorial): %v", err)
	}
	if got.Archetype != "editorial" || len(got.Tags) == 0 {
		t.Fatalf("unexpected editorial template row: %+v", got)
	}

	// Verify sites.template_id column exists.
	var colName string
	if err := pool.QueryRow(ctx, `SELECT column_name FROM information_schema.columns WHERE table_name = 'sites' AND column_name = 'template_id'`).Scan(&colName); err != nil {
		t.Fatalf("expected sites.template_id column: %v", err)
	}
}
