package runner

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"code.forgejo.org/forgejo/runner/v9/act/container"
	"code.forgejo.org/forgejo/runner/v9/act/exprparser"
	"code.forgejo.org/forgejo/runner/v9/act/model"
	"code.forgejo.org/forgejo/runner/v9/testutils"

	"github.com/docker/go-connections/nat"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	yaml "gopkg.in/yaml.v3"
)

func TestRunContext_EvalBool(t *testing.T) {
	var yml yaml.Node
	err := yml.Encode(map[string][]interface{}{
		"os":  {"Linux", "Windows"},
		"foo": {"bar", "baz"},
	})
	assert.NoError(t, err)

	rc := &RunContext{
		Config: &Config{
			Workdir: ".",
		},
		Env: map[string]string{
			"SOMETHING_TRUE":  "true",
			"SOMETHING_FALSE": "false",
			"SOME_TEXT":       "text",
		},
		Run: &model.Run{
			JobID: "job1",
			Workflow: &model.Workflow{
				Name: "test-workflow",
				Jobs: map[string]*model.Job{
					"job1": {
						Strategy: &model.Strategy{
							RawMatrix: yml,
						},
					},
				},
			},
		},
		Matrix: map[string]interface{}{
			"os":  "Linux",
			"foo": "bar",
		},
		StepResults: map[string]*model.StepResult{
			"id1": {
				Conclusion: model.StepStatusSuccess,
				Outcome:    model.StepStatusFailure,
				Outputs: map[string]string{
					"foo": "bar",
				},
			},
		},
	}
	rc.ExprEval = rc.NewExpressionEvaluator(context.Background())

	tables := []struct {
		in      string
		out     bool
		wantErr bool
	}{
		// The basic ones
		{in: "failure()", out: false},
		{in: "success()", out: true},
		{in: "cancelled()", out: false},
		{in: "always()", out: true},
		// TODO: move to sc.NewExpressionEvaluator(), because "steps" context is not available here
		// {in: "steps.id1.conclusion == 'success'", out: true},
		// {in: "steps.id1.conclusion != 'success'", out: false},
		// {in: "steps.id1.outcome == 'failure'", out: true},
		// {in: "steps.id1.outcome != 'failure'", out: false},
		{in: "true", out: true},
		{in: "false", out: false},
		// TODO: This does not throw an error, because the evaluator does not know if the expression is inside ${{ }} or not
		// {in: "!true", wantErr: true},
		// {in: "!false", wantErr: true},
		{in: "1 != 0", out: true},
		{in: "1 != 1", out: false},
		{in: "${{ 1 != 0 }}", out: true},
		{in: "${{ 1 != 1 }}", out: false},
		{in: "1 == 0", out: false},
		{in: "1 == 1", out: true},
		{in: "1 > 2", out: false},
		{in: "1 < 2", out: true},
		// And or
		{in: "true && false", out: false},
		{in: "true && 1 < 2", out: true},
		{in: "false || 1 < 2", out: true},
		{in: "false || false", out: false},
		// None boolable
		{in: "env.UNKNOWN == 'true'", out: false},
		{in: "env.UNKNOWN", out: false},
		// Inline expressions
		{in: "env.SOME_TEXT", out: true},
		{in: "env.SOME_TEXT == 'text'", out: true},
		{in: "env.SOMETHING_TRUE == 'true'", out: true},
		{in: "env.SOMETHING_FALSE == 'true'", out: false},
		{in: "env.SOMETHING_TRUE", out: true},
		{in: "env.SOMETHING_FALSE", out: true},
		// TODO: This does not throw an error, because the evaluator does not know if the expression is inside ${{ }} or not
		// {in: "!env.SOMETHING_TRUE", wantErr: true},
		// {in: "!env.SOMETHING_FALSE", wantErr: true},
		{in: "${{ !env.SOMETHING_TRUE }}", out: false},
		{in: "${{ !env.SOMETHING_FALSE }}", out: false},
		{in: "${{ ! env.SOMETHING_TRUE }}", out: false},
		{in: "${{ ! env.SOMETHING_FALSE }}", out: false},
		{in: "${{ env.SOMETHING_TRUE }}", out: true},
		{in: "${{ env.SOMETHING_FALSE }}", out: true},
		{in: "${{ !env.SOMETHING_TRUE }}", out: false},
		{in: "${{ !env.SOMETHING_FALSE }}", out: false},
		{in: "${{ !env.SOMETHING_TRUE && true }}", out: false},
		{in: "${{ !env.SOMETHING_FALSE && true }}", out: false},
		{in: "${{ !env.SOMETHING_TRUE || true }}", out: true},
		{in: "${{ !env.SOMETHING_FALSE || false }}", out: false},
		{in: "${{ env.SOMETHING_TRUE && true }}", out: true},
		{in: "${{ env.SOMETHING_FALSE || true }}", out: true},
		{in: "${{ env.SOMETHING_FALSE || false }}", out: true},
		// TODO: This does not throw an error, because the evaluator does not know if the expression is inside ${{ }} or not
		// {in: "!env.SOMETHING_TRUE || true", wantErr: true},
		{in: "${{ env.SOMETHING_TRUE == 'true'}}", out: true},
		{in: "${{ env.SOMETHING_FALSE == 'true'}}", out: false},
		{in: "${{ env.SOMETHING_FALSE == 'false'}}", out: true},
		{in: "${{ env.SOMETHING_FALSE }} && ${{ env.SOMETHING_TRUE }}", out: true},

		// All together now
		{in: "false || env.SOMETHING_TRUE == 'true'", out: true},
		{in: "true || env.SOMETHING_FALSE == 'true'", out: true},
		{in: "true && env.SOMETHING_TRUE == 'true'", out: true},
		{in: "false && env.SOMETHING_TRUE == 'true'", out: false},
		{in: "env.SOMETHING_FALSE == 'true' && env.SOMETHING_TRUE == 'true'", out: false},
		{in: "env.SOMETHING_FALSE == 'true' && true", out: false},
		{in: "${{ env.SOMETHING_FALSE == 'true' }} && true", out: true},
		{in: "true && ${{ env.SOMETHING_FALSE == 'true' }}", out: true},
		// Check github context
		{in: "github.actor == 'nektos/act'", out: true},
		{in: "github.actor == 'unknown'", out: false},
		{in: "github.job == 'job1'", out: true},
		// The special ACT flag
		{in: "${{ env.ACT }}", out: true},
		{in: "${{ !env.ACT }}", out: false},
		// Invalid expressions should be reported
		{in: "INVALID_EXPRESSION", wantErr: true},
	}

	for _, table := range tables {
		table := table
		t.Run(table.in, func(t *testing.T) {
			assertObject := assert.New(t)
			b, err := EvalBool(context.Background(), rc.ExprEval, table.in, exprparser.DefaultStatusCheckSuccess)
			if table.wantErr {
				assertObject.Error(err)
			}

			assertObject.Equal(table.out, b, fmt.Sprintf("Expected %s to be %v, was %v", table.in, table.out, b))
		})
	}
}

