package jobparser

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/rhysd/actionlint"
	"go.yaml.in/yaml/v3"

	"code.forgejo.org/forgejo/runner/v12/act/exprparser"
	"code.forgejo.org/forgejo/runner/v12/act/model"
)

var ErrUnsupportedReusableWorkflowFetch = errors.New("unable to support reusable workflow fetch")

// utility structure as we're working with the vague job definitions in jobparser, and the more complete ones from
// act/model
type bothJobTypes struct {
	id           string
	jobParserJob *Job
	workflowJob  *model.Job

	matrix   map[string]any
	jobNeeds []string

	overrideOnClause *yaml.Node

	withInvalidJobReference    *exprparser.InvalidJobOutputReferencedError
	withInvalidMatrixReference *exprparser.InvalidMatrixDimensionReferencedError
	internalIncompleteState    *SingleWorkflow
	workflowCallInputs         map[string]any
}

func Parse(content []byte, validate bool, options ...ParseOption) ([]*SingleWorkflow, error) {
	workflow := &SingleWorkflow{}
	if err := yaml.Unmarshal(content, workflow); err != nil {
		return nil, fmt.Errorf("yaml.Unmarshal: %w", err)
	}

	origin, err := model.ReadWorkflow(bytes.NewReader(content), validate)
	if err != nil {
		return nil, fmt.Errorf("model.ReadWorkflow: %w", err)
	}

	pc := &parseContext{}
	for _, o := range options {
		o(pc)
	}
	if pc.recursionDepth > 5 {
		return nil, fmt.Errorf("failed to parse workflow due to exceeding the workflow recursion limit (5)")
	}
	if workflow.IncompleteRecursionDepth != 0 {
		pc.recursionDepth = workflow.IncompleteRecursionDepth
	}

	results := map[string]*JobResult{}
	for id, job := range origin.Jobs {
		results[id] = &JobResult{
			Needs:   job.Needs(),
			Result:  pc.jobResults[id],
			Outputs: pc.jobOutputs[id],
		}
	}
	// See documentation on `WithWorkflowNeeds` for why we do this:
	for _, id := range pc.workflowNeeds {
		results[id] = &JobResult{
			Result:  pc.jobResults[id],
			Outputs: pc.jobOutputs[id],
		}
	}
	incompleteMatrix := make(map[string]*exprparser.InvalidJobOutputReferencedError) // map job id -> incomplete matrix reason
	for id, job := range origin.Jobs {
		if job.Strategy != nil {
			jobNeeds := pc.workflowNeeds
			if jobNeeds == nil {
				jobNeeds = job.Needs()
			}
			matrixEvaluator := NewExpressionEvaluator(NewInterpreter(id, job, nil, pc.gitContext, results, pc.vars, pc.inputs, exprparser.InvalidJobOutput, jobNeeds))
			if err := matrixEvaluator.EvaluateYamlNode(&job.Strategy.RawMatrix); err != nil {
				// IncompleteMatrix tagging is only supported when `WithJobOutputs()` is used as an option, in order to
				// maintain jobparser's backwards compatibility.
				var perr *exprparser.InvalidJobOutputReferencedError
				if pc.jobOutputs != nil && errors.As(err, &perr) {
					incompleteMatrix[id] = perr
				} else {
					return nil, fmt.Errorf("failure to evaluate strategy.matrix on job %s: %w", job.Name, err)
				}
			}
		}
	}

	ids, jobParserJobs, err := workflow.jobs()
	if err != nil {
		return nil, fmt.Errorf("invalid jobs: %w", err)
	}

	preMatrixJobs := make([]*bothJobTypes, len(ids))
	for i, jobName := range ids {
		preMatrixJobs[i] = &bothJobTypes{
			id:           jobName,
			jobParserJob: jobParserJobs[i],
			workflowJob:  origin.GetJob(jobName),
		}
	}

	// Expand `strategy.matrix` into multiple jobs:
	postMatrixJobs, err := expandMatrixJobs(preMatrixJobs, incompleteMatrix, pc, results)
	if err != nil {
		return nil, fmt.Errorf("failure to expand matrix jobs: %w", err)
	}

	// Expand reusable workflows `uses:...` into inner jobs:
	if pc.localWorkflowFetcher != nil || pc.remoteWorkflowFetcher != nil {
		newJobs, err := expandReusableWorkflows(postMatrixJobs, validate, incompleteMatrix, options, pc, results)
		if err != nil {
			return nil, err
		}
		postMatrixJobs = append(postMatrixJobs, newJobs...)
	}

	var ret []*SingleWorkflow
	for _, bothJobs := range postMatrixJobs {
		id := bothJobs.id
		job := bothJobs.jobParserJob
		workflowJob := bothJobs.workflowJob
		matrix := bothJobs.matrix
		jobNeeds := bothJobs.jobNeeds

		evaluator := NewExpressionEvaluator(NewInterpreter(id, workflowJob, matrix, pc.gitContext, results, pc.vars, pc.inputs, 0, jobNeeds))

		var runsOnInvalidJobReference *exprparser.InvalidJobOutputReferencedError
		var runsOnInvalidMatrixReference *exprparser.InvalidMatrixDimensionReferencedError
		var runsOn []string
		if pc.supportIncompleteRunsOn {
			evaluatorOutputAware := NewExpressionEvaluator(NewInterpreter(id, workflowJob, matrix, pc.gitContext, results, pc.vars, pc.inputs, exprparser.InvalidJobOutput|exprparser.InvalidMatrixDimension, jobNeeds))
			rawRunsOn := workflowJob.RawRunsOn
			// Evaluate the entire `runs-on` node at once, which permits behavior like `runs-on: ${{ fromJSON(...) }}`
			// where it can generate an array
			err = evaluatorOutputAware.EvaluateYamlNode(&rawRunsOn)
			if err != nil {
				// Store error and we'll use it to tag `IncompleteRunsOn`
				errors.As(err, &runsOnInvalidJobReference)
				errors.As(err, &runsOnInvalidMatrixReference)
			}
			runsOn = model.FlattenRunsOnNode(rawRunsOn)
		} else {
			// Legacy behaviour; run interpolator on each individual entry in the `runsOn` array without support for
			// `IncompleteRunsOn` detection:
			runsOn = workflowJob.RunsOn()
			for i, v := range runsOn {
				runsOn[i] = evaluator.Interpolate(v)
			}
		}

		job.RawRunsOn = encodeRunsOn(runsOn)
		swf := &SingleWorkflow{
			Name:               workflow.Name,
			RawOn:              workflow.RawOn,
			Env:                workflow.Env,
			Defaults:           workflow.Defaults,
			WorkflowCallInputs: bothJobs.workflowCallInputs,
		}
		if bothJobs.overrideOnClause != nil {
			swf.RawOn = *bothJobs.overrideOnClause
		}
		if refErr := incompleteMatrix[id]; refErr != nil {
			swf.IncompleteMatrix = true
			swf.IncompleteMatrixNeeds = &IncompleteNeeds{
				Job:    refErr.JobID,
				Output: refErr.OutputName,
			}
		}
		if runsOnInvalidJobReference != nil {
			swf.IncompleteRunsOn = true
			swf.IncompleteRunsOnNeeds = &IncompleteNeeds{
				Job:    runsOnInvalidJobReference.JobID,
				Output: runsOnInvalidJobReference.OutputName,
			}
		}
		if runsOnInvalidMatrixReference != nil {
			swf.IncompleteRunsOn = true
			swf.IncompleteRunsOnMatrix = &IncompleteMatrix{
				Dimension: runsOnInvalidMatrixReference.Dimension,
			}
		}
		if bothJobs.withInvalidJobReference != nil {
			swf.IncompleteWith = true
			swf.IncompleteWithNeeds = &IncompleteNeeds{
				Job:    bothJobs.withInvalidJobReference.JobID,
				Output: bothJobs.withInvalidJobReference.OutputName,
			}
		}
		if bothJobs.withInvalidMatrixReference != nil {
			swf.IncompleteWith = true
			swf.IncompleteWithMatrix = &IncompleteMatrix{
				Dimension: bothJobs.withInvalidMatrixReference.Dimension,
			}
		}
		// If a job was noticed as incomplete during reusable workflow expansion, that information would be lost in the
		// the new `SingleWorkflow` that we're creating here -- so preserve that state that was discovered in a previous
		// recursion of parsing:
		if bothJobs.internalIncompleteState != nil {
			if bothJobs.internalIncompleteState.IncompleteMatrix && !swf.IncompleteMatrix {
				swf.IncompleteMatrix = true
				swf.IncompleteMatrixNeeds = bothJobs.internalIncompleteState.IncompleteMatrixNeeds
			}
			if bothJobs.internalIncompleteState.IncompleteRunsOn && !swf.IncompleteRunsOn {
				swf.IncompleteRunsOn = true
				swf.IncompleteRunsOnMatrix = bothJobs.internalIncompleteState.IncompleteRunsOnMatrix
				swf.IncompleteRunsOnNeeds = bothJobs.internalIncompleteState.IncompleteRunsOnNeeds
			}
			if bothJobs.internalIncompleteState.IncompleteWith && !swf.IncompleteWith {
				swf.IncompleteWith = true
				swf.IncompleteWithMatrix = bothJobs.internalIncompleteState.IncompleteWithMatrix
				swf.IncompleteWithNeeds = bothJobs.internalIncompleteState.IncompleteWithNeeds
			}
		}
		if swf.IncompleteMatrix || swf.IncompleteRunsOn || swf.IncompleteWith {
			if bothJobs.internalIncompleteState != nil {
				swf.IncompleteRecursionDepth = bothJobs.internalIncompleteState.IncompleteRecursionDepth
			} else {
				swf.IncompleteRecursionDepth = pc.recursionDepth
			}
		}
		if err := swf.SetJob(id, job); err != nil {
			return nil, fmt.Errorf("SetJob: %w", err)
		}
		ret = append(ret, swf)
	}
	return ret, nil
}

