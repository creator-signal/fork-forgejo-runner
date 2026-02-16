// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package labels

import (
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
			args: "label1",
			want: &Label{
				Name:   "label1",
				Schema: SchemeDocker,
				Arg:    ArgDocker,
			},
			wantErr: false,
		},
		{
			args: "label1:docker",
			want: &Label{
				Name:   "label1",
				Schema: SchemeDocker,
				Arg:    ArgDocker,
			},
			wantErr: false,
		},
		{
			args: "label1:docker://node:18",
			want: &Label{
				Name:   "label1",
				Schema: SchemeDocker,
				Arg:    "//node:18",
			},
			wantErr: false,
		},

		{
			args: "label1:lxc",
			want: &Label{
				Name:   "label1",
				Schema: SchemeLXC,
				Arg:    ArgLXC,
			},
			wantErr: false,
		},
		{
			args: "label1:lxc://debian:buster",
			want: &Label{
				Name:   "label1",
				Schema: SchemeLXC,
				Arg:    "//debian:buster",
			},
			wantErr: false,
		},
		{
			args: "label1:firecracker",
			want: &Label{
				Name:   "label1",
				Schema: SchemeFirecracker,
				Arg:    ArgFirecracker,
			},
			wantErr: false,
		},
		{
			args: "label1:firecracker://ubuntu:24.04",
			want: &Label{
				Name:   "label1",
				Schema: SchemeFirecracker,
				Arg:    "//ubuntu:24.04",
			},
			wantErr: false,
		},
		{
			args: "label1:host",
			want: &Label{
				Name:   "label1",
				Schema: "host",
				Arg:    "",
			},
			wantErr: false,
		},
		{
			args:    "label1:host:something",
			want:    nil,
			wantErr: true,
		},
		{
			args:    "label1:invalidscheme",
			want:    nil,
			wantErr: true,
		},
		{
			args:    " label1:lxc://debian:buster",
			want:    nil,
			wantErr: true,
		},
		{
			args:    "label1 :lxc://debian:buster",
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
		})
	}
}

func TestMustParse(t *testing.T) {
	t.Run("panics if label is invalid", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("MustParse() did not panic")
			}
		}()

		MustParse(" very invalid ")
	})

	t.Run("accepts valid label", func(t *testing.T) {
		label := MustParse("label1:docker://node:18")

		assert.Equal(t, label.Name, "label1")
		assert.Equal(t, label.Schema, SchemeDocker)
		assert.Equal(t, label.Arg, "//node:18")
	})
}

func TestLabelsRequireFirecracker(t *testing.T) {
	tests := []struct {
		name   string
		labels Labels
		want   bool
	}{
		{
			name:   "empty labels",
			labels: Labels{},
			want:   false,
		},
		{
			name: "only docker label",
			labels: Labels{
				{Name: "ubuntu", Schema: SchemeDocker, Arg: "//node:18"},
			},
			want: false,
		},
		{
			name: "only lxc label",
			labels: Labels{
				{Name: "debian", Schema: SchemeLXC, Arg: "//debian:buster"},
			},
			want: false,
		},
		{
			name: "only host label",
			labels: Labels{
				{Name: "self-hosted", Schema: SchemeHost},
			},
			want: false,
		},
		{
			name: "single firecracker label",
			labels: Labels{
				{Name: "small", Schema: SchemeFirecracker, Arg: "//ubuntu:22.04"},
			},
			want: true,
		},
		{
			name: "mixed labels with firecracker",
			labels: Labels{
				{Name: "ubuntu", Schema: SchemeDocker, Arg: "//node:18"},
				{Name: "small", Schema: SchemeFirecracker, Arg: "//ubuntu:22.04"},
			},
			want: true,
		},
		{
			name: "multiple firecracker labels",
			labels: Labels{
				{Name: "small", Schema: SchemeFirecracker, Arg: "//ubuntu:22.04"},
				{Name: "large", Schema: SchemeFirecracker, Arg: "//ubuntu:22.04"},
			},
			want: true,
		},
		{
			name: "multiple non-firecracker labels",
			labels: Labels{
				{Name: "ubuntu", Schema: SchemeDocker, Arg: "//node:18"},
				{Name: "debian", Schema: SchemeLXC, Arg: "//debian:buster"},
				{Name: "self-hosted", Schema: SchemeHost},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.labels.RequireFirecracker()
			assert.Equal(t, got, tt.want)
		})
	}
}

func TestLabelsPickLabel(t *testing.T) {
	labels := Labels{
		{Name: "ubuntu-latest", Schema: SchemeDocker, Arg: "//node:18"},
		{Name: "small", Schema: SchemeFirecracker, Arg: "//ubuntu:22.04"},
		{Name: "large", Schema: SchemeFirecracker, Arg: "//ubuntu:22.04"},
		{Name: "debian", Schema: SchemeLXC, Arg: "//debian:buster"},
	}

	tests := []struct {
		name   string
		labels Labels
		runsOn []string
		want   string
	}{
		{
			name:   "empty runsOn",
			labels: labels,
			runsOn: []string{},
			want:   "",
		},
		{
			name:   "empty labels",
			labels: Labels{},
			runsOn: []string{"ubuntu-latest"},
			want:   "",
		},
		{
			name:   "match first label",
			labels: labels,
			runsOn: []string{"ubuntu-latest"},
			want:   "ubuntu-latest",
		},
		{
			name:   "match second option in runsOn",
			labels: labels,
			runsOn: []string{"unknown", "small"},
			want:   "small",
		},
		{
			name:   "match firecracker label",
			labels: labels,
			runsOn: []string{"large"},
			want:   "large",
		},
		{
			name:   "no match",
			labels: labels,
			runsOn: []string{"nonexistent", "also-nonexistent"},
			want:   "",
		},
		{
			name:   "case sensitive - no match",
			labels: labels,
			runsOn: []string{"Small", "LARGE"},
			want:   "",
		},
		{
			name:   "first match wins when multiple could match",
			labels: labels,
			runsOn: []string{"small", "large"},
			want:   "small",
		},
		{
			name:   "match lxc label",
			labels: labels,
			runsOn: []string{"debian"},
			want:   "debian",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.labels.PickLabel(tt.runsOn)
			assert.Equal(t, got, tt.want)
		})
	}
}