func TestRunContext_GetBindsAndMounts(t *testing.T) {
	rctemplate := &RunContext{
		Name: "TestRCName",
		Run: &model.Run{
			Workflow: &model.Workflow{
				Name: "TestWorkflowName",
			},
		},
		Config: &Config{
			BindWorkdir: false,
		},
	}

	tests := []struct {
		windowsPath bool
		name        string
		rc          *RunContext
		wantbind    string
		wantmount   string
	}{
		{false, "/mnt/linux", rctemplate, "/mnt/linux", "/mnt/linux"},
		{false, "/mnt/path with spaces/linux", rctemplate, "/mnt/path with spaces/linux", "/mnt/path with spaces/linux"},
		{true, "C:\\Users\\TestPath\\MyTestPath", rctemplate, "/mnt/c/Users/TestPath/MyTestPath", "/mnt/c/Users/TestPath/MyTestPath"},
		{true, "C:\\Users\\Test Path with Spaces\\MyTestPath", rctemplate, "/mnt/c/Users/Test Path with Spaces/MyTestPath", "/mnt/c/Users/Test Path with Spaces/MyTestPath"},
		{true, "/LinuxPathOnWindowsShouldFail", rctemplate, "", ""},
	}

	isWindows := runtime.GOOS == "windows"

	for _, testcase := range tests {
		// pin for scopelint
		testcase := testcase
		for _, bindWorkDir := range []bool{true, false} {
			// pin for scopelint
			bindWorkDir := bindWorkDir
			testBindSuffix := ""
			if bindWorkDir {
				testBindSuffix = "Bind"
			}

			// Only run windows path tests on windows and non-windows on non-windows
			if (isWindows && testcase.windowsPath) || (!isWindows && !testcase.windowsPath) {
				t.Run((testcase.name + testBindSuffix), func(t *testing.T) {
					config := testcase.rc.Config
					config.Workdir = testcase.name
					config.BindWorkdir = bindWorkDir
					gotbind, gotmount := rctemplate.GetBindsAndMounts()

					// Name binds/mounts are either/or
					if config.BindWorkdir {
						fullBind := testcase.name + ":" + testcase.wantbind
						if runtime.GOOS == "darwin" {
							fullBind += ":delegated"
						}
						assert.Contains(t, gotbind, fullBind)
					} else {
						mountkey := testcase.rc.jobContainerName()
						assert.EqualValues(t, testcase.wantmount, gotmount[mountkey])
					}
				})
			}
		}
	}

	t.Run("ContainerVolumeMountTest", func(t *testing.T) {
		tests := []struct {
			name      string
			volumes   []string
			wantbind  string
			wantmount map[string]string
		}{
			{"BindAnonymousVolume", []string{"/volume"}, "/volume", map[string]string{}},
			{"BindHostFile", []string{"/path/to/file/on/host:/volume"}, "/path/to/file/on/host:/volume", map[string]string{}},
			{"MountExistingVolume", []string{"volume-id:/volume"}, "", map[string]string{"volume-id": "/volume"}},
		}

		for _, testcase := range tests {
			t.Run(testcase.name, func(t *testing.T) {
				job := &model.Job{}
				err := job.RawContainer.Encode(map[string][]string{
					"volumes": testcase.volumes,
				})
				assert.NoError(t, err)

				rc := &RunContext{
					Name: "TestRCName",
					Run: &model.Run{
						Workflow: &model.Workflow{
							Name: "TestWorkflowName",
						},
					},
					Config: &Config{
						BindWorkdir: false,
					},
				}
				rc.Run.JobID = "job1"
				rc.Run.Workflow.Jobs = map[string]*model.Job{"job1": job}

				gotbind, gotmount := rc.GetBindsAndMounts()

				if len(testcase.wantbind) > 0 {
					assert.Contains(t, gotbind, testcase.wantbind)
				}

				for k, v := range testcase.wantmount {
					assert.Contains(t, gotmount, k)
					assert.Equal(t, gotmount[k], v)
				}
			})
		}
	})
}

