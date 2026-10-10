// Package safeguard runs configuration changes on trial: a change the node
// applies is undone by itself unless it is confirmed in time, so a change
// that cuts the administrators, or the devices, off undoes itself. The
// router and the devices controllers keep their configurations under it.
package safeguard

import (
	"encoding/json"
	"os"
	"time"
)

// Guard is what a Safeguard resource asks for.
type Guard struct {
	// ConfirmWithin is how long a change runs on trial; two minutes when 0.
	ConfirmWithin time.Duration
	// Confirm, set to the pending revision, makes it final.
	Confirm string
	// AutoConfirm makes a change final when it ran its trial healthy; without
	// it, only Confirm does.
	AutoConfirm bool
}

// Trial decides which configuration of type C a node runs: the one its
// resources describe, on trial until it is confirmed, or the last confirmed
// one once the trial failed.
type Trial[C any] struct {
	Confirmed    C
	ConfirmedRev string
	// Pending is the revision on trial, until Deadline.
	Pending  string
	Deadline time.Time
	// RolledBack is the last revision undone, and Why.
	RolledBack string
	Why        string
}

// Decide returns the configuration to run and its revision, and whether
// what is kept on disk changed. unhealthy, when not empty, says the
// candidate broke something the confirmed configuration had working; on
// trial, that undoes it at once.
func (t *Trial[C]) Decide(candidate C, rev string, g *Guard, unhealthy string, now time.Time) (C, string, bool) {
	confirm := func() (C, string, bool) {
		changed := t.ConfirmedRev != rev
		t.Confirmed, t.ConfirmedRev, t.Pending, t.Deadline, t.RolledBack, t.Why = candidate, rev, "", time.Time{}, "", ""
		return candidate, rev, changed
	}
	undo := func(why string) (C, string, bool) {
		// Kept on disk: after a restart the undone revision stays undone.
		t.RolledBack, t.Why, t.Pending, t.Deadline = rev, why, "", time.Time{}
		return t.Confirmed, t.ConfirmedRev, true
	}
	switch {
	case g == nil, t.ConfirmedRev == "", rev == t.ConfirmedRev:
		return confirm()
	case g.Confirm == rev:
		return confirm()
	case rev == t.RolledBack:
		// Undone already; it stays undone until the resources change.
		return t.Confirmed, t.ConfirmedRev, false
	}
	if t.Pending != rev {
		within := g.ConfirmWithin
		if within <= 0 {
			within = 2 * time.Minute
		}
		t.Pending, t.Deadline = rev, now.Add(within)
	}
	if unhealthy != "" {
		return undo(unhealthy)
	}
	if !now.Before(t.Deadline) {
		if g.AutoConfirm {
			return confirm()
		}
		return undo("not confirmed in time")
	}
	return candidate, rev, false
}

// saved is the confirmed configuration as kept on disk.
type saved[C any] struct {
	Revision   string `json:"revision"`
	Config     C      `json:"config"`
	RolledBack string `json:"rolledBack,omitempty"`
	Why        string `json:"why,omitempty"`
}

// Save keeps the confirmed configuration, and what was undone, on disk.
func (t *Trial[C]) Save(path string) error {
	raw, err := json.Marshal(saved[C]{t.ConfirmedRev, t.Confirmed, t.RolledBack, t.Why})
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load reads what Save kept, if anything.
func (t *Trial[C]) Load(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var s saved[C]
	if json.Unmarshal(raw, &s) == nil {
		t.Confirmed, t.ConfirmedRev, t.RolledBack, t.Why = s.Config, s.Revision, s.RolledBack, s.Why
	}
}
