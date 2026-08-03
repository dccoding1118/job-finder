package store

import (
	"context"
	"path/filepath"
	"testing"
)

// The switch has to survive a restart: a database that has never recorded it
// reports automatic processing on, and once turned off it stays off.
func TestAutoProcessingDefaultsOnAndPersists(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "settings.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := first.AutoProcessing(ctx)
	if err != nil || !enabled {
		t.Fatalf("unset switch = %v, %v; want on", enabled, err)
	}
	if setErr := first.SetAutoProcessing(ctx, false); setErr != nil {
		t.Fatal(setErr)
	}
	if closeErr := first.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	enabled, err = reopened.AutoProcessing(ctx)
	if err != nil || enabled {
		t.Fatalf("reopened switch = %v, %v; want off", enabled, err)
	}
	if setErr := reopened.SetAutoProcessing(ctx, true); setErr != nil {
		t.Fatal(setErr)
	}
	if enabled, err = reopened.AutoProcessing(ctx); err != nil || !enabled {
		t.Fatalf("switch back on = %v, %v", enabled, err)
	}
}
