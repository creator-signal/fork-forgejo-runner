package runner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"code.forgejo.org/forgejo/runner/v12/act/common"
	"code.forgejo.org/forgejo/runner/v12/act/common/git"
	"code.forgejo.org/forgejo/runner/v12/act/model"
)

type stepActionRemoteMocks struct {
	mock.Mock
}

func (sarm *stepActionRemoteMocks) readAction(_ context.Context, step *model.Step, actionDir, actionPath string, readFile actionYamlReader, writeFile fileWriter) (*model.Action, error) {
	args := sarm.Called(step, actionDir, actionPath, readFile, writeFile)
	return args.Get(0).(*model.Action), args.Error(1)
}

func (sarm *stepActionRemoteMocks) runAction(step actionStep, actionDir string, remoteAction *remoteAction) common.Executor {
	args := sarm.Called(step, actionDir, remoteAction)
	return args.Get(0).(func(context.Context) error)
}

type UselessWorktree struct {
	closed bool
}

func (t *UselessWorktree) Close() error {
	t.closed = true
	return nil
}

func (t *UselessWorktree) WorktreeDir() string {
	return ""
}

func TestStepActionRemoteOK(t *testing.T) {
	table := []struct {
		name      string
		stepModel *model.Step
		result    *model.StepResult
		mocks     struct {
			env    bool
			cloned bool
			read   bool
			run    bool
		}
		runError error
	}{
		{
			name: "run-successful",
			stepModel: &model.Step{
				ID:   "step",
				Uses: "remote/action@v1",
			},
			result: &model.StepResult{
				Conclusion: model.StepStatusSuccess,
				Outcome:    model.StepStatusSuccess,
				Outputs:    map[string]string{},
			},
			mocks: struct {
				env    bool
				cloned bool
				read   bool
				run    bool
			}{
				env:    true,
				cloned: true,
				read:   true,
				run:    true,
			},
		},
		{
			name: "run-skipped",
			stepModel: &model.Step{
				ID:   "step",
				Uses: "remote/action@v1",
				If:   yaml.Node{Value: "false"},
			},
			result: &model.StepResult{
				Conclusion: model.StepStatusSkipped,
				Outcome:    model.StepStatusSkipped,
				Outputs:    map[string]string{},
			},
			mocks: struct {
				env    bool
				cloned bool
				read   bool
				run    bool
			}{
				env:    true,
				cloned: true,
				read:   true,
				run:    false,
			},
		},
		{
			name: "run-error",
			stepModel: &model.Step{
				ID:   "step",
				Uses: "remote/action@v1",
			},
			result: &model.StepResult{
				Conclusion: model.StepStatusFailure,
				Outcome:    model.StepStatusFailure,
				Outputs:    map[string]string{},
			},
			mocks: struct {
				env    bool
				cloned bool
				read   bool
				run    bool
			}{
				env:    true,
				cloned: true,
				read:   true,
				run:    true,
			},
			runError: errors.New("error"),
		},
	}

	for _, tt := range table {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()

			cm := &containerMock{}
			sarm := &stepActionRemoteMocks{}
			wt := &UselessWorktree{}

			clonedAction := false

			origStepAtionRemoteGitClone := stepActionRemoteGitClone
			stepActionRemoteGitClone = func(ctx context.Context, input git.CloneInput) (git.Worktree, error) {
				clonedAction = true
				return wt, nil
			}
			defer (func() {
				stepActionRemoteGitClone = origStepAtionRemoteGitClone
			})()

			sar := &stepActionRemote{
				RunContext: &RunContext{
					Config: &Config{
						GitHubInstance: "github.com",
					},
					Run: &model.Run{
						JobID: "1",
						Workflow: &model.Workflow{
							Jobs: map[string]*model.Job{
								"1": {},
							},
						},
					},
					StepResults:  map[string]*model.StepResult{},
					JobContainer: cm,
				},
				Step:       tt.stepModel,
				readAction: sarm.readAction,
				runAction:  sarm.runAction,
			}
			sar.RunContext.ExprEval = sar.RunContext.NewExpressionEvaluator(ctx)

			if tt.mocks.read {
				sarm.On("readAction", sar.Step, mock.Anything, "", mock.Anything, mock.Anything).Return(&model.Action{}, nil)
			}
			if tt.mocks.run {
				sarm.On("runAction", sar, mock.Anything, newRemoteAction(sar.Step.Uses)).Return(func(ctx context.Context) error { return tt.runError })

				cm.On("Copy", "/var/run/act", mock.AnythingOfType("[]*container.FileEntry")).Return(func(ctx context.Context) error {
					return nil
				})

				cm.On("UpdateFromEnv", "/var/run/act/workflow/envs.txt", mock.AnythingOfType("*map[string]string")).Return(func(ctx context.Context) error {
					return nil
				})

				cm.On("UpdateFromEnv", "/var/run/act/workflow/statecmd.txt", mock.AnythingOfType("*map[string]string")).Return(func(ctx context.Context) error {
					return nil
				})

				cm.On("UpdateFromEnv", "/var/run/act/workflow/outputcmd.txt", mock.AnythingOfType("*map[string]string")).Return(func(ctx context.Context) error {
					return nil
				})

				cm.On("GetContainerArchive", ctx, "/var/run/act/workflow/SUMMARY.md").Return(io.NopCloser(&bytes.Buffer{}), nil)
				cm.On("GetContainerArchive", ctx, "/var/run/act/workflow/pathcmd.txt").Return(io.NopCloser(&bytes.Buffer{}), nil)
			}

			err := sar.pre()(ctx)
			if err == nil {
				err = sar.main()(ctx)
			}

			assert.ErrorIs(t, err, tt.runError)
			assert.Equal(t, tt.mocks.cloned, clonedAction)
			assert.Equal(t, sar.RunContext.StepResults["step"], tt.result)

			assert.False(t, wt.closed)
			err = sar.post()(ctx)
			require.NoError(t, err)
			assert.True(t, wt.closed)

			sarm.AssertExpectations(t)
			cm.AssertExpectations(t)
		})
	}
}

