package fileindex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeNFiles(t *testing.T, root string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(root, fmt.Sprintf("f%d.txt", i)), []byte("x"), 0644))
	}
}

// openMem opens an in-memory Index so scan timing assertions measure the
// throttle sleeps themselves rather than this machine's disk/fsync latency
// (which is highly variable for a file-backed SQLite db under WAL commits).
func openMem(t *testing.T) *Index {
	t.Helper()
	idx, err := Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

func TestScanIntoThrottles(t *testing.T) {
	root := t.TempDir()
	writeNFiles(t, root, 60)

	idxThrottled := openMem(t)
	idxThrottled.ThrottleEvery = 10
	idxThrottled.ThrottleSleep = 30 * time.Millisecond
	startT := time.Now()
	require.NoError(t, idxThrottled.ScanInto(context.Background(), root))
	elapsedThrottled := time.Since(startT)
	// 61 WalkDir visits (root + 60 files) at every=10 -> at least 5 throttle
	// sleeps of 30ms each.
	require.GreaterOrEqual(t, elapsedThrottled, 5*30*time.Millisecond, "throttled scan should take at least 150ms")

	idxUnthrottled := openMem(t)
	startU := time.Now()
	require.NoError(t, idxUnthrottled.ScanInto(context.Background(), root))
	elapsedUnthrottled := time.Since(startU)
	// Zero-value throttle fields: no throttling at all. A generous upper bound
	// well below a single throttle sleep keeps this from flaking under load.
	require.Less(t, elapsedUnthrottled, 30*time.Millisecond, "unthrottled scan (zero-value fields) should be fast")
}

func TestScanIntoCancellable(t *testing.T) {
	root := t.TempDir()
	writeNFiles(t, root, 2000)

	idx := openMem(t)
	idx.ThrottleEvery = 10
	idx.ThrottleSleep = 50 * time.Millisecond
	// Full scan at this rate would take 2000/10*50ms = 10s (>9s); cancel well before that.

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	done := make(chan error, 1)
	go func() {
		done <- idx.ScanInto(ctx, root)
	}()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(1 * time.Second):
		t.Fatal("ScanInto did not return within 1s of cancellation")
	}
}
