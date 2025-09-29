package artifactcache

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/timshannon/bolthold"
	"go.etcd.io/bbolt"
)

//go:generate mockery --inpackage --name caches
type caches interface {
	getDB() *bolthold.Store
	validateMac(rundata RunData) (string, error)
	readCache(id uint64, repo string) (*Cache, error)
	useCache(id uint64) error
	setgcAt(at time.Time)
	gcCache()
	close()

	serve(w http.ResponseWriter, r *http.Request, id uint64)
	commit(id uint64, size int64) (int64, error)
	exist(id uint64) (bool, error)
	write(id, offset uint64, reader io.Reader) error
}

type cachesImpl struct {
	dir     string
	storage *Storage
	logger  logrus.FieldLogger
	secret  string

	db *bolthold.Store

	gcing atomic.Bool
	gcAt  time.Time
}

func newCaches(dir, secret string, logger logrus.FieldLogger) (caches, error) {
	c := &cachesImpl{
		secret: secret,
	}

	c.logger = logger

	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(home, ".cache", "actcache")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	c.dir = dir

	storage, err := NewStorage(filepath.Join(dir, "cache"))
	if err != nil {
		return nil, err
	}
	c.storage = storage

	file := filepath.Join(c.dir, "bolt.db")
	db, err := bolthold.Open(file, 0o644, &bolthold.Options{
		Encoder: json.Marshal,
		Decoder: json.Unmarshal,
		Options: &bbolt.Options{
			Timeout:      5 * time.Second,
			NoGrowSync:   bbolt.DefaultOptions.NoGrowSync,
			FreelistType: bbolt.DefaultOptions.FreelistType,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("Open(%s): %w", file, err)
	}
	c.db = db

	c.gcCache()

	return c, nil
}

func (c *cachesImpl) close() {
	if c.db != nil {
		c.db.Close()
		c.db = nil
	}
}

func (c *cachesImpl) getDB() *bolthold.Store {
	return c.db
}

var findCacheWithIsolationKeyFallback = func(db *bolthold.Store, repo string, keys []string, version, writeIsolationKey string) (*Cache, error) {
	cache, err := findCache(db, repo, keys, version, writeIsolationKey)
	if err != nil {
		return nil, err
	}
	// If read was scoped to WriteIsolationKey and didn't find anything, we can fallback to the non-isolated cache read
	if cache == nil && writeIsolationKey != "" {
		cache, err = findCache(db, repo, keys, version, "")
		if err != nil {
			return nil, err
		}
	}
	return cache, nil
}

// if not found, return (nil, nil) instead of an error.
func findCache(db *bolthold.Store, repo string, keys []string, version, writeIsolationKey string) (*Cache, error) {
	cache := &Cache{}
	for _, prefix := range keys {
		// if a key in the list matches exactly, don't return partial matches
		if err := db.FindOne(cache,
			bolthold.Where("Repo").Eq(repo).Index("Repo").
				And("Key").Eq(prefix).
				And("Version").Eq(version).
				And("WriteIsolationKey").Eq(writeIsolationKey).
				And("Complete").Eq(true).
				SortBy("CreatedAt").Reverse()); err == nil || !errors.Is(err, bolthold.ErrNotFound) {
			if err != nil {
				return nil, fmt.Errorf("find cache entry equal to %s: %w", prefix, err)
			}
			return cache, nil
		}
		prefixPattern := fmt.Sprintf("^%s", regexp.QuoteMeta(prefix))
		re, err := regexp.Compile(prefixPattern)
		if err != nil {
			continue
		}
		if err := db.FindOne(cache,
			bolthold.Where("Repo").Eq(repo).Index("Repo").
				And("Key").RegExp(re).
				And("Version").Eq(version).
				And("WriteIsolationKey").Eq(writeIsolationKey).
				And("Complete").Eq(true).
				SortBy("CreatedAt").Reverse()); err != nil {
			if errors.Is(err, bolthold.ErrNotFound) {
				continue
			}
			return nil, fmt.Errorf("find cache entry starting with %s: %w", prefix, err)
		}
		return cache, nil
	}
	return nil, nil
}

func insertCache(db *bolthold.Store, cache *Cache) error {
	if err := db.Insert(bolthold.NextSequence(), cache); err != nil {
		return fmt.Errorf("insert cache: %w", err)
	}
	// write back id to db
	if err := db.Update(cache.ID, cache); err != nil {
		return fmt.Errorf("write back id to db: %w", err)
	}
	return nil
}

func (c *cachesImpl) readCache(id uint64, repo string) (*Cache, error) {
	db := c.getDB()
	cache := &Cache{}
	if err := db.Get(id, cache); err != nil {
		return nil, fmt.Errorf("readCache: Get(%v): %w", id, err)
	}
	if cache.Repo != repo {
		return nil, fmt.Errorf("readCache: Get(%v): cache.Repo %s != repo %s", id, cache.Repo, repo)
	}

	return cache, nil
}

func (c *cachesImpl) useCache(id uint64) error {
	db := c.getDB()
	cache := &Cache{}
	if err := db.Get(id, cache); err != nil {
		return fmt.Errorf("useCache: Get(%v): %w", id, err)
	}
	cache.UsedAt = time.Now().Unix()
	if err := db.Update(cache.ID, cache); err != nil {
		return fmt.Errorf("useCache: Update(%v): %v", cache.ID, err)
	}
	return nil
}

func (c *cachesImpl) serve(w http.ResponseWriter, r *http.Request, id uint64) {
	c.storage.Serve(w, r, id)
}

func (c *cachesImpl) commit(id uint64, size int64) (int64, error) {
	return c.storage.Commit(id, size)
}

func (c *cachesImpl) exist(id uint64) (bool, error) {
	return c.storage.Exist(id)
}

func (c *cachesImpl) write(id, offset uint64, reader io.Reader) error {
	return c.storage.Write(id, offset, reader)
}

const (
	keepUsed   = 30 * 24 * time.Hour
	keepUnused = 7 * 24 * time.Hour
	keepTemp   = 5 * time.Minute
	keepOld    = 5 * time.Minute
)

func (c *cachesImpl) setgcAt(at time.Time) {
	c.gcAt = at
}

func (c *cachesImpl) gcCache() {
	if c.gcing.Load() {
		return
	}
	if !c.gcing.CompareAndSwap(false, true) {
		return
	}
	defer c.gcing.Store(false)

	if time.Since(c.gcAt) < time.Hour {
		c.logger.Debugf("skip gc: %v", c.gcAt.String())
		return
	}
	c.gcAt = time.Now()
	c.logger.Debugf("gc: %v", c.gcAt.String())

	db := c.getDB()

	// Remove the caches which are not completed for a while, they are most likely to be broken.
	var caches []*Cache
	if err := db.Find(&caches, bolthold.
		Where("UsedAt").Lt(time.Now().Add(-keepTemp).Unix()).
		And("Complete").Eq(false),
	); err != nil {
		fatal(c.logger, fmt.Errorf("gc caches not completed: %v", err))
	} else {
		for _, cache := range caches {
			c.storage.Remove(cache.ID)
			if err := db.Delete(cache.ID, cache); err != nil {
				c.logger.Errorf("delete cache: %v", err)
				continue
			}
			c.logger.Infof("deleted cache: %+v", cache)
		}
	}

	// Remove the old caches which have not been used recently.
	caches = caches[:0]
	if err := db.Find(&caches, bolthold.
		Where("UsedAt").Lt(time.Now().Add(-keepUnused).Unix()),
	); err != nil {
		fatal(c.logger, fmt.Errorf("gc caches old not used: %v", err))
	} else {
		for _, cache := range caches {
			c.storage.Remove(cache.ID)
			if err := db.Delete(cache.ID, cache); err != nil {
				c.logger.Warnf("delete cache: %v", err)
				continue
			}
			c.logger.Infof("deleted cache: %+v", cache)
		}
	}

	// Remove the old caches which are too old.
	caches = caches[:0]
	if err := db.Find(&caches, bolthold.
		Where("CreatedAt").Lt(time.Now().Add(-keepUsed).Unix()),
	); err != nil {
		fatal(c.logger, fmt.Errorf("gc caches too old: %v", err))
	} else {
		for _, cache := range caches {
			c.storage.Remove(cache.ID)
			if err := db.Delete(cache.ID, cache); err != nil {
				c.logger.Warnf("delete cache: %v", err)
				continue
			}
			c.logger.Infof("deleted cache: %+v", cache)
		}
	}

	// Remove the old caches with the same key and version, keep the latest one.
	// Also keep the olds which have been used recently for a while in case of the cache is still in use.
	if results, err := db.FindAggregate(
		&Cache{},
		bolthold.Where("Complete").Eq(true),
		"Key", "Version",
	); err != nil {
		fatal(c.logger, fmt.Errorf("gc aggregate caches: %v", err))
	} else {
		for _, result := range results {
			if result.Count() <= 1 {
				continue
			}
			result.Sort("CreatedAt")
			caches = caches[:0]
			result.Reduction(&caches)
			for _, cache := range caches[:len(caches)-1] {
				if time.Since(time.Unix(cache.UsedAt, 0)) < keepOld {
					// Keep it since it has been used recently, even if it's old.
					// Or it could break downloading in process.
					continue
				}
				c.storage.Remove(cache.ID)
				if err := db.Delete(cache.ID, cache); err != nil {
					c.logger.Warnf("delete cache: %v", err)
					continue
				}
				c.logger.Infof("deleted cache: %+v", cache)
			}
		}
	}
}
