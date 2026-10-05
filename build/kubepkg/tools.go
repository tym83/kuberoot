//go:build tools

// Package tools records the kubepkg commands this module builds.
package tools

import (
	_ "github.com/tym83/kubepkg/pkg/cli"
	_ "github.com/tym83/kubepkg/pkg/operator"
)
