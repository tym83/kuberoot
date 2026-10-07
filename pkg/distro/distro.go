// Package distro is the distribution profile built into a kuberoot image:
// what its node roles generate and run, and which node API resources it serves.
package distro

import (
	"fmt"
	"os"

	"sigs.k8s.io/yaml"
)

// Path is where the image carries its profile.
const Path = "/usr/share/kuberoot/profile.yaml"

// Role names.
const (
	RoleControlPlane = "controlPlane"
	RoleWorker       = "worker"
)

type Profile struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec Spec `json:"spec"`
}

// Module is a kernel module and its parameters.
type Module struct {
	Name   string `json:"name"`
	Params string `json:"params,omitempty"`
}

type Spec struct {
	Sysctls map[string]string `json:"sysctls,omitempty"`
	// Modules are loaded at boot, in order, from the out-of-tree modules
	// built with the kernel; module loading is then disabled.
	Modules []Module        `json:"modules,omitempty"`
	Roles   map[string]Role `json:"roles"`
	NodeAPI NodeAPI         `json:"nodeAPI,omitempty"`
}

type Role struct {
	// Generators, by name, write what the role needs on disk.
	Generators []string `json:"generators"`
	// Addons: apply the bundled add-ons and install the distribution's packages.
	Addons   bool      `json:"addons,omitempty"`
	Services []Service `json:"services"`
}

type Service struct {
	Name string `json:"name"`
	// After names what must be ready before the first start: apiserver or kubelet-cert.
	After string   `json:"after,omitempty"`
	Env   []string `json:"env,omitempty"`
	// Args are templates over the role's facts; arguments that render empty are dropped.
	Args []string `json:"args"`
}

// NodeAPI picks the resources the node API serves; empty serves them all.
type NodeAPI struct {
	Resources []string `json:"resources,omitempty"`
}

// Load reads and checks a profile file.
func Load(path string) (*Profile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(raw, path)
}

func Parse(raw []byte, source string) (*Profile, error) {
	p := &Profile{}
	if err := yaml.UnmarshalStrict(raw, p); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	if p.Kind != "Distribution" {
		return nil, fmt.Errorf("%s: kind %q, want Distribution", source, p.Kind)
	}
	return p, nil
}