func expandMatrixJobs(jobs []*bothJobTypes, incompleteMatrix map[string]*exprparser.InvalidJobOutputReferencedError, pc *parseContext, results map[string]*JobResult) ([]*bothJobTypes, error) {
	retval := make([]*bothJobTypes, 0, len(jobs))

	for _, bothJobs := range jobs {
		id := bothJobs.id
		jobParserJob := bothJobs.jobParserJob
		workflowJob := bothJobs.workflowJob

		jobNeeds := pc.workflowNeeds
		if jobNeeds == nil {
			jobNeeds = jobParserJob.Needs()
		}

		matricxes, err := getMatrixes(workflowJob)
		if err != nil {
			return nil, fmt.Errorf("getMatrixes: %w", err)
		}
		if incompleteMatrix[id] != nil {
			// If this job is IncompleteMatrix, then ensure that the matrices for the job are undefined.  Otherwise if
			// there's an array like `[value1, ${{ needs... }}]` then multiple IncompleteMatrix jobs will be emitted.
			matricxes = []map[string]any{{}}
		}
		for _, matrix := range matricxes {
			job := jobParserJob.Clone()
			evaluator := NewExpressionEvaluator(NewInterpreter(id, workflowJob, matrix, pc.gitContext, results, pc.vars, pc.inputs, 0, jobNeeds))

			if incompleteMatrix[id] != nil {
				// Preserve the original incomplete `matrix` value so that when the `IncompleteMatrix` state is
				// discovered later, it can be expanded.
				job.Strategy.RawMatrix = workflowJob.Strategy.RawMatrix
			} else {
				job.Strategy.RawMatrix = encodeMatrix(matrix)
			}

			// If we're IncompleteMatrix, don't compute the job name -- this will allow it to remain blank and be
			// computed when the matrix is expanded in a future reparse.
			if incompleteMatrix[id] == nil {
				if job.Name == "" {
					job.Name = nameWithMatrix(id, matrix)
				} else if strings.HasSuffix(job.Name, " (incomplete matrix)") {
					job.Name = nameWithMatrix(strings.TrimSuffix(job.Name, " (incomplete matrix)"), matrix)
				} else {
					job.Name = evaluator.Interpolate(job.Name)
				}
			} else {
				if job.Name == "" {
					job.Name = nameWithMatrix(id, matrix) + " (incomplete matrix)"
				} else {
					job.Name = evaluator.Interpolate(job.Name) + " (incomplete matrix)"
				}
			}

			retval = append(retval, &bothJobTypes{
				id:               id,
				jobParserJob:     job,
				workflowJob:      workflowJob,
				matrix:           matrix,
				jobNeeds:         jobNeeds,
				overrideOnClause: bothJobs.overrideOnClause,
			})
		}
	}

	return retval, nil
}

