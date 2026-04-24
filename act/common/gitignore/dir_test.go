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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setUpTest(t *testing.T) string {
	t.Helper()

	tempDir := t.TempDir()

	err := os.MkdirAll(filepath.Join(tempDir, ".git/info"), os.ModePerm)
	require.NoError(t, err)

	f, err := os.Create(filepath.Join(tempDir, ".git/info/exclude"))
	require.NoError(t, err)
	_, err = f.Write([]byte("exclude.crlf\r\n"))
	require.NoError(t, err)
	err = f.Close()
	require.NoError(t, err)

	f, err = os.Create(filepath.Join(tempDir, ".gitignore"))
	require.NoError(t, err)
	_, err = f.Write([]byte("vendor/g*/\n"))
	require.NoError(t, err)
	_, err = f.Write([]byte("ignore.crlf\r\n"))
	require.NoError(t, err)
	_, err = f.Write([]byte("ignore_dir\n"))
	require.NoError(t, err)
	err = f.Close()
	require.NoError(t, err)

	err = os.MkdirAll(filepath.Join(tempDir, "vendor"), os.ModePerm)
	require.NoError(t, err)
	f, err = os.Create(filepath.Join(tempDir, "vendor/.gitignore"))
	require.NoError(t, err)
	_, err = f.Write([]byte("!github.com/\n"))
	require.NoError(t, err)
	err = f.Close()
	require.NoError(t, err)

	err = os.MkdirAll(filepath.Join(tempDir, "ignore_dir"), os.ModePerm)
	require.NoError(t, err)
	f, err = os.Create(filepath.Join(tempDir, "ignore_dir/.gitignore"))
	require.NoError(t, err)
	_, err = f.Write([]byte("!file\n"))
	require.NoError(t, err)
	err = f.Close()
	require.NoError(t, err)
	f, err = os.Create(filepath.Join(tempDir, "ignore_dir/file"))
	require.NoError(t, err)
	err = f.Close()
	require.NoError(t, err)

	err = os.MkdirAll(filepath.Join(tempDir, "another"), os.ModePerm)
	require.NoError(t, err)
	err = os.MkdirAll(filepath.Join(tempDir, "exclude.crlf"), os.ModePerm)
	require.NoError(t, err)
	err = os.MkdirAll(filepath.Join(tempDir, "ignore.crlf"), os.ModePerm)
	require.NoError(t, err)
	err = os.MkdirAll(filepath.Join(tempDir, "vendor/github.com"), os.ModePerm)
	require.NoError(t, err)
	err = os.MkdirAll(filepath.Join(tempDir, "vendor/gopkg.in"), os.ModePerm)
	require.NoError(t, err)

	err = os.MkdirAll(filepath.Join(tempDir, "multiple/sub/ignores/first"), os.ModePerm)
	require.NoError(t, err)
	err = os.MkdirAll(filepath.Join(tempDir, "multiple/sub/ignores/second"), os.ModePerm)
	require.NoError(t, err)
	f, err = os.Create(filepath.Join(tempDir, "multiple/sub/ignores/first/.gitignore"))
	require.NoError(t, err)
	_, err = f.Write([]byte("ignore_dir\n"))
	require.NoError(t, err)
	err = f.Close()
	require.NoError(t, err)
	f, err = os.Create(filepath.Join(tempDir, "multiple/sub/ignores/second/.gitignore"))
	require.NoError(t, err)
	_, err = f.Write([]byte("ignore_dir\n"))
	require.NoError(t, err)
	err = f.Close()
	require.NoError(t, err)
	err = os.MkdirAll(filepath.Join(tempDir, "multiple/sub/ignores/first/ignore_dir"), os.ModePerm)
	require.NoError(t, err)
	err = os.MkdirAll(filepath.Join(tempDir, "multiple/sub/ignores/second/ignore_dir"), os.ModePerm)
	require.NoError(t, err)

	return tempDir
}

func TestDir_ReadPatterns(t *testing.T) {
	tempDir := setUpTest(t)

	checkPatterns := func(ps []Pattern) {
		assert.Len(t, ps, 7)
		m := NewMatcher(ps)

		assert.True(t, m.Match([]string{"exclude.crlf"}, true))
		assert.True(t, m.Match([]string{"ignore.crlf"}, true))
		assert.True(t, m.Match([]string{"vendor", "gopkg.in"}, true))
		assert.True(t, m.Match([]string{"ignore_dir", "file"}, false))
		assert.False(t, m.Match([]string{"vendor", "github.com"}, true))
		assert.True(t, m.Match([]string{"multiple", "sub", "ignores", "first", "ignore_dir"}, true))
		assert.True(t, m.Match([]string{"multiple", "sub", "ignores", "second", "ignore_dir"}, true))
	}

	ps, err := ReadPatterns(tempDir)
	assert.NoError(t, err)
	checkPatterns(ps)
}
