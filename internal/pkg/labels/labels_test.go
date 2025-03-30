// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package labels

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gotest.tools/v3/assert"
)

func TestParse(t *testing.T) {
	tests := []struct {
		args    string
		want    *Label
		wantErr bool
	}{
		{
			args: "ubuntu:docker://node:18",
			want: &Label{
				Name:   "ubuntu",
				Schema: "docker",
				Arg:    "//node:18",
			},
			wantErr: false,
		},
		{
			args: "ubuntu:host",
			want: &Label{
				Name:   "ubuntu",
				Schema: "host",
				Arg:    "",
			},
			wantErr: false,
		},
		{
			args: "ubuntu",
			want: &Label{
				Name:   "ubuntu",
				Schema: "host",
				Arg:    "",
			},
			wantErr: false,
		},
		{
			args:    "ubuntu:vm:ubuntu-18.04",
			want:    nil,
			wantErr: true,
		},
		{
			args: " docker ",
			want: &Label{
				Name:   "docker",
				Schema: "host",
				Arg:    "",
			},
			wantErr: false,
		},
		{
			args:    "",
			want:    nil,
			wantErr: true,
		},
		{
			args:    " ",
			want:    nil,
			wantErr: true,
		},
		{
			args:    ":",
			want:    nil,
			wantErr: true,
		},
		{
			args:    "::",
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.args, func(t *testing.T) {
			got, err := Parse(tt.args)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.DeepEqual(t, got, tt.want)

			// Assert roundtrip... more or less.
			roundtrip, err := Parse(got.ToString())
			require.NoError(t, err)
			assert.DeepEqual(t, got, roundtrip)
		})
	}
}

func TestParseLabels(t *testing.T) {
	label := func(s string) *Label {
		l, err := Parse(s)
		require.NoError(t, err)
		return l
	}

	tests := []struct {
		args    []string
		want    Labels
		wantErr bool
	}{
		{
			args:    []string{"docker"},
			want:    Labels{label("docker")},
			wantErr: false,
		},
		{
			args:    []string{"docker", "other"},
			want:    Labels{label("docker"), label("other")},
			wantErr: false,
		},
		{
			args:    []string{""},
			want:    Labels{},
			wantErr: true,
		},
		{
			args:    []string{"", ":"},
			want:    Labels{},
			wantErr: true,
		},
		{
			args:    []string{"docker", ""},
			want:    Labels{label("docker")},
			wantErr: true,
		},
		{
			args:    []string{"docker:docker://alpine:edge", "other"},
			want:    Labels{label("docker:docker://alpine:edge"), label("other")},
			wantErr: false,
		},
	}
	for i, tt := range tests {
		t.Run(fmt.Sprintf("%d {%s}", i, strings.Join(tt.args, "|")), func(t *testing.T) {
			got, err := ParseLabels(tt.args)
			assert.DeepEqual(t, got, tt.want)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