func expandReusableWorkflows(jobs []*bothJobTypes, validate bool, incompleteMatrix map[string]*exprparser.InvalidJobOutputReferencedError, options []ParseOption, pc *parseContext, jobResults map[string]*JobResult) ([]*bothJobTypes, error) {
	retval := []*bothJobTypes{}
	for _, bothJobs := range jobs {
		if _, incomplete := incompleteMatrix[bothJobs.id]; incomplete {
			// Don't attempt to expand a reusable workflow when the caller doesn't have a fully evaluated matrix yet.
			continue
		}

		workflowJob := bothJobs.workflowJob

		jobType, err := workflowJob.Type()
		if err != nil {
			return nil, err
		}
		var reusableWorkflow []byte
		if jobType == model.JobTypeReusableWorkflowLocal && pc.localWorkflowFetcher != nil {
			contents, err := pc.localWorkflowFetcher(workflowJob.Uses)
			if err != nil {
				if errors.Is(err, ErrUnsupportedReusableWorkflowFetch) {
					// Skip workflow expansion.
					continue
				}
				return nil, fmt.Errorf("unable to read local workflow %q: %w", workflowJob.Uses, err)
			}
			reusableWorkflow = contents
		}
		if jobType == model.JobTypeReusableWorkflowRemote && pc.remoteWorkflowFetcher != nil {
			parsed, err := model.ParseRemoteReusableWorkflow(workflowJob.Uses)
			if err != nil {
				return nil, fmt.Errorf("unable to parse `uses: %q` as a valid reusable workflow: %w", workflowJob.Uses, err)
			}
			contents, err := pc.remoteWorkflowFetcher(parsed)
			if err != nil {
				if errors.Is(err, ErrUnsupportedReusableWorkflowFetch) {
					// Skip workflow expansion.
					continue
				}
				return nil, fmt.Errorf("unable to read remote workflow %q: %w", workflowJob.Uses, err)
			}
			reusableWorkflow = contents
		}
		if reusableWorkflow != nil {
			// If we encounter an InvalidJobOutputReferencedError error, we'll know that this is caused by a `with:`
			// clause referencing a job output that isn't present yet.  In this case, don't expand the job, but provide
			// the error back in `bothJobTypes` so that it can be returned in the `SingleWorkflow`.
			var withInvalidJobReference *exprparser.InvalidJobOutputReferencedError
			// Same type of error, but for accessing an invalid matrix during `with:`.
			var withInvalidMatrixReference *exprparser.InvalidMatrixDimensionReferencedError

			newJobs, err := expandReusableWorkflow(reusableWorkflow, validate, options, pc, jobResults, bothJobs.matrix, bothJobs)
			if err != nil {
				errors.As(err, &withInvalidJobReference)
				errors.As(err, &withInvalidMatrixReference)
				if withInvalidJobReference == nil && withInvalidMatrixReference == nil {
					return nil, fmt.Errorf("error expanding reusable workflow %q: %v", workflowJob.Uses, err)
				}
			}

			if withInvalidJobReference == nil && withInvalidMatrixReference == nil {
				// Append the inner jobs' IDs to the `needs` of the parent job.
				additionalNeeds := make([]string, len(newJobs))
				for i, b := range newJobs {
					additionalNeeds[i] = b.id
				}
				callerNeeds := bothJobs.jobParserJob.Needs()
				callerNeeds = append(callerNeeds, additionalNeeds...)
				_ = bothJobs.jobParserJob.RawNeeds.Encode(callerNeeds)

				// The calling job will still exist in order to act as a `sentinel` for `needs` job ordering & output
				// access. There may be some need for specialized detection of this case on the Forgejo side, but at the
				// moment we'll just mark it as a job with `if: false` and remove the `uses: ...` to ensure that it never
				// gets executed as its own reusable workflow.
				_ = bothJobs.jobParserJob.If.Encode(false)
				bothJobs.jobParserJob.Uses = ""
				bothJobs.jobParserJob.With = nil
			} else {
				// Retain all the original data of the job until it's really expanded, with its inputs, later
				bothJobs.withInvalidJobReference = withInvalidJobReference
				bothJobs.withInvalidMatrixReference = withInvalidMatrixReference
			}

			retval = append(retval, newJobs...)
		}
	}
	return retval, nil
}

