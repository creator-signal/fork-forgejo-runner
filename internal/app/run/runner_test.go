package run

import (
	"context"
	"errors"
	"fmt"
	"testing"

	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
	"runner.forgejo.org/internal/pkg/labels"

	"github.com/stretchr/testify/assert"
)

func TestExplainFailedGenerateWorkflow(t *testing.T) {
	logged := ""
	log := func(message string, args ...any) {
		logged += fmt.Sprintf(message, args...) + "\n"
	}
	task := &runnerv1.Task{
		WorkflowPayload: []byte("on: [push]\njobs:\n"),
	}
	generateWorkflowError := errors.New("message 1\nmessage 2")
	err := explainFailedGenerateWorkflow(task, log, generateWorkflowError)
	assert.Error(t, err)
	assert.Equal(t, "1: on: [push]\n2: jobs:\n3: \nErrors were found and although they tend to be cryptic the line number they refer to gives a hint as to where the problem might be.\nmessage 1\nmessage 2\n", logged)
}

func TestLabelUpdate(t *testing.T) {
	ctx := context.Background()
	ls := labels.Labels{}

	initialLabel, err := labels.Parse("testlabel:docker://alpine")
	assert.NoError(t, err)
	ls = append(ls, initialLabel)

	newLs := labels.Labels{}

	newLabel, err := labels.Parse("next label:host")
	assert.NoError(t, err)
	newLs = append(newLs, initialLabel)
	newLs = append(newLs, newLabel)

	runner := Runner{
		labels: ls,
	}

	assert.Contains(t, runner.labels, initialLabel)
	assert.NotContains(t, runner.labels, newLabel)

	runner.Update(ctx, newLs)

	assert.Contains(t, runner.labels, initialLabel)
	assert.Contains(t, runner.labels, newLabel)
}