func TestRunContext_GetGitHubContext(t *testing.T) {
	log.SetLevel(log.DebugLevel)

	cwd, err := os.Getwd()
	assert.Nil(t, err)

	rc := &RunContext{
		Config: &Config{
			EventName: "push",
			Workdir:   cwd,
		},
		Run: &model.Run{
			Workflow: &model.Workflow{
				Name: "GitHubContextTest",
			},
		},
		Name:           "GitHubContextTest",
		CurrentStep:    "step",
		Matrix:         map[string]interface{}{},
		Env:            map[string]string{},
		ExtraPath:      []string{},
		StepResults:    map[string]*model.StepResult{},
		OutputMappings: map[MappableOutput]MappableOutput{},
	}
	rc.Run.JobID = "job1"

	ghc := rc.getGithubContext(context.Background())

	log.Debugf("%v", ghc)

	actor := "nektos/act"
	if a := os.Getenv("ACT_ACTOR"); a != "" {
		actor = a
	}

	repo := "forgejo/runner"
	if r := os.Getenv("ACT_REPOSITORY"); r != "" {
		repo = r
	}

	owner := "code.forgejo.org"
	if o := os.Getenv("ACT_OWNER"); o != "" {
		owner = o
	}

	assert.Equal(t, ghc.RunID, "1")
	assert.Equal(t, ghc.RunNumber, "1")
	assert.Equal(t, ghc.RetentionDays, "0")
	assert.Equal(t, ghc.Actor, actor)
	assert.True(t, strings.HasSuffix(ghc.Repository, repo))
	assert.Equal(t, ghc.RepositoryOwner, owner)
	assert.Equal(t, ghc.RunnerPerflog, "/dev/null")
	assert.Equal(t, ghc.Token, rc.Config.Secrets["GITHUB_TOKEN"])
	assert.Equal(t, ghc.Token, rc.Config.Secrets["FORGEJO_TOKEN"])
	assert.Equal(t, ghc.Job, "job1")
}