func expandReusableWorkflow(contents []byte, validate bool, options []ParseOption, pc *parseContext, jobResults map[string]*JobResult, matrix map[string]any, callerJob *bothJobTypes) ([]*bothJobTypes, error) {
	innerParseOptions := append([]ParseOption{}, options...) // copy original slice
	innerParseOptions = append(innerParseOptions, withRecursionDepth(pc.recursionDepth+1))

	workflow, err := model.ReadWorkflow(bytes.NewReader(contents), validate)
	if err != nil {
		return nil, fmt.Errorf("model.ReadWorkflow: %w", err)
	}

	// Compute the inputs to the workflow call from `callerJob`'s `with` clause.  There are two outputs from this
	// calculation; one is the raw inputs which are passed to the next jobparser to expand the reusable workflow,
	// allowing things like `runs-on` to be populated if they directly reference an input (that's `WithInputs` below).
	// The second output is a rebuilt version of the `on.workflow_call` clause of the job which is returned in the
	// `SingleWorkflow` from the expansion, and the inputs in this clause should be used when this job is later executed
	// in order to fill in any other `${{ inputs... }}` evaluations in the jobs.
	inputs, rebuiltOn, err := evaluateReusableWorkflowInputs(workflow, pc, jobResults, matrix, callerJob)
	if err != nil {
		return nil, fmt.Errorf("failure to evaluate workflow inputs: %w", err)
	}
	// due to parse options being applied in-order, this will replace the calling job's inputs (if provided) with the
	// inputs of the workflow call:
	innerParseOptions = append(innerParseOptions, WithInputs(inputs))
	// After all the jobs in a reusable workflow are done, the outputs need to be evaluated, and `${{ inputs... }}` is a
	// valid context when evaluating them.  Store a copy of the inputs on the caller job for that evaluation.
	callerJob.workflowCallInputs = inputs

	err = migrateReusableWorkflowOutputs(workflow, callerJob)
	if err != nil {
		return nil, fmt.Errorf("failure to migrate workflow outputs: %w", err)
	}

	innerWorkflows, err := Parse(contents, validate, innerParseOptions...)
	if err != nil {
		return nil, fmt.Errorf("unable to parse local workflow: %w", err)
	}
	retval := []*bothJobTypes{}
	for _, swf := range innerWorkflows {
		err := rewriteReusableWorkflowNeeds(&swf.RawJobs, callerJob.id)
		if err != nil {
			return nil, fmt.Errorf("unable to rewrite reusable workflow needs: %w", err)
		}

		id, job := swf.Job()
		content, err := swf.Marshal()
		if err != nil {
			return nil, fmt.Errorf("unable to marshal SingleWorkflow: %w", err)
		}

		workflow, err := model.ReadWorkflow(bytes.NewReader(content), validate)
		if err != nil {
			return nil, fmt.Errorf("model.ReadWorkflow: %w", err)
		}

		originalNeeds := job.Needs()
		newNeeds := make([]string, len(originalNeeds))
		// Rewrite the `needs` of the job to be qualified within the job so they depend upon each other.
		for i := range originalNeeds {
			newNeeds[i] = fmt.Sprintf("%s.%s", callerJob.id, originalNeeds[i])
		}
		// Add all the jobs that the reusable workflow `needs`'d to the jobs within the reusable workflow as well:
		newNeeds = append(newNeeds, callerJob.jobParserJob.Needs()...)
		if len(newNeeds) != 0 {
			err = job.RawNeeds.Encode(newNeeds)
			if err != nil {
				return nil, fmt.Errorf("error encoding newNeeds to yaml: %w", err)
			}
		}

		newEntry := &bothJobTypes{
			id:               fmt.Sprintf("%s.%s", callerJob.id, id),
			jobParserJob:     job,
			workflowJob:      workflow.GetJob(id),
			overrideOnClause: rebuiltOn,
		}
		if swf.IncompleteMatrix || swf.IncompleteRunsOn || swf.IncompleteWith {
			newEntry.internalIncompleteState = swf
			// if we have a reference to a job stored in the incomplete state, then qualify that job name:
			if swf.IncompleteMatrixNeeds != nil {
				swf.IncompleteMatrixNeeds.Job = fmt.Sprintf("%s.%s", callerJob.id, swf.IncompleteMatrixNeeds.Job)
			}
			if swf.IncompleteRunsOnNeeds != nil {
				swf.IncompleteRunsOnNeeds.Job = fmt.Sprintf("%s.%s", callerJob.id, swf.IncompleteRunsOnNeeds.Job)
			}
			if swf.IncompleteWithNeeds != nil {
				swf.IncompleteWithNeeds.Job = fmt.Sprintf("%s.%s", callerJob.id, swf.IncompleteWithNeeds.Job)
			}
		}
		retval = append(retval, newEntry)
	}
	return retval, nil
}

