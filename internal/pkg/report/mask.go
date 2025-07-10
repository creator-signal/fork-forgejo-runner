// Copyright 2025 The Forgejo Authors.
// SPDX-License-Identifier: MIT

package report

import (
	"cmp"
	"slices"
	"strings"

	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
)

type masker struct {
	replacer   *strings.Replacer
	lines      []string
	multiLines [][]string
}

func newMasker() *masker {
	return &masker{
		lines:      make([]string, 0, 10),
		multiLines: make([][]string, 0, 10),
	}
}

func (o *masker) add(secret string) {
	if len(secret) == 0 {
		return
	}
	o.replacer = nil
	lines := strings.Split(strings.ReplaceAll(secret, "\r\n", "\n"), "\n")
	if len(lines) > 1 {
		o.multiLines = append(o.multiLines, lines)
		// make sure the longest secret are replaced first
		slices.SortFunc(o.multiLines, func(a, b []string) int {
			return cmp.Compare(len(b), len(a))
		})
	} else {
		o.lines = append(o.lines, lines[0])
		// make sure the longest secret are replaced first
		slices.SortFunc(o.lines, func(a, b string) int {
			return cmp.Compare(len(b), len(a))
		})
	}
}

func (o *masker) getReplacer() *strings.Replacer {
	if o.replacer == nil {
		oldnew := make([]string, 0, len(o.lines)*2)
		for _, line := range o.lines {
			oldnew = append(oldnew, line, "***")
		}
		o.replacer = strings.NewReplacer(oldnew...)
	}
	return o.replacer
}

func (o *masker) replaceLines(rows []*runnerv1.LogRow) {
	r := o.getReplacer()
	for _, row := range rows {
		row.Content = r.Replace(row.Content)
	}
}

func (o *masker) maybeReplaceMultiline(multiLine []string, rows []*runnerv1.LogRow) bool {
	equal, needMore := o.equalMultiline(multiLine, rows)
	if needMore {
		return needMore
	}
	if equal {
		o.replaceMultiline(multiLine, rows)
	}
	return false
}

func (o *masker) trimEOL(s string) string {
	return strings.TrimRightFunc(s, func(r rune) bool { return r == '\r' || r == '\n' })
}

func (o *masker) equalMultiline(multiLine []string, rows []*runnerv1.LogRow) (equal, needMore bool) {
	if len(rows) < 2 {
		needMore = true
		return equal, needMore
	}

	lastIndex := len(multiLine) - 1
	first := multiLine[0]
	if !strings.HasSuffix(o.trimEOL(rows[0].Content), first) {
		return equal, needMore // unreachable because the caller checks that already
	}
	for i, line := range multiLine[1:lastIndex] {
		rowIndex := i + 1
		if rowIndex >= len(rows) {
			needMore = true
			return equal, needMore
		}
		if o.trimEOL(rows[rowIndex].Content) != line {
			return equal, needMore
		}
	}
	last := multiLine[lastIndex]
	if lastIndex >= len(rows) {
		needMore = true
		return equal, needMore
	}
	if !strings.HasPrefix(o.trimEOL(rows[lastIndex].Content), last) {
		return equal, needMore
	}
	equal = true
	return equal, needMore
}

func (o *masker) replaceMultiline(multiLine []string, rows []*runnerv1.LogRow) {
	lastIndex := len(multiLine) - 1
	first := multiLine[0]
	rows[0].Content = strings.TrimSuffix(rows[0].Content, first) + "***"
	for _, row := range rows[1:lastIndex] {
		row.Content = "***"
	}
	last := multiLine[lastIndex]
	rows[lastIndex].Content = "***" + strings.TrimPrefix(rows[lastIndex].Content, last)
}

func (o *masker) replaceMultilines(rows []*runnerv1.LogRow) bool {
	for _, multiLine := range o.multiLines {
		for i, row := range rows {
			if strings.HasSuffix(o.trimEOL(row.Content), multiLine[0]) {
				needMore := o.maybeReplaceMultiline(multiLine, rows[i:])
				if needMore {
					return needMore
				}
			}
		}
	}
	return false
}

func (o *masker) replace(rows []*runnerv1.LogRow, noMore bool) bool {
	needMore := o.replaceMultilines(rows)
	if !noMore && needMore {
		return needMore
	}
	o.replaceLines(rows)
	return false
}
