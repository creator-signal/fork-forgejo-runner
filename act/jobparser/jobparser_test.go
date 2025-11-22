package jobparser

import (
	"strings"
	"testing"

	"code.forgejo.org/forgejo/runner/v12/act/model"
	"github.com/stretchr/testify/assert"

	"github.com/stretchr/testify/require"

	"go.yaml.in/yaml/v3"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		options []ParseOption
		wantErr bool
	}{
		{
			name:    "multiple_named_matrix",
			options: nil,
			wantErr: false,
		},
		{
			name:    "multiple_jobs",
			options: nil,
			wantErr: false,
		},
		{
			name:    "multiple_matrix",
			options: nil,
			wantErr: false,
		},
		{
			name:    "has_needs",
			options: nil,
			wantErr: false,
		},
		{
			name:    "has_with",
			options: nil,
			wantErr: false,
		},
		{
			name:    "job_concurrency",
			options: nil,
			wantErr: false,
		},
		{
			name:    "job_concurrency_eval",
			options: nil,
			wantErr: false,
		},
		{
			name:    "runs_on_forge_variables",
			options: []ParseOption{WithGitContext(&model.GithubContext{RunID: "18"})},
			wantErr: false,
		},
		{
			name:    "runs_on_github_variables",
			options: []ParseOption{WithGitContext(&model.GithubContext{RunID: "25"})},
			wantErr: false,
		},
		{
			name:    "runs_on_inputs_variables",
			options: []ParseOption{WithInputs(map[string]any{"chosen-os": "Ubuntu"})},
			wantErr: false,
		},
		{
			name:    "runs_on_vars_variables",
			options: []ParseOption{WithVars(map[string]string{"RUNNER": "Windows"})},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := ReadTestdata(t, tt.name+".in.yaml")
			want := ReadTestdata(t, tt.name+".out.yaml")
			got, err := Parse(content, false, tt.options...)
			if tt.wantErr {
				require.Error(t, err)
			}
			require.NoError(t, err)

			builder := &strings.Builder{}
			for _, v := range got {
				if builder.Len() > 0 {
					builder.WriteString("---\n")
				}
				encoder := yaml.NewEncoder(builder)
				encoder.SetIndent(2)
				require.NoError(t, encoder.Encode(v))
				id, job := v.Job()
				assert.NotEmpty(t, id)
				assert.NotNil(t, job)
			}
			assert.Equal(t, string(want), builder.String())
		})
	}
}
