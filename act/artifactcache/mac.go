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
)

var ErrValidation = errors.New("validation error")

func (c *cachesImpl) validateMac(rundata RunData) (string, error) {
	// TODO: allow configurable max age
	if !validateAge(rundata.Timestamp) {
		return "", ErrValidation
	}

	expectedMAC := ComputeMac(c.secret, rundata.Instance, rundata.RepositoryFullName, rundata.RunNumber, rundata.Timestamp, rundata.WriteIsolationKey)
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

func ComputeMac(secret, instance, repo, run, ts, writeIsolationKey string) string {
	if instance == "" {
		instance = "__default__"
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(instance))
	mac.Write([]byte(">"))
	mac.Write([]byte(repo))
	mac.Write([]byte(">"))
	mac.Write([]byte(run))
	mac.Write([]byte(">"))
	mac.Write([]byte(ts))
	mac.Write([]byte(">"))
	mac.Write([]byte(writeIsolationKey))
	return hex.EncodeToString(mac.Sum(nil))
}
