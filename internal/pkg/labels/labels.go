// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package labels

import (
	"fmt"
	"strings"
)

const (
	SchemeHost = "host"

	SchemeDocker = "docker"
	ArgDocker    = "//node:22-bookworm"

	SchemeLXC = "lxc"
	ArgLXC    = "//debian:bookworm"

	SchemeFirecracker = "firecracker"
	ArgFirecracker    = "//ubuntu:22.04"
)

type Label struct {
	Name   string
	Schema string
	Arg    string
}

func Parse(str string) (*Label, error) {
	splits := strings.SplitN(str, ":", 3)
	label := &Label{
		Name:   splits[0],
		Schema: "docker",
	}

	if strings.TrimSpace(label.Name) != label.Name {
		return nil, fmt.Errorf("invalid label %q: starting or ending with a space is invalid", label.Name)
	}

	if len(splits) >= 2 {
		label.Schema = splits[1]
		if label.Schema != SchemeHost && label.Schema != SchemeDocker && label.Schema != SchemeLXC && label.Schema != SchemeFirecracker {
			return nil, fmt.Errorf("unsupported schema: %s", label.Schema)
		}
	}

	if len(splits) >= 3 {
		if label.Schema == SchemeHost {
			return nil, fmt.Errorf("schema: %s does not have arguments", label.Schema)
		}

		label.Arg = splits[2]
	}
	if label.Arg == "" {
		switch label.Schema {
		case SchemeDocker:
			label.Arg = ArgDocker
		case SchemeLXC:
			label.Arg = ArgLXC
		case SchemeFirecracker:
			label.Arg = ArgFirecracker
		}
	}

	return label, nil
}

// MustParse is like Parse but panics if the string cannot be parsed.
func MustParse(str string) *Label {
	label, err := Parse(str)
	if err != nil {
		panic(`label: Parse(` + str + `): ` + err.Error())
	}
	return label
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

// RequireFirecracker returns true if any label uses the firecracker scheme.
func (l Labels) RequireFirecracker() bool {
	for _, label := range l {
		if label.Schema == SchemeFirecracker {
			return true
		}
	}
	return false
}

// PickLabel returns the name of the first label that matches any of the runsOn values.
// Returns empty string if no match is found.
func (l Labels) PickLabel(runsOn []string) string {
	names := make(map[string]struct{}, len(l))
	for _, label := range l {
		names[label.Name] = struct{}{}
	}
	for _, v := range runsOn {
		if _, ok := names[v]; ok {
			return v
		}
	}
	return ""
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
		case SchemeFirecracker:
			platforms[label.Name] = "firecracker:" + strings.TrimPrefix(label.Arg, "//")
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

	return strings.TrimPrefix(ArgDocker, "//")
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
		lbl := label.Name
		if label.Schema != "" {
			lbl += ":" + label.Schema
			if label.Arg != "" {
				lbl += ":" + label.Arg
			}
		}
		ls = append(ls, lbl)
	}
	return ls
}
