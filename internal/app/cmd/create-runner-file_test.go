// SPDX-License-Identifier: MIT

package cmd

import (
	"bytes"
	"context"
	"os"
	"testing"

	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
	"connectrpc.com/connect"
	"runner.forgejo.org/internal/pkg/client"
	"runner.forgejo.org/internal/pkg/config"
	"runner.forgejo.org/internal/pkg/ver"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func executeCommand(ctx context.Context, cmd *cobra.Command, args ...string) (string, error) {
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)

	err := cmd.ExecuteContext(ctx)

	return buf.String(), err
}

func Test_createRunnerFileCmd(t *testing.T) {
	configFile := "config.yml"
	ctx := context.Background()
	cmd := createRunnerFileCmd(ctx, &configFile)
	output, err := executeCommand(ctx, cmd)
	require.ErrorContains(t, err, `required flag(s) "instance", "secret" not set`)
	assert.Contains(t, output, "Usage:")
}

func Test_validateSecret(t *testing.T) {
	require.ErrorContains(t, validateSecret("abc"), "exactly 40 characters")
	require.ErrorContains(t, validateSecret("ZAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"), "must be an hexadecimal")
}

func Test_uuidFromSecret(t *testing.T) {
	uuid, err := uuidFromSecret("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	require.NoError(t, err)
	assert.Equal(t, "41414141-4141-4141-4141-414141414141", uuid)
}

func Test_ping(t *testing.T) {
	cfg := &config.Config{}
	address := os.Getenv("FORGEJO_URL")
	if address == "" {
		address = "https://code.forgejo.org"
	}
	reg := &config.Registration{
		Address: address,
		UUID:    "create-runner-file_test.go",
	}
	assert.NoError(t, ping(cfg, reg))
}

func Test_runCreateRunnerFile(t *testing.T) {
	//
	// Set the .runner file to be in a temporary directory
	//
	dir := t.TempDir()
	configFile := dir + "/config.yml"
	runnerFile := dir + "/.runner"
	cfg, _ := config.LoadDefault("")
	cfg.Runner.File = runnerFile
	yamlData, err := yaml.Marshal(cfg)
	require.NoError(t, err)
	assert.NoError(t, os.WriteFile(configFile, yamlData, 0o666))

	instance, has := os.LookupEnv("FORGEJO_URL")
	if !has {
		instance = "https://code.forgejo.org"
	}
	secret, has := os.LookupEnv("FORGEJO_RUNNER_SECRET")
	assert.True(t, has)
	name := "testrunner"

	//
	// Run create-runner-file
	//
	ctx := context.Background()
	cmd := createRunnerFileCmd(ctx, &configFile)
	output, err := executeCommand(ctx, cmd, "--connect", "--secret", secret, "--instance", instance, "--name", name)
	require.NoError(t, err)
	assert.Empty(t, output)

	//
	// Read back the runner file and verify its content
	//
	reg, err := config.LoadRegistration(runnerFile)
	require.NoError(t, err)
	assert.Equal(t, secret, reg.Token)
	assert.Equal(t, instance, reg.Address)

	//
	// Verify that fetching a task successfully returns there is
	// no task for this runner
	//
	cli := client.New(
		reg.Address,
		cfg.Runner.Insecure,
		reg.UUID,
		reg.Token,
		ver.Version(),
	)
	resp, err := cli.FetchTask(ctx, connect.NewRequest(&runnerv1.FetchTaskRequest{}))
	require.NoError(t, err)
	assert.Nil(t, resp.Msg.GetTask())
}
