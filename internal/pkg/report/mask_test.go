// Copyright 2025 The Forgejo Authors.
// SPDX-License-Identifier: MIT

package report

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReporterMask(t *testing.T) {
	lineOne := "secretOne"
	lineTwo := lineOne + "secretTwoIsLongerThanAndStartsWithIt"
	multiLineMixedSeparators := "A\nB\r\nC\r\nD"
	multiLineOne := lineOne + `
TWO
THREE`
	multiLineTwo := multiLineOne + `
FOUR
FIVE
SIX`
	for _, testCase := range []struct {
		name     string
		secrets  []string
		in       string
		out      string
		noMore   bool
		needMore bool
	}{
		{
			//
			// a multiline secret is masked
			//
			name: "MultilineIsMasked",
			secrets: []string{
				multiLineOne,
			},
			in:       fmt.Sprintf("line before\n%[1]s\nline after", multiLineOne),
			out:      "line before\n***\n***\n***\nline after\n",
			needMore: false,
		},
		{
			//
			// a multiline secret where newlines are represented
			// as \ followed by n is masked
			//
			name: "MultilineTransformedIsMasked",
			secrets: []string{
				multiLineOne,
			},
			in:       fmt.Sprintf("line before\n%[1]s\\nTWO\\nTHREE\nline after", lineOne),
			out:      "line before\n***\nline after\n",
			needMore: false,
		},
		{
			//
			// in a multiline secret \r\n is equivalent to \n and does
			// not change how it is masked
			//
			name: "MultilineWithMixedLineSeparatorsIsMasked",
			secrets: []string{
				multiLineMixedSeparators,
			},
			in:       fmt.Sprintf("line before\n%[1]s\nline after", multiLineMixedSeparators),
			out:      "line before\n***\n***\n***\n***\nline after\n",
			needMore: false,
		},
		{
			//
			// the last line of a multline secret is not a match
			//
			//
			name: "MultilineLastLineDoesNotMatch",
			secrets: []string{
				multiLineOne,
			},
			in:       fmt.Sprintf("%s\nTWO\nsomethingelse", lineOne),
			out:      lineOne + "\nTWO\nsomethingelse\n",
			needMore: false,
		},
		{
			//
			// non-multine secrets are masked
			//
			name: "SecretsAreMasked",
			secrets: []string{
				"",
				lineOne,
				lineTwo,
			},
			in:       fmt.Sprintf("line before\n%[1]s\n%[2]s\nline after", lineOne, lineTwo),
			out:      "line before\n***\n***\nline after\n",
			needMore: false,
		},
		{
			//
			// the first line of a multiline secret may be found
			// at the end of a line and the last line may be followed
			// by a suffix, e.g.
			//
			// >>>ONE
			// TWO
			// THREE<<<
			//
			// and is expected to be replaced with
			//
			// >>>***
			// ***
			// ***<<<
			//
			name: "MultilineWithSuffixAndPrefix",
			secrets: []string{
				multiLineOne,
			},
			in:       fmt.Sprintf(">>>%[1]s<<<", multiLineOne),
			out:      ">>>***\n***\n***<<<\n",
			needMore: false,
		},
		{
			//
			// multiline secrets are considered first
			// since only the first line of the multiLineOne secret
			// is found, it needs more input to decide and does not
			// mask anything.
			//
			// non-multiline secrets are not considered at all if
			// a multiline secret needs more input and the
			// lineOne secret is not masked even though it is found
			//
			// the first lines is found but not the second
			//
			name: "NeedMoreLines",
			secrets: []string{
				lineOne,
				multiLineOne,
			},
			in:       lineOne,
			out:      lineOne + "\n",
			needMore: true,
		},
		{
			//
			// the lines up to but not including the last are found
			//
			// See NeedMoreLines
			//
			name: "NeedMoreLinesVariation1",
			secrets: []string{
				multiLineOne,
			},
			in:       fmt.Sprintf("%s\nTWO", lineOne),
			out:      lineOne + "\nTWO\n",
			needMore: true,
		},
		{
			//
			// the lines up to the third out of six are found
			//
			// See NeedMoreLines
			//
			name: "NeedMoreLinesVariation2",
			secrets: []string{
				multiLineTwo,
			},
			in:       multiLineOne,
			out:      multiLineOne + "\n",
			needMore: true,
		},
		{
			//
			// a multiline secret will be masked if it is found
			// even when another multiline secret needs more input
			//
			// however non-multiline secrets will not be masked
			//
			name: "NeedMoreLinesAndMultilinePartialMasking",
			secrets: []string{
				lineOne,
				multiLineOne,
			},
			in: fmt.Sprintf(`%[1]s %[2]s
>>>%[3]s<<<
%[1]s`, lineOne, lineTwo, multiLineOne),
			out:      "secretOne secretOnesecretTwoIsLongerThanAndStartsWithIt\n>>>***\n***\n***<<<\nsecretOne\n",
			needMore: true,
		},
		{
			//
			// - oneline overlaps with lineTwo
			// - oneLine overlaps with multiLineOne and multiLineTwo
			// - multiLineOne overlaps with multiLineTwo
			//
			// they are all masked because the longest secret is masked
			// first
			//
			name: "OverlappingSecrets",
			secrets: []string{
				lineOne,
				lineTwo,
				multiLineOne,
				multiLineTwo,
			},
			in: fmt.Sprintf(`[[[%[1]s]]] {{{%[2]s}}}
>>>%[3]s<<<
(((%[4]s)))`, lineOne, lineTwo, multiLineOne, multiLineTwo),
			out: `[[[***]]] {{{***}}}
>>>***
***
***<<<
(((***
***
***
***
***
***)))
`,
			needMore: false,
		},
		{
			//
			// A multiline secret needing more lines does not
			// prevent single line secrets from being masked
			// when there is no more lines / these are the last
			// available lines and there is no sense in hoping
			// more will be available later.
			//
			name: "NeedMoreButNoMore",
			secrets: []string{
				lineTwo,
				multiLineOne,
			},
			in:       fmt.Sprintf(`[[[%[1]s]]] {{{%[2]s\nTWO}}}`, lineTwo, lineOne),
			out:      "[[[***]]] {{{secretOne\\nTWO}}}\n",
			noMore:   true,
			needMore: false,
		},
		{
			//
			// A variation where the partial multiline secret also
			// happens to contain a single line secret (TWO) which
			// needs to be masked.
			//
			// See NeedMoreButNoMore
			//
			name: "NeedMoreButNoMoreAndOverlappingSecret",
			secrets: []string{
				"TWO",
				lineTwo,
				multiLineOne,
			},
			in:       fmt.Sprintf(`[[[%[1]s]]] {{{%[2]s\nTWO}}}`, lineTwo, lineOne),
			out:      "[[[***]]] {{{secretOne\\n***}}}\n",
			noMore:   true,
			needMore: false,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			m := newMasker()
			for _, secret := range testCase.secrets {
				m.add(secret)
			}
			rows := stringToRows(testCase.in)
			needMore := m.replace(rows, testCase.noMore)
			assert.Equal(t, testCase.needMore, needMore)
			assert.Equal(t, testCase.out, rowsToString(rows))
		})
	}
}
