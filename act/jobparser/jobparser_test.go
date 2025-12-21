package jobparser

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"testing"

	"code.forgejo.org/forgejo/runner/v12/act/model"
	"github.com/stretchr/testify/assert"

	"github.com/stretchr/testify/require"

	"go.yaml.in/yaml/v3"
)

func TestParse(t *testing.T) {
	// Ensure any decoding errors cause test failures; these cause error logs in Forgejo.
	origOnDecodeNodeError := model.OnDecodeNodeError
	model.OnDecodeNodeError = func(node yaml.Node, out any, err error) {
		log.Panicf("Failed to decode node %v into %T: %v", node, out, err)
	}
	defer func() { model.OnDecodeNodeError = origOnDecodeNodeError }()

	tests := []struct {
		name    string
		options []ParseOption
		wantErr string

		// If we're expecting {name}.in.yaml to be a SingleWorkflow, which has additional fields that a normal workflow
		// doesn't have, then we can't validate the input as a workflow.
		reparsingSingleWorkflow bool

		// If we're expecting {name}.out.yaml to have additional fields (incomplete_*) that a normal workflow doesn't
		// have, then we can't validate the output as a workflow.
		expectingInvalidWorkflowOutput bool
	}{
		{
			name:    "multiple_named_matrix",
			options: nil,
		},
		{
			name:    "multiple_jobs",
			options: nil,
		},
		{
			name:    "multiple_matrix",
			options: nil,
		},
		{
			name:    "evaluated_matrix",
			options: nil,
		},
		{
			name:    "has_needs",
			options: nil,
		},
		{
			name:    "has_with",
			options: nil,
		},
		{
			name:    "job_concurrency",
			options: nil,
		},
		{
			name:    "job_concurrency_eval",
			options: nil,
		},
		{
			name:    "runs_on_forge_variables",
			options: []ParseOption{WithGitContext(&model.GithubContext{RunID: "18"})},
		},
		{
			name:    "runs_on_github_variables",
			options: []ParseOption{WithGitContext(&model.GithubContext{RunID: "25"})},
		},
		{
			name:    "runs_on_inputs_variables",
			options: []ParseOption{WithInputs(map[string]any{"chosen-os": "Ubuntu"})},
		},
		{
			name:    "runs_on_vars_variables",
			options: []ParseOption{WithVars(map[string]string{"RUNNER": "Windows"})},
		},
		{
			name:    "evaluated_matrix_needs",
			options: []ParseOption{WithJobOutputs(map[string]map[string]string{})},
		},
		{
			name:    "evaluated_matrix_needs_provided",
			options: []ParseOption{WithJobOutputs(map[string]map[string]string{"define-matrix": {"colors": "[\"red\",\"green\",\"blue\"]"}})},
		},
		{
			name:                    "evaluated_matrix_needs_external",
			reparsingSingleWorkflow: true,
			options: []ParseOption{
				WithJobOutputs(map[string]map[string]string{"define-matrix": {"colors": "[\"red\",\"green\",\"blue\"]"}}),
				WithWorkflowNeeds([]string{"define-matrix"}),
			},
		},
		{
			name:    "evaluated_matrix_needs_scalar_array",
			options: []ParseOption{WithJobOutputs(map[string]map[string]string{})},
		},
		{
			name: "runs_on_needs_variables",
			options: []ParseOption{
				WithJobOutputs(map[string]map[string]string{}),
				SupportIncompleteRunsOn(),
			},
		},
		{
			name:                    "runs_on_needs_variables_reparse",
			reparsingSingleWorkflow: true,
			options: []ParseOption{
				WithJobOutputs(map[string]map[string]string{"define-runs-on": {"runner": "ubuntu"}}),
				WithWorkflowNeeds([]string{"define-runs-on"}),
				SupportIncompleteRunsOn(),
			},
		},
		{
			name: "runs_on_needs_expr_array",
			options: []ParseOption{
				WithJobOutputs(map[string]map[string]string{}),
				SupportIncompleteRunsOn(),
			},
		},
		{
			name:                    "runs_on_needs_expr_array_reparse",
			reparsingSingleWorkflow: true,
			options: []ParseOption{
				WithJobOutputs(map[string]map[string]string{"define-runs-on": {"runners": "[\"ubuntu\", \"fedora\"]"}}),
				WithWorkflowNeeds([]string{"define-runs-on"}),
				SupportIncompleteRunsOn(),
			},
		},
		{
			name: "runs_on_incomplete_matrix",
			options: []ParseOption{
				WithJobOutputs(map[string]map[string]string{}),
				SupportIncompleteRunsOn(),
			},
		},
		{
			name:                           "expand_local_workflow",
			expectingInvalidWorkflowOutput: true,
			options: []ParseOption{
				ExpandLocalReusableWorkflows(func(job *Job, path string) ([]byte, error) {
					if path == "./.forgejo/workflows/expand_local_workflow_reusable-1.yml" {
						content := ReadTestdata(t, "expand_local_workflow_reusable-1.yaml", true)
						return content, nil
					}
					return nil, fmt.Errorf("unexpected local path: %q", path)
				}),
			},
		},
		{
			name: "expand_local_workflow_recursion_limit",
			options: []ParseOption{
				ExpandLocalReusableWorkflows(func(job *Job, path string) ([]byte, error) {
					if path == "./.forgejo/workflows/expand_local_workflow_recursion_limit-reusable-1.yml" {
						content := ReadTestdata(t, "expand_local_workflow_recursion_limit-reusable-1.yaml", true)
						return content, nil
					}
					return nil, fmt.Errorf("unexpected local path: %q", path)
				}),
			},
			wantErr: "failed to parse workflow due to exceeding the workflow recursion limit",
		},
		{
			name:                           "expand_remote_workflow",
			expectingInvalidWorkflowOutput: true,
			options: []ParseOption{
				ExpandRemoteReusableWorkflows(func(job *Job, ref *model.RemoteReusableWorkflowWithBaseURL) ([]byte, error) {
					if ref.Org != "some-org" {
						return nil, fmt.Errorf("unexpected remote Org: %q", ref.Org)
					}
					if ref.Repo != "some-repo" {
						return nil, fmt.Errorf("unexpected remote Repo: %q", ref.Repo)
					}
					if ref.GitPlatform != "forgejo" {
						return nil, fmt.Errorf("unexpected remote GitPlatform: %q", ref.GitPlatform)
					}
					if ref.BaseURL == nil {
						// relative reference in expand_remote_workflow.in.yaml
						if ref.Filename != "expand_remote_workflow_reusable-2.yml" {
							return nil, fmt.Errorf("unexpected remote Filename: %q", ref.Filename)
						}
					} else {
						// absolute reference in expand_remote_workflow.in.yaml
						if *ref.BaseURL != "https://example.com" {
							return nil, fmt.Errorf("unexpected remote Host: %v", ref.BaseURL)
						}
						if ref.Filename != "expand_remote_workflow_reusable-1.yml" {
							return nil, fmt.Errorf("unexpected remote Filename: %q", ref.Filename)
						}
					}
					if ref.Ref != "v1" {
						return nil, fmt.Errorf("unexpected remote Ref: %q", ref.Ref)
					}
					content := ReadTestdata(t, "expand_remote_workflow_reusable-1.yaml", true)
					return content, nil
				}),
			},
		},
		{
			name:                           "expand_inputs",
			reparsingSingleWorkflow:        true,
			expectingInvalidWorkflowOutput: true,
			options: []ParseOption{
				WithInputs(map[string]any{
					"caller-invalid-input": "this shouldn't appear in the reusable workflow",
				}),
				ExpandLocalReusableWorkflows(func(job *Job, path string) ([]byte, error) {
					content := ReadTestdata(t, "expand_inputs_reusable.yaml", true)
					return content, nil
				}),
			},
		},
		{
			name: "expand_reusable_needs",
			options: []ParseOption{
				ExpandLocalReusableWorkflows(func(job *Job, path string) ([]byte, error) {
					if path == "./.forgejo/workflows/expand_local_workflow_reusable-1.yml" {
						content := ReadTestdata(t, "expand_local_workflow_reusable-1.yaml", true)
						return content, nil
					}
					return nil, fmt.Errorf("unexpected local path: %q", path)
				}),
			},
		},
		{
			name:                           "expand_reusable_needs_recursive",
			expectingInvalidWorkflowOutput: true,
			options: []ParseOption{
				ExpandLocalReusableWorkflows(func(job *Job, path string) ([]byte, error) {
					if path == "./.forgejo/workflows/expand_reusable_needs_recursive-1.yml" {
						content := ReadTestdata(t, "expand_reusable_needs_recursive-1.yaml", true)
						return content, nil
					}
					if path == "./.forgejo/workflows/expand_reusable_needs_recursive-2.yml" {
						content := ReadTestdata(t, "expand_reusable_needs_recursive-2.yaml", true)
						return content, nil
					}
					return nil, fmt.Errorf("unexpected local path: %q", path)
				}),
			},
		},
		// A simple implementation of workflowCallID based upon the local job name would fail in this test case with the
		// "attempted to emit multiple jobs with workflowCallID" error because it creates multiple jobs with the same
		// job name and matrix values -- workflowCallIDs have to be based upon a complete top-to-bottom job naming and
		// global matrix knowledge to avoid this test failure.
		{
			name:                           "expand_reusable_unique_call_ids",
			expectingInvalidWorkflowOutput: true,
			options: []ParseOption{
				ExpandLocalReusableWorkflows(func(job *Job, path string) ([]byte, error) {
					if path == "./.forgejo/workflows/expand_reusable_unique_call_ids_reusable-1.yml" {
						content := ReadTestdata(t, "expand_reusable_unique_call_ids_reusable-1.yaml", true)
						return content, nil
					}
					if path == "./.forgejo/workflows/expand_reusable_unique_call_ids_reusable-2.yml" {
						content := ReadTestdata(t, "expand_reusable_unique_call_ids_reusable-2.yaml", true)
						return content, nil
					}
					return nil, fmt.Errorf("unexpected local path: %q", path)
				}),
			},
		},
		{
			name:                           "expand_reusable_outputs",
			expectingInvalidWorkflowOutput: true,
			options: []ParseOption{
				ExpandLocalReusableWorkflows(func(job *Job, path string) ([]byte, error) {
					if path == "./.forgejo/workflows/expand_reusable_outputs_reusable-1.yml" {
						content := ReadTestdata(t, "expand_reusable_outputs_reusable-1.yaml", true)
						return content, nil
					}
					return nil, fmt.Errorf("unexpected local path: %q", path)
				}),
			},
		},
		{
			name:                           "expand_reusable_crossreferences",
			expectingInvalidWorkflowOutput: true,
			options: []ParseOption{
				ExpandLocalReusableWorkflows(func(job *Job, path string) ([]byte, error) {
					if path == "./.forgejo/workflows/expand_reusable_crossreferences_reusable-1.yml" {
						content := ReadTestdata(t, "expand_reusable_crossreferences_reusable-1.yaml", true)
						return content, nil
					}
					return nil, fmt.Errorf("unexpected local path: %q", path)
				}),
			},
		},
		{
			name:                           "expand_reusable_caller_matrix",
			expectingInvalidWorkflowOutput: true,
			options: []ParseOption{
				ExpandLocalReusableWorkflows(func(job *Job, path string) ([]byte, error) {
					if path == "./.forgejo/workflows/expand_reusable_caller_matrix_reusable-1.yml" {
						content := ReadTestdata(t, "expand_reusable_caller_matrix_reusable-1.yaml", true)
						return content, nil
					}
					return nil, fmt.Errorf("unexpected local path: %q", path)
				}),
			},
		},
		// `expand_reusable_incomplete1` covers a test case where the caller of a reusable workflow has a `matrix` job
		// that references `${{ needs... }}`, and therefore requires job outputs before it can be expanded.
		{
			name: "expand_reusable_incomplete1",
			options: []ParseOption{
				WithJobOutputs(map[string]map[string]string{}),
				SupportIncompleteRunsOn(),
				ExpandLocalReusableWorkflows(func(job *Job, path string) ([]byte, error) {
					if path == "./.forgejo/workflows/expand_reusable_incomplete1_reusable.yml" {
						content := ReadTestdata(t, "expand_reusable_incomplete1_reusable.yaml", true)
						return content, nil
					}
					return nil, fmt.Errorf("unexpected local path: %q", path)
				}),
			},
		},
		// `expand_reusable_incomplete1_complete` covers reparsing the incomplete workflow from
		// `expand_reusable_incomplete1` after the `needs` is defined, allowing the matrix to be expanded.
		{
			name:                           "expand_reusable_incomplete1_complete",
			reparsingSingleWorkflow:        true,
			expectingInvalidWorkflowOutput: true,
			options: []ParseOption{
				WithWorkflowNeeds([]string{"define-runs-on"}),
				WithJobOutputs(map[string]map[string]string{
					"define-runs-on": {
						"runners": "[\"runner-a\", \"runner-b\"]",
					},
				}),
				SupportIncompleteRunsOn(),
				ExpandLocalReusableWorkflows(func(job *Job, path string) ([]byte, error) {
					if path == "./.forgejo/workflows/expand_reusable_incomplete1_reusable.yml" {
						content := ReadTestdata(t, "expand_reusable_incomplete1_reusable.yaml", true)
						return content, nil
					}
					return nil, fmt.Errorf("unexpected local path: %q", path)
				}),
			},
		},
		// `expand_reusable_incomplete2` covers a test case where the caller of a reusable workflow has a `with`
		// defining inputs for a reusable workflow that references `${{ needs... }}`, and therefore requires job outputs
		// before it can be expanded.
		{
			name:                           "expand_reusable_incomplete2",
			expectingInvalidWorkflowOutput: true,
			options: []ParseOption{
				WithJobOutputs(map[string]map[string]string{}),
				SupportIncompleteRunsOn(),
				ExpandLocalReusableWorkflows(func(job *Job, path string) ([]byte, error) {
					if path == "./.forgejo/workflows/expand_reusable_incomplete2_reusable.yml" {
						content := ReadTestdata(t, "expand_reusable_incomplete2_reusable.yaml", true)
						return content, nil
					}
					return nil, fmt.Errorf("unexpected local path: %q", path)
				}),
			},
		},
		// `expand_reusable_incomplete2_complete` covers reparsing the incomplete workflow from
		// `expand_reusable_incomplete2` after the `needs` is defined, allowing the `with` to be expanded.
		{
			name:                           "expand_reusable_incomplete2_complete",
			reparsingSingleWorkflow:        true,
			expectingInvalidWorkflowOutput: true,
			options: []ParseOption{
				WithWorkflowNeeds([]string{"define-with"}),
				WithJobOutputs(map[string]map[string]string{
					"define-with": {
						"runner": "ubuntu-29.99",
					},
				}),
				SupportIncompleteRunsOn(),
				ExpandLocalReusableWorkflows(func(job *Job, path string) ([]byte, error) {
					if path == "./.forgejo/workflows/expand_reusable_incomplete2_reusable.yml" {
						content := ReadTestdata(t, "expand_reusable_incomplete2_reusable.yaml", true)
						return content, nil
					}
					return nil, fmt.Errorf("unexpected local path: %q", path)
				}),
			},
		},
		// `expand_reusable_incomplete3` covers accessing `${{ matrix.something }}` in a `with` clause for a reusable
		// workflow when `something` isn't actually defined in the job's matrix.
		{
			name:                           "expand_reusable_incomplete3",
			reparsingSingleWorkflow:        true,
			expectingInvalidWorkflowOutput: true,
			options: []ParseOption{
				WithJobOutputs(map[string]map[string]string{}),
				SupportIncompleteRunsOn(),
				ExpandLocalReusableWorkflows(func(job *Job, path string) ([]byte, error) {
					if path == "./.forgejo/workflows/expand_reusable_incomplete3_reusable.yml" {
						content := ReadTestdata(t, "expand_reusable_incomplete3_reusable.yaml", true)
						return content, nil
					}
					return nil, fmt.Errorf("unexpected local path: %q", path)
				}),
			},
		},
		// `expand_reusable_incomplete4` tests a job within a reusable workflow being marked as incomplete because it
		// has a dependency on another job within the same workflow, and therefore can't be fully evaluated yet at
		// expansion time of the caller.  Specifically this case is a `runs-on: ${{ needs.other-local-job.outputs.blah
		// }}`, but it is expected that no specialized handling is required between the two cases where this is
		// supported (matrix, runs-on).
		{
			name:                           "expand_reusable_incomplete4",
			expectingInvalidWorkflowOutput: true,
			options: []ParseOption{
				WithJobOutputs(map[string]map[string]string{}),
				SupportIncompleteRunsOn(),
				ExpandLocalReusableWorkflows(func(job *Job, path string) ([]byte, error) {
					if path == "./.forgejo/workflows/expand_reusable_incomplete4_reusable.yml" {
						content := ReadTestdata(t, "expand_reusable_incomplete4_reusable.yaml", true)
						return content, nil
					}
					return nil, fmt.Errorf("unexpected local path: %q", path)
				}),
			},
		},
		// `expand_reusable_incomplete4_complete` covers reparsing the incomplete workflow from
		// `expand_reusable_incomplete4` after the `needs` is defined, allowing the `with` to be expanded.
		{
			name:                           "expand_reusable_incomplete4_complete",
			reparsingSingleWorkflow:        true,
			expectingInvalidWorkflowOutput: true,
			options: []ParseOption{
				WithWorkflowNeeds([]string{"reusable.inner-define-runs-on"}),
				WithJobOutputs(map[string]map[string]string{
					"reusable.inner-define-runs-on": {
						"runner": "ubuntu-29.99",
					},
				}),
				SupportIncompleteRunsOn(),
			},
		},
		// `expand_reusable_incomplete5` tests a job within a reusable workflow that can't be expanded because it also
		// references a reusable workflow, and it has a `with:` that has a dependency on another job within the same
		// workflow.  This is similar to `expand_reusable_incomplete4` but the `with` clause was identified as requiring
		// special handling in the reusable workflow expansion.
		{
			name:                           "expand_reusable_incomplete5",
			expectingInvalidWorkflowOutput: true,
			options: []ParseOption{
				WithJobOutputs(map[string]map[string]string{}),
				SupportIncompleteRunsOn(),
				ExpandLocalReusableWorkflows(func(job *Job, path string) ([]byte, error) {
					if path == "./.forgejo/workflows/expand_reusable_incomplete5_reusable.yml" {
						content := ReadTestdata(t, "expand_reusable_incomplete5_reusable.yaml", true)
						return content, nil
					}
					return nil, fmt.Errorf("unexpected local path: %q", path)
				}),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := ReadTestdata(t, tt.name+".in.yaml", tt.reparsingSingleWorkflow)
			got, err := Parse(content, false, tt.options...)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)

				want := ReadTestdata(t, tt.name+".out.yaml", tt.expectingInvalidWorkflowOutput)
				builder := &strings.Builder{}
				for _, v := range got {
					if builder.Len() > 0 {
						builder.WriteString("---\n")
					}
					encoder := yaml.NewEncoder(builder)
					encoder.SetIndent(2)
					require.NoError(t, encoder.Encode(v))
					id, job := v.Job()
					assert.NotEmpty(t, id)
					assert.NotNil(t, job)
				}
				assert.Equal(t, string(want), builder.String())
			}
		})
	}
}

