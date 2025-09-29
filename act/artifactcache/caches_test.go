package artifactcache

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/timshannon/bolthold"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheReadWrite(t *testing.T) {
	caches, err := newCaches(t.TempDir(), "secret", logrus.New())
	require.NoError(t, err)
	defer caches.close()
	t.Run("NotFound", func(t *testing.T) {
		found, err := caches.readCache(456, "repo")
		assert.Nil(t, found)
		assert.ErrorIs(t, err, bolthold.ErrNotFound)
	})

	repo := "repository"
	cache := &Cache{
		Repo:    repo,
		Key:     "key",
		Version: "version",
		Size:    444,
	}
	now := time.Now().Unix()
	cache.CreatedAt = now
	cache.UsedAt = now
	cache.Repo = repo

	t.Run("Insert", func(t *testing.T) {
		db := caches.getDB()
		assert.NoError(t, insertCache(db, cache))
	})

	t.Run("Found", func(t *testing.T) {
		found, err := caches.readCache(cache.ID, cache.Repo)
		require.NoError(t, err)
		assert.Equal(t, cache.ID, found.ID)
	})

	t.Run("InvalidRepo", func(t *testing.T) {
		invalidRepo := "INVALID REPO"
		found, err := caches.readCache(cache.ID, invalidRepo)
		assert.Nil(t, found)
		assert.ErrorContains(t, err, invalidRepo)
	})
}
