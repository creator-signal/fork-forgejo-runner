package schema

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestAdditionalFunctions(t *testing.T) {
	var node yaml.Node
	err := yaml.Unmarshal([]byte(`
on: push
jobs:
  job-with-condition:
    runs-on: self-hosted
    if: success() || success('joba', 'jobb') || failure() || failure('joba', 'jobb') || always() || cancelled()
    steps:
    - run: exit 0
`), &node)
	if !assert.NoError(t, err) {
		return
	}
	err = (&Node{
		Definition: "workflow-root-strict",
		Schema:     GetWorkflowSchema(),
	}).UnmarshalYAML(&node)
	assert.NoError(t, err)
}

func TestContextsInWorkflowMatrix(t *testing.T) {
	t.Run("KnownContexts", func(t *testing.T) {
		// Parse raw YAML snippet.
		var node yaml.Node
		err := yaml.Unmarshal([]byte(`
on: push

jobs:
  job:
    uses: ./.forgejo/workflow/test.yaml
    strategy:
      matrix:
        input1:
          - ${{ forge.KEY }}
          - ${{ forgejo.KEY }}
          - ${{ github.KEY }}
          - ${{ inputs.KEY }}
          - ${{ vars.KEY }}
          - ${{ needs.KEY }}
        include:
         - forge: ${{ forge.KEY }}
         - forgejo: ${{ forgejo.KEY }}
         - github: ${{ github.KEY }}
         - inputs: ${{ inputs.KEY }}
         - vars: ${{ vars.KEY }}
         - needs: ${{ needs.KEY }}
        exclude:
         - forge: ${{ forge.KEY }}
         - forgejo: ${{ forgejo.KEY }}
         - github: ${{ github.KEY }}
         - inputs: ${{ inputs.KEY }}
         - vars: ${{ vars.KEY }}
         - needs: ${{ needs.KEY }}
`), &node)
		if !assert.NoError(t, err) {
			return
		}

		// Parse YAML node as a validated workflow.
		err = (&Node{
			Definition: "workflow-root",
			Schema:     GetWorkflowSchema(),
		}).UnmarshalYAML(&node)
		assert.NoError(t, err)
	})

	t.Run("UnknownContext", func(t *testing.T) {
		for _, property := range []string{"include", "exclude", "input1"} {
			t.Run(property, func(t *testing.T) {
				for _, context := range []string{"secrets", "job", "steps", "runner", "matrix", "strategy"} {
					t.Run(context, func(t *testing.T) {
						var node yaml.Node
						err := yaml.Unmarshal([]byte(fmt.Sprintf(`
on: push

jobs:
  job:
    uses: ./.forgejo/workflow/test.yaml
    strategy:
      matrix:
        %[1]s:
          - input1: ${{ %[2]s.KEY }}
`, property, context)), &node)
						if !assert.NoError(t, err) {
							return
						}
						err = (&Node{
							Definition: "workflow-root",
							Schema:     GetWorkflowSchema(),
						}).UnmarshalYAML(&node)
						assert.ErrorContains(t, err, "Unknown Variable Access "+context)
					})
				}
			})
		}
	})
}

func TestReusableWorkflow(t *testing.T) {
	t.Run("KnownContexts", func(t *testing.T) {
		var node yaml.Node
		err := yaml.Unmarshal([]byte(`
on: push

jobs:
  job:
    uses: ./.forgejo/workflow/test.yaml
    with:
      input1: |
         ${{ forge.KEY }}
         ${{ github.KEY }}
         ${{ inputs.KEY }}
         ${{ vars.KEY }}
         ${{ env.KEY }}
         ${{ needs.KEY }}
         ${{ strategy.KEY }}
         ${{ matrix.KEY }}
`), &node)
		if !assert.NoError(t, err) {
			return
		}
		err = (&Node{
			Definition: "workflow-root",
			Schema:     GetWorkflowSchema(),
		}).UnmarshalYAML(&node)
		assert.NoError(t, err)
	})

	t.Run("UnknownContext", func(t *testing.T) {
		for _, context := range []string{"secrets", "job", "steps", "runner"} {
			t.Run(context, func(t *testing.T) {
				var node yaml.Node
				err := yaml.Unmarshal([]byte(fmt.Sprintf(`
on: push

jobs:
  job:
    uses: ./.forgejo/workflow/test.yaml
    with:
      input1: ${{ %[1]s.KEY }}
`, context)), &node)
				if !assert.NoError(t, err) {
					return
				}
				err = (&Node{
					Definition: "workflow-root",
					Schema:     GetWorkflowSchema(),
				}).UnmarshalYAML(&node)
				assert.ErrorContains(t, err, "Unknown Variable Access "+context)
			})
		}
	})
}

