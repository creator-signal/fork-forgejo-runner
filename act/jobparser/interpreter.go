package jobparser

import (
	"code.forgejo.org/forgejo/runner/v11/act/exprparser"
	"code.forgejo.org/forgejo/runner/v11/act/model"
	"go.yaml.in/yaml/v3"
)

func NewInterpreter(
	jobID string,
	job *model.Job,
	matrix map[string]any,
	gitCtx *model.GithubContext,
	results map[string]*JobResult,
	vars map[string]string,
	inputs map[string]any,
) exprparser.Interpreter {
	strategy := make(map[string]any)
	if job.Strategy != nil {
		strategy["fail-fast"] = job.Strategy.FailFast
		strategy["max-parallel"] = job.Strategy.MaxParallel
	}

	run := &model.Run{
		Workflow: &model.Workflow{
			Jobs: map[string]*model.Job{},
		},
		JobID: jobID,
	}
	for id, result := range results {
		need := yaml.Node{}
		_ = need.Encode(result.Needs)
		run.Workflow.Jobs[id] = &model.Job{
			RawNeeds: need,
			Result:   result.Result,
			Outputs:  result.Outputs,
		}
	}

	jobs := run.Workflow.Jobs
	jobNeeds := run.Job().Needs()

	using := map[string]exprparser.Needs{}
	for _, need := range jobNeeds {
		if v, ok := jobs[need]; ok {
			using[need] = exprparser.Needs{
				Outputs: v.Outputs,
				Result:  v.Result,
			}
		}
	}

	ee := &exprparser.EvaluationEnvironment{
		Github:   gitCtx,
		Env:      nil, // no need
		Job:      nil, // no need
		Steps:    nil, // no need
		Runner:   nil, // no need
		Secrets:  nil, // no need
		Strategy: strategy,
		Matrix:   matrix,
		Needs:    using,
		Inputs:   inputs,
		Vars:     vars,
	}

	config := exprparser.Config{
		Run:        run,
		WorkingDir: "", // WorkingDir is used for  the function hashFiles, but it's not needed in the server
		Context:    "job",
	}

	return exprparser.NewInterpreter(ee, config)
}

// Returns an interpreter used in the server in the context of workflow-level templates. Needs github, inputs, and vars
// context only.
func NewWorkflowInterpreter(
	gitCtx *model.GithubContext,
	vars map[string]string,
	inputs map[string]any,
) exprparser.Interpreter {
	ee := &exprparser.EvaluationEnvironment{
		Github:   gitCtx,
		Env:      nil, // no need
		Job:      nil, // no need
		Steps:    nil, // no need
		Runner:   nil, // no need
		Secrets:  nil, // no need
		Strategy: nil, // no need
		Matrix:   nil, // no need
		Needs:    nil, // no need
		Inputs:   inputs,
		Vars:     vars,
	}

	config := exprparser.Config{
		Run:        nil,
		WorkingDir: "", // WorkingDir is used for the function hashFiles, but it's not needed in the server
		Context:    "workflow",
	}

	return exprparser.NewInterpreter(ee, config)
}

// JobResult is the minimum requirement of job results for Interpreter
type JobResult struct {
	Needs   []string
	Result  string
	Outputs map[string]string
}