func TestRunContext_GetGithubContextRef(t *testing.T) {
	table := []struct {
		event string
		json  string
		ref   string
	}{
		{event: "push", json: `{"ref":"0000000000000000000000000000000000000000"}`, ref: "0000000000000000000000000000000000000000"},
		{event: "create", json: `{"ref":"0000000000000000000000000000000000000000"}`, ref: "0000000000000000000000000000000000000000"},
		{event: "workflow_dispatch", json: `{"ref":"0000000000000000000000000000000000000000"}`, ref: "0000000000000000000000000000000000000000"},
		{event: "delete", json: `{"repository":{"default_branch": "main"}}`, ref: "refs/heads/main"},
		{event: "pull_request", json: `{"number":123}`, ref: "refs/pull/123/merge"},
		{event: "pull_request_review", json: `{"number":123}`, ref: "refs/pull/123/merge"},
		{event: "pull_request_review_comment", json: `{"number":123}`, ref: "refs/pull/123/merge"},
		{event: "pull_request_target", json: `{"pull_request":{"base":{"ref": "main"}}}`, ref: "refs/heads/main"},
		{event: "deployment", json: `{"deployment": {"ref": "tag-name"}}`, ref: "tag-name"},
		{event: "deployment_status", json: `{"deployment": {"ref": "tag-name"}}`, ref: "tag-name"},
		{event: "release", json: `{"release": {"tag_name": "tag-name"}}`, ref: "refs/tags/tag-name"},
	}

	for _, data := range table {
		data := data
		t.Run(data.event, func(t *testing.T) {
			rc := &RunContext{
				EventJSON: data.json,
				Config: &Config{
					EventName: data.event,
					Workdir:   "",
				},
				Run: &model.Run{
					Workflow: &model.Workflow{
						Name: "GitHubContextTest",
					},
				},
			}

			ghc := rc.getGithubContext(context.Background())

			assert.Equal(t, data.ref, ghc.Ref)
		})
	}
}

func createIfTestRunContext(jobs map[string]*model.Job) *RunContext {
	rc := &RunContext{
		Config: &Config{
			Workdir: ".",
			Platforms: map[string]string{
				"ubuntu-latest": "ubuntu-latest",
			},
		},
		Env: map[string]string{},
		Run: &model.Run{
			JobID: "job1",
			Workflow: &model.Workflow{
				Name: "test-workflow",
				Jobs: jobs,
			},
		},
	}
	rc.ExprEval = rc.NewExpressionEvaluator(context.Background())

	return rc
}

func createJob(t *testing.T, input, result string) *model.Job {
	var job *model.Job
	err := yaml.Unmarshal([]byte(input), &job)
	assert.NoError(t, err)
	job.Result = result

	return job
}

