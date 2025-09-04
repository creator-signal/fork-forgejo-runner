package artifactcache

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMac(t *testing.T) {
	handler := &handler{
		secret: "secret for testing",
	}

	t.Run("validate correct mac", func(t *testing.T) {
		name := "org/reponame"
		run := "1"
		ts := strconv.FormatInt(time.Now().Unix(), 10)

		mac := ComputeMac(handler.secret, name, run, ts, "")
		rundata := RunData{
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

		mac := ComputeMac(handler.secret, name, run, ts, "")
		rundata := RunData{
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

		rundata := RunData{
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

		mac := ComputeMac(secret, name, run, ts, "")
		expectedMac := "4754474b21329e8beadd2b4054aa4be803965d66e710fa1fee091334ed804f29" // * Precomputed, anytime the ComputeMac function changes this needs to be recalculated
		require.Equal(t, expectedMac, mac)

		mac = ComputeMac(secret, name, run, ts, "refs/pull/12/head")
		expectedMac = "9ca8f4cb5e1b083ee8cd215215bc00f379b28511d3ef7930bf054767de34766d" // * Precomputed, anytime the ComputeMac function changes this needs to be recalculated
		require.Equal(t, expectedMac, mac)
	})
}
