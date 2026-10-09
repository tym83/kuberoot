package router

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"time"

	v1 "github.com/tym83/kuberoot/pkg/apis/router/v1alpha1"
)

// Revision names a configuration by what it asks of the node: the specs of
// its resources. The same resources make the same revision.
func Revision(c Config) string {
	type entry struct {
		Kind, Name string
		Spec       any
	}
	var all []entry
	for _, x := range c.Interfaces {
		all = append(all, entry{"Interface", x.Name, x.Spec})
	}
	for _, x := range c.Routes {
		all = append(all, entry{"Route", x.Name, x.Spec})
	}
	for _, x := range c.NAT {
		all = append(all, entry{"NATRule", x.Name, x.Spec})
	}
	for _, x := range c.Zones {
		all = append(all, entry{"FirewallZone", x.Name, x.Spec})
	}
	for _, x := range c.Rules {
		all = append(all, entry{"FirewallRule", x.Name, x.Spec})
	}
	for _, x := range c.DHCP {
		all = append(all, entry{"DHCPServer", x.Name, x.Spec})
	}
	for _, x := range c.BGP {
		all = append(all, entry{"BGPRouter", x.Name, x.Spec})
	}
	for _, x := range c.Peers {
		all = append(all, entry{"BGPPeer", x.Name, x.Spec})
	}
	raw, _ := json.Marshal(all)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:12]
}

// trial decides which configuration the node runs under a Safeguard: the
// one the resources describe, on trial until it is confirmed, or the last
// confirmed one once the trial ran out.
type trial struct {
	confirmed    Config
	confirmedRev string
	pending      string
	deadline     time.Time
	rolledBack   string
}

// decide returns the configuration to run and its revision. It reports
// whether the confirmed configuration changed, to be kept on disk.
func (t *trial) decide(candidate Config, rev string, guard *v1.Safeguard, now time.Time) (Config, string, bool) {
	confirm := func() (Config, string, bool) {
		changed := t.confirmedRev != rev
		t.confirmed, t.confirmedRev, t.pending, t.deadline, t.rolledBack = candidate, rev, "", time.Time{}, ""
		return candidate, rev, changed
	}
	switch {
	case guard == nil, t.confirmedRev == "", rev == t.confirmedRev:
		return confirm()
	case guard.Spec.Confirm == rev:
		return confirm()
	case rev == t.rolledBack:
		// Undone already; it stays undone until the resources change.
		return t.confirmed, t.confirmedRev, false
	}
	if t.pending != rev {
		within := guard.Spec.ConfirmWithin.Duration
		if within <= 0 {
			within = 2 * time.Minute
		}
		t.pending, t.deadline = rev, now.Add(within)
	}
	if !now.Before(t.deadline) {
		// Kept on disk: after a restart the undone revision stays undone.
		t.rolledBack, t.pending, t.deadline = rev, "", time.Time{}
		return t.confirmed, t.confirmedRev, true
	}
	return candidate, rev, false
}

// savedTrial is the confirmed configuration as kept on disk.
type savedTrial struct {
	Revision   string `json:"revision"`
	Config     Config `json:"config"`
	RolledBack string `json:"rolledBack,omitempty"`
}

func (t *trial) save(path string) error {
	raw, err := json.Marshal(savedTrial{t.confirmedRev, t.confirmed, t.rolledBack})
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (t *trial) load(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var s savedTrial
	if json.Unmarshal(raw, &s) == nil {
		t.confirmed, t.confirmedRev, t.rolledBack = s.Config, s.Revision, s.RolledBack
	}
}
