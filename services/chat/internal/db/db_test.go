package db

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/nexusmart/core/services/chat/migrations"
)

func TestValidateDatabaseName(t *testing.T) {
	valid := []string{"chat_db", "a", "chat_test_123", strings.Repeat("a", 63)}
	for _, name := range valid {
		if err := ValidateDatabaseName(name); err != nil {
			t.Errorf("expected %q to be valid, got %v", name, err)
		}
	}

	invalid := []string{
		"",
		"Chat_DB",
		"1chat",
		"chat-db",
		"chat; DROP TABLE users;",
		"chat\"db",
		strings.Repeat("a", 64),
	}
	for _, name := range invalid {
		if err := ValidateDatabaseName(name); err == nil {
			t.Errorf("expected %q to be rejected", name)
		}
	}
}

func TestEmbeddedMigrationsPresent(t *testing.T) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Name() == "000001_init.sql" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected 000001_init.sql in embedded migrations")
	}
}