func TestRunContext_RunsOnPlatformNames(t *testing.T) {
	log.SetLevel(log.DebugLevel)
	assertObject := assert.New(t)

	rc := createIfTestRunContext(map[string]*model.Job{
		"job1": createJob(t, `runs-on: ubuntu-latest`, ""),
	})
	assertObject.Equal([]string{"ubuntu-latest"}, rc.runsOnPlatformNames(context.Background()))

	rc = createIfTestRunContext(map[string]*model.Job{
		"job1": createJob(t, `runs-on: ${{ 'ubuntu-latest' }}`, ""),
	})
	assertObject.Equal([]string{"ubuntu-latest"}, rc.runsOnPlatformNames(context.Background()))

	rc = createIfTestRunContext(map[string]*model.Job{
		"job1": createJob(t, `runs-on: [self-hosted, my-runner]`, ""),
	})
	assertObject.Equal([]string{"self-hosted", "my-runner"}, rc.runsOnPlatformNames(context.Background()))

	rc = createIfTestRunContext(map[string]*model.Job{
		"job1": createJob(t, `runs-on: [self-hosted, "${{ 'my-runner' }}"]`, ""),
	})
	assertObject.Equal([]string{"self-hosted", "my-runner"}, rc.runsOnPlatformNames(context.Background()))

	rc = createIfTestRunContext(map[string]*model.Job{
		"job1": createJob(t, `runs-on: ${{ fromJSON('["ubuntu-latest"]') }}`, ""),
	})
	assertObject.Equal([]string{"ubuntu-latest"}, rc.runsOnPlatformNames(context.Background()))

	// test missing / invalid runs-on
	rc = createIfTestRunContext(map[string]*model.Job{
		"job1": createJob(t, `name: something`, ""),
	})
	assertObject.Equal([]string{}, rc.runsOnPlatformNames(context.Background()))

	rc = createIfTestRunContext(map[string]*model.Job{
		"job1": createJob(t, `runs-on:
  mapping: value`, ""),
	})
	assertObject.Equal([]string{}, rc.runsOnPlatformNames(context.Background()))

	rc = createIfTestRunContext(map[string]*model.Job{
		"job1": createJob(t, `runs-on: ${{ invalid expression }}`, ""),
	})
	assertObject.Equal([]string{}, rc.runsOnPlatformNames(context.Background()))
}

