package installer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func liveStore(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, kineStore)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec("CREATE TABLE kine (name TEXT); INSERT INTO kine VALUES ('/registry/namespaces/prod')"); err != nil {
		t.Fatal(err)
	}
}

func TestStateArchiveRoundTrip(t *testing.T) {
	root := t.TempDir()
	liveStore(t, root)
	writeFile(t, filepath.Join(root, "lib/kuberoot/pki/ca.key"), "ca key")
	writeFile(t, filepath.Join(root, "lib/kuberoot/machine-id"), "abc")
	writeFile(t, filepath.Join(root, "lib/kuberoot/backups/state-old.tar.gz"), "an older backup")
	writeFile(t, filepath.Join(root, "lib/kuberoot/upgrade/bundle.tar"), "a downloaded release")
	writeFile(t, filepath.Join(root, "lib/kubelet/pki/kubelet-client-2026.pem"), "client")
	if err := os.Symlink("/var/lib/kubelet/pki/kubelet-client-2026.pem", filepath.Join(root, "lib/kubelet/pki/kubelet-client-current.pem")); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := writeStateArchive(&buf, root, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "state.tar.gz")
	if err := os.WriteFile(archive, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	mnt := t.TempDir()
	if err := extractState(archive, mnt); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]string{"lib/kuberoot/pki/ca.key": "ca key", "lib/kuberoot/machine-id": "abc", "lib/kubelet/pki/kubelet-client-2026.pem": "client"} {
		if got, _ := os.ReadFile(filepath.Join(mnt, rel)); string(got) != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	if link, _ := os.Readlink(filepath.Join(mnt, "lib/kubelet/pki/kubelet-client-current.pem")); link != "/var/lib/kubelet/pki/kubelet-client-2026.pem" {
		t.Errorf("kubelet's current certificate link = %q", link)
	}
	for _, rel := range []string{"lib/kuberoot/backups", "lib/kuberoot/upgrade"} {
		if _, err := os.Stat(filepath.Join(mnt, rel)); err == nil {
			t.Errorf("%s is in the archive", rel)
		}
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(mnt, kineStore)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var name string
	if err := db.QueryRow("SELECT name FROM kine").Scan(&name); err != nil || name != "/registry/namespaces/prod" {
		t.Errorf("restored store has %q, %v", name, err)
	}
}

func TestStateArchiveNeedsAControlPlane(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "lib/kuberoot/machine-id"), "abc")
	if err := writeStateArchive(&bytes.Buffer{}, root, t.TempDir()); err == nil {
		t.Error("a node without a cluster store has nothing to back up")
	}
}

func tarGz(t *testing.T, entries map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	path := filepath.Join(t.TempDir(), "a.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractStateRefusesForeignPaths(t *testing.T) {
	for _, name := range []string{"../etc/passwd", "lib/kuberoot/../../../etc/shadow", "/etc/passwd", "lib/other/file", "lib/kuberoot/backups/x"} {
		archive := tarGz(t, map[string]string{kineStore: "db", name: "x"})
		if err := extractState(archive, t.TempDir()); err == nil || !strings.Contains(err.Error(), "unexpected path") {
			t.Errorf("%s: err = %v, want refused", name, err)
		}
	}
}

func TestExtractStateNeedsTheStore(t *testing.T) {
	archive := tarGz(t, map[string]string{"lib/kuberoot/machine-id": "abc"})
	if err := extractState(archive, t.TempDir()); err == nil {
		t.Error("an archive without a cluster store was accepted")
	}
}
