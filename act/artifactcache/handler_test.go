package artifactcache

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/timshannon/bolthold"
	"go.etcd.io/bbolt"
)

const (
	cacheRepo      = "testuser/repo"
	cacheRunnum    = "1"
	cacheTimestamp = "0"
	cacheMac       = "bc2e9167f9e310baebcead390937264e4c0b21d2fdd49f5b9470d54406099360"
)

var handlerExternalURL string

type AuthHeaderTransport struct {
	T                  http.RoundTripper
	WriteIsolationKey  string
	OverrideDefaultMac string
}

func (t *AuthHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Forgejo-Cache-Repo", cacheRepo)
	req.Header.Set("Forgejo-Cache-RunNumber", cacheRunnum)
	req.Header.Set("Forgejo-Cache-Timestamp", cacheTimestamp)
	if t.OverrideDefaultMac != "" {
		req.Header.Set("Forgejo-Cache-MAC", t.OverrideDefaultMac)
	} else {
		req.Header.Set("Forgejo-Cache-MAC", cacheMac)
	}
	req.Header.Set("Forgejo-Cache-Host", handlerExternalURL)
	if t.WriteIsolationKey != "" {
		req.Header.Set("Forgejo-Cache-WriteIsolationKey", t.WriteIsolationKey)
	}
	return t.T.RoundTrip(req)
}

var (
	httpClientTransport = AuthHeaderTransport{T: http.DefaultTransport}
	httpClient          = http.Client{Transport: &httpClientTransport}
)

