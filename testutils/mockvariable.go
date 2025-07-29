// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package testutils

func MockVariable[T any](p *T, v T) (reset func()) {
	old := *p
	*p = v
	return func() { *p = old }
}
