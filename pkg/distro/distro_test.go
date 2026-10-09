package distro

import (
	"path/filepath"
	"testing"
)

// Every distribution in the repository parses and has a control plane.
func TestDistributionsParse(t *testing.T) {
	files, err := filepath.Glob("../../distros/*/profile.yaml")
	if err != nil || len(files) < 2 {
		t.Fatalf("profiles: %v, %v", files, err)
	}
	for _, f := range files {
		p, err := Load(f)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		cp, ok := p.Spec.Roles[RoleControlPlane]
		if !ok || len(cp.Services) == 0 {
			t.Errorf("%s: no control plane services", f)
		}
		if filepath.Base(filepath.Dir(f)) != p.Metadata.Name {
			t.Errorf("%s is named %s", f, p.Metadata.Name)
		}
	}
}
