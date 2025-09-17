// Copyright 2025 The Forgejo Authors
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_validateCmd(t *testing.T) {
	ctx := context.Background()
	for _, testCase := range []struct {
		name    string
		args    []string
		message string
		cmdOut  string
		stdOut  string
		stdErr  string
	}{
		{
			name:    "MissingFlag",
			args:    []string{"--path", "testdata/validate/good-action.yml"},
			cmdOut:  "Usage:",
			message: "one of --workflow or --action must be set",
		},
		{
			name:    "MutuallyExclusiveActionWorkflow",
			args:    []string{"--action", "--workflow", "--path", "/tmp"},
			message: "[action workflow] were all set",
		},
		{
			name:    "MutuallyExclusiveRepositoryDirectory",
			args:    []string{"--repository", "example.com", "--directory", "."},
			message: "[directory repository] were all set",
		},
		{
			name:    "MutuallyExclusiveClonedirDirectory",
			args:    []string{"--clonedir", ".", "--directory", "."},
			message: "[clonedir directory] were all set",
		},
		{
			name:   "PathActionOK",
			args:   []string{"--action", "--path", "testdata/validate/good-action.yml"},
			stdOut: "schema validation OK",
		},
		{
			name:   "PathActionNOK",
			args:   []string{"--action", "--path", "testdata/validate/bad-action.yml"},
			stdOut: "Expected a mapping got scalar",
		},
		{
			name:   "PathWorkflowOK",
			args:   []string{"--workflow", "--path", "testdata/validate/good-workflow.yml"},
			stdOut: "schema validation OK",
		},
		{
			name:   "PathWorkflowNOK",
			args:   []string{"--workflow", "--path", "testdata/validate/bad-workflow.yml"},
			stdOut: "Unknown Property ruins-on",
		},
		{
			name:   "DirectoryOK",
			args:   []string{"--directory", "testdata/validate/good-directory"},
			stdOut: "action.yml action schema validation OK\nsubaction/action.yaml action schema validation OK\n.forgejo/workflows/action.yml workflow schema validation OK\n.forgejo/workflows/workflow1.yml workflow schema validation OK\n.forgejo/workflows/workflow2.yaml workflow schema validation OK",
		},
		{
			name:   "DirectoryActionNOK",
			args:   []string{"--directory", "testdata/validate/bad-directory"},
			stdOut: "action.yml action schema validation failed",
		},
		{
			name:   "DirectoryWorkflowNOK",
			args:   []string{"--directory", "testdata/validate/bad-directory"},
			stdOut: ".forgejo/workflows/workflow1.yml workflow schema validation failed",
		},
		{
			name:   "RepositoryOK",
			args:   []string{"--repository", "testdata/validate/good-repository"},
			stdOut: "action.yml action schema validation OK\nsubaction/action.yaml action schema validation OK\n.forgejo/workflows/action.yml workflow schema validation OK\n.forgejo/workflows/workflow1.yml workflow schema validation OK\n.forgejo/workflows/workflow2.yaml workflow schema validation OK",
		},
		{
			name:   "RepositoryActionNOK",
			args:   []string{"--repository", "testdata/validate/bad-repository"},
			stdOut: "action.yml action schema validation failed",
		},
		{
			name:   "RepositoryWorkflowNOK",
			args:   []string{"--repository", "testdata/validate/bad-repository"},
			stdOut: ".forgejo/workflows/workflow1.yml workflow schema validation failed",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cmd := loadValidateCmd(ctx)
			cmdOut, stdOut, stdErr, err := executeCommand(ctx, t, cmd, testCase.args...)
			if testCase.message != "" {
				assert.ErrorContains(t, err, testCase.message)
			} else {
				assert.NoError(t, err)
			}
			if testCase.stdOut != "" {
				assert.Contains(t, stdOut, testCase.stdOut)
			} else {
				assert.Empty(t, stdOut)
			}
			if testCase.stdErr != "" {
				assert.Contains(t, stdErr, testCase.stdErr)
			} else {
				assert.Empty(t, stdErr)
			}
			if testCase.cmdOut != "" {
				assert.Contains(t, cmdOut, testCase.cmdOut)
			}
		})
	}
}
