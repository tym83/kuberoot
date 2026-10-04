package upgrade

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/tym83/kuberoot/pkg/bootdisk"
	"github.com/tym83/kuberoot/pkg/installer"
)

const downloadDir = "/var/lib/kuberoot/upgrade"

// Fetch downloads a release bundle (a tar of vmlinuz.efi, initrd.cpio,
// rootfs.squashfs and VERSION) and unpacks it next to the state it will replace.
func Fetch(ctx context.Context, url, sum string, report func(done, total int64)) (bootdisk.Artifacts, error) {
	a := bootdisk.Artifacts{Dir: downloadDir, Arch: archName(), ConsoleArg: installer.CurrentBootArgs()}
	_ = os.RemoveAll(downloadDir)
	if err := os.MkdirAll(downloadDir, 0o755); err != nil {
		return a, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return a, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return a, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return a, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	hash := sha256.New()
	body := io.TeeReader(&countingReader{r: resp.Body, total: resp.ContentLength, report: report}, hash)
	tr := tar.NewReader(body)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return a, fmt.Errorf("read bundle: %w", err)
		}
		name := filepath.Base(h.Name)
		switch name {
		case "vmlinuz.efi", "initrd.cpio", "rootfs.squashfs", "VERSION":
		default:
			continue
		}
		f, err := os.Create(filepath.Join(downloadDir, name))
		if err != nil {
			return a, err
		}
		_, err = io.Copy(f, tr)
		f.Close()
		if err != nil {
			return a, err
		}
	}
	_, _ = io.Copy(io.Discard, body) // hash the tar padding too
	if sum != "" && !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), sum) {
		return a, fmt.Errorf("bundle checksum mismatch")
	}
	for _, f := range []string{"vmlinuz.efi", "initrd.cpio", "rootfs.squashfs", "VERSION"} {
		if _, err := os.Stat(filepath.Join(downloadDir, f)); err != nil {
			return a, fmt.Errorf("bundle has no %s", f)
		}
	}
	version, _ := os.ReadFile(filepath.Join(downloadDir, "VERSION"))
	a.Version = strings.TrimSpace(string(version))
	return a, nil
}

type countingReader struct {
	r      io.Reader
	done   int64
	total  int64
	report func(done, total int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.done += int64(n)
	c.report(c.done, c.total)
	return n, err
}
