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
	"bufio"
	gofs "io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

const (
	commentPrefix   = "#"
	gitDir          = ".git"
	gitignoreFile   = ".gitignore"
	infoExcludeFile = gitDir + "/info/exclude"
)

// readIgnoreFile reads a specific git ignore file.
func readIgnoreFile(fs, ignoreFile string) (ps []Pattern, err error) {
	ignoreFile, _ = replaceTildeWithHome(ignoreFile)

	f, err := os.Open(filepath.Join(fs, ignoreFile))
	if err == nil {
		defer func() { _ = f.Close() }()

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			s := scanner.Text()
			if !strings.HasPrefix(s, commentPrefix) && len(strings.TrimSpace(s)) > 0 {
				ps = append(ps, ParsePattern(s, []string{}))
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	return ps, err
}

// ReadPatterns reads the .git/info/exclude and then the gitignore patterns
// recursively traversing through the directory structure. The result is in
// the ascending order of priority (last higher).
func ReadPatterns(fs string) (ps []Pattern, err error) {
	ps, _ = readIgnoreFile(fs, infoExcludeFile)

	subps, _ := readIgnoreFile(fs, gitignoreFile)
	ps = append(ps, subps...)

	var fis []gofs.DirEntry
	fis, err = os.ReadDir(fs)
	if err != nil {
		return ps, err
	}

	for _, fi := range fis {
		if fi.IsDir() && fi.Name() != gitDir {
			if NewMatcher(ps).Match([]string{fi.Name()}, true) {
				continue
			}

			var subps []Pattern
			subps, err = ReadPatterns(filepath.Join(fs, fi.Name()))
			if err != nil {
				return ps, err
			}

			if len(subps) > 0 {
				ps = append(ps, subps...)
			}
		}
	}

	return ps, err
}

// replaceTildeWithHome replaces the tilde character at the beginning of a path
// with the appropriate home directory.
func replaceTildeWithHome(path string) (string, error) {
	if strings.HasPrefix(path, "~") {
		firstSlash := strings.Index(path, "/")
		if firstSlash == 1 {
			home, err := os.UserHomeDir()
			if err != nil {
				return path, err
			}
			return strings.Replace(path, "~", home, 1), nil
		} else if firstSlash > 1 {
			username := path[1:firstSlash]
			userAccount, err := user.Lookup(username)
			if err != nil {
				return path, err
			}
			return strings.Replace(path, path[:firstSlash], userAccount.HomeDir, 1), nil
		}
	}

	return path, nil
}
