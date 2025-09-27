package jobparser

import (
	"fmt"
	"strings"
	"testing"

	"code.forgejo.org/forgejo/runner/v11/act/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestParseRawOn(t *testing.T) {
	kases := []struct {
		input  string
		result []*Event
	}{
		{
			input: "on: issue_comment",
			result: []*Event{
				{
					Name: "issue_comment",
				},
			},
		},
		{
			input: "on:\n  push",
			result: []*Event{
				{
					Name: "push",
				},
			},
		},

		{
			input: "on:\n  - push\n  - pull_request",
			result: []*Event{
				{
					Name: "push",
				},
				{
					Name: "pull_request",
				},
			},
		},
		{
			input: "on:\n  push:\n    branches:\n      - master",
			result: []*Event{
				{
					Name: "push",
					acts: map[string][]string{
						"branches": {
							"master",
						},
					},
				},
			},
		},
		{
			input: "on:\n  branch_protection_rule:\n    types: [created, deleted]",
			result: []*Event{
				{
					Name: "branch_protection_rule",
					acts: map[string][]string{
						"types": {
							"created",
							"deleted",
						},
					},
				},
			},
		},
		{
			input: "on:\n  project:\n    types: [created, deleted]\n  milestone:\n    types: [opened, deleted]",
			result: []*Event{
				{
					Name: "project",
					acts: map[string][]string{
						"types": {
							"created",
							"deleted",
						},
					},
				},
				{
					Name: "milestone",
					acts: map[string][]string{
						"types": {
							"opened",
							"deleted",
						},
					},
				},
			},
		},
		{
			input: "on:\n  pull_request:\n    types:\n      - opened\n    branches:\n      - 'releases/**'",
			result: []*Event{
				{
					Name: "pull_request",
					acts: map[string][]string{
						"types": {
							"opened",
						},
						"branches": {
							"releases/**",
						},
					},
				},
			},
		},
		{
			input: "on:\n  push:\n    branches:\n      - main\n  pull_request:\n    types:\n      - opened\n    branches:\n      - '**'",
			result: []*Event{
				{
					Name: "push",
					acts: map[string][]string{
						"branches": {
							"main",
						},
					},
				},
				{
					Name: "pull_request",
					acts: map[string][]string{
						"types": {
							"opened",
						},
						"branches": {
							"**",
						},
					},
				},
			},
		},
		{
			input: "on:\n  push:\n    branches:\n      - 'main'\n      - 'releases/**'",
			result: []*Event{
				{
					Name: "push",
					acts: map[string][]string{
						"branches": {
							"main",
							"releases/**",
						},
					},
				},
			},
		},
		{
			input: "on:\n  push:\n    tags:\n      - v1.**",
			result: []*Event{
				{
					Name: "push",
					acts: map[string][]string{
						"tags": {
							"v1.**",
						},
					},
				},
			},
		},
		{
			input: "on: [pull_request, workflow_dispatch, workflow_call]",
			result: []*Event{
				{
					Name: "pull_request",
				},
				{
					Name: "workflow_dispatch",
				},
				{
					Name: "workflow_call",
				},
			},
		},
		{
			input: "on:\n  schedule:\n    - cron: '20 6 * * *'",
			result: []*Event{
				{
					Name: "schedule",
					schedules: []map[string]string{
						{
							"cron": "20 6 * * *",
						},
					},
				},
			},
		},
		{
			input: `
on:
  workflow_dispatch:
    inputs:
      test:
        type: string
    silently: ignore
`,
			result: []*Event{
				{
					Name: "workflow_dispatch",
				},
			},
		},
		{
			input: `
on:
  workflow_call:
    inputs:
      test:
        type: string
    outputs:
      output:
        value: something
    silently: ignore
`,
			result: []*Event{
				{
					Name: "workflow_call",
				},
			},
		},
	}
	for _, kase := range kases {
		t.Run(kase.input, func(t *testing.T) {
			origin, err := model.ReadWorkflow(strings.NewReader(kase.input), false)
			assert.NoError(t, err)

			events, err := ParseRawOn(&origin.RawOn)
			assert.NoError(t, err)
			assert.EqualValues(t, kase.result, events, fmt.Sprintf("%#v", events))
		})
	}
}

func TestSingleWorkflow_SetJob(t *testing.T) {
	t.Run("erase needs", func(t *testing.T) {
		content := ReadTestdata(t, "erase_needs.in.yaml")
		want := ReadTestdata(t, "erase_needs.out.yaml")
		swf, err := Parse(content, false)
		require.NoError(t, err)
		builder := &strings.Builder{}
		for _, v := range swf {
			id, job := v.Job()
			require.NoError(t, v.SetJob(id, job.EraseNeeds()))

			if builder.Len() > 0 {
				builder.WriteString("---\n")
			}
			encoder := yaml.NewEncoder(builder)
			encoder.SetIndent(2)
			require.NoError(t, encoder.Encode(v))
		}
		assert.Equal(t, string(want), builder.String())
	})
}