func TestEvaluateReusableWorkflowInputs(t *testing.T) {
	testWorkflow := `
on:
  workflow_call:
    inputs:
      example-string-required:
        required: true
        type: string
      example-boolean-required:
        required: true
        type: boolean
      example-number-required:
        required: true
        type: number
      context-forgejo:
        type: string
      context-inputs:
        type: string
      context-matrix:
        type: string
      context-needs:
        type: string
      context-strategy:
        type: string
      context-vars:
        type: string
      default-forgejo:
        type: string
        default: ${{ forgejo.event_name }}
      default-vars:
        type: string
        default: ${{ vars.best-var }}
jobs:
  job:
    steps: []
`

	workflow, err := model.ReadWorkflow(bytes.NewReader([]byte(testWorkflow)), true)
	require.NoError(t, err)

	inputs, rebuildInputs, err := evaluateReusableWorkflowInputs(
		workflow,
		&parseContext{
			gitContext: &model.GithubContext{
				EventName: "workflow_call",
			},
			inputs:        map[string]any{"my_input": "my_input_value"},
			workflowNeeds: []string{"some-job"},
			vars:          map[string]string{"best-var": "the-best-var"},
		},
		map[string]*JobResult{
			"some-job": {
				Outputs: map[string]string{"some-output": "some-output-value"},
			},
		},
		map[string]any{
			"os": "nixos",
		},
		&bothJobTypes{
			workflowJob: &model.Job{
				With: map[string]any{
					"example-string-required":  "example string",
					"example-boolean-required": true,
					"example-number-required":  123.456,
					"context-forgejo":          "${{ forgejo.event_name }}",
					"context-inputs":           "${{ inputs.my_input }}",
					"context-needs":            "${{ needs.some-job.outputs.some-output }}",
					"context-strategy":         "${{ strategy.fail-fast }}",
					"context-vars":             "${{ vars.best-var }}",
					"context-matrix":           "${{ matrix.os }}",
				},
				Strategy: &model.Strategy{
					FailFast: true,
				},
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, rebuildInputs)

	// These could all be one `assert.Subset`, but then it's hard to see in the test output what value was missing

	// Simple value inputs passed in from `with: ...`
	assert.Subset(t, inputs, map[string]any{"example-string-required": "example string"})
	assert.Subset(t, inputs, map[string]any{"example-boolean-required": true})
	assert.Subset(t, inputs, map[string]any{"example-number-required": 123.456})

	// Variable-accessing values passed in from `with: ...`
	assert.Subset(t, inputs, map[string]any{"context-forgejo": "workflow_call"})
	assert.Subset(t, inputs, map[string]any{"context-inputs": "my_input_value"})
	assert.Subset(t, inputs, map[string]any{"context-matrix": "nixos"})
	assert.Subset(t, inputs, map[string]any{"context-needs": "some-output-value"})
	assert.Subset(t, inputs, map[string]any{"context-strategy": true})
	assert.Subset(t, inputs, map[string]any{"context-vars": "the-best-var"})

	// Variable-accessing values defined in `on.workflow_call.inputs.<input_name>.default`
	assert.Subset(t, inputs, map[string]any{"default-forgejo": "workflow_call"})
	assert.Subset(t, inputs, map[string]any{"default-vars": "the-best-var"})
}

func TestRecursionDepthLimit(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		testWorkflow := `
on:
  pull_request:
jobs:
  job:
    uses: some-org/some-repo/.forgejo/workflows/recursive.yaml@v1
`

		swf, err := Parse(
			[]byte(testWorkflow),
			false,
			ExpandRemoteReusableWorkflows(func(job *Job, ref *model.RemoteReusableWorkflowWithBaseURL) ([]byte, error) {
				return []byte(testWorkflow), nil
			}),
		)
		require.ErrorContains(t, err, "exceeding the workflow recursion limit")
		assert.Nil(t, swf)
	})

	// An incomplete workflow requires reparsing and that can lost the recursion tracking state if it's not separately
	// supported for this case.
	t.Run("incomplete", func(t *testing.T) {
		outerWorkflow := `
on:
  pull_request:
jobs:
  job:
    uses: some-org/some-repo/.forgejo/workflows/recursive.yaml@v1
`
		innerWorkflow := `
on:
  workflow_call:
    inputs:
      input:
        type: string
jobs:
  # theoretically 'some-other-job' would be here, but we'll mock it's output
  job:
    uses: some-org/some-repo/.forgejo/workflows/recursive.yaml@v1
    needs: some-other-job
    with:
      input: ${{ needs.some-other-job.outputs.some-output }}
`

		jobOutputs := map[string]map[string]string{}

		swf, err := Parse(
			[]byte(outerWorkflow),
			false,
			WithJobOutputs(jobOutputs),
			ExpandRemoteReusableWorkflows(func(job *Job, ref *model.RemoteReusableWorkflowWithBaseURL) ([]byte, error) {
				return []byte(innerWorkflow), nil
			}),
		)
		require.NoError(t, err)

		require.Len(t, swf, 2) // two jobs - the parent placeholder for the caller, and the incomplete job from the reusable workflow
		assert.False(t, swf[0].IncompleteWith)
		assert.True(t, swf[1].IncompleteWith)
		require.NotNil(t, swf[1].Metadata)
		assert.Equal(t, 1, swf[1].Metadata.IncompleteRecursionDepth) // should have tracked that we had to recurse one level already to get here

		// Now we'll re-parse the second job and provide the missing inputs.  The goal is to get a recursion error here,
		// and because we already recursed one level in the first parsing, to get the recursion error in less than 5
		// iterations.
		incompleteWorkflow, err := swf[1].Marshal()
		require.NoError(t, err)

		callCount := 0
		_, err = Parse(
			incompleteWorkflow,
			false,
			WithJobOutputs(map[string]map[string]string{
				// Make sure it's complete now:
				"some-other-job": {
					"some-output": "abc",
				},
			}),
			WithWorkflowNeeds([]string{"some-other-job"}),
			ExpandRemoteReusableWorkflows(func(job *Job, ref *model.RemoteReusableWorkflowWithBaseURL) ([]byte, error) {
				callCount++
				return []byte(outerWorkflow), nil
			}),
		)
		require.ErrorContains(t, err, "exceeding the workflow recursion limit")
		assert.Equal(t, 5, callCount) // would reach 6 if internal state of the first track wasn't persisted
	})
}

