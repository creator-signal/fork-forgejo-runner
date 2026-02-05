package artifactcache

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMac(t *testing.T) {
	cache := &cachesImpl{
		secret: "secret for testing",
	}

	t.Run("validate correct mac", func(t *testing.T) {
		name := "org/reponame"
		run := "1"
		ts := strconv.FormatInt(time.Now().Unix(), 10)

		mac := ComputeMac(cache.secret, "", name, run, ts, "")
		rundata := RunData{
			RepositoryFullName: name,
			RunNumber:          run,
			Timestamp:          ts,
			RepositoryMAC:      mac,
		}

		repoName, err := cache.validateMac(rundata)
		require.NoError(t, err)
		require.Equal(t, name, repoName)
	})

	t.Run("validate correct mac with instance", func(t *testing.T) {
		instance := "https://codeberg.org"
		name := "org/reponame"
		run := "1"
		ts := strconv.FormatInt(time.Now().Unix(), 10)

		mac := ComputeMac(cache.secret, instance, name, run, ts, "")
		rundata := RunData{
			Instance:           instance,
			RepositoryFullName: name,
			RunNumber:          run,
			Timestamp:          ts,
			RepositoryMAC:      mac,
		}

		repoName, err := cache.validateMac(rundata)
		require.NoError(t, err)
		require.Equal(t, name, repoName)
	})

	t.Run("validate incorrect instance", func(t *testing.T) {
		instance := "https://codeberg.org"
		name := "org/reponame"
		run := "1"
		ts := strconv.FormatInt(time.Now().Unix(), 10)

		mac := ComputeMac(cache.secret, instance, name, run, ts, "")
		rundata := RunData{
			Instance:           "https://forgejo.org",
			RepositoryFullName: name,
			RunNumber:          run,
			Timestamp:          ts,
			RepositoryMAC:      mac,
		}

		_, err := cache.validateMac(rundata)
		require.Error(t, err)
	})

	t.Run("validate incorrect timestamp", func(t *testing.T) {
		name := "org/reponame"
		run := "1"
		ts := "9223372036854775807" // This should last us for a while...

		mac := ComputeMac(cache.secret, "", name, run, ts, "")
		rundata := RunData{
			RepositoryFullName: name,
			RunNumber:          run,
			Timestamp:          ts,
			RepositoryMAC:      mac,
		}

		_, err := cache.validateMac(rundata)
		require.Error(t, err)
	})

	t.Run("validate incorrect mac", func(t *testing.T) {
		name := "org/reponame"
		run := "1"
		ts := strconv.FormatInt(time.Now().Unix(), 10)

		rundata := RunData{
			RepositoryFullName: name,
			RunNumber:          run,
			Timestamp:          ts,
			RepositoryMAC:      "this is not the right mac :D",
		}

		repoName, err := cache.validateMac(rundata)
		require.Error(t, err)
		require.Equal(t, "", repoName)
	})

	t.Run("compute correct mac", func(t *testing.T) {
		secret := "this is my cool secret string :3"
		name := "org/reponame"
		run := "42"
		ts := "1337"

		mac := ComputeMac(secret, "", name, run, ts, "")
		expectedMac := "cc5196827f59ec895b475ca1972d729f6314d9edb198c33fa1d49dea9fa6bcb7" // * Precomputed, anytime the ComputeMac function changes this needs to be recalculated
		require.Equal(t, expectedMac, mac)

		mac = ComputeMac(secret, "", name, run, ts, "refs/pull/12/head")
		expectedMac = "c4b018cf7b7250174d51b0eb9c5cf2ae35d97231f9b96c5815e44be53d9b06e1" // * Precomputed, anytime the ComputeMac function changes this needs to be recalculated
		require.Equal(t, expectedMac, mac)

		mac = ComputeMac(secret, "https://codeberg.org", name, run, ts, "")
		expectedMac = "fb1c1ae1782c6b3a926501784dae631c20af25346666c09fab044a0fdbf3302c" // * Precomputed, anytime the ComputeMac function changes this needs to be recalculated
		require.Equal(t, expectedMac, mac)
	})
}
