package runner

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestActionsOS(t *testing.T) {
	// The keys are possible values of runtime.GOOS. The list can be compiled by running:
	// go tool dist list | cut -d/ -f1 | sort -u
	for in, want := range map[string]string{
		"aix":       "undefined",
		"android":   "undefined",
		"darwin":    "macOS",
		"dragonfly": "undefined",
		"freebsd":   "undefined",
		"illumos":   "undefined",
		"ios":       "undefined",
		"js":        "undefined",
		"linux":     "Linux",
		"netbsd":    "undefined",
		"openbsd":   "undefined",
		"plan9":     "undefined",
		"solaris":   "undefined",
		"wasip1":    "undefined",
		"windows":   "Windows",
	} {
		t.Run(in, func(t *testing.T) {
			assert.Equal(t, want, actionsOS(in))
		})
	}
}

func TestActionsArch(t *testing.T) {
	// The keys are possible values of runtime.GOARCH. The list can be compiled by running:
	// go tool dist list | cut -d/ -f2 | sort -u
	for in, want := range map[string]string{
		"386":      "X86",
		"amd64":    "X64",
		"arm":      "ARM",
		"arm64":    "ARM64",
		"loong64":  "undefined",
		"mips":     "undefined",
		"mips64":   "undefined",
		"mips64le": "undefined",
		"mipsle":   "undefined",
		"ppc64":    "undefined",
		"ppc64le":  "undefined",
		"riscv64":  "undefined",
		"s390x":    "undefined",
		"wasm":     "undefined",
	} {
		t.Run(in, func(t *testing.T) {
			assert.Equal(t, want, actionsArch(in))
		})
	}
}
