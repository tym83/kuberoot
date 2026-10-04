// Package upgrade moves a node between its A and B root slots: it stages a new
// release into the inactive slot, boots it on probation and keeps it only once
// the node comes up healthy. systemd-boot counts the attempts and falls back.
package upgrade

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/tym83/kuberoot/pkg/bootdisk"
	"github.com/tym83/kuberoot/pkg/installer"
)

const (
	efiMount   = "/run/kuberoot/efi"
	entriesDir = efiMount + "/loader/entries"
	// Tries is how many boots a freshly staged slot gets to prove itself.
	Tries = 3
)

// Entry states.
const (
	StateGood   = "Good"   // booted successfully at least once
	StateTrying = "Trying" // staged, boot attempts left
	StateBad    = "Bad"    // ran out of attempts; systemd-boot skips it
)

// Entry is the boot entry of one slot as found on the EFI partition.
type Entry struct {
	Slot    string
	File    string // file name, including any boot counter
	Version int    // sort version: the highest good-or-trying entry boots by default
	Release string // kuberoot release in the title
	State   string
	Left    int
	Done    int
	Booted  bool
	Default bool
}

var counter = regexp.MustCompile(`^kuberoot-([ab])(?:\+(\d+)(?:-(\d+))?)?\.conf$`)

// mu serialises everything that touches the EFI partition.
var mu sync.Mutex

// CurrentSlot is the slot the running system booted from.
func CurrentSlot() string {
	raw, _ := os.ReadFile("/proc/cmdline")
	for _, f := range strings.Fields(string(raw)) {
		if v, ok := strings.CutPrefix(f, "kuberoot.root=PARTLABEL="+bootdisk.RootLabel("")); ok {
			return v
		}
	}
	return ""
}

func otherSlot(slot string) string {
	if slot == "a" {
		return "b"
	}
	return "a"
}

// withESP mounts the EFI partition of the system disk for the duration of fn.
func withESP(fn func() error) error {
	mu.Lock()
	defer mu.Unlock()
	dev := installer.PartitionOn("", bootdisk.EFILabel)
	if dev == "" {
		return fmt.Errorf("no EFI partition: not an installed system")
	}
	if err := os.MkdirAll(efiMount, 0o755); err != nil {
		return err
	}
	if err := unix.Mount(dev, efiMount, "vfat", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "umask=0077"); err != nil {
		return fmt.Errorf("mount EFI partition: %w", err)
	}
	defer func() {
		unix.Sync()
		_ = unix.Unmount(efiMount, 0)
	}()
	return fn()
}

// Entries lists the slot entries, newest first.
func Entries() ([]Entry, error) {
	var out []Entry
	err := withESP(func() error {
		var err error
		out, err = readEntries()
		return err
	})
	return out, err
}

func readEntries() ([]Entry, error) {
	files, err := os.ReadDir(entriesDir)
	if err != nil {
		return nil, err
	}
	current := CurrentSlot()
	var out []Entry
	for _, f := range files {
		m := counter.FindStringSubmatch(f.Name())
		if m == nil {
			continue
		}
		e := Entry{Slot: m[1], File: f.Name(), State: StateGood, Booted: m[1] == current}
		if m[2] != "" {
			e.Left, _ = strconv.Atoi(m[2])
			e.Done, _ = strconv.Atoi(m[3])
			e.State = StateTrying
			if e.Left == 0 {
				e.State = StateBad
			}
		}
		raw, _ := os.ReadFile(filepath.Join(entriesDir, f.Name()))
		for _, line := range strings.Split(string(raw), "\n") {
			if v, ok := strings.CutPrefix(line, "version "); ok {
				e.Version, _ = strconv.Atoi(strings.TrimSpace(v))
			}
			if v, ok := strings.CutPrefix(line, "title "); ok {
				e.Release = strings.Fields(v + " ? ")[1]
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	for i := range out {
		if out[i].State != StateBad {
			out[i].Default = true
			break
		}
	}
	return out, nil
}

// Stage writes a release into the inactive slot and makes it the next boot,
// on probation: it gets Tries boots to come up healthy.
func Stage(a bootdisk.Artifacts, report func(done, total int64)) (string, error) {
	current := CurrentSlot()
	if current == "" {
		return "", fmt.Errorf("not booted from an installed slot")
	}
	target := otherSlot(current)
	dev := installer.PartitionOn("", bootdisk.RootLabel(target))
	if dev == "" {
		return "", fmt.Errorf("no root partition for slot %s", target)
	}
	if err := installer.WriteRoot(nil, a, dev, report); err != nil {
		return "", fmt.Errorf("write slot %s: %w", target, err)
	}
	return target, withESP(func() error {
		entries, err := readEntries()
		if err != nil {
			return err
		}
		top := 0
		for _, e := range entries {
			top = max(top, e.Version)
			if e.Slot == target {
				_ = os.Remove(filepath.Join(entriesDir, e.File))
			}
		}
		slotDir := filepath.Join(efiMount, "kuberoot", target)
		if err := os.MkdirAll(slotDir, 0o755); err != nil {
			return err
		}
		for _, f := range []string{"vmlinuz.efi", "initrd.cpio"} {
			if err := copyFile(filepath.Join(a.Dir, f), filepath.Join(slotDir, f)); err != nil {
				return err
			}
		}
		entry := bootdisk.SlotEntry(a, target, strconv.Itoa(top+1))
		name := fmt.Sprintf("%s+%d.conf", entry.ID, Tries)
		return os.WriteFile(filepath.Join(entriesDir, name), []byte(entry.String()), 0o644)
	})
}

// MarkGood ends the probation of the running slot by dropping its boot counter.
func MarkGood() (bool, error) {
	var marked bool
	err := withESP(func() error {
		entries, err := readEntries()
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.Booted && e.State != StateGood {
				marked = true
				return os.Rename(filepath.Join(entriesDir, e.File), filepath.Join(entriesDir, "kuberoot-"+e.Slot+".conf"))
			}
		}
		return nil
	})
	return marked, err
}

// Prefer makes slot the default for the next boot: a manual rollback or roll-forward.
func Prefer(slot string) error {
	return withESP(func() error {
		entries, err := readEntries()
		if err != nil {
			return err
		}
		top := 0
		var file string
		for _, e := range entries {
			top = max(top, e.Version)
			if e.Slot == slot {
				file = e.File
			}
		}
		if file == "" {
			return fmt.Errorf("slot %s has no boot entry", slot)
		}
		path := filepath.Join(entriesDir, file)
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := strings.Split(string(raw), "\n")
		for i, line := range lines {
			if strings.HasPrefix(line, "version ") {
				lines[i] = "version " + strconv.Itoa(top+1)
			}
		}
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			return err
		}
		// A slot chosen by hand is trusted again.
		return os.Rename(path, filepath.Join(entriesDir, "kuberoot-"+slot+".conf"))
	})
}

func copyFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, raw, 0o644)
}