func TestRunContext_IsEnabled(t *testing.T) {
	log.SetLevel(log.DebugLevel)
	assertObject := assert.New(t)

	// success()
	rc := createIfTestRunContext(map[string]*model.Job{
		"job1": createJob(t, `runs-on: ubuntu-latest
if: success()`, ""),
	})
	assertObject.True(rc.isEnabled(context.Background()))

	rc = createIfTestRunContext(map[string]*model.Job{
		"job1": createJob(t, `runs-on: ubuntu-latest`, "failure"),
		"job2": createJob(t, `runs-on: ubuntu-latest
needs: [job1]
if: success()`, ""),
	})
	rc.Run.JobID = "job2"
	assertObject.False(rc.isEnabled(context.Background()))

	rc = createIfTestRunContext(map[string]*model.Job{
		"job1": createJob(t, `runs-on: ubuntu-latest`, "success"),
		"job2": createJob(t, `runs-on: ubuntu-latest
needs: [job1]
if: success()`, ""),
	})
	rc.Run.JobID = "job2"
	assertObject.True(rc.isEnabled(context.Background()))

	rc = createIfTestRunContext(map[string]*model.Job{
		"job1": createJob(t, `runs-on: ubuntu-latest`, "failure"),
		"job2": createJob(t, `runs-on: ubuntu-latest
if: success()`, ""),
	})
	rc.Run.JobID = "job2"
	assertObject.True(rc.isEnabled(context.Background()))

	// failure()
	rc = createIfTestRunContext(map[string]*model.Job{
		"job1": createJob(t, `runs-on: ubuntu-latest
if: failure()`, ""),
	})
	assertObject.False(rc.isEnabled(context.Background()))

	rc = createIfTestRunContext(map[string]*model.Job{
		"job1": createJob(t, `runs-on: ubuntu-latest`, "failure"),
		"job2": createJob(t, `runs-on: ubuntu-latest
needs: [job1]
if: failure()`, ""),
	})
	rc.Run.JobID = "job2"
	assertObject.True(rc.isEnabled(context.Background()))

	rc = createIfTestRunContext(map[string]*model.Job{
		"job1": createJob(t, `runs-on: ubuntu-latest`, "success"),
		"job2": createJob(t, `runs-on: ubuntu-latest
needs: [job1]
if: failure()`, ""),
	})
	rc.Run.JobID = "job2"
	assertObject.False(rc.isEnabled(context.Background()))

	rc = createIfTestRunContext(map[string]*model.Job{
		"job1": createJob(t, `runs-on: ubuntu-latest`, "failure"),
		"job2": createJob(t, `runs-on: ubuntu-latest
if: failure()`, ""),
	})
	rc.Run.JobID = "job2"
	assertObject.False(rc.isEnabled(context.Background()))

	// always()
	rc = createIfTestRunContext(map[string]*model.Job{
		"job1": createJob(t, `runs-on: ubuntu-latest
if: always()`, ""),
	})
	assertObject.True(rc.isEnabled(context.Background()))

	rc = createIfTestRunContext(map[string]*model.Job{
		"job1": createJob(t, `runs-on: ubuntu-latest`, "failure"),
		"job2": createJob(t, `runs-on: ubuntu-latest
needs: [job1]
if: always()`, ""),
	})
	rc.Run.JobID = "job2"
	assertObject.True(rc.isEnabled(context.Background()))

	rc = createIfTestRunContext(map[string]*model.Job{
		"job1": createJob(t, `runs-on: ubuntu-latest`, "success"),
		"job2": createJob(t, `runs-on: ubuntu-latest
needs: [job1]
if: always()`, ""),
	})
	rc.Run.JobID = "job2"
	assertObject.True(rc.isEnabled(context.Background()))

	rc = createIfTestRunContext(map[string]*model.Job{
		"job1": createJob(t, `runs-on: ubuntu-latest`, "success"),
		"job2": createJob(t, `runs-on: ubuntu-latest
if: always()`, ""),
	})
	rc.Run.JobID = "job2"
	assertObject.True(rc.isEnabled(context.Background()))

	rc = createIfTestRunContext(map[string]*model.Job{
		"job1": createJob(t, `uses: ./.github/workflows/reusable.yml`, ""),
	})
	assertObject.True(rc.isEnabled(context.Background()))

	rc = createIfTestRunContext(map[string]*model.Job{
		"job1": createJob(t, `uses: ./.github/workflows/reusable.yml
if: false`, ""),
	})
	assertObject.False(rc.isEnabled(context.Background()))
}

func TestRunContext_GetEnv(t *testing.T) {
	tests := []struct {
		description string
		rc          *RunContext
		targetEnv   string
		want        string
	}{
		{
			description: "Env from Config should overwrite",
			rc: &RunContext{
				Config: &Config{
					Env: map[string]string{"OVERWRITTEN": "true"},
				},
				Run: &model.Run{
					Workflow: &model.Workflow{
						Jobs: map[string]*model.Job{"test": {Name: "test"}},
						Env:  map[string]string{"OVERWRITTEN": "false"},
					},
					JobID: "test",
				},
			},
			targetEnv: "OVERWRITTEN",
			want:      "true",
		},
		{
			description: "No overwrite occurs",
			rc: &RunContext{
				Config: &Config{
					Env: map[string]string{"SOME_OTHER_VAR": "true"},
				},
				Run: &model.Run{
					Workflow: &model.Workflow{
						Jobs: map[string]*model.Job{"test": {Name: "test"}},
						Env:  map[string]string{"OVERWRITTEN": "false"},
					},
					JobID: "test",
				},
			},
			targetEnv: "OVERWRITTEN",
			want:      "false",
		},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			envMap := test.rc.GetEnv()
			assert.EqualValues(t, test.want, envMap[test.targetEnv])
		})
	}
}

