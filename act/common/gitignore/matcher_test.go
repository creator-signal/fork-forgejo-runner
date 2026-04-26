// Copyright 2018 Sourced Technologies, S.L.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package gitignore

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatcher_Match(t *testing.T) {
	ps := []Pattern{
		ParsePattern("**/middle/v[uo]l?ano", nil),
		ParsePattern("!volcano", nil),
	}

	m := NewMatcher(ps)
	assert.True(t, m.Match([]string{"head", "middle", "vulkano"}, false))
	assert.False(t, m.Match([]string{"head", "middle", "volcano"}, false))
}

// Test that the "exclude everything except" example
// from https://git-scm.com/docs/gitignore works
// (copied below):
//
//	$ cat .gitignore
//	# exclude everything except directory foo/bar
//	/*
//	!/foo
//	/foo/*
//	!/foo/bar
func TestMatcher_EverythingExceptExample(t *testing.T) {
	ps := []Pattern{
		ParsePattern("/*", nil),
		ParsePattern("!/foo", nil),
		ParsePattern("/foo/*", nil),
		ParsePattern("!/foo/bar", nil),
	}

	m := NewMatcher(ps)
	assert.False(t, m.Match([]string{"foo"}, true))
	assert.False(t, m.Match([]string{"foo", "bar"}, false))
	assert.False(t, m.Match([]string{"foo", "bar"}, true))

	assert.True(t, m.Match([]string{"baz"}, false))
	assert.True(t, m.Match([]string{"baz"}, true))
	assert.True(t, m.Match([]string{"baz", "foo"}, false))
	assert.True(t, m.Match([]string{"baz", "foo"}, true))
	assert.True(t, m.Match([]string{"foo", "baz"}, false))
	assert.True(t, m.Match([]string{"foo", "baz"}, true))
}
