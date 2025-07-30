// Copyright 2025 The Forgejo Authors
// SPDX-License-Identifier: MIT

package testutils

import (
	"errors"
	"os"
)

func FileExists(pathname string) (bool, error) {
	_, err := os.Stat(pathname)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