func TestStepActionRemotePre(t *testing.T) {
	table := []struct {
		name      string
		stepModel *model.Step
	}{
		{
			name: "run-pre",
			stepModel: &model.Step{
				Uses: "org/repo/path@ref",
			},
		},
	}

	for _, tt := range table {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()

			clonedAction := false
			sarm := &stepActionRemoteMocks{}

			origStepAtionRemoteGitClone := stepActionRemoteGitClone
			stepActionRemoteGitClone = func(ctx context.Context, input git.CloneInput) (git.Worktree, error) {
				clonedAction = true
				return &UselessWorktree{}, nil
			}
			defer (func() {
				stepActionRemoteGitClone = origStepAtionRemoteGitClone
			})()

			sar := &stepActionRemote{
				Step: tt.stepModel,
				RunContext: &RunContext{
					Config: &Config{
						GitHubInstance: "https://github.com",
					},
					Run: &model.Run{
						JobID: "1",
						Workflow: &model.Workflow{
							Jobs: map[string]*model.Job{
								"1": {},
							},
						},
					},
				},
				readAction: sarm.readAction,
			}

			sarm.On("readAction", sar.Step, mock.Anything, "path", mock.Anything, mock.Anything).Return(&model.Action{}, nil)

			err := sar.pre()(ctx)

			assert.Nil(t, err)
			assert.Equal(t, true, clonedAction)

			sarm.AssertExpectations(t)
		})
	}
}

func TestStepActionRemotePreThroughAction(t *testing.T) {
	table := []struct {
		name      string
		stepModel *model.Step
	}{
		{
			name: "run-pre",
			stepModel: &model.Step{
				Uses: "org/repo/path@ref",
			},
		},
	}

	for _, tt := range table {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()

			clonedAction := false
			sarm := &stepActionRemoteMocks{}

			origStepAtionRemoteGitClone := stepActionRemoteGitClone
			stepActionRemoteGitClone = func(ctx context.Context, input git.CloneInput) (git.Worktree, error) {
				if input.URL == "https://github.com/org/repo" {
					clonedAction = true
				}
				return &UselessWorktree{}, nil
			}
			defer (func() {
				stepActionRemoteGitClone = origStepAtionRemoteGitClone
			})()

			sar := &stepActionRemote{
				Step: tt.stepModel,
				RunContext: &RunContext{
					Config: &Config{
						GitHubInstance:                "https://enterprise.github.com",
						ReplaceGheActionWithGithubCom: []string{"org/repo"},
					},
					Run: &model.Run{
						JobID: "1",
						Workflow: &model.Workflow{
							Jobs: map[string]*model.Job{
								"1": {},
							},
						},
					},
				},
				readAction: sarm.readAction,
			}

			sarm.On("readAction", sar.Step, mock.Anything, "path", mock.Anything, mock.Anything).Return(&model.Action{}, nil)

			err := sar.pre()(ctx)

			assert.Nil(t, err)
			assert.Equal(t, true, clonedAction)

			sarm.AssertExpectations(t)
		})
	}
}

