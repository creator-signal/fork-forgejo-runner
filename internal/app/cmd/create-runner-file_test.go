// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"os"
	"testing"

	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
	"code.forgejo.org/forgejo/runner/v9/internal/pkg/client"
	"code.forgejo.org/forgejo/runner/v9/internal/pkg/config"
	"code.forgejo.org/forgejo/runner/v9/internal/pkg/ver"
	"connectrpc.com/connect"

	"github.com/stretchr/testify/assert"
	"go.yaml.in/yaml/v3"
)

func Test_createRunnerFileCmd(t *testing.T) {
	configFile := "config.yml"
	ctx := context.Background()
	cmd := createRunnerFileCmd(ctx, &configFile)
	output, _, _, err := executeCommand(ctx, t, cmd)
	assert.ErrorContains(t, err, `required flag(s) "instance", "secret" not set`)
	assert.Contains(t, output, "Usage:")
}

func Test_validateSecret(t *testing.T) {
	assert.ErrorContains(t, validateSecret("abc"), "exactly 40 characters")
	assert.ErrorContains(t, validateSecret("ZAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"), "must be an hexadecimal")
}

func Test_uuidFromSecret(t *testing.T) {
	uuid, err := uuidFromSecret("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	assert.NoError(t, err)
	assert.EqualValues(t, uuid, "41414141-4141-4141-4141-414141414141")
}

func getForgejoFromEnv(t *testing.T) string {
	t.Helper()
	address := os.Getenv("FORGEJO_URL")
	if address == "" {
		t.Skip("skipping because FORGEJO_URL is not set")
	}
	return address
}

func Test_ping(t *testing.T) {
	cfg := &config.Config{}
	address := getForgejoFromEnv(t)
	reg := &config.Registration{
		Address: address,
		UUID:    "create-runner-file_test.go",
	}
	assert.NoError(t, ping(cfg, reg))
}

func Test_runCreateRunnerFile(t *testing.T) {
	instance := getForgejoFromEnv(t)

	//
	// Set the .runner file to be in a temporary directory
	//
	dir := t.TempDir()
	configFile := dir + "/config.yml"
	runnerFile := dir + "/.runner"
	cfg, _ := config.LoadDefault("")
	cfg.Runner.File = runnerFile
	yamlData, err := yaml.Marshal(cfg)
	assert.NoError(t, err)
	assert.NoError(t, os.WriteFile(configFile, yamlData, 0o666))

	secret, has := os.LookupEnv("FORGEJO_RUNNER_SECRET")
	assert.True(t, has)
	name := "testrunner"

	//
	// Run create-runner-file
	//
	ctx := context.Background()
	cmd := createRunnerFileCmd(ctx, &configFile)
	output, _, _, err := executeCommand(ctx, t, cmd, "--connect", "--secret", secret, "--instance", instance, "--name", name)
	assert.NoError(t, err)
	assert.EqualValues(t, "", output)

	//
	// Read back the runner file and verify its content
	//
	reg, err := config.LoadRegistration(runnerFile)
	assert.NoError(t, err)
	assert.EqualValues(t, secret, reg.Token)
	assert.EqualValues(t, instance, reg.Address)

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
	assert.NoError(t, err)
	assert.Nil(t, resp.Msg.GetTask())
}
