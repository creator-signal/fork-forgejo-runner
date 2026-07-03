package testutils

import (
	"log"
	"os"
	"strings"
	"testing"
)

type TestFeature string

const (
	// TestFeatureDocker enables tests that require Docker or Podman.
	TestFeatureDocker TestFeature = "docker"
)

var enabledTestFeatures = map[TestFeature]struct{}{}

func init() {
	// Windows' treatment of empty environment variables is inconsistent. cmd.exe and versions of PowerShell before 7.5
	// treat them as non-existent. Windows users can therefore use TEST_FEATURES="-" to disable all test features.
	testFeaturesToEnable, exists := os.LookupEnv("TEST_FEATURES")
	if !exists {
		enabledTestFeatures = map[TestFeature]struct{}{
			TestFeatureDocker: {},
		}
	} else if testFeaturesToEnable != "" && testFeaturesToEnable != "-" {
		for _, feature := range strings.Split(testFeaturesToEnable, ",") {
			switch feature {
			case "docker":
				enabledTestFeatures[TestFeatureDocker] = struct{}{}
			default:
				log.Panicf("Unknown test feature: %q", feature)
			}
		}
	}

	keys := make([]string, 0, len(enabledTestFeatures))
	for feature := range enabledTestFeatures {
		keys = append(keys, string(feature))
	}
	log.Printf("Enabled test features: %q", strings.Join(keys, ", "))
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
