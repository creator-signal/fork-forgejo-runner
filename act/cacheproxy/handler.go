// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cacheproxy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/sirupsen/logrus"

	"code.forgejo.org/forgejo/runner/v9/act/common"
)

const (
	urlBase = "/_apis/artifactcache"
)

var urlRegex = regexp.MustCompile(`/(\w+)(/_apis/artifactcache/.+)`)

type Handler struct {
	router   *httprouter.Router
	listener net.Listener
	server   *http.Server
	logger   logrus.FieldLogger

	outboundIP string

	cacheServerHost string

	cacheSecret string

	runs sync.Map
}

type RunData struct {
	RepositoryFullName string
	RunNumber          string
	Timestamp          string
	RepositoryMAC      string
}

func (h *Handler) CreateRunData(fullName, runNumber, timestamp string) RunData {
	mac := computeMac(h.cacheSecret, fullName, runNumber, timestamp)
	return RunData{
		RepositoryFullName: fullName,
		RunNumber:          runNumber,
		Timestamp:          timestamp,
		RepositoryMAC:      mac,
	}
}

func StartHandler(targetHost, outboundIP string, port uint16, cacheSecret string, logger logrus.FieldLogger) (*Handler, error) {
	h := &Handler{}

	if logger == nil {
		discard := logrus.New()
		discard.Out = io.Discard
		logger = discard
	}
	logger = logger.WithField("module", "artifactcache")
	h.logger = logger

	h.cacheSecret = cacheSecret

	if outboundIP != "" {
		h.outboundIP = outboundIP
	} else if ip := common.GetOutboundIP(); ip == nil {
		return nil, fmt.Errorf("unable to determine outbound IP address")
	} else {
		h.outboundIP = ip.String()
	}

	h.cacheServerHost = targetHost

	proxy, err := h.newReverseProxy(targetHost)
	if err != nil {
		return nil, fmt.Errorf("unable to set up proxy to target host")
	}

	router := httprouter.New()
	router.HandlerFunc("GET", "/:runId"+urlBase+"/cache", proxyRequestHandler(proxy))
	router.HandlerFunc("POST", "/:runId"+urlBase+"/caches", proxyRequestHandler(proxy))
	router.HandlerFunc("PATCH", "/:runId"+urlBase+"/caches/:id", proxyRequestHandler(proxy))
	router.HandlerFunc("POST", "/:runId"+urlBase+"/caches/:id", proxyRequestHandler(proxy))
	router.HandlerFunc("GET", "/:runId"+urlBase+"/artifacts/:id", proxyRequestHandler(proxy))
	router.HandlerFunc("POST", "/:runId"+urlBase+"/clean", proxyRequestHandler(proxy))

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

func proxyRequestHandler(proxy *httputil.ReverseProxy) func(http.ResponseWriter, *http.Request) {
	return proxy.ServeHTTP
}

func (h *Handler) newReverseProxy(targetHost string) (*httputil.ReverseProxy, error) {
	targetURL, err := url.Parse(targetHost)
	if err != nil {
		return nil, err
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			matches := urlRegex.FindStringSubmatch(r.In.URL.Path)
			id := matches[1]
			data, ok := h.runs.Load(id)
			if !ok {
				// The ID doesn't exist.
				h.logger.Warn(fmt.Sprintf("Tried starting a cache proxy with id %s, which does not exist.", id))
				return
			}
			runData := data.(RunData)
			uri := matches[2]

			r.SetURL(targetURL)
			r.Out.URL.Path = uri

			r.Out.Header.Set("Forgejo-Cache-Repo", runData.RepositoryFullName)
			r.Out.Header.Set("Forgejo-Cache-RunNumber", runData.RunNumber)
			r.Out.Header.Set("Forgejo-Cache-RunId", id)
			r.Out.Header.Set("Forgejo-Cache-Timestamp", runData.Timestamp)
			r.Out.Header.Set("Forgejo-Cache-MAC", runData.RepositoryMAC)
			r.Out.Header.Set("Forgejo-Cache-Host", h.ExternalURL())
		},
	}
	return proxy, nil
}

func (h *Handler) ExternalURL() string {
	// TODO: make the external url configurable if necessary
	return fmt.Sprintf("http://%s", net.JoinHostPort(h.outboundIP, strconv.Itoa(h.listener.Addr().(*net.TCPAddr).Port)))
}

// Informs the proxy of a workflow run that can make cache requests.
// The RunData contains the information about the repository.
// The function returns the 32-bit random key which the run will use to identify itself.
func (h *Handler) AddRun(data RunData) (string, error) {
	for retries := 0; retries < 3; retries++ {
		key := common.MustRandName(4)
		_, loaded := h.runs.LoadOrStore(key, data)
		if !loaded {
			// The key was unique and added successfully
			return key, nil
		}
	}
	return "", errors.New("Repeated collisions in generating run id")
}

func (h *Handler) RemoveRun(runID string) error {
	_, existed := h.runs.LoadAndDelete(runID)
	if !existed {
		return errors.New("The run id was not known to the proxy")
	}
	return nil
}

func (h *Handler) Close() error {
	if h == nil {
		return nil
	}
	var retErr error
	if h.server != nil {
		err := h.server.Close()
		if err != nil {
			retErr = err
		}
		h.server = nil
	}
	if h.listener != nil {
		err := h.listener.Close()
		if !errors.Is(err, net.ErrClosed) {
			retErr = err
		}
		h.listener = nil
	}
	return retErr
}

func computeMac(secret, repo, run, ts string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(repo))
	mac.Write([]byte(">"))
	mac.Write([]byte(run))
	mac.Write([]byte(">"))
	mac.Write([]byte(ts))
	return hex.EncodeToString(mac.Sum(nil))
}