func TestRunContext_CreateSimpleContainerName(t *testing.T) {
	tests := []struct {
		parts []string
		want  string
	}{
		{
			parts: []string{"a--a", "BB正", "c-C"},
			want:  "a-a_BB_c-C",
		},
		{
			parts: []string{"a-a", "", "-"},
			want:  "a-a",
		},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.parts, " "), func(t *testing.T) {
			assert.Equalf(t, tt.want, createSimpleContainerName(tt.parts...), "createSimpleContainerName(%v)", tt.parts)
		})
	}
}

func TestRunContext_SanitizeNetworkAlias(t *testing.T) {
	same := "same"
	assert.Equal(t, same, sanitizeNetworkAlias(context.Background(), same))
	original := "or.igin'A-L"
	sanitized := "or_igin_a-l"
	assert.Equal(t, sanitized, sanitizeNetworkAlias(context.Background(), original))
}

func TestRunContext_PrepareJobContainer(t *testing.T) {
	yaml := `
on:
  push:

jobs:
  job:
    runs-on: docker
    container:
      image: some:image
      credentials:
        username: containerusername
        password: containerpassword
    services:
      service1:
        image: service1:image
        credentials:
          username: service1username
          password: service1password
      service2:
        image: service2:image
        credentials:
          username: service2username
          password: service2password
    steps:
      - run: echo ok
`
	workflow, err := model.ReadWorkflow(strings.NewReader(yaml), true)
	require.NoError(t, err)

	testCases := []struct {
		name   string
		step   actionStep
		inputs []container.NewContainerInput
	}{
		{
			name: "Overlapping",
			step: &stepActionRemote{
				Step: &model.Step{
					Uses: "org/repo/path@ref",
				},
				RunContext: &RunContext{
					Config: &Config{
						Workdir: "/my/workdir",
					},
					Run: &model.Run{
						JobID:    "job",
						Workflow: workflow,
					},
				},
				env: map[string]string{},
			},
			inputs: []container.NewContainerInput{
				{
					Name:       "WORKFLOW-e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855_JOB",
					Image:      "some:image",
					Username:   "containerusername",
					Password:   "containerpassword",
					Entrypoint: []string{"tail", "-f", "/dev/null"},
					Cmd:        nil,
					WorkingDir: "/my/workdir",
					Env:        []string{},
					ToolCache:  "/opt/hostedtoolcache",
					Binds:      []string{"/var/run/docker.sock:/var/run/docker.sock"},
					Mounts: map[string]string{
						"WORKFLOW-e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855_JOB":     "/my/workdir",
						"WORKFLOW-e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855_JOB-env": "/var/run/act",
					},
					NetworkMode:    "WORKFLOW-e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855_JOB-job-network",
					Privileged:     false,
					UsernsMode:     "",
					Platform:       "",
					NetworkAliases: []string{""},
					ExposedPorts:   nil,
					PortBindings:   nil,
					ConfigOptions:  "",
					JobOptions:     "",
					AutoRemove:     false,
					ValidVolumes: []string{
						"WORKFLOW-e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855_JOB",
						"WORKFLOW-e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855_JOB-env",
						"/var/run/docker.sock",
					},
				},
				{
					Name:           "WORKFLOW-e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca49599-fe7f4c0058dbd2161ebe4aafa71cd83bd96ee19d3ca8043d5e4bc477a664a80c",
					Image:          "service1:image",
					Username:       "service1username",
					Password:       "service1password",
					Entrypoint:     nil,
					Cmd:            []string{},
					WorkingDir:     "",
					Env:            []string{},
					ToolCache:      "/opt/hostedtoolcache",
					Binds:          []string{"/var/run/docker.sock:/var/run/docker.sock"},
					Mounts:         map[string]string{},
					NetworkMode:    "WORKFLOW-e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855_JOB-job-network",
					Privileged:     false,
					UsernsMode:     "",
					Platform:       "",
					NetworkAliases: []string{"service1"},
					ExposedPorts:   nat.PortSet{},
					PortBindings:   nat.PortMap{},
					ConfigOptions:  "",
					JobOptions:     "",
					AutoRemove:     false,
					ValidVolumes: []string{
						"WORKFLOW-e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855_JOB",
						"WORKFLOW-e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855_JOB-env",
						"/var/run/docker.sock",
					},
				},
				{
					Name:           "WORKFLOW-e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca49599-c233cf913e1d0c90cc1404ee09917e625f9cb82156ca3d7cb10b729d563728ea",
					Image:          "service2:image",
					Username:       "service2username",
					Password:       "service2password",
					Entrypoint:     nil,
					Cmd:            []string{},
					WorkingDir:     "",
					Env:            []string{},
					ToolCache:      "/opt/hostedtoolcache",
					Binds:          []string{"/var/run/docker.sock:/var/run/docker.sock"},
					Mounts:         map[string]string{},
					NetworkMode:    "WORKFLOW-e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855_JOB-job-network",
					Privileged:     false,
					UsernsMode:     "",
					Platform:       "",
					NetworkAliases: []string{"service2"},
					ExposedPorts:   nat.PortSet{},
					PortBindings:   nat.PortMap{},
					ConfigOptions:  "",
					JobOptions:     "",
					AutoRemove:     false,
					ValidVolumes: []string{
						"WORKFLOW-e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855_JOB",
						"WORKFLOW-e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855_JOB-env",
						"/var/run/docker.sock",
					},
				},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			containerInputs := make([]container.NewContainerInput, 0, 5)
			newContainer := container.NewContainer
			defer testutils.MockVariable(&container.NewContainer, func(input *container.NewContainerInput) container.ExecutionsEnvironment {
				c := *input
				c.Stdout = nil
				c.Stderr = nil
				c.Env = []string{}
				containerInputs = append(containerInputs, c)
				return newContainer(input)
			})()

			ctx := context.Background()
			rc := testCase.step.getRunContext()
			rc.ExprEval = rc.NewExpressionEvaluator(ctx)

			require.NoError(t, rc.prepareJobContainer(ctx))
			slices.SortFunc(containerInputs, func(a, b container.NewContainerInput) int { return cmp.Compare(a.Username, b.Username) })
			for i := 0; i < len(containerInputs); i++ {
				assert.EqualValues(t, testCase.inputs[i], containerInputs[i], containerInputs[i].Username)
			}
		})
	}
}

