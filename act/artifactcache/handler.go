package artifactcache

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/sirupsen/logrus"
	"github.com/timshannon/bolthold"

	"code.forgejo.org/forgejo/runner/v11/act/common"
)

const (
	urlBase = "/_apis/artifactcache"
)

var fatal = func(logger logrus.FieldLogger, err error) {
	logger.Errorf("unrecoverable error in the cache: %v", err)
	if err := suicide(); err != nil {
		logger.Errorf("unrecoverable error in the cache: failed to send the TERM signal to shutdown the daemon %v", err)
	}
}

type Handler interface {
	ExternalURL() string
	Close() error
	isClosed() bool
	getCaches() caches
	setCaches(caches caches)
	find(w http.ResponseWriter, r *http.Request, params httprouter.Params)
	reserve(w http.ResponseWriter, r *http.Request, params httprouter.Params)
	upload(w http.ResponseWriter, r *http.Request, params httprouter.Params)
	commit(w http.ResponseWriter, r *http.Request, params httprouter.Params)
	get(w http.ResponseWriter, r *http.Request, params httprouter.Params)
	clean(w http.ResponseWriter, r *http.Request, _ httprouter.Params)
	middleware(handler httprouter.Handle) httprouter.Handle
	responseJSON(w http.ResponseWriter, r *http.Request, code int, v ...any)
}

type handler struct {
	caches   caches
	router   *httprouter.Router
	listener net.Listener
	server   *http.Server
	logger   logrus.FieldLogger

	outboundIP string
}

func StartHandler(dir, outboundIP string, port uint16, secret string, logger logrus.FieldLogger) (Handler, error) {
	h := &handler{}

	if logger == nil {
		discard := logrus.New()
		discard.Out = io.Discard
		logger = discard
	}
	logger = logger.WithField("module", "artifactcache")
	h.logger = logger

	caches, err := newCaches(dir, secret, logger)
	if err != nil {
		return nil, err
	}
	h.caches = caches

	if outboundIP != "" {
		h.outboundIP = outboundIP
	} else if ip := common.GetOutboundIP(); ip == nil {
		return nil, fmt.Errorf("unable to determine outbound IP address")
	} else {
		h.outboundIP = ip.String()
	}

	router := httprouter.New()
	router.GET(urlBase+"/cache", h.middleware(h.find))
	router.POST(urlBase+"/caches", h.middleware(h.reserve))
	router.PATCH(urlBase+"/caches/:id", h.middleware(h.upload))
	router.POST(urlBase+"/caches/:id", h.middleware(h.commit))
	router.GET(urlBase+"/artifacts/:id", h.middleware(h.get))
	router.POST(urlBase+"/clean", h.middleware(h.clean))

	h.router = router

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port)) // listen on all interfaces
	if err != nil {
		return nil, err
	}
	server := &http.Server{
		ReadHeaderTimeout: 2 * time.Second,
		Handler:           router,
	}
	go func() {
		if err := server.Serve(listener); err != nil && errors.Is(err, net.ErrClosed) {
			logger.Errorf("http serve: %v", err)
		}
	}()
	h.listener = listener
	h.server = server

	return h, nil
}

func (h *handler) ExternalURL() string {
	port := strconv.Itoa(h.listener.Addr().(*net.TCPAddr).Port)

	// TODO: make the external url configurable if necessary
	return fmt.Sprintf("http://%s", net.JoinHostPort(h.outboundIP, port))
}

func (h *handler) Close() error {
	if h == nil {
		return nil
	}
	var retErr error
	if h.caches != nil {
		h.caches.close()
		h.caches = nil
	}
	if h.server != nil {
		err := h.server.Close()
		if err != nil {
			retErr = err
		}
		h.server = nil
	}
	if h.listener != nil {
		err := h.listener.Close()
		if errors.Is(err, net.ErrClosed) {
			err = nil
		}
		if err != nil {
			retErr = err
		}
		h.listener = nil
	}
	return retErr
}

func (h *handler) isClosed() bool {
	return h.listener == nil && h.server == nil
}

func (h *handler) getCaches() caches {
	return h.caches
}

func (h *handler) setCaches(caches caches) {
	if h.caches != nil {
		h.caches.close()
	}
	h.caches = caches
}

