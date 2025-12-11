package exprparser

import (
	"strings"
	"testing"

	"github.com/rhysd/actionlint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			newExprNode, err := mutator.mutate(exprNode)
			require.NoError(t, err)

			var builder strings.Builder
			err = render(&builder, newExprNode)
			require.NoError(t, err)
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
}
