package exprparser

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/rhysd/actionlint"
)

// Given a workflow string which may contain an expression, apply an expression `Mutation` to the string and return a
// new one.  Mutations have the capability to replace parts of the abstract-syntax-tree (AST) for the expression with
// new contents, allowing for sophisticated changes to the expression.
//
// As the expressions within the string are parsed into an AST, mutated, and then re-rendered from the new AST to text,
// arbitrary formatting changes may occur, particularly with whitespace.  For example, a function call like
// `format('id={0}',123)` will be returned with a space between function parameters, even if a mutator didn't change
// this function.
func Mutate(input string, mutations ...Mutation) (string, error) {
	// Skip raw strings with no expression evaluation.
	if !strings.Contains(input, "${{") || !strings.Contains(input, "}}") {
		return input, nil
	}

	// Parse expression into an AST.
	expr := RewriteSubExpression(input, false)
	expr = strings.TrimPrefix(expr, "${{")
	parser := actionlint.NewExprParser()
	exprNode, exprErr := parser.Parse(actionlint.NewExprLexer(expr + "}}"))
	if exprErr != nil {
		return "", fmt.Errorf("failed to parse: %w", exprErr)
	}

	// Mutate the AST.
	mutator := &mutator{mutations}
	newNode, err := mutator.mutate(exprNode)
	if err != nil {
		return "", fmt.Errorf("failed to mutate: %w", err)
	}

	// Render the new expression.
	var builder strings.Builder
	builder.WriteString("${{ ")
	err = render(&builder, newNode)
	if err != nil {
		return "", fmt.Errorf("failed to render: %w", err)
	}
	builder.WriteString(" }}")

	return builder.String(), nil
}

// Mutations are provided to `Mutate` to change an expression.
type Mutation interface {
	// Given a single AST node, return either `nil` to allow the original node to be used, or a new `ExprNode` to
	// replace it in the AST.
	SingleNodeMutation(exprNode actionlint.ExprNode) (actionlint.ExprNode, error)
}

type mutator struct {
	mutations []Mutation
}

// Recursively rewrite the AST expression tree, applying the mutator's `mutations` as it is processed.
func (m *mutator) mutate(exprNode actionlint.ExprNode) (actionlint.ExprNode, error) {
	newNode, err := m.noopRewrite(exprNode)
	for _, mutation := range m.mutations {
		replacement, err := mutation.SingleNodeMutation(exprNode)
		if err != nil {
			return nil, err
		} else if replacement != nil {
			newNode = replacement
		}
	}
	return newNode, err
}

// Create a copy of `exprNode` with the same data as the original node, applying mutations to all child nodes (function
// arguments, etc.).
func (m *mutator) noopRewrite(exprNode actionlint.ExprNode) (actionlint.ExprNode, error) {
	switch node := exprNode.(type) {
	case *actionlint.VariableNode:
		return &actionlint.VariableNode{Name: node.Name}, nil
	case *actionlint.BoolNode:
		return &actionlint.BoolNode{Value: node.Value}, nil
	case *actionlint.NullNode:
		return &actionlint.NullNode{}, nil
	case *actionlint.IntNode:
		return &actionlint.IntNode{Value: node.Value}, nil
	case *actionlint.FloatNode:
		return &actionlint.FloatNode{Value: node.Value}, nil
	case *actionlint.StringNode:
		return &actionlint.StringNode{Value: node.Value}, nil
	case *actionlint.IndexAccessNode:
		newOperand, err := m.mutate(node.Operand)
		if err != nil {
			return nil, fmt.Errorf("failure mutating IndexAccessNode operand: %w", err)
		}
		return &actionlint.IndexAccessNode{
			Operand: newOperand,
			Index:   node.Index,
		}, nil
	case *actionlint.ObjectDerefNode:
		newReceiver, err := m.mutate(node.Receiver)
		if err != nil {
			return nil, fmt.Errorf("failure mutating ObjectDerefNode receiver: %w", err)
		}
		return &actionlint.ObjectDerefNode{
			Receiver: newReceiver,
			Property: node.Property,
		}, nil
	case *actionlint.ArrayDerefNode:
		newReceiver, err := m.mutate(node.Receiver)
		if err != nil {
			return nil, fmt.Errorf("failure mutating ArrayDerefNode receiver: %w", err)
		}
		return &actionlint.ArrayDerefNode{
			Receiver: newReceiver,
		}, nil
	case *actionlint.NotOpNode:
		newOperand, err := m.mutate(node.Operand)
		if err != nil {
			return nil, fmt.Errorf("failure mutating NotOpNode operand: %w", err)
		}
		return &actionlint.NotOpNode{
			Operand: newOperand,
		}, nil
	case *actionlint.CompareOpNode:
		newLeft, err := m.mutate(node.Left)
		if err != nil {
			return nil, fmt.Errorf("failure mutating CompareOpNode left: %w", err)
		}
		newRight, err := m.mutate(node.Right)
		if err != nil {
			return nil, fmt.Errorf("failure mutating CompareOpNode right: %w", err)
		}
		return &actionlint.CompareOpNode{
			Left:  newLeft,
			Right: newRight,
			Kind:  node.Kind,
		}, nil
	case *actionlint.LogicalOpNode:
		newLeft, err := m.mutate(node.Left)
		if err != nil {
			return nil, fmt.Errorf("failure mutating CompareOpNode left: %w", err)
		}
		newRight, err := m.mutate(node.Right)
		if err != nil {
			return nil, fmt.Errorf("failure mutating CompareOpNode right: %w", err)
		}
		return &actionlint.LogicalOpNode{
			Left:  newLeft,
			Right: newRight,
			Kind:  node.Kind,
		}, nil
	case *actionlint.FuncCallNode:
		newArgs := make([]actionlint.ExprNode, len(node.Args))
		for i, arg := range node.Args {
			newArg, err := m.mutate(arg)
			if err != nil {
				return nil, fmt.Errorf("failure mutating FuncCallNode arg %d: %w", i, err)
			}
			newArgs[i] = newArg
		}
		return &actionlint.FuncCallNode{
			Callee: node.Callee,
			Args:   newArgs,
		}, nil
	default:
		return nil, fmt.Errorf("unknown node type: %s node: %+v", reflect.TypeOf(exprNode), exprNode)
	}
}