func TestAdditionalFunctionsFailure(t *testing.T) {
	var node yaml.Node
	err := yaml.Unmarshal([]byte(`
on: push
jobs:
  job-with-condition:
    runs-on: self-hosted
    if: success() || success('joba', 'jobb') || failure() || failure('joba', 'jobb') || always('error')
    steps:
    - run: exit 0
`), &node)
	if !assert.NoError(t, err) {
		return
	}
	err = (&Node{
		Definition: "workflow-root-strict",
		Schema:     GetWorkflowSchema(),
	}).UnmarshalYAML(&node)
	assert.Error(t, err)
}

func TestAdditionalFunctionsSteps(t *testing.T) {
	var node yaml.Node
	err := yaml.Unmarshal([]byte(`
on: push
jobs:
  job-with-condition:
    runs-on: self-hosted
    steps:
    - run: exit 0
      if: success() || failure() || always()
    - uses: https://${{ secrets.PAT }}@example.com/action/here@v1
`), &node)
	if !assert.NoError(t, err) {
		return
	}
	err = (&Node{
		Definition: "workflow-root-strict",
		Schema:     GetWorkflowSchema(),
	}).UnmarshalYAML(&node)
	assert.NoError(t, err)
}

func TestAdditionalFunctionsStepsExprSyntax(t *testing.T) {
	var node yaml.Node
	err := yaml.Unmarshal([]byte(`
on: push
jobs:
  job-with-condition:
    runs-on: self-hosted
    steps:
    - run: exit 0
      if: ${{ success() || failure() || always() }}
`), &node)
	if !assert.NoError(t, err) {
		return
	}
	err = (&Node{
		Definition: "workflow-root-strict",
		Schema:     GetWorkflowSchema(),
	}).UnmarshalYAML(&node)
	assert.NoError(t, err)
}

func TestWorkflowCallRunsOn(t *testing.T) {
	var node yaml.Node
	err := yaml.Unmarshal([]byte(`
name: Build Silo Frontend DEV
on:
  push:
    branches:
      - dev
      - dev-*
jobs:
  build_frontend_dev:
    name: Build Silo Frontend DEV
    runs-on: ubuntu-latest
    container:
      image: code.forgejo.org/oci/node:22-bookworm
    uses: ./.forgejo/workflows/${{ vars.PATHNAME }}
    with:
      STAGE: dev
    secrets:
      PACKAGE_WRITER_TOKEN: ${{ secrets.PACKAGE_WRITER_TOKEN }}
`), &node)
	require.NoError(t, err)
	n := &Node{
		Definition: "workflow-root",
		Schema:     GetWorkflowSchema(),
	}
	require.NoError(t, n.UnmarshalYAML(&node))
}

func TestActionSchema(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		action string
	}{
		{
			name: "Expressions",
			action: `
name: 'action name'
author: 'action authors'
description: |
  action ${{ env.SOMETHING }} description
inputs:
  url:
    description: 'url description'
    default: '${{ env.GITHUB_SERVER_URL }}'
  repo:
    description: 'repo description'
    default: '${{ github.repository }} ${{ vars.VARIABLE }} ${{ inputs.VARIABLE }}'
runs:
  using: "composite"
  steps:
    - run: |
        echo "${{ github.action_path }}"
      env:
          MYVAR: ${{ vars.VARIABLE }}
`,
		},
		{
			name: "NoInputs",
			action: `
runs:
  using: "composite"
  steps:
    - run: echo OK
`,
		},
		{
			name: "NoMappingInputs",
			action: `
inputs:
  parameter1:
  parameter2:
runs:
  using: "composite"
  steps:
    - run: echo OK
`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var node yaml.Node
			err := yaml.Unmarshal([]byte(testCase.action), &node)
			if !assert.NoError(t, err) {
				return
			}
			err = (&Node{
				Definition: "action-root",
				Schema:     GetActionSchema(),
			}).UnmarshalYAML(&node)
			assert.NoError(t, err)
		})
	}
}

// https://yaml.org/spec/1.2.1/#id2785586
// An anchor is denoted by the “&” indicator. It marks a node for future reference.
// https://yaml.org/type/merge.html
// Specify one or more mappings to be merged with the current one.
func TestSchema_AnchorAndReference(t *testing.T) {
	var node yaml.Node
	err := yaml.Unmarshal([]byte(`
on: [push]
jobs:
  test1:
    runs-on: docker
    steps:
      - &step
        run: echo All good!
      - *step
  test2:
    runs-on: docker
    steps:
      - << : *step
        name: other name
  test3:
    runs-on: docker
    steps:
      - !!merge << : *step
        name: other name
`), &node)
	if !assert.NoError(t, err) {
		return
	}
	err = (&Node{
		Definition: "workflow-root",
		Schema:     GetWorkflowSchema(),
	}).UnmarshalYAML(&node)
	assert.NoError(t, err)
}
