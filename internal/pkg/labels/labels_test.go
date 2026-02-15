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