func TestReusableWorkflowFetcherArgs(t *testing.T) {
	t.Run("ExpandRemoteReusableWorkflows", func(t *testing.T) {
		testWorkflow := `
on:
  pull_request:
jobs:
  job:
    runs-on: ubuntu-latest
    uses: some-org/some-repo/.forgejo/workflows/recursive.yaml@v1
  other-job:
    runs-on: ubuntu-oldest
    steps: []
`

		executed := false
		swf, err := Parse(
			[]byte(testWorkflow),
			false,
			ExpandRemoteReusableWorkflows(func(job *Job, ref *model.RemoteReusableWorkflowWithBaseURL) ([]byte, error) {
				executed = true
				// validate `job` passed in is correct/expected object
				assert.Equal(t, []string{"ubuntu-latest"}, job.RunsOn())
				assert.Equal(t, "some-org", ref.Org)
				return []byte("jobs: { somejob: {} }"), nil
			}),
		)
		require.NoError(t, err)
		require.NotNil(t, swf)
		assert.True(t, executed)
	})

	t.Run("ExpandLocalReusableWorkflows", func(t *testing.T) {
		testWorkflow := `
on:
  pull_request:
jobs:
  job:
    runs-on: ubuntu-latest
    uses: ./.forgejo/workflows/recursive.yaml
  other-job:
    runs-on: ubuntu-oldest
    steps: []
`

		executed := false
		swf, err := Parse(
			[]byte(testWorkflow),
			false,
			ExpandLocalReusableWorkflows(func(job *Job, path string) ([]byte, error) {
				executed = true
				// validate `job` passed in is correct/expected object
				assert.Equal(t, []string{"ubuntu-latest"}, job.RunsOn())
				assert.Equal(t, "./.forgejo/workflows/recursive.yaml", path)
				return []byte("jobs: { somejob: {} }"), nil
			}),
		)
		require.NoError(t, err)
		require.NotNil(t, swf)
		assert.True(t, executed)
	})
}
