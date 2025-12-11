package exprparser

import (
	"fmt"
	"regexp"
	"strings"
)

// The expression parsing library that runner uses to evaluate workflow expressions (eg. `${{ ... }}`) doesn't support a
// string which is partially text and partially expression, like `Hello ${{ vars.world }}!`.  `RewriteSubExpression`
// converts a string in this format into a single expression evaluation, such as `format('Hello {0}!', vars.world)`.
// The return value may still be `${{ ... }}` delimited.
func RewriteSubExpression(in string, forceFormat bool) string {
	if !strings.Contains(in, "${{") || !strings.Contains(in, "}}") {
		return in
	}

	strPattern := regexp.MustCompile("(?:''|[^'])*'")
	pos := 0
	exprStart := -1
	strStart := -1
	var results []string
	formatOut := ""
	for pos < len(in) {
		if strStart > -1 {
			matches := strPattern.FindStringIndex(in[pos:])
			if matches == nil {
				panic("unclosed string.")
			}

			strStart = -1
			pos += matches[1]
		} else if exprStart > -1 {
			exprEnd := strings.Index(in[pos:], "}}")
			strStart = strings.Index(in[pos:], "'")

			if exprEnd > -1 && strStart > -1 {
				if exprEnd < strStart {
					strStart = -1
				} else {
					exprEnd = -1
				}
			}

			if exprEnd > -1 {
				formatOut += fmt.Sprintf("{%d}", len(results))
				results = append(results, strings.TrimSpace(in[exprStart:pos+exprEnd]))
				pos += exprEnd + 2
				exprStart = -1
			} else if strStart > -1 {
				pos += strStart + 1
			} else {
				panic("unclosed expression.")
			}
		} else {
			exprStart = strings.Index(in[pos:], "${{")
			if exprStart != -1 {
				formatOut += escapeFormatString(in[pos : pos+exprStart])
				exprStart = pos + exprStart + 3
				pos = exprStart
			} else {
				formatOut += escapeFormatString(in[pos:])
				pos = len(in)
			}
		}
	}

	if len(results) == 1 && formatOut == "{0}" && !forceFormat {
		return in
	}

	out := fmt.Sprintf("format('%s', %s)", strings.ReplaceAll(formatOut, "'", "''"), strings.Join(results, ", "))
	return out
}

// Within a `format('abc')` function call, references to arguments are .NET-style formatted with `{0}`.  If a static
// string contains '{' or `}` and is put into this arg, it must be escaped as '{{' or '}}'.
func escapeFormatString(in string) string {
	return strings.ReplaceAll(strings.ReplaceAll(in, "{", "{{"), "}", "}}")
}