func evaluateReusableWorkflowInputs(workflow *model.Workflow, pc *parseContext, jobResults map[string]*JobResult, matrix map[string]any, callerJob *bothJobTypes) (map[string]any, *yaml.Node, error) {
	jobNeeds := pc.workflowNeeds
	if jobNeeds == nil {
		jobNeeds = callerJob.jobParserJob.Needs()
	}

	// For evaluating on the caller side's `with` fields, expected contexts to be available: env, forgejo, inputs, job,
	// matrix, needs, runner, secrets, steps, strategy, vars
	callerEvaluator := NewExpressionEvaluator(NewInterpreter(callerJob.id, callerJob.workflowJob, matrix, pc.gitContext,
		jobResults, pc.vars, pc.inputs, exprparser.InvalidJobOutput|exprparser.InvalidMatrixDimension, jobNeeds))

	// For evaluating on the reusable workflow's side, with `on.workflow_call.inputs.<input_name>.default`, expected
	// contexts to be available: forgejo, vars
	reusableEvaluator := NewExpressionEvaluator(NewInterpreter(callerJob.id, callerJob.workflowJob, nil, pc.gitContext,
		nil, pc.vars, nil, exprparser.InvalidJobOutput|exprparser.InvalidMatrixDimension, nil))

	workflowConfig := workflow.WorkflowCallConfig()
	withInput := callerJob.workflowJob.With

	retval := make(map[string]any)

	for name, input := range workflowConfig.Inputs {
		value := withInput[name]

		if value != nil {
			node := yaml.Node{}
			err := node.Encode(value)
			if err != nil {
				return nil, nil, fmt.Errorf("unable to yaml encode value for input %q: %w", name, err)
			}
			err = callerEvaluator.EvaluateYamlNode(&node)
			if err != nil {
				return nil, nil, fmt.Errorf("unable to evaluate expression for input %q: %w", name, err)
			}
			err = node.Decode(&value)
			if err != nil {
				return nil, nil, fmt.Errorf("unable to yaml decode value for input %q: %w", name, err)
			}
		}

		if value == nil {
			def := input.Default
			err := reusableEvaluator.EvaluateYamlNode(&def)
			if err != nil {
				return nil, nil, fmt.Errorf("unable to evaluate expression for default value of input %q: %w", name, err)
			}
			err = def.Decode(&value)
			if err != nil {
				return nil, nil, fmt.Errorf("unable to yaml decode value for default value of input %q: %w", name, err)
			}
		}

		retval[name] = value
	}

	if len(retval) == 0 {
		// Don't bother rebuild the `on.workflow_call` for no inputs.
		return retval, nil, nil
	}

	// `retval` contains the evaluated inputs which are ready to be used in re-parsing this workflow.  But later when
	// this workflow is actually executed, we need to store these inputs so that they can be used.  To do this, we
	// rebuild the `on.workflow_call` section of the workflow and provide the now-evaluated values as the default values
	// of each input -- that way the `with` clause from the caller isn't needed again to run this workflow.
	rebuildInputs := make(map[string]any, len(retval))
	for name, input := range workflowConfig.Inputs {
		rebuildInputs[name] = map[string]any{
			"type":    input.Type,
			"default": retval[name],
		}
	}
	var rebuiltOn yaml.Node
	err := rebuiltOn.Encode(map[string]any{"workflow_call": map[string]any{"inputs": rebuildInputs}})
	if err != nil {
		return nil, nil, fmt.Errorf("unable to yaml encode `on.workflow_call` of single workflow: %w", err)
	}

	return retval, &rebuiltOn, nil
}