func TestParseMappingNode(t *testing.T) {
	tests := []struct {
		input   string
		scalars []string
		datas   []any
	}{
		{
			input:   "on:\n  push:\n    branches:\n      - master",
			scalars: []string{"push"},
			datas: []any{
				map[string]any{
					"branches": []any{"master"},
				},
			},
		},
		{
			input:   "on:\n  branch_protection_rule:\n    types: [created, deleted]",
			scalars: []string{"branch_protection_rule"},
			datas: []any{
				map[string]any{
					"types": []any{"created", "deleted"},
				},
			},
		},
		{
			input:   "on:\n  project:\n    types: [created, deleted]\n  milestone:\n    types: [opened, deleted]",
			scalars: []string{"project", "milestone"},
			datas: []any{
				map[string]any{
					"types": []any{"created", "deleted"},
				},
				map[string]any{
					"types": []any{"opened", "deleted"},
				},
			},
		},
		{
			input:   "on:\n  pull_request:\n    types:\n      - opened\n    branches:\n      - 'releases/**'",
			scalars: []string{"pull_request"},
			datas: []any{
				map[string]any{
					"types":    []any{"opened"},
					"branches": []any{"releases/**"},
				},
			},
		},
		{
			input:   "on:\n  push:\n    branches:\n      - main\n  pull_request:\n    types:\n      - opened\n    branches:\n      - '**'",
			scalars: []string{"push", "pull_request"},
			datas: []any{
				map[string]any{
					"branches": []any{"main"},
				},
				map[string]any{
					"types":    []any{"opened"},
					"branches": []any{"**"},
				},
			},
		},
		{
			input:   "on:\n  schedule:\n    - cron: '20 6 * * *'",
			scalars: []string{"schedule"},
			datas: []any{
				[]any{map[string]any{
					"cron": "20 6 * * *",
				}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			workflow, err := model.ReadWorkflow(strings.NewReader(test.input), false)
			assert.NoError(t, err)

			scalars, datas, err := parseMappingNode[any](&workflow.RawOn)
			assert.NoError(t, err)
			assert.EqualValues(t, test.scalars, scalars, fmt.Sprintf("%#v", scalars))
			assert.EqualValues(t, test.datas, datas, fmt.Sprintf("%#v", datas))
		})
	}
}

func TestEvaluateConcurrency(t *testing.T) {
	tests := []struct {
		name                string
		input               model.RawConcurrency
		group               string
		cancelInProgressNil bool
		cancelInProgress    bool
	}{
		{
			name: "basic",
			input: model.RawConcurrency{
				Group:            "group-name",
				CancelInProgress: "true",
			},
			group:            "group-name",
			cancelInProgress: true,
		},
		{
			name:                "undefined",
			input:               model.RawConcurrency{},
			group:               "",
			cancelInProgressNil: true,
		},
		{
			name: "group-evaluation",
			input: model.RawConcurrency{
				Group: "${{ github.workflow }}-${{ github.ref }}",
			},
			group:               "test_workflow-main",
			cancelInProgressNil: true,
		},
		{
			name: "cancel-evaluation-true",
			input: model.RawConcurrency{
				Group:            "group-name",
				CancelInProgress: "${{ !contains(github.ref, 'release/')}}",
			},
			group:            "group-name",
			cancelInProgress: true,
		},
		{
			name: "cancel-evaluation-false",
			input: model.RawConcurrency{
				Group:            "group-name",
				CancelInProgress: "${{ contains(github.ref, 'release/')}}",
			},
			group:            "group-name",
			cancelInProgress: false,
		},
		{
			name: "event-evaluation",
			input: model.RawConcurrency{
				Group: "user-${{ github.event.commits[0].author.username }}",
			},
			group:               "user-someone",
			cancelInProgressNil: true,
		},
		{
			name: "arbitrary-var",
			input: model.RawConcurrency{
				Group: "${{ vars.eval_arbitrary_var }}",
			},
			group:               "123",
			cancelInProgressNil: true,
		},
		{
			name: "arbitrary-input",
			input: model.RawConcurrency{
				Group: "${{ inputs.eval_arbitrary_input }}",
			},
			group:               "456",
			cancelInProgressNil: true,
		},
		{
			name: "cancel-in-progress-only",
			input: model.RawConcurrency{
				CancelInProgress: "true",
			},
			group:            "",
			cancelInProgress: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			group, cancelInProgress, err := EvaluateWorkflowConcurrency(
				&test.input,
				// gitCtx
				&model.GithubContext{
					Workflow: "test_workflow",
					Ref:      "main",
					Event: map[string]any{
						"commits": []any{
							map[string]any{
								"author": map[string]any{
									"username": "someone",
								},
							},
							map[string]any{
								"author": map[string]any{
									"username": "someone-else",
								},
							},
						},
					},
				},
				// vars
				map[string]string{
					"eval_arbitrary_var": "123",
				},
				// inputs
				map[string]any{
					"eval_arbitrary_input": "456",
				},
			)
			assert.NoError(t, err)
			assert.EqualValues(t, test.group, group)
			if test.cancelInProgressNil {
				assert.Nil(t, cancelInProgress)
			} else {
				require.NotNil(t, cancelInProgress)
				assert.EqualValues(t, test.cancelInProgress, *cancelInProgress)
			}
		})
	}
}
