// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package client

import (
	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
)

func BackwardCompatibleContext(task *runnerv1.Task, suffix string) string {
	for _, prefix := range []string{"forgejo_", "gitea_"} {
		value := task.Context.Fields[prefix+suffix]
		if value != nil {
			return value.GetStringValue()
		}
	}
	return ""
}
