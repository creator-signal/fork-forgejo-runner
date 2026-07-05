// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package testutils

import (
	"flag"
	"fmt"
	"strings"
	"testing"
)

type TestFeature string

const (
	// TestFeatureDocker enables tests that exercise functionality that requires Docker or Podman.
	TestFeatureDocker TestFeature = "docker"

	// TestFeatureLXC enables tests that exercise functionality that requires LXC.
	TestFeatureLXC TestFeature = "lxc"
)

type featureList map[TestFeature]struct{}

var enabledTestFeatures featureList = map[TestFeature]struct{}{
	TestFeatureDocker: {},
	TestFeatureLXC:    {},
}

func (f *featureList) String() string {
	features := make([]string, 0, len(*f))
	for feature := range *f {
		features = append(features, string(feature))
	}

	return strings.Join(features, ",")
}

func (f *featureList) Set(value string) error {
	enabledTestFeatures = map[TestFeature]struct{}{}

	requestedFeatures := strings.Split(value, ",")
	for _, requestedFeature := range requestedFeatures {
		switch strings.TrimSpace(requestedFeature) {
		case string(TestFeatureDocker):
			(*f)[TestFeatureDocker] = struct{}{}
		case string(TestFeatureLXC):
			(*f)[TestFeatureLXC] = struct{}{}
		case "-":
			for key := range *f {
				delete(*f, key)
			}
			return nil
		default:
			return fmt.Errorf("unknown feature: %q", value)
		}
	}
	return nil
}

func init() {
	flag.Var(&enabledTestFeatures, "features",
		"comma-separated list of feature whose tests should be included, \"-\" for none")
}

// RequireTestFeatures skips a test if not all the given TestFeature instances are enabled.
func RequireTestFeatures(t *testing.T, requiredFeatures ...TestFeature) {
	t.Helper()

	for _, requiredFeature := range requiredFeatures {
		if _, exists := enabledTestFeatures[requiredFeature]; !exists {
			t.Skipf("Tests of feature %q are disabled", requiredFeature)
		}
	}
}
