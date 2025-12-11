package exprparser

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExpressionRewriteSubExpression(t *testing.T) {
	table := []struct {
		in  string
		out string
	}{
		{in: "Hello World", out: "Hello World"},
		{in: "${{ true }}", out: "${{ true }}"},
		{in: "${{ true }} ${{ true }}", out: "format('{0} {1}', true, true)"},
		{in: "${{ true || false }} ${{ true && true }}", out: "format('{0} {1}', true || false, true && true)"},
		{in: "${{ '}}' }}", out: "${{ '}}' }}"},
		{in: "${{ '''}}''' }}", out: "${{ '''}}''' }}"},
		{in: "${{ '''' }}", out: "${{ '''' }}"},
		{in: `${{ fromJSON('"}}"') }}`, out: `${{ fromJSON('"}}"') }}`},
		{in: `${{ fromJSON('"\"}}\""') }}`, out: `${{ fromJSON('"\"}}\""') }}`},
		{in: `${{ fromJSON('"''}}"') }}`, out: `${{ fromJSON('"''}}"') }}`},
		{in: "Hello ${{ 'World' }}", out: "format('Hello {0}', 'World')"},
	}

	for _, table := range table {
		t.Run("TestRewriteSubExpression", func(t *testing.T) {
			assertObject := assert.New(t)
			out := RewriteSubExpression(table.in, false)
			assertObject.Equal(table.out, out, table.in)
		})
	}
}

func TestExpressionRewriteSubExpressionForceFormat(t *testing.T) {
	table := []struct {
		in  string
		out string
	}{
		{in: "Hello World", out: "Hello World"},
		{in: "${{ true }}", out: "format('{0}', true)"},
		{in: "${{ '}}' }}", out: "format('{0}', '}}')"},
		{in: `${{ fromJSON('"}}"') }}`, out: `format('{0}', fromJSON('"}}"'))`},
		{in: "Hello ${{ 'World' }}", out: "format('Hello {0}', 'World')"},
	}

	for _, table := range table {
		t.Run("TestRewriteSubExpressionForceFormat", func(t *testing.T) {
			assertObject := assert.New(t)
			out := RewriteSubExpression(table.in, true)
			assertObject.Equal(table.out, out, table.in)
		})
	}
}
