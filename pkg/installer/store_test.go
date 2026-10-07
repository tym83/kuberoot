package installer

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestSnapshotStoreCopiesALiveDatabase(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "state.db")
	live, err := sql.Open("sqlite", "file:"+src+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if _, err := live.Exec("CREATE TABLE kine (name TEXT); INSERT INTO kine VALUES ('/registry/nodes/a')"); err != nil {
		t.Fatal(err)
	}
	// The writer stays open, as kine does, while the snapshot is taken.
	dst := filepath.Join(dir, "target", "lib", "kine", "state.db")
	if err := snapshotStore(src, dst); err != nil {
		t.Fatal(err)
	}
	snap, err := sql.Open("sqlite", "file:"+dst+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	var name string
	if err := snap.QueryRow("SELECT name FROM kine").Scan(&name); err != nil || name != "/registry/nodes/a" {
		t.Errorf("snapshot has %q, %v", name, err)
	}
}

func TestSnapshotStoreSkipsAMissingStore(t *testing.T) {
	if err := snapshotStore(filepath.Join(t.TempDir(), "none.db"), filepath.Join(t.TempDir(), "x.db")); err != nil {
		t.Errorf("worker without a store: %v", err)
	}
}
