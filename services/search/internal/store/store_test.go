package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPG_S10_AcquireReindexLock_HijackClosesConnOnUnlockFailure(t *testing.T) {
	dbURL := strings.TrimSpace(os.Getenv("TEST_SEARCH_DB_URL"))
	if dbURL == "" {
		t.Skip("TEST_SEARCH_DB_URL not set; run via TEST_WITH_SEARCH_DB=1 bash scripts/test-db.sh --run ...")
	}

	ctx := context.Background()
	pool, err := OpenPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("OpenPool: %v", err)
	}
	t.Cleanup(pool.Close)
	st := New(pool)

	// Acquire lock with an immediately-expiring unlock timeout (1ns) so pg_advisory_unlock fails
	// with context.DeadlineExceeded while the Postgres session is still alive holding the lock.
	release, acquired, err := st.acquireReindexLockWithUnlockTimeout(ctx, 1*time.Nanosecond)
	if err != nil || !acquired {
		t.Fatalf("initial AcquireReindexLock failed: acquired=%v err=%v", acquired, err)
	}

	// While held, a second caller cannot acquire the lock.
	_, acquired2, err := st.AcquireReindexLock(ctx)
	if err != nil || acquired2 {
		t.Fatalf("expected second AcquireReindexLock to be rejected while lock held: acquired=%v err=%v", acquired2, err)
	}

	// Calling release() triggers unlock error -> S10 MUST Hijack().Close() the connection instead
	// of returning it to the pool with the lock still held!
	release()

	// Postgres closes the session and frees the session advisory lock; a new AcquireReindexLock MUST succeed!
	var (
		relAfter func()
		okAfter  bool
	)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		relAfter, okAfter, err = st.AcquireReindexLock(ctx)
		if err != nil {
			t.Fatalf("AcquireReindexLock after hijack-close error: %v", err)
		}
		if okAfter {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !okAfter {
		t.Fatalf("S10: lock remained permanently stuck after failed pg_advisory_unlock!")
	}
	relAfter()
}
