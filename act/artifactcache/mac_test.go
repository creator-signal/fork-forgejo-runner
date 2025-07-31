package artifactcache

import (
	"strconv"
	"testing"
	"time"

	"code.forgejo.org/forgejo/runner/v9/act/cacheproxy"
	"github.com/stretchr/testify/require"
)

func TestMac(t *testing.T) {
	handler := &Handler{
		secret: "secret for testing",
	}

	t.Run("validate correct mac", func(t *testing.T) {
		name := "org/reponame"
		run := "1"
		ts := strconv.FormatInt(time.Now().Unix(), 10)

		mac := computeMac(handler.secret, name, run, ts)
		rundata := cacheproxy.RunData{
			RepositoryFullName: name,
			RunNumber:          run,
			Timestamp:          ts,
			RepositoryMAC:      mac,
		}

		repoName, err := handler.validateMac(rundata)
		require.NoError(t, err)
		require.Equal(t, name, repoName)
	})

	t.Run("validate incorrect timestamp", func(t *testing.T) {
		name := "org/reponame"
		run := "1"
		ts := "9223372036854775807" // This should last us for a while...

		mac := computeMac(handler.secret, name, run, ts)
		rundata := cacheproxy.RunData{
			RepositoryFullName: name,
			RunNumber:          run,
			Timestamp:          ts,
			RepositoryMAC:      mac,
		}

		_, err := handler.validateMac(rundata)
		require.Error(t, err)
	})

	t.Run("validate incorrect mac", func(t *testing.T) {
		name := "org/reponame"
		run := "1"
		ts := strconv.FormatInt(time.Now().Unix(), 10)

		rundata := cacheproxy.RunData{
			RepositoryFullName: name,
			RunNumber:          run,
			Timestamp:          ts,
			RepositoryMAC:      "this is not the right mac :D",
		}

		repoName, err := handler.validateMac(rundata)
		require.Error(t, err)
		require.Equal(t, "", repoName)
	})

	t.Run("compute correct mac", func(t *testing.T) {
		secret := "this is my cool secret string :3"
		name := "org/reponame"
		run := "42"
		ts := "1337"

		mac := computeMac(secret, name, run, ts)
		expectedMac := "f666f06f917acb7186e152195b2a8c8d36d068ce683454be0878806e08e04f2b" // * Precomputed, anytime the computeMac function changes this needs to be recalculated

		require.Equal(t, mac, expectedMac)
	})
}
