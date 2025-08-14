// Copyright 2025 The Forgejo Authors
// SPDX-License-Identifier: MIT
package common

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func randName(size int) (string, error) {
	randBytes := make([]byte, size)
	if _, err := rand.Read(randBytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(randBytes), nil
}

func MustRandName(size int) string {
	name, err := randName(size)
	if err != nil {
		panic(fmt.Errorf("RandName(%d): %v", size, err))
	}
	return name
}
