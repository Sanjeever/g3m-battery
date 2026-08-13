//go:build windows

package main

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var versionFile string

func applicationVersion() string {
	return strings.TrimSpace(versionFile)
}
