// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package artifactcache

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"code.forgejo.org/forgejo/runner/v9/act/cacheproxy"
)

var ErrValidation = errors.New("validation error")

func (h *Handler) validateMac(rundata cacheproxy.RunData) (string, error) {
	// TODO: allow configurable max age
	if !validateAge(rundata.Timestamp) {
		return "", ErrValidation
	}

	expectedMAC := computeMac(h.secret, rundata.RepositoryFullName, rundata.RunNumber, rundata.Timestamp)
	if hmac.Equal([]byte(expectedMAC), []byte(rundata.RepositoryMAC)) {
		return rundata.RepositoryFullName, nil
	}
	return "", ErrValidation
}

func validateAge(ts string) bool {
	tsInt, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	if tsInt > time.Now().Unix() {
		return false
	}
	return true
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