// GET /_apis/artifactcache/cache
func (h *handler) find(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	rundata := runDataFromHeaders(r)
	repo, err := h.caches.validateMac(rundata)
	if err != nil {
		h.responseJSON(w, r, 403, err)
		return
	}

	keys := strings.Split(r.URL.Query().Get("keys"), ",")
	// cache keys are case insensitive
	for i, key := range keys {
		keys[i] = strings.ToLower(key)
	}
	version := r.URL.Query().Get("version")

	db := h.caches.getDB()

	cache, err := findCacheWithIsolationKeyFallback(db, repo, keys, version, rundata.WriteIsolationKey)
	if err != nil {
		h.responseFatalJSON(w, r, err)
		return
	}
	if cache == nil {
		h.responseJSON(w, r, 204)
		return
	}

	if ok, err := h.caches.exist(cache.ID); err != nil {
		h.responseJSON(w, r, 500, err)
		return
	} else if !ok {
		_ = db.Delete(cache.ID, cache)
		h.responseJSON(w, r, 204)
		return
	}
	archiveLocation := fmt.Sprintf("%s/%s%s/artifacts/%d", r.Header.Get("Forgejo-Cache-Host"), r.Header.Get("Forgejo-Cache-RunId"), urlBase, cache.ID)
	h.responseJSON(w, r, 200, map[string]any{
		"result":          "hit",
		"archiveLocation": archiveLocation,
		"cacheKey":        cache.Key,
	})
}

// POST /_apis/artifactcache/caches
func (h *handler) reserve(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	rundata := runDataFromHeaders(r)
	repo, err := h.caches.validateMac(rundata)
	if err != nil {
		h.responseJSON(w, r, 403, err)
		return
	}

	api := &Request{}
	if err := json.NewDecoder(r.Body).Decode(api); err != nil {
		h.responseJSON(w, r, 400, err)
		return
	}
	// cache keys are case insensitive
	api.Key = strings.ToLower(api.Key)

	cache := api.ToCache()
	db := h.caches.getDB()

	now := time.Now().Unix()
	cache.CreatedAt = now
	cache.UsedAt = now
	cache.Repo = repo
	cache.WriteIsolationKey = rundata.WriteIsolationKey
	if err := insertCache(db, cache); err != nil {
		h.responseJSON(w, r, 500, err)
		return
	}
	h.responseJSON(w, r, 200, map[string]any{
		"cacheId": cache.ID,
	})
}

// PATCH /_apis/artifactcache/caches/:id
func (h *handler) upload(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	rundata := runDataFromHeaders(r)
	repo, err := h.caches.validateMac(rundata)
	if err != nil {
		h.responseJSON(w, r, 403, err)
		return
	}

	id, err := strconv.ParseUint(params.ByName("id"), 10, 64)
	if err != nil {
		h.responseJSON(w, r, 400, err)
		return
	}

	cache, err := h.caches.readCache(id, repo)
	if err != nil {
		if errors.Is(err, bolthold.ErrNotFound) {
			h.responseJSON(w, r, 404, fmt.Errorf("cache %d: not reserved", id))
			return
		}
		h.responseFatalJSON(w, r, fmt.Errorf("cache Get: %w", err))
		return
	}

	if cache.WriteIsolationKey != rundata.WriteIsolationKey {
		h.responseJSON(w, r, 403, fmt.Errorf("cache authorized for write isolation %q, but attempting to operate on %q", rundata.WriteIsolationKey, cache.WriteIsolationKey))
		return
	}

	if cache.Complete {
		h.responseJSON(w, r, 400, fmt.Errorf("cache %v %q: already complete", cache.ID, cache.Key))
		return
	}
	start, _, err := parseContentRange(r.Header.Get("Content-Range"))
	if err != nil {
		h.responseJSON(w, r, 400, fmt.Errorf("cache parseContentRange(%s): %w", r.Header.Get("Content-Range"), err))
		return
	}
	if err := h.caches.write(cache.ID, start, r.Body); err != nil {
		h.responseJSON(w, r, 500, fmt.Errorf("cache storage.Write: %w", err))
		return
	}
	if err := h.caches.useCache(id); err != nil {
		h.responseJSON(w, r, 500, fmt.Errorf("cache useCache: %w", err))
		return
	}
	h.responseJSON(w, r, 200)
}

// POST /_apis/artifactcache/caches/:id
func (h *handler) commit(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	rundata := runDataFromHeaders(r)
	repo, err := h.caches.validateMac(rundata)
	if err != nil {
		h.responseJSON(w, r, 403, err)
		return
	}

	id, err := strconv.ParseUint(params.ByName("id"), 10, 64)
	if err != nil {
		h.responseJSON(w, r, 400, err)
		return
	}

	cache, err := h.caches.readCache(id, repo)
	if err != nil {
		if errors.Is(err, bolthold.ErrNotFound) {
			h.responseJSON(w, r, 404, fmt.Errorf("cache %d: not reserved", id))
			return
		}
		h.responseFatalJSON(w, r, fmt.Errorf("cache Get: %w", err))
		return
	}

	if cache.WriteIsolationKey != rundata.WriteIsolationKey {
		h.responseJSON(w, r, 403, fmt.Errorf("cache authorized for write isolation %q, but attempting to operate on %q", rundata.WriteIsolationKey, cache.WriteIsolationKey))
		return
	}

	if cache.Complete {
		h.responseJSON(w, r, 400, fmt.Errorf("cache %v %q: already complete", cache.ID, cache.Key))
		return
	}

	size, err := h.caches.commit(cache.ID, cache.Size)
	if err != nil {
		h.responseJSON(w, r, 500, fmt.Errorf("commit(%v): %w", cache.ID, err))
		return
	}
	// write real size back to cache, it may be different from the current value when the request doesn't specify it.
	cache.Size = size

	db := h.caches.getDB()

	cache.Complete = true
	if err := db.Update(cache.ID, cache); err != nil {
		h.responseJSON(w, r, 500, err)
		return
	}

	h.responseJSON(w, r, 200)
}