// Converts an AST back to a text expression.
func render(builder *strings.Builder, exprNode actionlint.ExprNode) error {
	switch node := exprNode.(type) {
	case *actionlint.VariableNode:
		builder.WriteString(node.Name)
	case *actionlint.BoolNode:
		if node.Value {
			builder.WriteString("true")
		} else {
			builder.WriteString("false")
		}
	case *actionlint.NullNode:
		builder.WriteString("null")
	case *actionlint.IntNode:
		fmt.Fprintf(builder, "%d", node.Value)
	case *actionlint.FloatNode:
		fmt.Fprintf(builder, "%#v", node.Value)
	case *actionlint.StringNode:
		builder.WriteString("'")
		builder.WriteString(strings.ReplaceAll(node.Value, "'", "''"))
		builder.WriteString("'")
	case *actionlint.IndexAccessNode:
		if err := render(builder, node.Operand); err != nil {
			return nil
		}
		builder.WriteString("[")
		if err := render(builder, node.Index); err != nil {
			return nil
		}
		builder.WriteString("]")
	case *actionlint.ObjectDerefNode:
		if err := render(builder, node.Receiver); err != nil {
			return nil
		}
		builder.WriteString(".")
		builder.WriteString(node.Property)
	case *actionlint.ArrayDerefNode:
		if err := render(builder, node.Receiver); err != nil {
			return nil
		}
		builder.WriteString(".*")
	case *actionlint.NotOpNode:
		builder.WriteString("!")
		if err := render(builder, node.Operand); err != nil {
			return nil
		}
	case *actionlint.CompareOpNode:
		if err := render(builder, node.Left); err != nil {
			return nil
		}
		builder.WriteString(" ")
		builder.WriteString(node.Kind.String())
		builder.WriteString(" ")
		if err := render(builder, node.Right); err != nil {
			return nil
		}
	case *actionlint.LogicalOpNode:
		if err := render(builder, node.Left); err != nil {
			return nil
		}
		builder.WriteString(" ")
		builder.WriteString(node.Kind.String())
		builder.WriteString(" ")
		if err := render(builder, node.Right); err != nil {
			return nil
		}
	case *actionlint.FuncCallNode:
		builder.WriteString(node.Callee)
		builder.WriteString("(")
		for i, arg := range node.Args {
			if err := render(builder, arg); err != nil {
				return nil
			}
			if i != len(node.Args)-1 {
				builder.WriteString(", ")
			}
		}
		builder.WriteString(")")
	default:
		return fmt.Errorf("unknown node type: %s node: %+v", reflect.TypeOf(exprNode), exprNode)
	}
	return nil
}

// VariableAccessMutator is used to rewrite access to top-level variables (contexts, in the runner terminology). When a
// variable matching the name `Variable` is accessed, invoke the Rewriter function to get a replacement `ExprNode`. Both
// access to a variable with an object dereference (eg. `var.foo`) and an index-style access (`var.['foo']`) is
// supported for mutation.  Note that because index-style access is supported, the mutator does not know the specific
// value of the property name being accessed -- it could be computed or derived from another property
// (`var[other-var.something]`).
type VariableAccessMutator struct {
	// Variable to trigger the mutator upon.  If set to `needs`, for example, then `needs.hello` will trigger
	// `Rewriter`.
	Variable string
	// Rewrite routine to use.  `property` will be an AST node representing the property accessed.  Typically you would
	// not want to inspect inside the contents of the `property` as it could be any type of AST from a simple string
	// literal to a function call.
	Rewriter func(property actionlint.ExprNode) actionlint.ExprNode
}

func (vam *VariableAccessMutator) SingleNodeMutation(exprNode actionlint.ExprNode) (actionlint.ExprNode, error) {
	switch node := exprNode.(type) {
	case *actionlint.ObjectDerefNode:
		if varAccess, ok := node.Receiver.(*actionlint.VariableNode); ok && varAccess.Name == vam.Variable {
			return vam.Rewriter(&actionlint.StringNode{Value: node.Property}), nil
		}
	case *actionlint.IndexAccessNode:
		if varAccess, ok := node.Operand.(*actionlint.VariableNode); ok && varAccess.Name == vam.Variable {
			return vam.Rewriter(node.Index), nil
		}
	}
	return nil, nil
}
