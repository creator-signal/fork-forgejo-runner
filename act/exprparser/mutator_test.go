package exprparser

import (
	"strings"
	"testing"

	"github.com/rhysd/actionlint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestRender(t *testing.T) {
	testCases := []struct {
		input string
	}{
		{input: "'hello'"},
		{input: "123"},
		{input: "123.456"},
		{input: "null"},
		{input: "true"},
		{input: "false"},
		{input: "jobs"},
		{input: "jobs[0]"},
		{input: "jobs.needs.input.thing"},
		{input: "jobs.*"},
		{input: "!false"},
		{input: "1 > 2"},
		{input: "true || false"},
		{input: "somefunc(1, 2, 3)"},
		{input: "somefunc(1)"},
		{input: "somefunc()"},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			parser := actionlint.NewExprParser()
			exprNode, exprErr := parser.Parse(actionlint.NewExprLexer(tc.input + "}}"))
			require.Nil(t, exprErr)

			var builder strings.Builder
			err := render(&builder, exprNode)
			require.NoError(t, err)
			assert.Equal(t, tc.input, builder.String())
		})
	}
}

// Tests that without any mutation configuration, the mutator can round-trip an expression without change.
func TestNoopMutator(t *testing.T) {
	testCases := []struct {
		input string
	}{
		{input: "'hello'"},
		{input: "123"},
		{input: "123.456"},
		{input: "null"},
		{input: "true"},
		{input: "false"},
		{input: "jobs"},
		{input: "jobs[0]"},
		{input: "jobs.needs.input.thing"},
		{input: "jobs.*"},
		{input: "!false"},
		{input: "1 > 2"},
		{input: "true || false"},
		{input: "somefunc(1, 2, 3)"},
		{input: "somefunc(1)"},
		{input: "somefunc()"},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			parser := actionlint.NewExprParser()
			exprNode, exprErr := parser.Parse(actionlint.NewExprLexer(tc.input + "}}"))
			require.Nil(t, exprErr)

			mutator := &mutator{}
			newExprNode, hasEffect, err := mutator.mutate(exprNode)
			require.NoError(t, err)

			var builder strings.Builder
			err = render(&builder, newExprNode)
			require.NoError(t, err)
			assert.False(t, hasEffect)
			assert.Equal(t, tc.input, builder.String())
		})
	}
}

func TestVariableAccessMutator(t *testing.T) {
	vam := &VariableAccessMutator{
		Variable: "jobs",
		Rewriter: func(property actionlint.ExprNode) actionlint.ExprNode {
			// jobs[property] has been accessed; rewrite to outputs[property]
			return &actionlint.IndexAccessNode{
				Operand: &actionlint.VariableNode{Name: "outputs"},
				Index:   property,
			}
		},
	}

	t.Run("object deref", func(t *testing.T) {
		retval, err := vam.SingleNodeMutation(&actionlint.ObjectDerefNode{
			Receiver: &actionlint.VariableNode{Name: "jobs"},
			Property: "some-job",
		})
		require.NoError(t, err)

		var builder strings.Builder
		err = render(&builder, retval)
		require.NoError(t, err)
		assert.Equal(t, "outputs['some-job']", builder.String())
	})

	t.Run("array deref", func(t *testing.T) {
		retval, err := vam.SingleNodeMutation(&actionlint.IndexAccessNode{
			Operand: &actionlint.VariableNode{Name: "jobs"},
			Index: &actionlint.FuncCallNode{
				Callee: "format",
				Args: []actionlint.ExprNode{
					&actionlint.StringNode{Value: "hello world"},
				},
			},
		})
		require.NoError(t, err)

		var builder strings.Builder
		err = render(&builder, retval)
		require.NoError(t, err)
		assert.Equal(t, "outputs[format('hello world')]", builder.String())
	})
}

func TestMutate(t *testing.T) {
	vam1 := &VariableAccessMutator{
		Variable: "variable",
		Rewriter: func(property actionlint.ExprNode) actionlint.ExprNode {
			// jobs[property] has been accessed; rewrite to outputs[property]
			return &actionlint.IndexAccessNode{
				Operand: &actionlint.VariableNode{Name: "outputs"},
				Index:   property,
			}
		},
	}
	vam2 := &VariableAccessMutator{
		Variable: "constant",
		Rewriter: func(property actionlint.ExprNode) actionlint.ExprNode {
			// jobs[property] has been accessed; rewrite to outputs[property]
			return &actionlint.IndexAccessNode{
				Operand: &actionlint.VariableNode{Name: "forgejo"},
				Index:   property,
			}
		},
	}

	output, err := Mutate("Hello ${{ variable.content }}, welcome to ${{ constant.real-world }}.", vam1, vam2)
	require.NoError(t, err)
	assert.Equal(t, "${{ format('Hello {0}, welcome to {1}.', outputs['content'], forgejo['real-world']) }}", output)

	// Verify that a string with no mutations performed is returned identical to input.
	input := "Hello ${{ vars.content }}, welcome to ${{ consts.real-world }}."
	output, err = Mutate(input, vam1, vam2)
	require.NoError(t, err)
	assert.Equal(t, input, output)
}

func TestMutateYamlNode(t *testing.T) {
	testCases := []struct {
		input  string
		output string
	}{
		{
			input:  "hello ${{ var.world }}",
			output: "${{ format('hello {0}', rewritten-var['world']) }}\n",
		},
		{
			input:  "3.1415926",
			output: "3.1415926\n",
		},
		{
			input:  "- hello ${{ var.world }}\n- goodbye, ${{ var.something }}\n",
			output: "- ${{ format('hello {0}', rewritten-var['world']) }}\n- ${{ format('goodbye, {0}', rewritten-var['something']) }}\n",
		},
		{
			input:  "key: hello ${{ var.world }}\n",
			output: "key: ${{ format('hello {0}', rewritten-var['world']) }}\n",
		},
	}

	vam := &VariableAccessMutator{
		Variable: "var",
		Rewriter: func(property actionlint.ExprNode) actionlint.ExprNode {
			return &actionlint.IndexAccessNode{
				Operand: &actionlint.VariableNode{Name: "rewritten-var"},
				Index:   property,
			}
		},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			var node yaml.Node
			err := yaml.Unmarshal([]byte(tc.input), &node)
			require.NoError(t, err)

			err = MutateYamlNode(node.Content[0], vam)
			require.NoError(t, err)

			myYaml, err := yaml.Marshal(node.Content[0])
			require.NoError(t, err)
			assert.Equal(t, tc.output, string(myYaml))
		})
	}
}