func TestHandler(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "artifactcache")
	handler, err := StartHandler(dir, "", 0, "secret", nil)
	require.NoError(t, err)

	handlerExternalURL = handler.ExternalURL()
	base := fmt.Sprintf("%s%s", handler.ExternalURL(), urlBase)

	defer func() {
		t.Run("inpect db", func(t *testing.T) {
			db, err := handler.openDB()
			require.NoError(t, err)
			defer db.Close()
			require.NoError(t, db.Bolt().View(func(tx *bbolt.Tx) error {
				return tx.Bucket([]byte("Cache")).ForEach(func(k, v []byte) error {
					t.Logf("%s: %s", k, v)
					return nil
				})
			}))
		})
		t.Run("close", func(t *testing.T) {
			require.NoError(t, handler.Close())
			assert.True(t, handler.isClosed())
			_, err := httpClient.Post(fmt.Sprintf("%s/caches/%d", base, 1), "", nil)
			assert.Error(t, err)
		})
	}()

	t.Run("get not exist", func(t *testing.T) {
		key := strings.ToLower(t.Name())
		version := "c19da02a2bd7e77277f1ac29ab45c09b7d46a4ee758284e26bb3045ad11d9d20"
		resp, err := httpClient.Get(fmt.Sprintf("%s/cache?keys=%s&version=%s", base, key, version))
		require.NoError(t, err)
		require.Equal(t, 204, resp.StatusCode)
	})

	t.Run("reserve and upload", func(t *testing.T) {
		key := strings.ToLower(t.Name())
		version := "c19da02a2bd7e77277f1ac29ab45c09b7d46a4ee758284e26bb3045ad11d9d20"
		content := make([]byte, 100)
		_, err := rand.Read(content)
		require.NoError(t, err)
		uploadCacheNormally(t, base, key, version, "", content)
	})

	t.Run("clean", func(t *testing.T) {
		resp, err := httpClient.Post(fmt.Sprintf("%s/clean", base), "", nil)
		require.NoError(t, err)
		assert.Equal(t, 200, resp.StatusCode)
	})

	t.Run("reserve with bad request", func(t *testing.T) {
		body := []byte(`invalid json`)
		require.NoError(t, err)
		resp, err := httpClient.Post(fmt.Sprintf("%s/caches", base), "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		assert.Equal(t, 400, resp.StatusCode)
	})

	t.Run("duplicate reserve", func(t *testing.T) {
		key := strings.ToLower(t.Name())
		version := "c19da02a2bd7e77277f1ac29ab45c09b7d46a4ee758284e26bb3045ad11d9d20"
		var first, second struct {
			CacheID uint64 `json:"cacheId"`
		}
		{
			body, err := json.Marshal(&Request{
				Key:     key,
				Version: version,
				Size:    100,
			})
			require.NoError(t, err)
			resp, err := httpClient.Post(fmt.Sprintf("%s/caches", base), "application/json", bytes.NewReader(body))
			require.NoError(t, err)
			assert.Equal(t, 200, resp.StatusCode)

			require.NoError(t, json.NewDecoder(resp.Body).Decode(&first))
			assert.NotZero(t, first.CacheID)
		}
		{
			body, err := json.Marshal(&Request{
				Key:     key,
				Version: version,
				Size:    100,
			})
			require.NoError(t, err)
			resp, err := httpClient.Post(fmt.Sprintf("%s/caches", base), "application/json", bytes.NewReader(body))
			require.NoError(t, err)
			assert.Equal(t, 200, resp.StatusCode)

			require.NoError(t, json.NewDecoder(resp.Body).Decode(&second))
			assert.NotZero(t, second.CacheID)
		}

		assert.NotEqual(t, first.CacheID, second.CacheID)
	})

	t.Run("upload with bad id", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPatch,
			fmt.Sprintf("%s/caches/invalid_id", base), bytes.NewReader(nil))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Content-Range", "bytes 0-99/*")
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, 400, resp.StatusCode)
	})

	t.Run("upload without reserve", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPatch,
			fmt.Sprintf("%s/caches/%d", base, 1000), bytes.NewReader(nil))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Content-Range", "bytes 0-99/*")
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, 404, resp.StatusCode)
	})

	t.Run("upload with complete", func(t *testing.T) {
		key := strings.ToLower(t.Name())
		version := "c19da02a2bd7e77277f1ac29ab45c09b7d46a4ee758284e26bb3045ad11d9d20"
		var id uint64
		content := make([]byte, 100)
		_, err := rand.Read(content)
		require.NoError(t, err)
		{
			body, err := json.Marshal(&Request{
				Key:     key,
				Version: version,
				Size:    100,
			})
			require.NoError(t, err)
			resp, err := httpClient.Post(fmt.Sprintf("%s/caches", base), "application/json", bytes.NewReader(body))
			require.NoError(t, err)
			assert.Equal(t, 200, resp.StatusCode)

			got := struct {
				CacheID uint64 `json:"cacheId"`
			}{}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
			id = got.CacheID
		}
		{
			req, err := http.NewRequest(http.MethodPatch,
				fmt.Sprintf("%s/caches/%d", base, id), bytes.NewReader(content))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/octet-stream")
			req.Header.Set("Content-Range", "bytes 0-99/*")
			resp, err := httpClient.Do(req)
			require.NoError(t, err)
			assert.Equal(t, 200, resp.StatusCode)
		}
		{
			resp, err := httpClient.Post(fmt.Sprintf("%s/caches/%d", base, id), "", nil)
			require.NoError(t, err)
			assert.Equal(t, 200, resp.StatusCode)
		}
		{
			req, err := http.NewRequest(http.MethodPatch,
				fmt.Sprintf("%s/caches/%d", base, id), bytes.NewReader(content))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/octet-stream")
			req.Header.Set("Content-Range", "bytes 0-99/*")
			resp, err := httpClient.Do(req)
			require.NoError(t, err)
			assert.Equal(t, 400, resp.StatusCode)
		}
	})

	t.Run("upload with invalid range", func(t *testing.T) {
		key := strings.ToLower(t.Name())
		version := "c19da02a2bd7e77277f1ac29ab45c09b7d46a4ee758284e26bb3045ad11d9d20"
		var id uint64
		content := make([]byte, 100)
		_, err := rand.Read(content)
		require.NoError(t, err)
		{
			body, err := json.Marshal(&Request{
				Key:     key,
				Version: version,
				Size:    100,
			})
			require.NoError(t, err)
			resp, err := httpClient.Post(fmt.Sprintf("%s/caches", base), "application/json", bytes.NewReader(body))
			require.NoError(t, err)
			assert.Equal(t, 200, resp.StatusCode)

			got := struct {
				CacheID uint64 `json:"cacheId"`
			}{}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
			id = got.CacheID
		}
		{
			req, err := http.NewRequest(http.MethodPatch,
				fmt.Sprintf("%s/caches/%d", base, id), bytes.NewReader(content))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/octet-stream")
			req.Header.Set("Content-Range", "bytes xx-99/*")
			resp, err := httpClient.Do(req)
			require.NoError(t, err)
			assert.Equal(t, 400, resp.StatusCode)
		}
	})

	t.Run("commit with bad id", func(t *testing.T) {
		{
			resp, err := httpClient.Post(fmt.Sprintf("%s/caches/invalid_id", base), "", nil)
			require.NoError(t, err)
			assert.Equal(t, 400, resp.StatusCode)
		}
	})

	t.Run("commit with not exist id", func(t *testing.T) {
		{
			resp, err := httpClient.Post(fmt.Sprintf("%s/caches/%d", base, 100), "", nil)
			require.NoError(t, err)
			assert.Equal(t, 404, resp.StatusCode)
		}
	})

	t.Run("duplicate commit", func(t *testing.T) {
		key := strings.ToLower(t.Name())
		version := "c19da02a2bd7e77277f1ac29ab45c09b7d46a4ee758284e26bb3045ad11d9d20"
		var id uint64
		content := make([]byte, 100)
		_, err := rand.Read(content)
		require.NoError(t, err)
		{
			body, err := json.Marshal(&Request{
				Key:     key,
				Version: version,
				Size:    100,
			})
			require.NoError(t, err)
			resp, err := httpClient.Post(fmt.Sprintf("%s/caches", base), "application/json", bytes.NewReader(body))
			require.NoError(t, err)
			assert.Equal(t, 200, resp.StatusCode)

			got := struct {
				CacheID uint64 `json:"cacheId"`
			}{}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
			id = got.CacheID
		}
		{
			req, err := http.NewRequest(http.MethodPatch,
				fmt.Sprintf("%s/caches/%d", base, id), bytes.NewReader(content))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/octet-stream")
			req.Header.Set("Content-Range", "bytes 0-99/*")
			resp, err := httpClient.Do(req)
			require.NoError(t, err)
			assert.Equal(t, 200, resp.StatusCode)
		}
		{
			resp, err := httpClient.Post(fmt.Sprintf("%s/caches/%d", base, id), "", nil)
			require.NoError(t, err)
			assert.Equal(t, 200, resp.StatusCode)
		}
		{
			resp, err := httpClient.Post(fmt.Sprintf("%s/caches/%d", base, id), "", nil)
			require.NoError(t, err)
			assert.Equal(t, 400, resp.StatusCode)
		}
	})

	t.Run("commit early", func(t *testing.T) {
		key := strings.ToLower(t.Name())
		version := "c19da02a2bd7e77277f1ac29ab45c09b7d46a4ee758284e26bb3045ad11d9d20"
		var id uint64
		content := make([]byte, 100)
		_, err := rand.Read(content)
		require.NoError(t, err)
		{
			body, err := json.Marshal(&Request{
				Key:     key,
				Version: version,
				Size:    100,
			})
			require.NoError(t, err)
			resp, err := httpClient.Post(fmt.Sprintf("%s/caches", base), "application/json", bytes.NewReader(body))
			require.NoError(t, err)
			assert.Equal(t, 200, resp.StatusCode)

			got := struct {
				CacheID uint64 `json:"cacheId"`
			}{}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
			id = got.CacheID
		}
		{
			req, err := http.NewRequest(http.MethodPatch,
				fmt.Sprintf("%s/caches/%d", base, id), bytes.NewReader(content[:50]))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/octet-stream")
			req.Header.Set("Content-Range", "bytes 0-59/*")
			resp, err := httpClient.Do(req)
			require.NoError(t, err)
			assert.Equal(t, 200, resp.StatusCode)
		}
		{
			resp, err := httpClient.Post(fmt.Sprintf("%s/caches/%d", base, id), "", nil)
			require.NoError(t, err)
			assert.Equal(t, 500, resp.StatusCode)
		}
	})

	t.Run("get with bad id", func(t *testing.T) {
		resp, err := httpClient.Get(fmt.Sprintf("%s/artifacts/invalid_id", base))
		require.NoError(t, err)
		require.Equal(t, 400, resp.StatusCode)
	})

	t.Run("get with not exist id", func(t *testing.T) {
		resp, err := httpClient.Get(fmt.Sprintf("%s/artifacts/%d", base, 100))
		require.NoError(t, err)
		require.Equal(t, 404, resp.StatusCode)
	})

	t.Run("get with not exist id", func(t *testing.T) {
		resp, err := httpClient.Get(fmt.Sprintf("%s/artifacts/%d", base, 100))
		require.NoError(t, err)
		require.Equal(t, 404, resp.StatusCode)
	})

	t.Run("get with bad MAC", func(t *testing.T) {
		key := strings.ToLower(t.Name())
		version := "c19da02a2bd7e77277f1ac29ab45c09b7d46b4ee758284e26bb3045ad11d9d20"
		content := make([]byte, 100)
		_, err := rand.Read(content)
		require.NoError(t, err)

		uploadCacheNormally(t, base, key, version, "", content)

		// Perform the request with the custom `httpClient` which will send correct MAC data
		resp, err := httpClient.Get(fmt.Sprintf("%s/cache?keys=%s&version=%s", base, key, version))
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)

		// Perform the same request with incorrect MAC data
		req, err := http.NewRequest("GET", fmt.Sprintf("%s/cache?keys=%s&version=%s", base, key, version), nil)
		require.NoError(t, err)
		req.Header.Set("Forgejo-Cache-Repo", cacheRepo)
		req.Header.Set("Forgejo-Cache-RunNumber", cacheRunnum)
		req.Header.Set("Forgejo-Cache-Timestamp", cacheTimestamp)
		req.Header.Set("Forgejo-Cache-MAC", "33f0e850ba0bdfd2f3e66ff79c1f8004b8226114e3b2e65c229222bb59df0f9d") // ! This is not the correct MAC
		req.Header.Set("Forgejo-Cache-Host", handlerExternalURL)
		resp, err = http.Get(fmt.Sprintf("%s/cache?keys=%s&version=%s", base, key, version))
		require.NoError(t, err)
		require.Equal(t, 403, resp.StatusCode)
	})

	t.Run("get with multiple keys", func(t *testing.T) {
		version := "c19da02a2bd7e77277f1ac29ab45c09b7d46a4ee758284e26bb3045ad11d9d20"
		key := strings.ToLower(t.Name())
		keys := [3]string{
			key + "_a_b_c",
			key + "_a_b",
			key + "_a",
		}
		contents := [3][]byte{
			make([]byte, 100),
			make([]byte, 200),
			make([]byte, 300),
		}
		for i := range contents {
			_, err := rand.Read(contents[i])
			require.NoError(t, err)
			uploadCacheNormally(t, base, keys[i], version, "", contents[i])
			time.Sleep(time.Second) // ensure CreatedAt of caches are different
		}

		reqKeys := strings.Join([]string{
			key + "_a_b_x",
			key + "_a_b",
			key + "_a",
		}, ",")

		resp, err := httpClient.Get(fmt.Sprintf("%s/cache?keys=%s&version=%s", base, reqKeys, version))
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)

		/*
			Expect `key_a_b` because:
			- `key_a_b_x" doesn't match any caches.
			- `key_a_b" matches `key_a_b` and `key_a_b_c`, but `key_a_b` is newer.
		*/
		except := 1

		got := struct {
			Result          string `json:"result"`
			ArchiveLocation string `json:"archiveLocation"`
			CacheKey        string `json:"cacheKey"`
		}{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
		assert.Equal(t, "hit", got.Result)
		assert.Equal(t, keys[except], got.CacheKey)

		contentResp, err := httpClient.Get(got.ArchiveLocation)
		require.NoError(t, err)
		require.Equal(t, 200, contentResp.StatusCode)
		content, err := io.ReadAll(contentResp.Body)
		require.NoError(t, err)
		assert.Equal(t, contents[except], content)
	})

	t.Run("find can't match without WriteIsolationKey match", func(t *testing.T) {
		defer func() { httpClientTransport.WriteIsolationKey = "" }()

		version := "c19da02a2bd7e77277f1ac29ab45c09b7d46a4ee758284e26bb3045ad11d9d20"
		key := strings.ToLower(t.Name())

		uploadCacheNormally(t, base, key, version, "TestWriteKey", make([]byte, 64))

		func() {
			defer overrideWriteIsolationKey("AnotherTestWriteKey")()
			resp, err := httpClient.Get(fmt.Sprintf("%s/cache?keys=%s&version=%s", base, key, version))
			require.NoError(t, err)
			require.Equal(t, 204, resp.StatusCode)
		}()

		{
			resp, err := httpClient.Get(fmt.Sprintf("%s/cache?keys=%s&version=%s", base, key, version))
			require.NoError(t, err)
			require.Equal(t, 204, resp.StatusCode)
		}

		func() {
			defer overrideWriteIsolationKey("TestWriteKey")()
			resp, err := httpClient.Get(fmt.Sprintf("%s/cache?keys=%s&version=%s", base, key, version))
			require.NoError(t, err)
			require.Equal(t, 200, resp.StatusCode)
		}()
	})

	t.Run("find prefers WriteIsolationKey match", func(t *testing.T) {
		version := "c19da02a2bd7e77277f1ac29ab45c09b7d46a4ee758284e26bb3045ad11d9d21"
		key := strings.ToLower(t.Name())

		// Between two values with the same `key`...
		uploadCacheNormally(t, base, key, version, "TestWriteKey", make([]byte, 64))
		uploadCacheNormally(t, base, key, version, "", make([]byte, 128))

		// We should read the value with the matching WriteIsolationKey from the cache...
		func() {
			defer overrideWriteIsolationKey("TestWriteKey")()

			resp, err := httpClient.Get(fmt.Sprintf("%s/cache?keys=%s&version=%s", base, key, version))
			require.NoError(t, err)
			require.Equal(t, 200, resp.StatusCode)

			got := struct {
				ArchiveLocation string `json:"archiveLocation"`
			}{}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
			contentResp, err := httpClient.Get(got.ArchiveLocation)
			require.NoError(t, err)
			require.Equal(t, 200, contentResp.StatusCode)
			content, err := io.ReadAll(contentResp.Body)
			require.NoError(t, err)
			// Which we finally check matches the correct WriteIsolationKey's content here.
			assert.Equal(t, make([]byte, 64), content)
		}()
	})

	t.Run("find falls back if matching WriteIsolationKey not available", func(t *testing.T) {
		version := "c19da02a2bd7e77277f1ac29ab45c09b7d46a4ee758284e26bb3045ad11d9d21"
		key := strings.ToLower(t.Name())

		uploadCacheNormally(t, base, key, version, "", make([]byte, 128))

		func() {
			defer overrideWriteIsolationKey("TestWriteKey")()

			resp, err := httpClient.Get(fmt.Sprintf("%s/cache?keys=%s&version=%s", base, key, version))
			require.NoError(t, err)
			require.Equal(t, 200, resp.StatusCode)

			got := struct {
				ArchiveLocation string `json:"archiveLocation"`
			}{}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
			contentResp, err := httpClient.Get(got.ArchiveLocation)
			require.NoError(t, err)
			require.Equal(t, 200, contentResp.StatusCode)
			content, err := io.ReadAll(contentResp.Body)
			require.NoError(t, err)
			assert.Equal(t, make([]byte, 128), content)
		}()
	})

	t.Run("case insensitive", func(t *testing.T) {
		version := "c19da02a2bd7e77277f1ac29ab45c09b7d46a4ee758284e26bb3045ad11d9d20"
		key := strings.ToLower(t.Name())
		content := make([]byte, 100)
		_, err := rand.Read(content)
		require.NoError(t, err)
		uploadCacheNormally(t, base, key+"_ABC", version, "", content)

		{
			reqKey := key + "_aBc"
			resp, err := httpClient.Get(fmt.Sprintf("%s/cache?keys=%s&version=%s", base, reqKey, version))
			require.NoError(t, err)
			require.Equal(t, 200, resp.StatusCode)
			got := struct {
				Result          string `json:"result"`
				ArchiveLocation string `json:"archiveLocation"`
				CacheKey        string `json:"cacheKey"`
			}{}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
			assert.Equal(t, "hit", got.Result)
			assert.Equal(t, key+"_abc", got.CacheKey)
		}
	})

	t.Run("exact keys are preferred (key 0)", func(t *testing.T) {
		version := "c19da02a2bd7e77277f1ac29ab45c09b7d46a4ee758284e26bb3045ad11d9d20"
		key := strings.ToLower(t.Name())
		keys := [3]string{
			key + "_a",
			key + "_a_b_c",
			key + "_a_b",
		}
		contents := [3][]byte{
			make([]byte, 100),
			make([]byte, 200),
			make([]byte, 300),
		}
		for i := range contents {
			_, err := rand.Read(contents[i])
			require.NoError(t, err)
			uploadCacheNormally(t, base, keys[i], version, "", contents[i])
			time.Sleep(time.Second) // ensure CreatedAt of caches are different
		}

		reqKeys := strings.Join([]string{
			key + "_a",
			key + "_a_b",
		}, ",")

		resp, err := httpClient.Get(fmt.Sprintf("%s/cache?keys=%s&version=%s", base, reqKeys, version))
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)

		/*
			Expect `key_a` because:
			- `key_a` matches `key_a`, `key_a_b` and `key_a_b_c`, but `key_a` is an exact match.
			- `key_a_b` matches `key_a_b` and `key_a_b_c`, but previous key had a match
		*/
		expect := 0

		got := struct {
			ArchiveLocation string `json:"archiveLocation"`
			CacheKey        string `json:"cacheKey"`
		}{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
		assert.Equal(t, keys[expect], got.CacheKey)

		contentResp, err := httpClient.Get(got.ArchiveLocation)
		require.NoError(t, err)
		require.Equal(t, 200, contentResp.StatusCode)
		content, err := io.ReadAll(contentResp.Body)
		require.NoError(t, err)
		assert.Equal(t, contents[expect], content)
	})

	t.Run("exact keys are preferred (key 1)", func(t *testing.T) {
		version := "c19da02a2bd7e77277f1ac29ab45c09b7d46a4ee758284e26bb3045ad11d9d20"
		key := strings.ToLower(t.Name())
		keys := [3]string{
			key + "_a",
			key + "_a_b_c",
			key + "_a_b",
		}
		contents := [3][]byte{
			make([]byte, 100),
			make([]byte, 200),
			make([]byte, 300),
		}
		for i := range contents {
			_, err := rand.Read(contents[i])
			require.NoError(t, err)
			uploadCacheNormally(t, base, keys[i], version, "", contents[i])
			time.Sleep(time.Second) // ensure CreatedAt of caches are different
		}

		reqKeys := strings.Join([]string{
			"------------------------------------------------------",
			key + "_a",
			key + "_a_b",
		}, ",")

		resp, err := httpClient.Get(fmt.Sprintf("%s/cache?keys=%s&version=%s", base, reqKeys, version))
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)

		/*
			Expect `key_a` because:
			- `------------------------------------------------------` doesn't match any caches.
			- `key_a` matches `key_a`, `key_a_b` and `key_a_b_c`, but `key_a` is an exact match.
			- `key_a_b` matches `key_a_b` and `key_a_b_c`, but previous key had a match
		*/
		expect := 0

		got := struct {
			ArchiveLocation string `json:"archiveLocation"`
			CacheKey        string `json:"cacheKey"`
		}{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
		assert.Equal(t, keys[expect], got.CacheKey)

		contentResp, err := httpClient.Get(got.ArchiveLocation)
		require.NoError(t, err)
		require.Equal(t, 200, contentResp.StatusCode)
		content, err := io.ReadAll(contentResp.Body)
		require.NoError(t, err)
		assert.Equal(t, contents[expect], content)
	})

	t.Run("upload across WriteIsolationKey", func(t *testing.T) {
		defer overrideWriteIsolationKey("CorrectKey")()

		key := strings.ToLower(t.Name())
		version := "c19da02a2bd7e77277f1ac29ab45c09b7d46a4ee758284e26bb3045ad11d9d20"
		content := make([]byte, 256)

		var id uint64
		// reserve
		{
			body, err := json.Marshal(&Request{
				Key:     key,
				Version: version,
				Size:    int64(len(content)),
			})
			require.NoError(t, err)
			resp, err := httpClient.Post(fmt.Sprintf("%s/caches", base), "application/json", bytes.NewReader(body))
			require.NoError(t, err)
			assert.Equal(t, 200, resp.StatusCode)

			got := struct {
				CacheID uint64 `json:"cacheId"`
			}{}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
			id = got.CacheID
		}
		// upload, but with the incorrect write isolation key relative to the cache obj created
		func() {
			defer overrideWriteIsolationKey("WrongKey")()
			req, err := http.NewRequest(http.MethodPatch,
				fmt.Sprintf("%s/caches/%d", base, id), bytes.NewReader(content))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/octet-stream")
			req.Header.Set("Content-Range", "bytes 0-99/*")
			resp, err := httpClient.Do(req)
			require.NoError(t, err)
			assert.Equal(t, 403, resp.StatusCode)
		}()
	})

	t.Run("commit across WriteIsolationKey", func(t *testing.T) {
		defer overrideWriteIsolationKey("CorrectKey")()

		key := strings.ToLower(t.Name())
		version := "c19da02a2bd7e77277f1ac29ab45c09b7d46a4ee758284e26bb3045ad11d9d20"
		content := make([]byte, 256)

		var id uint64
		// reserve
		{
			body, err := json.Marshal(&Request{
				Key:     key,
				Version: version,
				Size:    int64(len(content)),
			})
			require.NoError(t, err)
			resp, err := httpClient.Post(fmt.Sprintf("%s/caches", base), "application/json", bytes.NewReader(body))
			require.NoError(t, err)
			assert.Equal(t, 200, resp.StatusCode)

			got := struct {
				CacheID uint64 `json:"cacheId"`
			}{}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
			id = got.CacheID
		}
		// upload
		{
			req, err := http.NewRequest(http.MethodPatch,
				fmt.Sprintf("%s/caches/%d", base, id), bytes.NewReader(content))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/octet-stream")
			req.Header.Set("Content-Range", "bytes 0-99/*")
			resp, err := httpClient.Do(req)
			require.NoError(t, err)
			assert.Equal(t, 200, resp.StatusCode)
		}
		// commit, but with the incorrect write isolation key relative to the cache obj created
		func() {
			defer overrideWriteIsolationKey("WrongKey")()
			resp, err := httpClient.Post(fmt.Sprintf("%s/caches/%d", base, id), "", nil)
			require.NoError(t, err)
			assert.Equal(t, 403, resp.StatusCode)
		}()
	})

	t.Run("get across WriteIsolationKey", func(t *testing.T) {
		defer func() { httpClientTransport.WriteIsolationKey = "" }()

		version := "c19da02a2bd7e77277f1ac29ab45c09b7d46a4ee758284e26bb3045ad11d9d21"
		key := strings.ToLower(t.Name())
		uploadCacheNormally(t, base, key, version, "", make([]byte, 128))
		keyIsolated := strings.ToLower(t.Name()) + "_isolated"
		uploadCacheNormally(t, base, keyIsolated, version, "CorrectKey", make([]byte, 128))

		// Perform the 'get' without the right WriteIsolationKey for the cache entry... should be OK for `key` since it
		// was written with WriteIsolationKey "" meaning it is available for non-isolated access
		func() {
			defer overrideWriteIsolationKey("WhoopsWrongKey")()

			resp, err := httpClient.Get(fmt.Sprintf("%s/cache?keys=%s&version=%s", base, key, version))
			require.NoError(t, err)
			require.Equal(t, 200, resp.StatusCode)
			got := struct {
				ArchiveLocation string `json:"archiveLocation"`
			}{}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))

			contentResp, err := httpClient.Get(got.ArchiveLocation)
			require.NoError(t, err)
			require.Equal(t, 200, contentResp.StatusCode)
			httpClientTransport.WriteIsolationKey = "CorrectKey" // reset for next find
		}()

		// Perform the 'get' without the right WriteIsolationKey for the cache entry... should be 403 for `keyIsolated`
		// because it was written with a different WriteIsolationKey.
		{
			got := func() struct {
				ArchiveLocation string `json:"archiveLocation"`
			} {
				defer overrideWriteIsolationKey("CorrectKey")() // for test purposes make the `find` successful...
				resp, err := httpClient.Get(fmt.Sprintf("%s/cache?keys=%s&version=%s", base, keyIsolated, version))
				require.NoError(t, err)
				require.Equal(t, 200, resp.StatusCode)
				got := struct {
					ArchiveLocation string `json:"archiveLocation"`
				}{}
				require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
				return got
			}()

			func() {
				defer overrideWriteIsolationKey("WhoopsWrongKey")() // but then access w/ the wrong key for `get`
				contentResp, err := httpClient.Get(got.ArchiveLocation)
				require.NoError(t, err)
				require.Equal(t, 403, contentResp.StatusCode)
			}()
		}
	})
}