func TestStepActionRemotePost(t *testing.T) {
	table := []struct {
		name               string
		stepModel          *model.Step
		actionModel        *model.Action
		initialStepResults map[string]*model.StepResult
		IntraActionState   map[string]map[string]string
		expectedEnv        map[string]string
		err                error
		mocks              struct {
			env  bool
			exec bool
		}
	}{
		{
			name: "main-success",
			stepModel: &model.Step{
				ID:   "step",
				Uses: "remote/action@v1",
			},
			actionModel: &model.Action{
				Runs: model.ActionRuns{
					Using:  "node16",
					Post:   "post.js",
					PostIf: "always()",
				},
			},
			initialStepResults: map[string]*model.StepResult{
				"step": {
					Conclusion: model.StepStatusSuccess,
					Outcome:    model.StepStatusSuccess,
					Outputs:    map[string]string{},
				},
			},
			IntraActionState: map[string]map[string]string{
				"step": {
					"key": "value",
				},
			},
			expectedEnv: map[string]string{
				"STATE_key": "value",
			},
			mocks: struct {
				env  bool
				exec bool
			}{
				env:  true,
				exec: true,
			},
		},
		{
			name: "main-failed",
			stepModel: &model.Step{
				ID:   "step",
				Uses: "remote/action@v1",
			},
			actionModel: &model.Action{
				Runs: model.ActionRuns{
					Using:  "node16",
					Post:   "post.js",
					PostIf: "always()",
				},
			},
			initialStepResults: map[string]*model.StepResult{
				"step": {
					Conclusion: model.StepStatusFailure,
					Outcome:    model.StepStatusFailure,
					Outputs:    map[string]string{},
				},
			},
			mocks: struct {
				env  bool
				exec bool
			}{
				env:  true,
				exec: true,
			},
		},
		{
			name: "skip-if-failed",
			stepModel: &model.Step{
				ID:   "step",
				Uses: "remote/action@v1",
			},
			actionModel: &model.Action{
				Runs: model.ActionRuns{
					Using:  "node16",
					Post:   "post.js",
					PostIf: "success()",
				},
			},
			initialStepResults: map[string]*model.StepResult{
				"step": {
					Conclusion: model.StepStatusFailure,
					Outcome:    model.StepStatusFailure,
					Outputs:    map[string]string{},
				},
			},
			mocks: struct {
				env  bool
				exec bool
			}{
				env:  true,
				exec: false,
			},
		},
		{
			name: "skip-if-main-skipped",
			stepModel: &model.Step{
				ID:   "step",
				If:   yaml.Node{Value: "failure()"},
				Uses: "remote/action@v1",
			},
			actionModel: &model.Action{
				Runs: model.ActionRuns{
					Using:  "node16",
					Post:   "post.js",
					PostIf: "always()",
				},
			},
			initialStepResults: map[string]*model.StepResult{
				"step": {
					Conclusion: model.StepStatusSkipped,
					Outcome:    model.StepStatusSkipped,
					Outputs:    map[string]string{},
				},
			},
			mocks: struct {
				env  bool
				exec bool
			}{
				env:  false,
				exec: false,
			},
		},
	}

	for _, tt := range table {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()

			cm := &containerMock{}

			sar := &stepActionRemote{
				env: map[string]string{},
				RunContext: &RunContext{
					Config: &Config{
						GitHubInstance: "https://github.com",
					},
					JobContainer: cm,
					Run: &model.Run{
						JobID: "1",
						Workflow: &model.Workflow{
							Jobs: map[string]*model.Job{
								"1": {},
							},
						},
					},
					StepResults:      tt.initialStepResults,
					IntraActionState: tt.IntraActionState,
				},
				Step:     tt.stepModel,
				action:   tt.actionModel,
				workTree: &UselessWorktree{},
			}
			sar.RunContext.ExprEval = sar.RunContext.NewExpressionEvaluator(ctx)

			if tt.mocks.exec {
				cm.On("Exec", mock.Anything, sar.env, "", "").Return(func(ctx context.Context) error { return tt.err })

				cm.On("Copy", "/var/run/act", mock.AnythingOfType("[]*container.FileEntry")).Return(func(ctx context.Context) error {
					return nil
				})

				cm.On("UpdateFromEnv", "/var/run/act/workflow/envs.txt", mock.AnythingOfType("*map[string]string")).Return(func(ctx context.Context) error {
					return nil
				})

				cm.On("UpdateFromEnv", "/var/run/act/workflow/statecmd.txt", mock.AnythingOfType("*map[string]string")).Return(func(ctx context.Context) error {
					return nil
				})

				cm.On("UpdateFromEnv", "/var/run/act/workflow/outputcmd.txt", mock.AnythingOfType("*map[string]string")).Return(func(ctx context.Context) error {
					return nil
				})

				cm.On("GetContainerArchive", ctx, "/var/run/act/workflow/SUMMARY.md").Return(io.NopCloser(&bytes.Buffer{}), nil)
				cm.On("GetContainerArchive", ctx, "/var/run/act/workflow/pathcmd.txt").Return(io.NopCloser(&bytes.Buffer{}), nil)
			}

			err := sar.post()(ctx)

			assert.Equal(t, tt.err, err)
			if tt.expectedEnv != nil {
				for key, value := range tt.expectedEnv {
					assert.Equal(t, value, sar.env[key])
				}
			}
			// Enshure that StepResults is nil in this test
			assert.Equal(t, sar.RunContext.StepResults["post-step"], (*model.StepResult)(nil))
			cm.AssertExpectations(t)
		})
	}
}

