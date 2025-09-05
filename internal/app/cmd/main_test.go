// Copyright 2025 The Forgejo Authors
// SPDX-License-Identifier: MIT

package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"code.forgejo.org/forgejo/runner/v11/testutils"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// Capture what's being written into a standard file descriptor.
func captureOutput(t *testing.T, stdFD *os.File) (finish func() (output string)) {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err)
	resetStdout := testutils.MockVariable(stdFD, *w)

	return func() (output string) {
		w.Close()
		resetStdout()

		out, err := io.ReadAll(r)
		require.NoError(t, err)
		return string(out)
	}
}

func executeCommand(ctx context.Context, t *testing.T, cmd *cobra.Command, args ...string) (cmdOut, stdOut, stdErr string, err error) {
	t.Helper()
	finishStdout := captureOutput(t, os.Stdout)
	finishStderr := captureOutput(t, os.Stderr)
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)

	err = cmd.ExecuteContext(ctx)

	cmdOut = buf.String()
	stdOut = finishStdout()
	stdErr = finishStderr()
	return cmdOut, stdOut, stdErr, err
}
