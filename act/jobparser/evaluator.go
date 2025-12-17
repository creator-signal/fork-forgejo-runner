package jobparser

import (
	"fmt"
	"regexp"
	"strings"

	"code.forgejo.org/forgejo/runner/v12/act/exprparser"
	"go.yaml.in/yaml/v3"
)

// ExpressionEvaluator is copied from runner.expressionEvaluator,
// to avoid unnecessary dependencies
type ExpressionEvaluator struct {
	interpreter exprparser.Interpreter
}

func newExpressionEvaluator(interpreter exprparser.Interpreter) *ExpressionEvaluator {
	return &ExpressionEvaluator{interpreter: interpreter}
}

func (ee ExpressionEvaluator) evaluate(in string, defaultStatusCheck exprparser.DefaultStatusCheck) (any, error) {
	evaluated, err := ee.interpreter.Evaluate(in, defaultStatusCheck)

	return evaluated, err
}

func (ee ExpressionEvaluator) evaluateScalarYamlNode(node *yaml.Node) error {
	var in string
	if err := node.Decode(&in); err != nil {
		return err
	}
	if !strings.Contains(in, "${{") || !strings.Contains(in, "}}") {
		return nil
	}
	expr := exprparser.RewriteSubExpression(in, false)
	res, err := ee.evaluate(expr, exprparser.DefaultStatusCheckNone)
	if err != nil {
		return err
	}
	return node.Encode(res)
}

func (ee ExpressionEvaluator) evaluateMappingYamlNode(node *yaml.Node) error {
	// GitHub has this undocumented feature to merge maps, called insert directive
	insertDirective := regexp.MustCompile(`\${{\s*insert\s*}}`)
	for i := 0; i < len(node.Content)/2; {
		k := node.Content[i*2]
		v := node.Content[i*2+1]
		if err := ee.EvaluateYamlNode(v); err != nil {
			return err
		}
		var sk string
		// Merge the nested map of the insert directive
		if k.Decode(&sk) == nil && insertDirective.MatchString(sk) {
			node.Content = append(append(node.Content[:i*2], v.Content...), node.Content[(i+1)*2:]...)
			i += len(v.Content) / 2
		} else {
			if err := ee.EvaluateYamlNode(k); err != nil {
				return err
			}
			i++
		}
	}
	return nil
}

func (ee ExpressionEvaluator) evaluateSequenceYamlNode(node *yaml.Node) error {
	for i := 0; i < len(node.Content); {
		v := node.Content[i]
		// Preserve nested sequences
		wasseq := v.Kind == yaml.SequenceNode
		if err := ee.EvaluateYamlNode(v); err != nil {
			return err
		}
		// GitHub has this undocumented feature to merge sequences / arrays
		// We have a nested sequence via evaluation, merge the arrays
		if v.Kind == yaml.SequenceNode && !wasseq {
			node.Content = append(append(node.Content[:i], v.Content...), node.Content[i+1:]...)
			i += len(v.Content)
		} else {
			i++
		}
	}
	return nil
}

func (ee ExpressionEvaluator) EvaluateYamlNode(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		return ee.evaluateScalarYamlNode(node)
	case yaml.MappingNode:
		return ee.evaluateMappingYamlNode(node)
	case yaml.SequenceNode:
		return ee.evaluateSequenceYamlNode(node)
	default:
		return nil
	}
}

func (ee ExpressionEvaluator) Interpolate(in string) string {
	if !strings.Contains(in, "${{") || !strings.Contains(in, "}}") {
		return in
	}

	expr := exprparser.RewriteSubExpression(in, true)
	evaluated, err := ee.evaluate(expr, exprparser.DefaultStatusCheckNone)
	if err != nil {
		return ""
	}

	value, ok := evaluated.(string)
	if !ok {
		panic(fmt.Sprintf("Expression %s did not evaluate to a string", expr))
	}

	return value
}
