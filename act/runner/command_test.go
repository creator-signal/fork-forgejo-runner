package runner

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"

	"code.forgejo.org/forgejo/runner/v13/act/common"
	"code.forgejo.org/forgejo/runner/v13/act/model"
)

func TestCommandSetEnv(t *testing.T) {
	rc := new(RunContext)
	handler := rc.commandHandler(t.Context())

	// set-env has been removed. Verify it has no effect.
	handler("::set-env name=x::valz\n")
	assert.NotContains(t, rc.Env, "x")
}

func TestCommandSetOutput(t *testing.T) {
	rc := new(RunContext)
	rc.StepResults = make(map[string]*model.StepResult)
	handler := rc.commandHandler(t.Context())

	rc.CurrentStep = "my-step"
	rc.StepResults[rc.CurrentStep] = &model.StepResult{
		Outputs: make(map[string]string),
	}

	// set-output has been removed. Verify it has no effect.
	handler("::set-output name=x::valz\n")
	assert.NotContains(t, rc.StepResults["my-step"].Outputs, "x")
}

func TestCommandStopCommands(t *testing.T) {
	logger, hook := test.NewNullLogger()

	a := assert.New(t)
	ctx := common.WithLogger(t.Context(), logger)
	rc := new(RunContext)
	handler := rc.commandHandler(ctx)

	handler("::add-mask::one\n")
	assert.Contains(t, rc.Masks, "one")
	handler("::stop-commands::my-end-token\n")
	handler("::add-mask::two\n")
	assert.NotContains(t, rc.Masks, "two")
	handler("::my-end-token::\n")
	handler("::add-mask::three\n")
	assert.Contains(t, rc.Masks, "three")

	messages := make([]string, 0, len(hook.AllEntries()))
	for _, entry := range hook.AllEntries() {
		messages = append(messages, entry.Message)
	}

	a.NotContains(messages, "  \U00002699  ::add-mask::one\n")
	a.Contains(messages, "  \U00002699  ::add-mask::two\n")
	a.NotContains(messages, "  \U00002699  ::add-mask::three\n")
}

func TestCommandAddmask(t *testing.T) {
	logger, hook := test.NewNullLogger()

	a := assert.New(t)
	ctx := t.Context()
	loggerCtx := common.WithLogger(ctx, logger)

	rc := new(RunContext)
	handler := rc.commandHandler(loggerCtx)
	handler("::add-mask::my-secret-value\n")

	a.Equal("  \U00002699  ***", hook.LastEntry().Message)
	a.NotEqual("  \U00002699  *my-secret-value", hook.LastEntry().Message)
}

// based on https://stackoverflow.com/a/10476304
func captureOutput(t *testing.T, f func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	f()

	outC := make(chan string)

	go func() {
		var buf bytes.Buffer
		_, err := io.Copy(&buf, r)
		if err != nil {
			a := assert.New(t)
			a.Fail("io.Copy failed")
		}
		outC <- buf.String()
	}()

	w.Close()
	os.Stdout = old
	out := <-outC

	return out
}

func TestCommandAddmaskUsemask(t *testing.T) {
	rc := new(RunContext)
	rc.StepResults = make(map[string]*model.StepResult)
	rc.CurrentStep = "my-step"
	rc.StepResults[rc.CurrentStep] = &model.StepResult{
		Outputs: make(map[string]string),
	}

	config := &Config{
		Secrets:         map[string]string{},
		InsecureSecrets: false,
	}

	re := captureOutput(t, func() {
		ctx := t.Context()
		ctx = WithJobLogger(ctx, "0", "testjob", config, &rc.Masks, map[string]any{})

		handler := rc.commandHandler(ctx)
		handler("::add-mask::secret\n")
		rc.setOutput(ctx, map[string]string{"name": "token"}, "secret")
	})

	assert.NotContains(t, re, "secret")
	assert.Equal(t, "[testjob]   \U00002699  ***\n[testjob]   \U00002699  Setting output token=***\n", re)
}

func TestCommandSaveState(t *testing.T) {
	rc := &RunContext{
		CurrentStep: "step",
		StepResults: map[string]*model.StepResult{},
	}

	ctx := t.Context()

	handler := rc.commandHandler(ctx)
	handler("::save-state name=state-name::state-value\n")

	assert.Equal(t, "state-value", rc.IntraActionState["step"]["state-name"])
}
