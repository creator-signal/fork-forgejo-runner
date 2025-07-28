// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_validateCmdError(t *testing.T) {
	ctx := context.Background()
	cmd := loadValidateCmd(ctx)
	output, _, _, err := executeCommand(ctx, t, cmd)
	assert.ErrorContains(t, err, `--path "": open : no such file or directory`)
	assert.Contains(t, output, "Usage:")
}

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
			name:    "MutuallyExclusive",
			args:    []string{"--action", "--workflow"},
			message: "[action workflow] were all set",
		},
		{
			name:   "ActionOK",
			args:   []string{"--action", "--path", "testdata/validate/good-action.yml"},
			stdOut: "schema validation OK",
		},
		{
			name:   "ActionNOK",
			args:   []string{"--action", "--path", "testdata/validate/bad-action.yml"},
			stdOut: "Expected a mapping got scalar",
		},
		{
			name:   "WorkflowOK",
			args:   []string{"--workflow", "--path", "testdata/validate/good-workflow.yml"},
			stdOut: "schema validation OK",
		},
		{
			name:   "WorkflowNOK",
			args:   []string{"--workflow", "--path", "testdata/validate/bad-workflow.yml"},
			stdOut: "Unknown Property ruins-on",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cmd := loadValidateCmd(ctx)
			cmdOut, stdOut, _, err := executeCommand(ctx, t, cmd, testCase.args...)
			if testCase.message != "" {
				assert.ErrorContains(t, err, testCase.message)
			} else {
				require.NoError(t, err)
			}
			if testCase.stdOut != "" {
				assert.Contains(t, stdOut, testCase.stdOut)
			}
			if testCase.cmdOut != "" {
				assert.Contains(t, cmdOut, testCase.cmdOut)
			}
		})
	}
}
