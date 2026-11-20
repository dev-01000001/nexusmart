package e2e

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	cfg := LoadConfig()
	cleanup := func(phase string) {
		if cfg.RedisAddr == "" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := ResetRateLimitKeys(ctx, cfg.RedisAddr); err != nil {
			fmt.Fprintf(os.Stderr, "[e2e] warning: reset rate-limit keys (%s) on %s failed: %v\n", phase, cfg.RedisAddr, err)
		}
	}

	cleanup("pre-run")
	code := m.Run()
	cleanup("post-run")
	os.Exit(code)
}
