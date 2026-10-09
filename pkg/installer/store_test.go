package installer

import (
	"database/sql"
	"os"
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

func TestCopyTreeKeepsSymlinksAndSkipsUpgrades(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(src, "lib/kuberoot/upgrade"), 0o755))
	must(os.WriteFile(filepath.Join(src, "lib/kuberoot/upgrade/bundle.tar"), []byte("big"), 0o600))
	must(os.MkdirAll(filepath.Join(src, "lib/kubelet/pki"), 0o755))
	must(os.WriteFile(filepath.Join(src, "lib/kubelet/pki/kubelet-client-2026.pem"), []byte("cert"), 0o600))
	must(os.Symlink("kubelet-client-2026.pem", filepath.Join(src, "lib/kubelet/pki/kubelet-client-current.pem")))
	for _, rel := range []string{"lib/kuberoot", "lib/kubelet/pki"} {
		must(copyTree(filepath.Join(src, rel), filepath.Join(dst, rel)))
	}
	if link, err := os.Readlink(filepath.Join(dst, "lib/kubelet/pki/kubelet-client-current.pem")); err != nil || link != "kubelet-client-2026.pem" {
		t.Errorf("current certificate is not the symlink it was: %q %v", link, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "lib/kuberoot/upgrade")); !os.IsNotExist(err) {
		t.Errorf("upgrade bundles were carried: %v", err)
	}
}