func Test_newRemoteAction(t *testing.T) {
	tests := []struct {
		action       string
		want         *remoteAction
		wantCloneURL string
	}{
		{
			action: "actions/heroku@main",
			want: &remoteAction{
				URL:  "",
				Org:  "actions",
				Repo: "heroku",
				Path: "",
				Ref:  "main",
			},
			wantCloneURL: "https://github.com/actions/heroku",
		},
		{
			action: "actions/aws/ec2@main",
			want: &remoteAction{
				URL:  "",
				Org:  "actions",
				Repo: "aws",
				Path: "ec2",
				Ref:  "main",
			},
			wantCloneURL: "https://github.com/actions/aws",
		},
		{
			action: "./.github/actions/my-action", // it's valid for GitHub, but act don't support it
			want:   nil,
		},
		{
			action: "docker://alpine:3.8", // it's valid for GitHub, but act don't support it
			want:   nil,
		},
		{
			action: "https://gitea.com/actions/heroku@main", // it's invalid for GitHub, but gitea supports it
			want: &remoteAction{
				URL:  "https://gitea.com",
				Org:  "actions",
				Repo: "heroku",
				Path: "",
				Ref:  "main",
			},
			wantCloneURL: "https://gitea.com/actions/heroku",
		},
		{
			action: "https://gitea.com/actions/aws/ec2@main", // it's invalid for GitHub, but gitea supports it
			want: &remoteAction{
				URL:  "https://gitea.com",
				Org:  "actions",
				Repo: "aws",
				Path: "ec2",
				Ref:  "main",
			},
			wantCloneURL: "https://gitea.com/actions/aws",
		},
		{
			action: "http://gitea.com/actions/heroku@main", // it's invalid for GitHub, but gitea supports it
			want: &remoteAction{
				URL:  "http://gitea.com",
				Org:  "actions",
				Repo: "heroku",
				Path: "",
				Ref:  "main",
			},
			wantCloneURL: "http://gitea.com/actions/heroku",
		},
		{
			action: "http://gitea.com/actions/aws/ec2@main", // it's invalid for GitHub, but gitea supports it
			want: &remoteAction{
				URL:  "http://gitea.com",
				Org:  "actions",
				Repo: "aws",
				Path: "ec2",
				Ref:  "main",
			},
			wantCloneURL: "http://gitea.com/actions/aws",
		},
	}
	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			got := newRemoteAction(tt.action)
			assert.Equalf(t, tt.want, got, "newRemoteAction(%v)", tt.action)
			cloneURL := ""
			if got != nil {
				cloneURL = got.CloneURL("github.com")
			}
			assert.Equalf(t, tt.wantCloneURL, cloneURL, "newRemoteAction(%v).CloneURL()", tt.action)
		})
	}
}
