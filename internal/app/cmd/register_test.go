// Copyright 2025 The Forgejo Authors
// SPDX-License-Identifier: MIT

package cmd

import (
	"slices"
	"testing"
)

func TestCommaSplit(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"", []string{}},
		{"abc", []string{"abc"}},
		{"abc,def", []string{"abc", "def"}},
		{"abc, def", []string{"abc", "def"}},
		{" abc , def ", []string{"abc", "def"}},
		{"abc,  ,def", []string{"abc", "def"}},
		{" , , ", []string{}},
	}

	for _, test := range tests {
		result := commaSplit(test.input)
		if !slices.Equal(result, test.expected) {
			t.Errorf("commaSplit(%q) = %v, want %v", test.input, result, test.expected)
		}
	}
}