// GET /_apis/artifactcache/artifacts/:id
func (h *handler) get(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	rundata := runDataFromHeaders(r)
	repo, err := h.caches.validateMac(rundata)
	if err != nil {
		h.responseJSON(w, r, 403, err)
		return
	}

	id, err := strconv.ParseUint(params.ByName("id"), 10, 64)
	if err != nil {
		h.responseJSON(w, r, 400, err)
		return
	}

	cache, err := h.caches.readCache(id, repo)
	if err != nil {
		if errors.Is(err, bolthold.ErrNotFound) {
			h.responseJSON(w, r, 404, fmt.Errorf("cache %d: not reserved", id))
			return
		}
		h.responseFatalJSON(w, r, fmt.Errorf("cache Get: %w", err))
		return
	}

	// reads permitted against caches w/ the same isolation key, or no isolation key
	if cache.WriteIsolationKey != rundata.WriteIsolationKey && cache.WriteIsolationKey != "" {
		h.responseJSON(w, r, 403, fmt.Errorf("cache authorized for write isolation %q, but attempting to operate on %q", rundata.WriteIsolationKey, cache.WriteIsolationKey))
		return
	}

	if err := h.caches.useCache(id); err != nil {
		h.responseJSON(w, r, 500, fmt.Errorf("cache useCache: %w", err))
		return
	}
	h.caches.serve(w, r, id)
}

// POST /_apis/artifactcache/clean
func (h *handler) clean(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	rundata := runDataFromHeaders(r)
	_, err := h.caches.validateMac(rundata)
	if err != nil {
		h.responseJSON(w, r, 403, err)
		return
	}
	// TODO: don't support force deleting cache entries
	// see: https://docs.github.com/en/actions/using-workflows/caching-dependencies-to-speed-up-workflows#force-deleting-cache-entries

	h.responseJSON(w, r, 200)
}

func (h *handler) middleware(handler httprouter.Handle) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
		h.logger.Debugf("%s %s", r.Method, r.RequestURI)
		handler(w, r, params)
		go h.caches.gcCache()
	}
}

func (h *handler) responseFatalJSON(w http.ResponseWriter, r *http.Request, err error) {
	h.responseJSON(w, r, 500, err)
	fatal(h.logger, err)
}

func (h *handler) responseJSON(w http.ResponseWriter, r *http.Request, code int, v ...any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	var data []byte
	if len(v) == 0 || v[0] == nil {
		data, _ = json.Marshal(struct{}{})
	} else if err, ok := v[0].(error); ok {
		h.logger.Errorf("%v %v: %v", r.Method, r.RequestURI, err)
		data, _ = json.Marshal(map[string]any{
			"error": err.Error(),
		})
	} else {
		data, _ = json.Marshal(v[0])
	}
	w.WriteHeader(code)
	_, _ = w.Write(data)
}

func parseContentRange(s string) (uint64, uint64, error) {
	// support the format like "bytes 11-22/*" only
	s, _, _ = strings.Cut(strings.TrimPrefix(s, "bytes "), "/")
	s1, s2, _ := strings.Cut(s, "-")

	start, err := strconv.ParseUint(s1, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parse %q: %w", s, err)
	}
	stop, err := strconv.ParseUint(s2, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parse %q: %w", s, err)
	}
	return start, stop, nil
}

type RunData struct {
	RepositoryFullName string
	RunNumber          string
	Timestamp          string
	RepositoryMAC      string
	WriteIsolationKey  string
}

func runDataFromHeaders(r *http.Request) RunData {
	return RunData{
		RepositoryFullName: r.Header.Get("Forgejo-Cache-Repo"),
		RunNumber:          r.Header.Get("Forgejo-Cache-RunNumber"),
		Timestamp:          r.Header.Get("Forgejo-Cache-Timestamp"),
		RepositoryMAC:      r.Header.Get("Forgejo-Cache-MAC"),
		WriteIsolationKey:  r.Header.Get("Forgejo-Cache-WriteIsolationKey"),
	}
}