// `on.workflow_call.outputs` on the reusable workflow will be converted into an `job.<job_id>.outputs` on the caller job.
func migrateReusableWorkflowOutputs(workflow *model.Workflow, callerJob *bothJobTypes) error {
	// Rewrite `jobs.<job-id>....` into `needs[format("{0}.{1}", parent-job-id, job-id)]....`
	vam := &exprparser.VariableAccessMutator{
		// "Variable access": Whenever we find `jobs[x]` or `jobs.x`...
		Variable: "jobs",
		Rewriter: func(property actionlint.ExprNode) actionlint.ExprNode {
			// "Mutator": replace it with `needs[format('{0}.{1}', "y", x)]`, where "y" is the caller job's ID.
			return &actionlint.IndexAccessNode{
				Operand: &actionlint.VariableNode{Name: "needs"},
				Index: &actionlint.FuncCallNode{
					Callee: "format",
					Args: []actionlint.ExprNode{
						&actionlint.StringNode{Value: "{0}.{1}"},
						&actionlint.StringNode{Value: callerJob.id},
						property,
					},
				},
			}
		},
	}

	workflowConfig := workflow.WorkflowCallConfig()
	for key, output := range workflowConfig.Outputs {
		mutatedOutputValue, err := exprparser.Mutate(output.Value, vam)
		if err != nil {
			return fmt.Errorf("failure to mutate output value: %w", err)
		}
		if callerJob.jobParserJob.Outputs == nil {
			callerJob.jobParserJob.Outputs = make(map[string]string)
		}
		callerJob.jobParserJob.Outputs[key] = mutatedOutputValue
	}

	return nil
}

