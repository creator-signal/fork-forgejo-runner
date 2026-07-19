// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"fmt"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// parsePlatform converts platform identifiers like `linux/amd64` into the format expected by the Moby client libraries.
func parsePlatform(platform string) (ocispec.Platform, error) {
	os, arch, found := strings.Cut(platform, `/`)
	if !found || os == "" || arch == "" {
		return ocispec.Platform{}, fmt.Errorf("malformed container platform: %q", platform)
	}

	return ocispec.Platform{Architecture: arch, OS: os}, nil
}