type waitForServiceContainerMock struct {
	mock.Mock
	container.Container
	container.LinuxContainerEnvironmentExtensions
}

func (o *waitForServiceContainerMock) IsHealthy(ctx context.Context) (time.Duration, error) {
	args := o.Called(ctx)
	return args.Get(0).(time.Duration), args.Error(1)
}

func Test_waitForServiceContainer(t *testing.T) {
	t.Run("Wait", func(t *testing.T) {
		m := &waitForServiceContainerMock{}
		ctx := context.Background()
		mock.InOrder(
			m.On("IsHealthy", ctx).Return(1*time.Millisecond, nil).Once(),
			m.On("IsHealthy", ctx).Return(time.Duration(0), nil).Once(),
		)
		require.NoError(t, waitForServiceContainer(ctx, m))
		m.AssertExpectations(t)
	})

	t.Run("Cancel", func(t *testing.T) {
		m := &waitForServiceContainerMock{}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		m.On("IsHealthy", ctx).Return(1*time.Millisecond, nil).Once()
		require.NoError(t, waitForServiceContainer(ctx, m))
		m.AssertExpectations(t)
	})

	t.Run("Error", func(t *testing.T) {
		m := &waitForServiceContainerMock{}
		ctx := context.Background()
		m.On("IsHealthy", ctx).Return(time.Duration(0), errors.New("ERROR"))
		require.ErrorContains(t, waitForServiceContainer(ctx, m), "ERROR")
		m.AssertExpectations(t)
	})
}