func rewriteReusableWorkflowNeeds(job *yaml.Node, prefix string) error {
	// Rewrite `needs.<job-id>....` into `needs[format("{0}.{1}", parent-job-id, job-id)]....`
	vam := &exprparser.VariableAccessMutator{
		// "Variable access": Whenever we find `needs[x]` or `needs.x`...
		Variable: "needs",
		Rewriter: func(property actionlint.ExprNode) actionlint.ExprNode {
			// "Mutator": replace it with `needs[format('{0}.{1}', "y", x)]`, where "y" is the caller job's ID.
			return &actionlint.IndexAccessNode{
				Operand: &actionlint.VariableNode{Name: "needs"},
				Index: &actionlint.FuncCallNode{
					Callee: "format",
					Args: []actionlint.ExprNode{
						&actionlint.StringNode{Value: "{0}.{1}"},
						&actionlint.StringNode{Value: prefix},
						property,
					},
				},
			}
		},
	}
	return exprparser.MutateYamlNode(job, vam)
}

func WithJobResults(results map[string]string) ParseOption {
	return func(c *parseContext) {
		c.jobResults = results
	}
}

func WithJobOutputs(outputs map[string]map[string]string) ParseOption {
	return func(c *parseContext) {
		c.jobOutputs = outputs
	}
}

func WithGitContext(context *model.GithubContext) ParseOption {
	return func(c *parseContext) {
		c.gitContext = context
	}
}

func WithInputs(inputs map[string]any) ParseOption {
	return func(c *parseContext) {
		c.inputs = inputs
	}
}

func WithVars(vars map[string]string) ParseOption {
	return func(c *parseContext) {
		c.vars = vars
	}
}

func SupportIncompleteRunsOn() ParseOption {
	return func(c *parseContext) {
		c.supportIncompleteRunsOn = true
	}
}

// `WithWorkflowNeeds` allows overridding the `needs` field for a job being parsed.
//
// In the case that a `SingleWorkflow`, returned from `Parse`, is passed back into `Parse` later in order to expand its
// IncompleteMatrix, then the jobs that it needs will not be present in the workflow (because `SingleWorkflow` only has
// one job in it).  The `needs` field on the job itself may also be absent (Forgejo truncates the `needs` so that it can
// coordinate dispatching the jobs one-by-one without the runner panicing over missing jobs). However, the `needs` field
// is needed in order to populate the `needs` variable context. `WithWorkflowNeeds` can be used to indicate the needs
// exist and are fulfilled.
func WithWorkflowNeeds(needs []string) ParseOption {
	return func(c *parseContext) {
		c.workflowNeeds = needs
	}
}

