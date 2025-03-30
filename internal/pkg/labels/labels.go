// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package labels

import (
	"errors"
	"fmt"
	"strings"
)

const (
	SchemeHost   = "host"
	SchemeDocker = "docker"
	SchemeLXC    = "lxc"
)

type Label struct {
	Name   string
	Schema string
	Arg    string
}

func (l *Label) ToString() string {
	lbl := l.Name
	if l.Schema != SchemeHost {
		lbl += ":" + l.Schema
		if l.Arg != "" {
			lbl += ":" + l.Arg
		}
	}
	return lbl
}

func Parse(str string) (*Label, error) {
	str = strings.TrimSpace(str)
	// Empty labels exist only to confuse
	if len(str) == 0 {
		return nil, errors.New("expected non-empty label")
	}

	splits := strings.SplitN(str, ":", 3)
	label := &Label{
		Name:   splits[0],
		Schema: "host",
		Arg:    "",
	}

	if len(splits) >= 2 {
		label.Schema = splits[1]
	}
	if len(splits) >= 3 {
		label.Arg = splits[2]
	}

	// An empty name is more likely a typo
	if len(label.Name) == 0 {
		return nil, fmt.Errorf("expected non-empty name for label: %s", str)
	}

	if label.Schema != SchemeHost && label.Schema != SchemeDocker && label.Schema != SchemeLXC {
		return nil, fmt.Errorf("unsupported schema: %s", label.Schema)
	}
	return label, nil
}

func ParseLabels(strs []string) (Labels, error) {
	errs := make([]error, 0)
	ls := make(Labels, 0)
	for _, l := range strs {
		label, err := Parse(l)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		ls = append(ls, label)
	}

	if len(errs) > 0 {
		return ls, errors.Join(errs...)
	}

	return ls, nil
}

type Labels []*Label

func (l Labels) RequireDocker() bool {
	for _, label := range l {
		if label.Schema == SchemeDocker {
			return true
		}
	}
	return false
}

func (l Labels) PickPlatform(runsOn []string) string {
	platforms := make(map[string]string, len(l))
	for _, label := range l {
		switch label.Schema {
		case SchemeDocker:
			// "//" will be ignored
			platforms[label.Name] = strings.TrimPrefix(label.Arg, "//")
		case SchemeHost:
			platforms[label.Name] = "-self-hosted"
		case SchemeLXC:
			platforms[label.Name] = "lxc:" + strings.TrimPrefix(label.Arg, "//")
		default:
			// It should not happen, because Parse has checked it.
			continue
		}
	}
	for _, v := range runsOn {
		if v, ok := platforms[v]; ok {
			return v
		}
	}

	// TODO: support multiple labels
	// like:
	//   ["ubuntu-22.04"] => "ubuntu:22.04"
	//   ["with-gpu"] => "linux:with-gpu"
	//   ["ubuntu-22.04", "with-gpu"] => "ubuntu:22.04_with-gpu"

	// return default.
	// So the runner receives a task with a label that the runner doesn't have,
	// it happens when the user have edited the label of the runner in the web UI.
	// TODO: it may be not correct, what if the runner is used as host mode only?
	return "node:20-bullseye"
}

func (l Labels) Names() []string {
	names := make([]string, 0, len(l))
	for _, label := range l {
		names = append(names, label.Name)
	}
	return names
}

func (l Labels) ToStrings() []string {
	ls := make([]string, 0, len(l))
	for _, label := range l {
		ls = append(ls, label.ToString())
	}
	return ls
}
