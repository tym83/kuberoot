package main

import (
	"fmt"
	"os"
	"strings"
)

// applySysctls sets the kernel parameters of the distribution profile.
func applySysctls(sysctls map[string]string) error {
	for key, value := range sysctls {
		path := "/proc/sys/" + strings.ReplaceAll(key, ".", "/")
		if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
			return fmt.Errorf("sysctl %s: %w", key, err)
		}
	}
	return nil
}