// Allows the job parser to convert a workflow job that references a local reusable workflow (eg. `uses:
// ./.forgejo/workflows/reusable.yml`) into one-or-more jobs contained within the local workflow.  The
// `localWorkflowFetcher` function allows jobparser to read the target workflow file.
//
// The `localWorkflowFetcher` can return the error ErrUnsupportedReusableWorkflowFetch if the fetcher doesn't support
// the target workflow for job parsing.  The job will go to the "fallback" mode of operation where its internal jobs are
// not expanded into the parsed workflow, and it can still be executed as a single monolithic job.  All other errors are
// considered fatal for job parsing.
func ExpandLocalReusableWorkflows(localWorkflowFetcher func(path string) ([]byte, error)) ParseOption {
	return func(c *parseContext) {
		c.localWorkflowFetcher = localWorkflowFetcher
	}
}

// Allows the job parser to convert a workflow job that references a remote (eg. not part of the current workflow's
// repository) reusable workflow (eg. `uses: some-org/some-repo/.forgejo/workflows/reusable.yml`) into one-or-more jobs
// contained within the remote workflow.  The `remoteWorkflowFetcher` function allows jobparser to read the target
// workflow file.
//
// `ref.Host` will be `nil` if the remote reference was not a fully-qualified URL.  No default value is provided.
//
// The `remoteWorkflowFetcher` can return the error ErrUnsupportedReusableWorkflowFetch if the fetcher doesn't support
// the target workflow for job parsing.  The job will go to the "fallback" mode of operation where its internal jobs are
// not expanded into the parsed workflow, and it can still be executed as a single monolithic job.  All other errors are
// considered fatal for job parsing.
func ExpandRemoteReusableWorkflows(remoteWorkflowFetcher func(ref *model.RemoteReusableWorkflowWithBaseURL) ([]byte, error)) ParseOption {
	return func(c *parseContext) {
		c.remoteWorkflowFetcher = remoteWorkflowFetcher
	}
}

func withRecursionDepth(depth int) ParseOption {
	return func(c *parseContext) {
		c.recursionDepth = depth
	}
}

type parseContext struct {
	jobResults              map[string]string
	jobOutputs              map[string]map[string]string // map job ID -> output key -> output value
	gitContext              *model.GithubContext
	inputs                  map[string]any
	vars                    map[string]string
	workflowNeeds           []string
	supportIncompleteRunsOn bool
	localWorkflowFetcher    func(path string) ([]byte, error)
	remoteWorkflowFetcher   func(ref *model.RemoteReusableWorkflowWithBaseURL) ([]byte, error)
	recursionDepth          int
}

type ParseOption func(c *parseContext)

func getMatrixes(job *model.Job) ([]map[string]any, error) {
	ret, err := job.GetMatrixes()
	if err != nil {
		return nil, fmt.Errorf("GetMatrixes: %w", err)
	}
	sort.Slice(ret, func(i, j int) bool {
		return matrixName(ret[i]) < matrixName(ret[j])
	})
	return ret, nil
}

func encodeMatrix(matrix map[string]any) yaml.Node {
	if len(matrix) == 0 {
		return yaml.Node{}
	}
	value := map[string][]any{}
	for k, v := range matrix {
		value[k] = []any{v}
	}
	node := yaml.Node{}
	_ = node.Encode(value)
	return node
}

func encodeRunsOn(runsOn []string) yaml.Node {
	node := yaml.Node{}
	if len(runsOn) == 1 {
		_ = node.Encode(runsOn[0])
	} else {
		_ = node.Encode(runsOn)
	}
	return node
}

func nameWithMatrix(name string, m map[string]any) string {
	if len(m) == 0 {
		return name
	}

	return name + " " + matrixName(m)
}

func matrixName(m map[string]any) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	vs := make([]string, 0, len(m))
	for _, v := range ks {
		vs = append(vs, fmt.Sprint(m[v]))
	}

	return fmt.Sprintf("(%s)", strings.Join(vs, ", "))
}
