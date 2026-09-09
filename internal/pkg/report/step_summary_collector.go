// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package report

import (
	"cmp"
	"slices"
	"strings"
	"unicode/utf8"

	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
)

type stepSummaryCollector struct {
	limit     int
	mask      func(string) string
	summaries map[int64]*stepSummary
}

type stepSummary struct {
	content strings.Builder
	dirty   bool
}

func newStepSummaryCollector(limit int, mask func(string) string) *stepSummaryCollector {
	return &stepSummaryCollector{
		limit:     limit,
		mask:      mask,
		summaries: make(map[int64]*stepSummary),
	}
}

func (c *stepSummaryCollector) Append(stepNumber int64, content string) {
	summary := c.summaries[stepNumber]
	if summary == nil {
		summary = &stepSummary{}
		c.summaries[stepNumber] = summary
	}
	content = strings.ToValidUTF8(content, "?")
	content = c.mask(content)
	remaining := c.limit - summary.content.Len()

	if len(content) > remaining {
		if remaining <= 0 {
			return
		}
		content = truncateStringAtWordBoundary(content, remaining)
	}
	if content == "" {
		return
	}
	summary.content.WriteString(content)
	summary.dirty = true
}

const ellipsis = "…"

// truncateStringAtWordBoundary truncates input to at most n bytes, appending an ellipsis. If the truncation
// point is inside a word and a space is close by, the truncation happens at that word boundary instead.
// (duplicates forgejo's util.TruncateStringAtWordBoundary)
func truncateStringAtWordBoundary(input string, n int) string {
	if len(input) <= n {
		return input
	}
	appendEllipsis := n > len(ellipsis)
	if appendEllipsis {
		input = input[:n-len(ellipsis)]
	} else {
		input = input[:n]
	}

	for len(input) > 0 {
		if r, size := utf8.DecodeLastRuneInString(input); r == utf8.RuneError && size <= 1 {
			input = input[:len(input)-1]
		} else {
			break
		}
	}
	if !appendEllipsis {
		return input
	}
	// in case the content is in a Latin family language, we remove the last broken word. The ellipsis is
	// counted into the lookbehind window so the word trimming matches Forgejo's implementation exactly.
	if lastSpaceIdx := strings.LastIndex(input, " "); lastSpaceIdx != -1 && len(input)+len(ellipsis)-lastSpaceIdx < 15 {
		input = input[:lastSpaceIdx]
	}
	return input + ellipsis
}

// Collect returns all changed summaries since the last Commit (ordered by stepNumber),
func (c *stepSummaryCollector) Collect() []*runnerv1.StepSummary {
	var summaries []*runnerv1.StepSummary
	for stepNumber, summary := range c.summaries {
		if !summary.dirty {
			continue
		}
		summaries = append(summaries, &runnerv1.StepSummary{
			StepNumber: stepNumber,
			Content:    summary.content.String(),
		})
	}
	slices.SortFunc(summaries, func(a, b *runnerv1.StepSummary) int {
		return cmp.Compare(a.StepNumber, b.StepNumber)
	})
	return summaries
}

// Commit updates the dirty-flag of all summaries.
// They're clean if the content didn't grow.
func (c *stepSummaryCollector) Commit(summaries []*runnerv1.StepSummary) {
	for _, sent := range summaries {
		if summary := c.summaries[sent.StepNumber]; summary != nil && summary.content.Len() == len(sent.Content) {
			summary.dirty = false
		}
	}
}
