package exprparser

import (
	"fmt"
	"math"
	"slices"
	"testing"

	"code.forgejo.org/forgejo/runner/v12/act/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLiterals(t *testing.T) {
	table := []struct {
		input    string
		expected any
		name     string
	}{
		{"true", true, "true"},
		{"false", false, "false"},
		{"null", nil, "null"},
		{"123", 123, "integer"},
		{"-9.7", -9.7, "float"},
		{"0xff", 255, "hex"},
		{"-2.99e-2", -2.99e-2, "exponential"},
		{"'foo'", "foo", "string"},
		{"'it''s foo'", "it's foo", "string"},
	}

	env := &EvaluationEnvironment{}

	for _, tt := range table {
		t.Run(tt.name, func(t *testing.T) {
			output, err := NewInterpreter(env, Config{}).Evaluate(tt.input, DefaultStatusCheckNone)
			assert.Nil(t, err)

			assert.Equal(t, tt.expected, output)
		})
	}
}

func TestOperators(t *testing.T) {
	table := []struct {
		input    string
		expected any
		name     string
		error    string
	}{
		{"(false || (false || true))", true, "logical-grouping", ""},
		{"github.action", "push", "property-dereference", ""},
		{"github['action']", "push", "property-index", ""},
		{"github.action[0]", nil, "string-index", ""},
		{"github.action['0']", nil, "string-index", ""},
		{"fromJSON('[0,1]')[1]", 1.0, "array-index", ""},
		{"fromJSON('[0,1]')[1.1]", nil, "array-index", ""},
		// Disabled weird things are happening
		// {"fromJSON('[0,1]')['1.1']", nil, "array-index", ""},
		{"(github.event.commits.*.author.username)[0]", "someone", "array-index-0", ""},
		{"fromJSON('[0,1]')[2]", nil, "array-index-out-of-bounds-0", ""},
		{"fromJSON('[0,1]')[34553]", nil, "array-index-out-of-bounds-1", ""},
		{"fromJSON('[0,1]')[-1]", nil, "array-index-out-of-bounds-2", ""},
		{"fromJSON('[0,1]')[-34553]", nil, "array-index-out-of-bounds-3", ""},
		{"!true", false, "not", ""},
		{"1 < 2", true, "less-than", ""},
		{`'b' <= 'a'`, false, "less-than-or-equal", ""},
		{"1 > 2", false, "greater-than", ""},
		{`'b' >= 'a'`, true, "greater-than-or-equal", ""},
		{`'a' == 'a'`, true, "equal", ""},
		{`'a' != 'a'`, false, "not-equal", ""},
		{`true && false`, false, "and", ""},
		{`true || false`, true, "or", ""},
		{`fromJSON('{}') && true`, true, "and-boolean-object", ""},
		{`fromJSON('{}') || false`, make(map[string]any), "or-boolean-object", ""},
		{"github.event.commits[0].author.username != github.event.commits[1].author.username", true, "property-comparison1", ""},
		{"github.event.commits[0].author.username1 != github.event.commits[1].author.username", true, "property-comparison2", ""},
		{"github.event.commits[0].author.username != github.event.commits[1].author.username1", true, "property-comparison3", ""},
		{"github.event.commits[0].author.username1 != github.event.commits[1].author.username2", true, "property-comparison4", ""},
		{"secrets != env", nil, "property-comparison5", "Compare not implemented for types: left: ptr, right: ptr"},
		{"case(true,'correct',false,'incorrect','nochance')", "correct", "case-bool1", ""},
		{"case(false,'incorrect',true,'correct','nochance')", "correct", "case-bool2", ""},
		{"case(false,'incorrect','correct')", "correct", "case-bool3", ""},
		{"case(true,'correct','incorrect')", "correct", "case-bool4", ""},
		{"case(1 == 1,'correct',1 == 2,'incorrect','nochance')", "correct", "case-expression1", ""},
		{"case(1 == 2,'incorrect',1 == 1,'correct','nochance')", "correct", "case-expression2", ""},
		{"case(1 == 2,'incorrect',2 == 1,'nochance','correct')", "correct", "case-expression3", ""},
		{"case()", nil, "case-empty", "'case' expected at least 3 arguments but got 0 instead"},
		{"case('incorrect')", nil, "case-missing-arguments1", "'case' expected at least 3 arguments but got 1 instead"},
		{"case(true,'incorrect')", nil, "case-missing-arguments2", "'case' expected at least 3 arguments but got 2 instead"},
		{"case(1 == 2,'incorrect',2 == 1,'nochance')", nil, "case-missing-arguments3", "'case' expected odd number of arguments"},
		{"case('string','incorrect',2 == 1,'nochance','false')", nil, "case-incorrect-argument1", "'case' expected predicate 'string' to be boolean"},
		{"case(1,'incorrect',2 == 1,'nochance','false')", nil, "case-incorrect-argument2", "'case' expected predicate '1' to be boolean"},
		{"case(1,'incorrect',2 == 1,'nochance','false')", nil, "case-incorrect-argument2", "'case' expected predicate '1' to be boolean"},
		{"case(1.1,'incorrect',2 == 1,'nochance','false')", nil, "case-incorrect-argument2", "'case' expected predicate '1.1' to be boolean"},
		{"case(true,'correct','string','nochance','false')", "correct", "case-incorrect-argument3", ""}, // predicates are evaluated lazily
		{"case(true,true,false,'nochance','incorrect')", "true", "case-bool-return", ""},
		{"case(true,1,'incorrect')", "1", "case-integer-return", ""},
		{"case(true,1,'incorrect')", "1", "case-float-return1", ""},
		{"case(true,1.1,'incorrect')", "1.1", "case-float-return2", ""},
	}

	env := &EvaluationEnvironment{
		Github: &model.GithubContext{
			Action: "push",
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
	}

	for _, tt := range table {
		t.Run(tt.name, func(t *testing.T) {
			output, err := NewInterpreter(env, Config{}).Evaluate(tt.input, DefaultStatusCheckNone)
			if tt.error != "" {
				require.Error(t, err)
				assert.Equal(t, tt.error, err.Error())
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.expected, output)
		})
	}
}

func TestOperatorsCompare(t *testing.T) {
	table := []struct {
		input    string
		expected any
		name     string
	}{
		{"!null", true, "not-null"},
		{"!-10", false, "not-neg-num"},
		{"!0", true, "not-zero"},
		{"!3.14", false, "not-pos-float"},
		{"!''", true, "not-empty-str"},
		{"!'abc'", false, "not-str"},
		{"!fromJSON('{}')", false, "not-obj"},
		{"!fromJSON('[]')", false, "not-arr"},
		{`null == 0 }}`, true, "null-coercion"},
		{`true == 1 }}`, true, "boolean-coercion"},
		{`'' == 0 }}`, true, "string-0-coercion"},
		{`'3' == 3 }}`, true, "string-3-coercion"},
		{`0 == null }}`, true, "null-coercion-alt"},
		{`1 == true }}`, true, "boolean-coercion-alt"},
		{`0 == '' }}`, true, "string-0-coercion-alt"},
		{`3 == '3' }}`, true, "string-3-coercion-alt"},
		{`'TEST' == 'test' }}`, true, "string-casing"},
		{"true > false }}", true, "bool-greater-than"},
		{"true >= false }}", true, "bool-greater-than-eq"},
		{"true >= true }}", true, "bool-greater-than-1"},
		{"true != false }}", true, "bool-not-equal"},
		{`fromJSON('{}') < 2 }}`, false, "object-with-less"},
		{`fromJSON('{}') < fromJSON('[]') }}`, false, "object/arr-with-lt"},
		{`fromJSON('{}') > fromJSON('[]') }}`, false, "object/arr-with-gt"},
	}

	env := &EvaluationEnvironment{
		Github: &model.GithubContext{
			Action: "push",
		},
	}

	for _, tt := range table {
		t.Run(tt.name, func(t *testing.T) {
			output, err := NewInterpreter(env, Config{}).Evaluate(tt.input, DefaultStatusCheckNone)
			assert.Nil(t, err)

			assert.Equal(t, tt.expected, output)
		})
	}
}

func TestOperatorsBooleanEvaluation(t *testing.T) {
	table := []struct {
		input    string
		expected any
		name     string
	}{
		// true &&
		{"true && true", true, "true-and"},
		{"true && false", false, "true-and"},
		{"true && null", nil, "true-and"},
		{"true && -10", -10, "true-and"},
		{"true && 0", 0, "true-and"},
		{"true && 10", 10, "true-and"},
		{"true && 3.14", 3.14, "true-and"},
		{"true && 0.0", 0, "true-and"},
		{"true && Infinity", math.Inf(1), "true-and"},
		// {"true && -Infinity", math.Inf(-1), "true-and"},
		{"true && NaN", math.NaN(), "true-and"},
		{"true && ''", "", "true-and"},
		{"true && 'abc'", "abc", "true-and"},
		// false &&
		{"false && true", false, "false-and"},
		{"false && false", false, "false-and"},
		{"false && null", false, "false-and"},
		{"false && -10", false, "false-and"},
		{"false && 0", false, "false-and"},
		{"false && 10", false, "false-and"},
		{"false && 3.14", false, "false-and"},
		{"false && 0.0", false, "false-and"},
		{"false && Infinity", false, "false-and"},
		// {"false && -Infinity", false, "false-and"},
		{"false && NaN", false, "false-and"},
		{"false && ''", false, "false-and"},
		{"false && 'abc'", false, "false-and"},
		// true ||
		{"true || true", true, "true-or"},
		{"true || false", true, "true-or"},
		{"true || null", true, "true-or"},
		{"true || -10", true, "true-or"},
		{"true || 0", true, "true-or"},
		{"true || 10", true, "true-or"},
		{"true || 3.14", true, "true-or"},
		{"true || 0.0", true, "true-or"},
		{"true || Infinity", true, "true-or"},
		// {"true || -Infinity", true, "true-or"},
		{"true || NaN", true, "true-or"},
		{"true || ''", true, "true-or"},
		{"true || 'abc'", true, "true-or"},
		// false ||
		{"false || true", true, "false-or"},
		{"false || false", false, "false-or"},
		{"false || null", nil, "false-or"},
		{"false || -10", -10, "false-or"},
		{"false || 0", 0, "false-or"},
		{"false || 10", 10, "false-or"},
		{"false || 3.14", 3.14, "false-or"},
		{"false || 0.0", 0, "false-or"},
		{"false || Infinity", math.Inf(1), "false-or"},
		// {"false || -Infinity", math.Inf(-1), "false-or"},
		{"false || NaN", math.NaN(), "false-or"},
		{"false || ''", "", "false-or"},
		{"false || 'abc'", "abc", "false-or"},
		// null &&
		{"null && true", nil, "null-and"},
		{"null && false", nil, "null-and"},
		{"null && null", nil, "null-and"},
		{"null && -10", nil, "null-and"},
		{"null && 0", nil, "null-and"},
		{"null && 10", nil, "null-and"},
		{"null && 3.14", nil, "null-and"},
		{"null && 0.0", nil, "null-and"},
		{"null && Infinity", nil, "null-and"},
		// {"null && -Infinity", nil, "null-and"},
		{"null && NaN", nil, "null-and"},
		{"null && ''", nil, "null-and"},
		{"null && 'abc'", nil, "null-and"},
		// null ||
		{"null || true", true, "null-or"},
		{"null || false", false, "null-or"},
		{"null || null", nil, "null-or"},
		{"null || -10", -10, "null-or"},
		{"null || 0", 0, "null-or"},
		{"null || 10", 10, "null-or"},
		{"null || 3.14", 3.14, "null-or"},
		{"null || 0.0", 0, "null-or"},
		{"null || Infinity", math.Inf(1), "null-or"},
		// {"null || -Infinity", math.Inf(-1), "null-or"},
		{"null || NaN", math.NaN(), "null-or"},
		{"null || ''", "", "null-or"},
		{"null || 'abc'", "abc", "null-or"},
		// -10 &&
		{"-10 && true", true, "neg-num-and"},
		{"-10 && false", false, "neg-num-and"},
		{"-10 && null", nil, "neg-num-and"},
		{"-10 && -10", -10, "neg-num-and"},
		{"-10 && 0", 0, "neg-num-and"},
		{"-10 && 10", 10, "neg-num-and"},
		{"-10 && 3.14", 3.14, "neg-num-and"},
		{"-10 && 0.0", 0, "neg-num-and"},
		{"-10 && Infinity", math.Inf(1), "neg-num-and"},
		// {"-10 && -Infinity", math.Inf(-1), "neg-num-and"},
		{"-10 && NaN", math.NaN(), "neg-num-and"},
		{"-10 && ''", "", "neg-num-and"},
		{"-10 && 'abc'", "abc", "neg-num-and"},
		// -10 ||
		{"-10 || true", -10, "neg-num-or"},
		{"-10 || false", -10, "neg-num-or"},
		{"-10 || null", -10, "neg-num-or"},
		{"-10 || -10", -10, "neg-num-or"},
		{"-10 || 0", -10, "neg-num-or"},
		{"-10 || 10", -10, "neg-num-or"},
		{"-10 || 3.14", -10, "neg-num-or"},
		{"-10 || 0.0", -10, "neg-num-or"},
		{"-10 || Infinity", -10, "neg-num-or"},
		// {"-10 || -Infinity", -10, "neg-num-or"},
		{"-10 || NaN", -10, "neg-num-or"},
		{"-10 || ''", -10, "neg-num-or"},
		{"-10 || 'abc'", -10, "neg-num-or"},
		// 0 &&
		{"0 && true", 0, "zero-and"},
		{"0 && false", 0, "zero-and"},
		{"0 && null", 0, "zero-and"},
		{"0 && -10", 0, "zero-and"},
		{"0 && 0", 0, "zero-and"},
		{"0 && 10", 0, "zero-and"},
		{"0 && 3.14", 0, "zero-and"},
		{"0 && 0.0", 0, "zero-and"},
		{"0 && Infinity", 0, "zero-and"},
		// {"0 && -Infinity", 0, "zero-and"},
		{"0 && NaN", 0, "zero-and"},
		{"0 && ''", 0, "zero-and"},
		{"0 && 'abc'", 0, "zero-and"},
		// 0 ||
		{"0 || true", true, "zero-or"},
		{"0 || false", false, "zero-or"},
		{"0 || null", nil, "zero-or"},
		{"0 || -10", -10, "zero-or"},
		{"0 || 0", 0, "zero-or"},
		{"0 || 10", 10, "zero-or"},
		{"0 || 3.14", 3.14, "zero-or"},
		{"0 || 0.0", 0, "zero-or"},
		{"0 || Infinity", math.Inf(1), "zero-or"},
		// {"0 || -Infinity", math.Inf(-1), "zero-or"},
		{"0 || NaN", math.NaN(), "zero-or"},
		{"0 || ''", "", "zero-or"},
		{"0 || 'abc'", "abc", "zero-or"},
		// 10 &&
		{"10 && true", true, "pos-num-and"},
		{"10 && false", false, "pos-num-and"},
		{"10 && null", nil, "pos-num-and"},
		{"10 && -10", -10, "pos-num-and"},
		{"10 && 0", 0, "pos-num-and"},
		{"10 && 10", 10, "pos-num-and"},
		{"10 && 3.14", 3.14, "pos-num-and"},
		{"10 && 0.0", 0, "pos-num-and"},
		{"10 && Infinity", math.Inf(1), "pos-num-and"},
		// {"10 && -Infinity", math.Inf(-1), "pos-num-and"},
		{"10 && NaN", math.NaN(), "pos-num-and"},
		{"10 && ''", "", "pos-num-and"},
		{"10 && 'abc'", "abc", "pos-num-and"},
		// 10 ||
		{"10 || true", 10, "pos-num-or"},
		{"10 || false", 10, "pos-num-or"},
		{"10 || null", 10, "pos-num-or"},
		{"10 || -10", 10, "pos-num-or"},
		{"10 || 0", 10, "pos-num-or"},
		{"10 || 10", 10, "pos-num-or"},
		{"10 || 3.14", 10, "pos-num-or"},
		{"10 || 0.0", 10, "pos-num-or"},
		{"10 || Infinity", 10, "pos-num-or"},
		// {"10 || -Infinity", 10, "pos-num-or"},
		{"10 || NaN", 10, "pos-num-or"},
		{"10 || ''", 10, "pos-num-or"},
		{"10 || 'abc'", 10, "pos-num-or"},
		// 3.14 &&
		{"3.14 && true", true, "pos-float-and"},
		{"3.14 && false", false, "pos-float-and"},
		{"3.14 && null", nil, "pos-float-and"},
		{"3.14 && -10", -10, "pos-float-and"},
		{"3.14 && 0", 0, "pos-float-and"},
		{"3.14 && 10", 10, "pos-float-and"},
		{"3.14 && 3.14", 3.14, "pos-float-and"},
		{"3.14 && 0.0", 0, "pos-float-and"},
		{"3.14 && Infinity", math.Inf(1), "pos-float-and"},
		// {"3.14 && -Infinity", math.Inf(-1), "pos-float-and"},
		{"3.14 && NaN", math.NaN(), "pos-float-and"},
		{"3.14 && ''", "", "pos-float-and"},
		{"3.14 && 'abc'", "abc", "pos-float-and"},
		// 3.14 ||
		{"3.14 || true", 3.14, "pos-float-or"},
		{"3.14 || false", 3.14, "pos-float-or"},
		{"3.14 || null", 3.14, "pos-float-or"},
		{"3.14 || -10", 3.14, "pos-float-or"},
		{"3.14 || 0", 3.14, "pos-float-or"},
		{"3.14 || 10", 3.14, "pos-float-or"},
		{"3.14 || 3.14", 3.14, "pos-float-or"},
		{"3.14 || 0.0", 3.14, "pos-float-or"},
		{"3.14 || Infinity", 3.14, "pos-float-or"},
		// {"3.14 || -Infinity", 3.14, "pos-float-or"},
		{"3.14 || NaN", 3.14, "pos-float-or"},
		{"3.14 || ''", 3.14, "pos-float-or"},
		{"3.14 || 'abc'", 3.14, "pos-float-or"},
		// Infinity &&
		{"Infinity && true", true, "pos-inf-and"},
		{"Infinity && false", false, "pos-inf-and"},
		{"Infinity && null", nil, "pos-inf-and"},
		{"Infinity && -10", -10, "pos-inf-and"},
		{"Infinity && 0", 0, "pos-inf-and"},
		{"Infinity && 10", 10, "pos-inf-and"},
		{"Infinity && 3.14", 3.14, "pos-inf-and"},
		{"Infinity && 0.0", 0, "pos-inf-and"},
		{"Infinity && Infinity", math.Inf(1), "pos-inf-and"},
		// {"Infinity && -Infinity", math.Inf(-1), "pos-inf-and"},
		{"Infinity && NaN", math.NaN(), "pos-inf-and"},
		{"Infinity && ''", "", "pos-inf-and"},
		{"Infinity && 'abc'", "abc", "pos-inf-and"},
		// Infinity ||
		{"Infinity || true", math.Inf(1), "pos-inf-or"},
		{"Infinity || false", math.Inf(1), "pos-inf-or"},
		{"Infinity || null", math.Inf(1), "pos-inf-or"},
		{"Infinity || -10", math.Inf(1), "pos-inf-or"},
		{"Infinity || 0", math.Inf(1), "pos-inf-or"},
		{"Infinity || 10", math.Inf(1), "pos-inf-or"},
		{"Infinity || 3.14", math.Inf(1), "pos-inf-or"},
		{"Infinity || 0.0", math.Inf(1), "pos-inf-or"},
		{"Infinity || Infinity", math.Inf(1), "pos-inf-or"},
		// {"Infinity || -Infinity", math.Inf(1), "pos-inf-or"},
		{"Infinity || NaN", math.Inf(1), "pos-inf-or"},
		{"Infinity || ''", math.Inf(1), "pos-inf-or"},
		{"Infinity || 'abc'", math.Inf(1), "pos-inf-or"},
		// -Infinity &&
		// {"-Infinity && true", true, "neg-inf-and"},
		// {"-Infinity && false", false, "neg-inf-and"},
		// {"-Infinity && null", nil, "neg-inf-and"},
		// {"-Infinity && -10", -10, "neg-inf-and"},
		// {"-Infinity && 0", 0, "neg-inf-and"},
		// {"-Infinity && 10", 10, "neg-inf-and"},
		// {"-Infinity && 3.14", 3.14, "neg-inf-and"},
		// {"-Infinity && 0.0", 0, "neg-inf-and"},
		// {"-Infinity && Infinity", math.Inf(1), "neg-inf-and"},
		// {"-Infinity && -Infinity", math.Inf(-1), "neg-inf-and"},
		// {"-Infinity && NaN", math.NaN(), "neg-inf-and"},
		// {"-Infinity && ''", "", "neg-inf-and"},
		// {"-Infinity && 'abc'", "abc", "neg-inf-and"},
		// -Infinity ||
		// {"-Infinity || true", math.Inf(-1), "neg-inf-or"},
		// {"-Infinity || false", math.Inf(-1), "neg-inf-or"},
		// {"-Infinity || null", math.Inf(-1), "neg-inf-or"},
		// {"-Infinity || -10", math.Inf(-1), "neg-inf-or"},
		// {"-Infinity || 0", math.Inf(-1), "neg-inf-or"},
		// {"-Infinity || 10", math.Inf(-1), "neg-inf-or"},
		// {"-Infinity || 3.14", math.Inf(-1), "neg-inf-or"},
		// {"-Infinity || 0.0", math.Inf(-1), "neg-inf-or"},
		// {"-Infinity || Infinity", math.Inf(-1), "neg-inf-or"},
		// {"-Infinity || -Infinity", math.Inf(-1), "neg-inf-or"},
		// {"-Infinity || NaN", math.Inf(-1), "neg-inf-or"},
		// {"-Infinity || ''", math.Inf(-1), "neg-inf-or"},
		// {"-Infinity || 'abc'", math.Inf(-1), "neg-inf-or"},
		// NaN &&
		{"NaN && true", math.NaN(), "nan-and"},
		{"NaN && false", math.NaN(), "nan-and"},
		{"NaN && null", math.NaN(), "nan-and"},
		{"NaN && -10", math.NaN(), "nan-and"},
		{"NaN && 0", math.NaN(), "nan-and"},
		{"NaN && 10", math.NaN(), "nan-and"},
		{"NaN && 3.14", math.NaN(), "nan-and"},
		{"NaN && 0.0", math.NaN(), "nan-and"},
		{"NaN && Infinity", math.NaN(), "nan-and"},
		// {"NaN && -Infinity", math.NaN(), "nan-and"},
		{"NaN && NaN", math.NaN(), "nan-and"},
		{"NaN && ''", math.NaN(), "nan-and"},
		{"NaN && 'abc'", math.NaN(), "nan-and"},
		// NaN ||
		{"NaN || true", true, "nan-or"},
		{"NaN || false", false, "nan-or"},
		{"NaN || null", nil, "nan-or"},
		{"NaN || -10", -10, "nan-or"},
		{"NaN || 0", 0, "nan-or"},
		{"NaN || 10", 10, "nan-or"},
		{"NaN || 3.14", 3.14, "nan-or"},
		{"NaN || 0.0", 0, "nan-or"},
		{"NaN || Infinity", math.Inf(1), "nan-or"},
		// {"NaN || -Infinity", math.Inf(-1), "nan-or"},
		{"NaN || NaN", math.NaN(), "nan-or"},
		{"NaN || ''", "", "nan-or"},
		{"NaN || 'abc'", "abc", "nan-or"},
		// "" &&
		{"'' && true", "", "empty-str-and"},
		{"'' && false", "", "empty-str-and"},
		{"'' && null", "", "empty-str-and"},
		{"'' && -10", "", "empty-str-and"},
		{"'' && 0", "", "empty-str-and"},
		{"'' && 10", "", "empty-str-and"},
		{"'' && 3.14", "", "empty-str-and"},
		{"'' && 0.0", "", "empty-str-and"},
		{"'' && Infinity", "", "empty-str-and"},
		// {"'' && -Infinity", "", "empty-str-and"},
		{"'' && NaN", "", "empty-str-and"},
		{"'' && ''", "", "empty-str-and"},
		{"'' && 'abc'", "", "empty-str-and"},
		// "" ||
		{"'' || true", true, "empty-str-or"},
		{"'' || false", false, "empty-str-or"},
		{"'' || null", nil, "empty-str-or"},
		{"'' || -10", -10, "empty-str-or"},
		{"'' || 0", 0, "empty-str-or"},
		{"'' || 10", 10, "empty-str-or"},
		{"'' || 3.14", 3.14, "empty-str-or"},
		{"'' || 0.0", 0, "empty-str-or"},
		{"'' || Infinity", math.Inf(1), "empty-str-or"},
		// {"'' || -Infinity", math.Inf(-1), "empty-str-or"},
		{"'' || NaN", math.NaN(), "empty-str-or"},
		{"'' || ''", "", "empty-str-or"},
		{"'' || 'abc'", "abc", "empty-str-or"},
		// "abc" &&
		{"'abc' && true", true, "str-and"},
		{"'abc' && false", false, "str-and"},
		{"'abc' && null", nil, "str-and"},
		{"'abc' && -10", -10, "str-and"},
		{"'abc' && 0", 0, "str-and"},
		{"'abc' && 10", 10, "str-and"},
		{"'abc' && 3.14", 3.14, "str-and"},
		{"'abc' && 0.0", 0, "str-and"},
		{"'abc' && Infinity", math.Inf(1), "str-and"},
		// {"'abc' && -Infinity", math.Inf(-1), "str-and"},
		{"'abc' && NaN", math.NaN(), "str-and"},
		{"'abc' && ''", "", "str-and"},
		{"'abc' && 'abc'", "abc", "str-and"},
		// "abc" ||
		{"'abc' || true", "abc", "str-or"},
		{"'abc' || false", "abc", "str-or"},
		{"'abc' || null", "abc", "str-or"},
		{"'abc' || -10", "abc", "str-or"},
		{"'abc' || 0", "abc", "str-or"},
		{"'abc' || 10", "abc", "str-or"},
		{"'abc' || 3.14", "abc", "str-or"},
		{"'abc' || 0.0", "abc", "str-or"},
		{"'abc' || Infinity", "abc", "str-or"},
		// {"'abc' || -Infinity", "abc", "str-or"},
		{"'abc' || NaN", "abc", "str-or"},
		{"'abc' || ''", "abc", "str-or"},
		{"'abc' || 'abc'", "abc", "str-or"},
		// extra tests
		{"0.0 && true", 0, "float-evaluation-0-alt"},
		{"-1.5 && true", true, "float-evaluation-neg-alt"},
	}

	env := &EvaluationEnvironment{
		Github: &model.GithubContext{
			Action: "push",
		},
	}

	for _, tt := range table {
		t.Run(tt.name, func(t *testing.T) {
			output, err := NewInterpreter(env, Config{}).Evaluate(tt.input, DefaultStatusCheckNone)
			assert.Nil(t, err)

			if expected, ok := tt.expected.(float64); ok && math.IsNaN(expected) {
				assert.True(t, math.IsNaN(output.(float64)))
			} else {
				assert.Equal(t, tt.expected, output)
			}
		})
	}
}

func TestContexts(t *testing.T) {
	table := []struct {
		input    string
		expected any
		name     string
	}{
		{"github.action", "push", "github-context"},
		{"github.event.commits[0].message", nil, "github-context-noexist-prop"},
		{"fromjson('{\"commits\":[]}').commits[0].message", nil, "github-context-noexist-prop"},
		{"github.event.pull_request.labels.*.name", nil, "github-context-noexist-prop"},
		{"env.TEST", "value", "env-context"},
		{"job.status", "success", "job-context"},
		{"steps.step-id.outputs.name", "value", "steps-context"},
		{"steps.step-id.conclusion", "success", "steps-context-conclusion"},
		{"steps.step-id.conclusion && true", true, "steps-context-conclusion"},
		{"steps.step-id2.conclusion", "skipped", "steps-context-conclusion"},
		{"steps.step-id2.conclusion && true", true, "steps-context-conclusion"},
		{"steps.step-id.outcome", "success", "steps-context-outcome"},
		{"steps.step-id['outcome']", "success", "steps-context-outcome"},
		{"steps.step-id.outcome == 'success'", true, "steps-context-outcome"},
		{"steps.step-id['outcome'] == 'success'", true, "steps-context-outcome"},
		{"steps.step-id.outcome && true", true, "steps-context-outcome"},
		{"steps['step-id']['outcome'] && true", true, "steps-context-outcome"},
		{"steps.step-id2.outcome", "failure", "steps-context-outcome"},
		{"steps.step-id2.outcome && true", true, "steps-context-outcome"},
		// Disabled, since the interpreter is still too broken
		// {"contains(steps.*.outcome, 'success')", true, "steps-context-array-outcome"},
		// {"contains(steps.*.outcome, 'failure')", true, "steps-context-array-outcome"},
		// {"contains(steps.*.outputs.name, 'value')", true, "steps-context-array-outputs"},
		{"runner.os", "Linux", "runner-context"},
		{"secrets.name", "value", "secrets-context"},
		{"vars.name", "value", "vars-context"},
		{"strategy.fail-fast", true, "strategy-context"},
		{"matrix.os", "Linux", "matrix-context"},
		{"needs.job-id.outputs.output-name", "value", "needs-context"},
		{"needs.job-id.result", "success", "needs-context"},
		{"contains(needs.*.result, 'success')", true, "needs-wildcard-context-contains-success"},
		{"contains(needs.*.result, 'failure')", false, "needs-wildcard-context-contains-failure"},
		{"inputs.name", "value", "inputs-context"},
	}

	env := &EvaluationEnvironment{
		Github: &model.GithubContext{
			Action: "push",
		},
		Env: map[string]string{
			"TEST": "value",
		},
		Job: &model.JobContext{
			Status: "success",
		},
		Steps: map[string]*model.StepResult{
			"step-id": {
				Outputs: map[string]string{
					"name": "value",
				},
			},
			"step-id2": {
				Outcome:    model.StepStatusFailure,
				Conclusion: model.StepStatusSkipped,
			},
		},
		Runner: map[string]any{
			"os":         "Linux",
			"temp":       "/tmp",
			"tool_cache": "/opt/hostedtoolcache",
		},
		Secrets: map[string]string{
			"name": "value",
		},
		Vars: map[string]string{
			"name": "value",
		},
		Strategy: map[string]any{
			"fail-fast": true,
		},
		Matrix: map[string]any{
			"os": "Linux",
		},
		Needs: map[string]Needs{
			"job-id": {
				Outputs: map[string]string{
					"output-name": "value",
				},
				Result: "success",
			},
			"another-job-id": {
				Outputs: map[string]string{
					"output-name": "value",
				},
				Result: "success",
			},
		},
		Inputs: map[string]any{
			"name": "value",
		},
	}

	for _, tt := range table {
		t.Run(tt.name, func(t *testing.T) {
			output, err := NewInterpreter(env, Config{}).Evaluate(tt.input, DefaultStatusCheckNone)
			assert.Nil(t, err)

			assert.Equal(t, tt.expected, output)
		})
	}
}

func TestErrorModes(t *testing.T) {
	table := []struct {
		input                 string
		expected              any
		postProcess           func(expected any) any
		name                  string
		errorMode             ErrorMode
		testWithAllErrorModes bool // test case that should have the same result in every error mode
		expectedErr           error
	}{
		{
			name:                  "needs-output-defaulterrmode",
			input:                 "needs.job-id.outputs.output-name",
			expected:              "value",
			testWithAllErrorModes: true,
		},
		{
			name:                  "needs-output-defaulterrmode-index",
			input:                 "needs['job-id']['outputs']['output-name']",
			expected:              "value",
			testWithAllErrorModes: true,
		},
		{
			name:                  "needs-output-defaulterrmode-deref-1",
			input:                 "needs.job-id.outputs.*",
			expected:              map[string]string{"output-name": "value"},
			testWithAllErrorModes: true,
		},
		{
			name:                  "needs-output-defaulterrmode-deref-2",
			input:                 "needs.job-id.*.outputs",
			expected:              map[string]string{"output-name": "value"},
			testWithAllErrorModes: true,
		},
		{
			name:     "needs-output-defaulterrmode-deref-3",
			input:    "needs.*.outputs.output-name",
			expected: []any{"another value", "value"},
			postProcess: func(expected any) any {
				// need to sort for a consistent test
				expectedSlice := expected.([]any)
				// make it into a []string so we can sort it
				val := make([]string, 0, len(expectedSlice))
				for _, s := range expectedSlice {
					val = append(val, s.(string))
				}
				slices.Sort(val)
				// make it back into a []any so we can compare with `expected`
				retval := make([]any, 0, len(val))
				for _, s := range val {
					retval = append(retval, s)
				}
				return retval
			},
			testWithAllErrorModes: true,
		},
		{
			name:                  "needs-result-defaulterrmode",
			input:                 "needs.job-id.result",
			expected:              "success",
			testWithAllErrorModes: true,
		},
		{
			name:                  "needs-result-defaulterrmode-index",
			input:                 "needs['job-id']['result']",
			expected:              "success",
			testWithAllErrorModes: true,
		},
		{
			name:                  "needs-result-defaulterrmode-deref",
			input:                 "needs.*.result",
			expected:              []any{"success", "success"},
			testWithAllErrorModes: true,
		},
		{
			name:      "needs-output-invalidjoboutputerrmode-err",
			input:     "needs.job-id.outputs.non-existent-output",
			errorMode: InvalidJobOutput,
			expectedErr: &InvalidJobOutputReferencedError{
				JobID:      "job-id",
				OutputName: "non-existent-output",
				String:     "output \"non-existent-output\" is not available on job \"job-id\"",
			},
		},
		{
			name:      "needs-output-invalidjoboutputerrmode-err-index",
			input:     "needs.job-id.outputs['non-existent-output']",
			errorMode: InvalidJobOutput,
			expectedErr: &InvalidJobOutputReferencedError{
				JobID:      "job-id",
				OutputName: "non-existent-output",
				String:     "output \"non-existent-output\" is not available on job \"job-id\"",
			},
		},
		{
			name:      "needs-output-badjob-invalidjoboutputerrmode-err",
			input:     "needs.non-existent-job.outputs.non-existent-output",
			errorMode: InvalidJobOutput,
			expectedErr: &InvalidJobOutputReferencedError{
				JobID:  "non-existent-job",
				String: "job \"non-existent-job\" is not available",
			},
		},
		{
			name:      "needs-output-badjob-invalidjoboutputerrmode-err-index",
			input:     "needs['non-existent-job'].outputs.non-existent-output",
			errorMode: InvalidJobOutput,
			expectedErr: &InvalidJobOutputReferencedError{
				JobID:  "non-existent-job",
				String: "job \"non-existent-job\" is not available",
			},
		},
		{
			name:                  "matrix-defaulterrmode-present",
			input:                 "matrix.os",
			expected:              "nixos",
			testWithAllErrorModes: true,
		},
		{
			name:                  "matrix-defaulterrmode-present-index",
			input:                 "matrix['os']",
			expected:              "nixos",
			testWithAllErrorModes: true,
		},
		{
			name:                  "matrix-defaulterrmode-present-deref",
			input:                 "matrix.*",
			expected:              map[string]any{"os": "nixos"},
			testWithAllErrorModes: true,
		},
		{
			name:     "matrix-defaulterrmode-absent",
			input:    "matrix.platform",
			expected: nil,
		},
		{
			name:      "matrix-invalidmatrixdimerrormode-absent",
			input:     "matrix.platform",
			errorMode: InvalidMatrixDimension,
			expectedErr: &InvalidMatrixDimensionReferencedError{
				Dimension: "platform",
				String:    "matrix dimension \"platform\" is not defined",
			},
		},
		{
			name:      "matrix-invalidmatrixdimerrormode-absent-array",
			input:     "matrix['platform']",
			errorMode: InvalidMatrixDimension,
			expectedErr: &InvalidMatrixDimensionReferencedError{
				Dimension: "platform",
				String:    "matrix dimension \"platform\" is not defined",
			},
		},
		{
			name:      "env-blockenv",
			input:     "env.PATH",
			errorMode: BlockEnv,
			expectedErr: &EnvReferencedError{
				Key:    "path",
				String: "env value \"path\" referenced",
			},
		},
		{
			name:      "env-blockenv-index",
			input:     "env['PATH']",
			errorMode: BlockEnv,
			expectedErr: &EnvReferencedError{
				Key:    "PATH",
				String: "env value \"PATH\" referenced",
			},
		},
		{
			name:      "env-blockenv-deref",
			input:     "env.*",
			errorMode: BlockEnv,
			expectedErr: &EnvReferencedError{
				String: "env dereferenced via 'env.*'",
			},
		},
		{
			name:      "env-defaulterrmode-present-deref",
			input:     "env.*",
			expected:  map[string]string{"PATH": "/usr/bin"},
			errorMode: 0,
		},
		{
			name:      "secrets-blockenv",
			input:     "secrets.PATH",
			errorMode: BlockSecrets,
			expectedErr: &SecretsReferencedError{
				Key:    "path",
				String: "secrets value \"path\" referenced",
			},
		},
		{
			name:      "secrets-blockenv-index",
			input:     "secrets['PATH']",
			errorMode: BlockSecrets,
			expectedErr: &SecretsReferencedError{
				Key:    "PATH",
				String: "secrets value \"PATH\" referenced",
			},
		},
		{
			name:      "secrets-blockenv-deref",
			input:     "secrets.*",
			errorMode: BlockSecrets,
			expectedErr: &SecretsReferencedError{
				String: "secrets dereferenced via 'secrets.*'",
			},
		},
		{
			name:      "secrets-defaulterrmode-present-deref",
			input:     "secrets.*",
			expected:  map[string]string{"password": "Password?"},
			errorMode: 0,
		},
	}

	env := &EvaluationEnvironment{
		Needs: map[string]Needs{
			"job-id": {
				Outputs: map[string]string{
					"output-name": "value",
				},
				Result: "success",
			},
			"another-job-id": {
				Outputs: map[string]string{
					"output-name": "another value",
				},
				Result: "success",
			},
		},
		Inputs: map[string]any{
			"name": "value",
		},
		Matrix: map[string]any{
			"os": "nixos",
		},
		Env: map[string]string{
			"PATH": "/usr/bin",
		},
		Secrets: map[string]string{
			"password": "Password?",
		},
	}

	for _, tt := range table {
		t.Run(tt.name, func(t *testing.T) {
			if tt.testWithAllErrorModes {
				for _, errorMode := range []ErrorMode{0, InvalidJobOutput, InvalidMatrixDimension, InvalidJobOutput | InvalidMatrixDimension, BlockEnv, BlockSecrets, BlockEnv | BlockSecrets} {
					t.Run(fmt.Sprintf("ErrorMode=%v", errorMode), func(t *testing.T) {
						env.ErrorMode = errorMode
						output, err := NewInterpreter(env, Config{}).Evaluate(tt.input, DefaultStatusCheckNone)
						if tt.expectedErr != nil {
							assert.Equal(t, tt.expectedErr, err)
						} else {
							assert.Nil(t, err)
							if tt.postProcess != nil {
								output = tt.postProcess(output)
							}
							assert.Equal(t, tt.expected, output)
						}
					})
				}
			} else {
				env.ErrorMode = tt.errorMode
				output, err := NewInterpreter(env, Config{}).Evaluate(tt.input, DefaultStatusCheckNone)
				if tt.expectedErr != nil {
					assert.Equal(t, tt.expectedErr, err)
				} else {
					assert.Nil(t, err)
					if tt.postProcess != nil {
						output = tt.postProcess(output)
					}
					assert.Equal(t, tt.expected, output)
				}
			}
		})
	}
}
