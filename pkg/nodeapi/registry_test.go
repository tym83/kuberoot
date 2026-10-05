package nodeapi

import (
	"sort"
	"strings"
	"testing"
)

func names(m map[string]any) string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func TestEnabledResources(t *testing.T) {
	all := enabledResources(nil, resourceDeps{})
	if len(all) != len(nodeResources) {
		t.Errorf("no list served %d resources, want all %d", len(all), len(nodeResources))
	}
	some := enabledResources([]string{"osconfigs", "nodeservices", "nonsense"}, resourceDeps{})
	got := map[string]any{}
	for k, v := range some {
		got[k] = v
	}
	if names(got) != "nodeservices,osconfigs" {
		t.Errorf("served %s", names(got))
	}
}
