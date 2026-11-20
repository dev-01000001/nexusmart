package env

import (
	"testing"
	"time"
)

func TestEnvHelpers(t *testing.T) {
	t.Setenv("TEST_ENV_STR", "  hello  ")
	t.Setenv("TEST_ENV_INT", "42")
	t.Setenv("TEST_ENV_INT_BAD", "notanint")
	t.Setenv("TEST_ENV_DUR", "15m")
	t.Setenv("TEST_ENV_DUR_BAD", "bogus")
	t.Setenv("TEST_ENV_BOOL", "true")

	if got := EnvString("TEST_ENV_STR", "default"); got != "hello" {
		t.Fatalf("EnvString got %q, want %q", got, "hello")
	}
	if got := EnvString("TEST_ENV_MISSING", "fallback"); got != "fallback" {
		t.Fatalf("EnvString missing got %q, want %q", got, "fallback")
	}
	if got := EnvInt("TEST_ENV_INT", 1); got != 42 {
		t.Fatalf("EnvInt got %d, want 42", got)
	}
	if got := EnvInt("TEST_ENV_INT_BAD", 9); got != 9 {
		t.Fatalf("EnvInt invalid got %d, want 9", got)
	}
	if got := EnvDuration("TEST_ENV_DUR", time.Second); got != 15*time.Minute {
		t.Fatalf("EnvDuration got %v, want 15m", got)
	}
	if got := EnvDuration("TEST_ENV_DUR_BAD", 5*time.Second); got != 5*time.Second {
		t.Fatalf("EnvDuration invalid got %v, want 5s", got)
	}
	if got := EnvBool("TEST_ENV_BOOL", false); !got {
		t.Fatalf("EnvBool got false, want true")
	}
}
