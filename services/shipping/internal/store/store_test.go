package store

import (
	"context"
	"errors"
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/nexusmart/core/services/shipping/migrations"
)

func TestStoreHelpersAndMigrationsFS(t *testing.T) {
	if NormalizeDirection("") != DirectionForward ||
		NormalizeDirection("forward") != DirectionForward ||
		NormalizeDirection("FORWARD") != DirectionForward ||
		NormalizeDirection("return") != DirectionReturn ||
		NormalizeDirection(" RETURN ") != DirectionReturn {
		t.Fatalf("unexpected NormalizeDirection behavior")
	}

	if !isTerminalStatus("delivered") || !isTerminalStatus("returned") || !isTerminalStatus("cancelled") {
		t.Fatalf("expected delivered/returned/cancelled to be terminal")
	}
	if isTerminalStatus("created") || isTerminalStatus("delivering") || isTerminalStatus("returning") {
		t.Fatalf("expected active statuses not terminal")
	}

	// Empty ward code returns ErrNotFound without touching pool.
	pg := NewPGStore(nil)
	if _, err := pg.GetWard(context.Background(), "   "); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for blank wardCode, got %v", err)
	}

	// Verify embedded migrations include both 000001_init.sql and 000002_return_shipments.sql in order.
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read migrations FS: %v", err)
	}
	var sqlFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			sqlFiles = append(sqlFiles, e.Name())
		}
	}
	sort.Strings(sqlFiles)
	if len(sqlFiles) < 2 || sqlFiles[0] != "000001_init.sql" || sqlFiles[1] != "000002_return_shipments.sql" {
		t.Fatalf("unexpected embedded SQL migrations: %+v", sqlFiles)
	}
	raw, err := fs.ReadFile(migrations.FS, "000002_return_shipments.sql")
	if err != nil {
		t.Fatalf("read 000002_return_shipments.sql: %v", err)
	}
	sqlText := string(raw)
	for _, needle := range []string{
		"ADD COLUMN IF NOT EXISTS direction",
		"ADD COLUMN IF NOT EXISTS return_request_id",
		"DROP CONSTRAINT IF EXISTS shipments_order_id_key",
		"uq_shipments_order_direction",
	} {
		if !strings.Contains(sqlText, needle) {
			t.Fatalf("000002_return_shipments.sql missing %q", needle)
		}
	}
}