func overrideWriteIsolationKey(writeIsolationKey string) func() {
	originalWriteIsolationKey := httpClientTransport.WriteIsolationKey
	originalMac := httpClientTransport.OverrideDefaultMac

	httpClientTransport.WriteIsolationKey = writeIsolationKey
	httpClientTransport.OverrideDefaultMac = computeMac("secret", cacheRepo, cacheRunnum, cacheTimestamp, httpClientTransport.WriteIsolationKey)

	return func() {
		httpClientTransport.WriteIsolationKey = originalWriteIsolationKey
		httpClientTransport.OverrideDefaultMac = originalMac
	}
}

func uploadCacheNormally(t *testing.T, base, key, version, writeIsolationKey string, content []byte) {
	if writeIsolationKey != "" {
		defer overrideWriteIsolationKey(writeIsolationKey)()
	}

	var id uint64
	{
		body, err := json.Marshal(&Request{
			Key:     key,
			Version: version,
			Size:    int64(len(content)),
		})
		require.NoError(t, err)
		resp, err := httpClient.Post(fmt.Sprintf("%s/caches", base), "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		assert.Equal(t, 200, resp.StatusCode)

		got := struct {
			CacheID uint64 `json:"cacheId"`
		}{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
		id = got.CacheID
	}
	{
		req, err := http.NewRequest(http.MethodPatch,
			fmt.Sprintf("%s/caches/%d", base, id), bytes.NewReader(content))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Content-Range", "bytes 0-99/*")
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, 200, resp.StatusCode)
	}
	{
		resp, err := httpClient.Post(fmt.Sprintf("%s/caches/%d", base, id), "", nil)
		require.NoError(t, err)
		assert.Equal(t, 200, resp.StatusCode)
	}
	var archiveLocation string
	{
		resp, err := httpClient.Get(fmt.Sprintf("%s/cache?keys=%s&version=%s", base, key, version))
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)
		got := struct {
			Result          string `json:"result"`
			ArchiveLocation string `json:"archiveLocation"`
			CacheKey        string `json:"cacheKey"`
		}{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
		assert.Equal(t, "hit", got.Result)
		assert.Equal(t, strings.ToLower(key), got.CacheKey)
		archiveLocation = got.ArchiveLocation
	}
	{
		resp, err := httpClient.Get(archiveLocation)
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)
		got, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, content, got)
	}
}

func TestHandler_gcCache(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "artifactcache")
	handler, err := StartHandler(dir, "", 0, "", nil)
	require.NoError(t, err)

	defer func() {
		require.NoError(t, handler.Close())
	}()

	now := time.Now()

	cases := []struct {
		Cache *Cache
		Kept  bool
	}{
		{
			// should be kept, since it's used recently and not too old.
			Cache: &Cache{
				Key:       "test_key_1",
				Version:   "test_version",
				Complete:  true,
				UsedAt:    now.Unix(),
				CreatedAt: now.Add(-time.Hour).Unix(),
			},
			Kept: true,
		},
		{
			// should be removed, since it's not complete and not used for a while.
			Cache: &Cache{
				Key:       "test_key_2",
				Version:   "test_version",
				Complete:  false,
				UsedAt:    now.Add(-(keepTemp + time.Second)).Unix(),
				CreatedAt: now.Add(-(keepTemp + time.Hour)).Unix(),
			},
			Kept: false,
		},
		{
			// should be removed, since it's not used for a while.
			Cache: &Cache{
				Key:       "test_key_3",
				Version:   "test_version",
				Complete:  true,
				UsedAt:    now.Add(-(keepUnused + time.Second)).Unix(),
				CreatedAt: now.Add(-(keepUnused + time.Hour)).Unix(),
			},
			Kept: false,
		},
		{
			// should be removed, since it's used but too old.
			Cache: &Cache{
				Key:       "test_key_3",
				Version:   "test_version",
				Complete:  true,
				UsedAt:    now.Unix(),
				CreatedAt: now.Add(-(keepUsed + time.Second)).Unix(),
			},
			Kept: false,
		},
		{
			// should be kept, since it has a newer edition but be used recently.
			Cache: &Cache{
				Key:       "test_key_1",
				Version:   "test_version",
				Complete:  true,
				UsedAt:    now.Add(-(keepOld - time.Minute)).Unix(),
				CreatedAt: now.Add(-(time.Hour + time.Second)).Unix(),
			},
			Kept: true,
		},
		{
			// should be removed, since it has a newer edition and not be used recently.
			Cache: &Cache{
				Key:       "test_key_1",
				Version:   "test_version",
				Complete:  true,
				UsedAt:    now.Add(-(keepOld + time.Second)).Unix(),
				CreatedAt: now.Add(-(time.Hour + time.Second)).Unix(),
			},
			Kept: false,
		},
	}

	db, err := handler.openDB()
	require.NoError(t, err)
	for _, c := range cases {
		require.NoError(t, insertCache(db, c.Cache))
	}
	require.NoError(t, db.Close())

	handler.setgcAt(time.Time{}) // ensure gcCache will not skip
	handler.gcCache()

	db, err = handler.openDB()
	require.NoError(t, err)
	for i, v := range cases {
		t.Run(fmt.Sprintf("%d_%s", i, v.Cache.Key), func(t *testing.T) {
			cache := &Cache{}
			err = db.Get(v.Cache.ID, cache)
			if v.Kept {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, bolthold.ErrNotFound)
			}
		})
	}
	require.NoError(t, db.Close())
}

func TestHandler_ExternalURL(t *testing.T) {
	t.Run("reports correct URL on IPv4", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "artifactcache")
		handler, err := StartHandler(dir, "127.0.0.1", 34567, "secret", nil)
		require.NoError(t, err)

		assert.Equal(t, handler.ExternalURL(), "http://127.0.0.1:34567")
		require.NoError(t, handler.Close())
		assert.True(t, handler.isClosed())
	})

	t.Run("reports correct URL on IPv6 zero host", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "artifactcache")
		handler, err := StartHandler(dir, "2001:db8::", 34567, "secret", nil)
		require.NoError(t, err)

		assert.Equal(t, handler.ExternalURL(), "http://[2001:db8::]:34567")
		require.NoError(t, handler.Close())
		assert.True(t, handler.isClosed())
	})

	t.Run("reports correct URL on IPv6", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "artifactcache")
		handler, err := StartHandler(dir, "2001:db8::1:2:3:4", 34567, "secret", nil)
		require.NoError(t, err)

		assert.Equal(t, handler.ExternalURL(), "http://[2001:db8::1:2:3:4]:34567")
		require.NoError(t, handler.Close())
		assert.True(t, handler.isClosed())
	})
}
