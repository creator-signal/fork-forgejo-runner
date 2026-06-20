// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"

	"code.forgejo.org/forgejo/runner/v12/internal/pkg/labels"
	"go.yaml.in/yaml/v3"
)

// labelList holds runner labels read from the configuration file. It accepts two forms:
//
//	labels:
//	  - ubuntu-latest:docker://node:20-bookworm
//	  - freebsd-15:docker://ghcr.io/freebsd/freebsd-runtime:15.0?platform=freebsd/amd64
//
// or the more explicit mapping form:
//
//	labels:
//	  freebsd-15:
//	    backend: docker
//	    backend-options:
//	      image: ghcr.io/freebsd/freebsd-runtime:15.0
//	      platform: freebsd/amd64
//
// Both forms are normalized to the canonical label string so the rest of the runner only
// ever deals with label strings.
type labelList []string

// labelImageOption is the backend-option that selects the image (or template) the backend runs.
// It maps to the argument part of the label string (the part after the scheme).
const labelImageOption = "image"

// serializedLabelSpec is the mapping form of a single label.
type serializedLabelSpec struct {
	Backend        string            `yaml:"backend"`         // container backend, e.g. docker, host, lxc. Defaults to docker.
	BackendOptions map[string]string `yaml:"backend-options"` // backend-specific options, e.g. image and platform.
}

func (ll *labelList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.SequenceNode:
		var list []string
		if err := node.Decode(&list); err != nil {
			return err
		}
		*ll = list
		return nil
	case yaml.MappingNode:
		list := make([]string, 0, len(node.Content)/2)
		for i := 0; i+1 < len(node.Content); i += 2 {
			var name string
			if err := node.Content[i].Decode(&name); err != nil {
				return err
			}
			var spec serializedLabelSpec
			if err := node.Content[i+1].Decode(&spec); err != nil {
				return fmt.Errorf("invalid label %q: %w", name, err)
			}
			str, err := spec.labelString(name)
			if err != nil {
				return err
			}
			list = append(list, str)
		}
		*ll = list
		return nil
	default:
		return fmt.Errorf("`labels` must be a list or a mapping")
	}
}

// labelString renders the mapping form as a canonical label string and validates it.
func (s serializedLabelSpec) labelString(name string) (string, error) {
	backend := s.Backend
	if backend == "" {
		backend = labels.SchemeDocker
	}
	label := &labels.Label{Name: name, Schema: backend}

	// `image` is the one backend-option that maps to the label argument (the image or template);
	// every other option is carried as-is.
	options := make(map[string]string, len(s.BackendOptions))
	for key, value := range s.BackendOptions {
		if key == labelImageOption {
			label.Arg = "//" + value
			continue
		}
		options[key] = value
	}
	if len(options) > 0 {
		label.Options = options
	}

	// Reparse to validate the scheme and options and to fill in defaults (e.g. the default image).
	parsed, err := labels.Parse(label.String())
	if err != nil {
		return "", fmt.Errorf("invalid label %q: %w", name, err)
	}
	return parsed.String(), nil
}
