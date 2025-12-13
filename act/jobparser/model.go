package jobparser

import (
	"fmt"
	"strings"

	"code.forgejo.org/forgejo/runner/v12/act/model"
	"go.yaml.in/yaml/v3"
)

// SingleWorkflow is a workflow with single job and single matrix
type SingleWorkflow struct {
	Name     string            `yaml:"name,omitempty"`
	RawOn    yaml.Node         `yaml:"on,omitempty"`
	Env      map[string]string `yaml:"env,omitempty"`
	RawJobs  yaml.Node         `yaml:"jobs,omitempty"`
	Defaults Defaults          `yaml:"defaults,omitempty"`

	// IncompleteMatrix flag indicates that it wasn't possible to evaluate the `strategy.matrix` section of the job
	// because it references a job output that is currently undefined.  The workflow that this job came from will need
	// to be reparsed using the `WithJobOutputs()` option, and it may result in this job being expanded into multiple
	// jobs.
	IncompleteMatrix      bool             `yaml:"incomplete_matrix,omitempty"`
	IncompleteMatrixNeeds *IncompleteNeeds `yaml:"incomplete_matrix_needs,omitempty"`

	// IncompleteRunsOn indicates that it wasn't possible to evaluate the `runs_on` section of the job
	// because it references a job output that is currently undefined.
	IncompleteRunsOn       bool              `yaml:"incomplete_runs_on,omitempty"`
	IncompleteRunsOnNeeds  *IncompleteNeeds  `yaml:"incomplete_runs_on_needs,omitempty"`
	IncompleteRunsOnMatrix *IncompleteMatrix `yaml:"incomplete_runs_on_matrix,omitempty"`
}

type IncompleteNeeds struct {
	Job    string `yaml:"job,omitempty"`    // if ${{ needs.some-job.outputs.some-output }} was incomplete, this will contain "some-job"
	Output string `yaml:"output,omitempty"` // if ${{ needs.some-job.outputs.some-output }} was incomplete, this will contain "some-output"
}

type IncompleteMatrix struct {
	Dimension string `yaml:"dimension,omitempty"` // if ${{ matrix.some-dimension }} was incomplete, this will contain "some-dimension"
}

func (w *SingleWorkflow) Job() (string, *Job) {
	ids, jobs, _ := w.jobs()
	if len(ids) >= 1 {
		return ids[0], jobs[0]
	}
	return "", nil
}

func (w *SingleWorkflow) jobs() ([]string, []*Job, error) {
	ids, jobs, err := parseMappingNode[*Job](&w.RawJobs)
	if err != nil {
		return nil, nil, err
	}

	for _, job := range jobs {
		steps := make([]*Step, 0, len(job.Steps))
		for _, s := range job.Steps {
			if s != nil {
				steps = append(steps, s)
			}
		}
		job.Steps = steps
	}

	return ids, jobs, nil
}

func (w *SingleWorkflow) SetJob(id string, job *Job) error {
	m := map[string]*Job{
		id: job,
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	node := yaml.Node{}
	if err := yaml.Unmarshal(out, &node); err != nil {
		return err
	}
	if len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("can not set job: %q", out)
	}
	w.RawJobs = *node.Content[0]
	return nil
}

func (w *SingleWorkflow) Marshal() ([]byte, error) {
	return yaml.Marshal(w)
}

type Job struct {
	Name           string                    `yaml:"name,omitempty"`
	RawNeeds       yaml.Node                 `yaml:"needs,omitempty"`
	RawRunsOn      yaml.Node                 `yaml:"runs-on,omitempty"`
	Env            yaml.Node                 `yaml:"env,omitempty"`
	If             yaml.Node                 `yaml:"if,omitempty"`
	Steps          []*Step                   `yaml:"steps,omitempty"`
	TimeoutMinutes string                    `yaml:"timeout-minutes,omitempty"`
	Services       map[string]*ContainerSpec `yaml:"services,omitempty"`
	Strategy       Strategy                  `yaml:"strategy,omitempty"`
	RawContainer   yaml.Node                 `yaml:"container,omitempty"`
	Defaults       Defaults                  `yaml:"defaults,omitempty"`
	Outputs        map[string]string         `yaml:"outputs,omitempty"`
	Uses           string                    `yaml:"uses,omitempty"`
	With           map[string]any            `yaml:"with,omitempty"`
	RawSecrets     yaml.Node                 `yaml:"secrets,omitempty"`
	RawConcurrency *model.RawConcurrency     `yaml:"concurrency,omitempty"`
}

func (j *Job) Clone() *Job {
	if j == nil {
		return nil
	}
	return &Job{
		Name:           j.Name,
		RawNeeds:       j.RawNeeds,
		RawRunsOn:      j.RawRunsOn,
		Env:            j.Env,
		If:             j.If,
		Steps:          j.Steps,
		TimeoutMinutes: j.TimeoutMinutes,
		Services:       j.Services,
		Strategy:       j.Strategy,
		RawContainer:   j.RawContainer,
		Defaults:       j.Defaults,
		Outputs:        j.Outputs,
		Uses:           j.Uses,
		With:           j.With,
		RawSecrets:     j.RawSecrets,
		RawConcurrency: j.RawConcurrency,
	}
}

func (j *Job) Needs() []string {
	return (&model.Job{RawNeeds: j.RawNeeds}).Needs()
}

func (j *Job) EraseNeeds() *Job {
	j.RawNeeds = yaml.Node{}
	return j
}

func (j *Job) RunsOn() []string {
	return (&model.Job{RawRunsOn: j.RawRunsOn}).RunsOn()
}

type Step struct {
	ID               string            `yaml:"id,omitempty"`
	If               yaml.Node         `yaml:"if,omitempty"`
	Name             string            `yaml:"name,omitempty"`
	Uses             string            `yaml:"uses,omitempty"`
	Run              string            `yaml:"run,omitempty"`
	WorkingDirectory string            `yaml:"working-directory,omitempty"`
	Shell            string            `yaml:"shell,omitempty"`
	Env              yaml.Node         `yaml:"env,omitempty"`
	With             map[string]string `yaml:"with,omitempty"`
	ContinueOnError  bool              `yaml:"continue-on-error,omitempty"`
	TimeoutMinutes   string            `yaml:"timeout-minutes,omitempty"`
}

// String gets the name of step
func (s *Step) String() string {
	if s == nil {
		return ""
	}
	return (&model.Step{
		ID:   s.ID,
		Name: s.Name,
		Uses: s.Uses,
		Run:  s.Run,
	}).String()
}

type ContainerSpec struct {
	Image       string            `yaml:"image,omitempty"`
	Env         map[string]string `yaml:"env,omitempty"`
	Ports       []string          `yaml:"ports,omitempty"`
	Volumes     []string          `yaml:"volumes,omitempty"`
	Options     string            `yaml:"options,omitempty"`
	Credentials map[string]string `yaml:"credentials,omitempty"`
	Cmd         []string          `yaml:"cmd,omitempty"`
}

type Strategy struct {
	FailFastString    string    `yaml:"fail-fast,omitempty"`
	MaxParallelString string    `yaml:"max-parallel,omitempty"`
	RawMatrix         yaml.Node `yaml:"matrix,omitempty"`
}

type Defaults struct {
	Run RunDefaults `yaml:"run,omitempty"`
}

type RunDefaults struct {
	Shell            string `yaml:"shell,omitempty"`
	WorkingDirectory string `yaml:"working-directory,omitempty"`
}

type Event struct {
	Name      string
	acts      map[string][]string
	schedules []map[string]string
}

func (evt *Event) IsSchedule() bool {
	return evt.schedules != nil
}

func (evt *Event) Acts() map[string][]string {
	return evt.acts
}

func (evt *Event) Schedules() []map[string]string {
	return evt.schedules
}

// Convert the raw YAML from the `concurrency` block on a workflow into the evaluated concurrency group and
// cancel-in-progress value. This implementation only supports workflow-level concurrency definition, where we expect
// expressions to be able to access only the github, inputs and vars contexts. If RawConcurrency is empty, then the
// returned concurrency group will be "" and cancel-in-progress will be nil -- this can be used to distinguish from an
// explicit cancel-in-progress choice even if a group isn't specified.
func EvaluateWorkflowConcurrency(rc *model.RawConcurrency, gitCtx *model.GithubContext, vars map[string]string, inputs map[string]any) (string, *bool, error) {
	evaluator := NewExpressionEvaluator(NewWorkflowInterpreter(gitCtx, vars, inputs))
	var node yaml.Node
	if err := node.Encode(rc); err != nil {
		return "", nil, fmt.Errorf("failed to encode concurrency: %w", err)
	}
	if err := evaluator.EvaluateYamlNode(&node); err != nil {
		return "", nil, fmt.Errorf("failed to evaluate concurrency: %w", err)
	}
	var evaluated model.RawConcurrency
	if err := node.Decode(&evaluated); err != nil {
		return "", nil, fmt.Errorf("failed to unmarshal evaluated concurrency: %w", err)
	}
	if evaluated.RawExpression != "" {
		return evaluated.RawExpression, nil, nil
	}
	if evaluated.CancelInProgress == "" {
		return evaluated.Group, nil, nil
	}
	cancelInProgress := evaluated.CancelInProgress == "true"
	return evaluated.Group, &cancelInProgress, nil
}

func ParseRawOn(rawOn *yaml.Node) ([]*Event, error) {
	switch rawOn.Kind {
	case yaml.ScalarNode:
		var val string
		err := rawOn.Decode(&val)
		if err != nil {
			return nil, fmt.Errorf("unable to interpret scalar value into a string: %w", err)
		}
		return []*Event{
			{Name: val},
		}, nil
	case yaml.SequenceNode:
		var val []any
		err := rawOn.Decode(&val)
		if err != nil {
			return nil, err
		}
		res := make([]*Event, 0, len(val))
		for i, v := range val {
			switch t := v.(type) {
			case string:
				res = append(res, &Event{Name: t})
			default:
				return nil, fmt.Errorf("value at index %d was unexpected type %[2]T; must be a string but was %#[2]v", i, v)
			}
		}
		return res, nil
	case yaml.MappingNode:
		events, triggers, err := parseMappingNode[any](rawOn)
		if err != nil {
			return nil, err
		}
		res := make([]*Event, 0, len(events))
		for i, k := range events {
			v := triggers[i]
			if v == nil {
				res = append(res, &Event{
					Name: k,
					acts: map[string][]string{},
				})
				continue
			}
			switch t := v.(type) {
			case map[string]any:
				acts := make(map[string][]string, len(t))
				for act, branches := range t {
					switch b := branches.(type) {
					case string:
						acts[act] = []string{b}
					case []string:
						acts[act] = b
					case []any:
						acts[act] = make([]string, len(b))
						for i, v := range b {
							var ok bool
							if acts[act][i], ok = v.(string); !ok {
								return nil, fmt.Errorf("key %q.%q index %d had unexpected type %[4]T; a string was expected but was %#[4]v", k, act, i, v)
							}
						}
					case map[string]any:
						if err := isInvalidOnType(k, act); err != nil {
							return nil, fmt.Errorf("invalid value on key %q: %w", k, err)
						}
					default:
						return nil, fmt.Errorf("key %q.%q had unexpected type %T; was %#v", k, act, branches, branches)
					}
				}
				if k == "workflow_dispatch" || k == "workflow_call" {
					acts = nil
				}
				res = append(res, &Event{
					Name: k,
					acts: acts,
				})
			case []any:
				if k != "schedule" {
					return nil, fmt.Errorf("key %q had an type %T; only the 'schedule' key is expected with this type", k, v)
				}
				schedules := make([]map[string]string, len(t))
				for i, tt := range t {
					vv, ok := tt.(map[string]any)
					if !ok {
						return nil, fmt.Errorf("key %q[%d] had unexpected type %[3]T; a map with a key \"cron\" was expected, but value was %#[3]v", k, i, tt)
					}
					schedules[i] = make(map[string]string, len(vv))
					for kk, vvv := range vv {
						if strings.ToLower(kk) != "cron" {
							return nil, fmt.Errorf("key %q[%d] had unexpected key %q; \"cron\" was expected", k, i, kk)
						}
						var ok bool
						if schedules[i][kk], ok = vvv.(string); !ok {
							return nil, fmt.Errorf("key %q[%d].%q had unexpected type %[4]T; a string was expected by was %#[4]v", k, i, kk, vvv)
						}
					}
				}
				res = append(res, &Event{
					Name:      k,
					schedules: schedules,
				})
			default:
				return nil, fmt.Errorf("key %q had unexpected type %[2]T; expected a map or array but was %#[2]v", k, v)
			}
		}
		return res, nil
	default:
		return nil, fmt.Errorf("unexpected yaml node in `on`: %v", rawOn.Kind)
	}
}

func isInvalidOnType(onType, subKey string) error {
	if onType == "workflow_dispatch" {
		if subKey == "inputs" {
			return nil
		}
		return fmt.Errorf("workflow_dispatch only supports key \"inputs\", but key %q was found", subKey)
	}
	if onType == "workflow_call" {
		if subKey == "inputs" || subKey == "outputs" {
			return nil
		}
		return fmt.Errorf("workflow_call only supports keys \"inputs\" and \"outputs\", but key %q was found", subKey)
	}
	return fmt.Errorf("unexpected key %q.%q", onType, subKey)
}

// parseMappingNode parse a mapping node and preserve order.
func parseMappingNode[T any](node *yaml.Node) ([]string, []T, error) {
	if node.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("input node is not a mapping node")
	}

	var scalars []string
	var datas []T
	expectKey := true
	for _, item := range node.Content {
		if expectKey {
			if item.Kind != yaml.ScalarNode {
				return nil, nil, fmt.Errorf("not a valid scalar node: %v", item.Value)
			}
			scalars = append(scalars, item.Value)
			expectKey = false
		} else {
			var val T
			if err := item.Decode(&val); err != nil {
				return nil, nil, err
			}
			datas = append(datas, val)
			expectKey = true
		}
	}

	if len(scalars) != len(datas) {
		return nil, nil, fmt.Errorf("invalid definition of on: %v", node.Value)
	}

	return scalars, datas, nil
}

// Outputs from a reusable workflow are translated into outputs on the caller job, which is transformed into a
// placeholder job that is never really executed.  Because it isn't executed, special handling for evaluating its
// outputs is required, which `EvaluateWorkflowCallOutputs` provides.
func EvaluateWorkflowCallOutputs(callerJob *Job, gitCtx *model.GithubContext, vars map[string]string, needs []string, jobResults map[string]string, jobOutputs map[string]map[string]string) map[string]string {
	// Documented supported contexts: forgejo, inputs, jobs, vars
	//
	// `jobs` is rewritten during workflow evaluation to `needs` that reference other jobs, so we can replace `jobs`
	// context with `needs` context in the above list.
	//
	// TODO: `inputs` for a workflow call is really the `with` content of the caller, which was already evaluated when
	// the workflow was expanded, but we never saved a value of those evaluated `with` -> `inputs` in a way that can be
	// accessed from the caller's workflow... perhaps we can smuggle it forward from that initial expansion, but for the
	// moment we won't support inputs.

	results := map[string]*JobResult{}
	for _, id := range needs {
		results[id] = &JobResult{
			Result:  jobResults[id],
			Outputs: jobOutputs[id],
		}
	}

	evaluator := NewExpressionEvaluator(newWorkflowCallOutputsInterpreter(gitCtx, vars, results, needs))

	outputs := map[string]string{}
	for key, value := range callerJob.Outputs {
		outputs[key] = evaluator.Interpolate(value)
	}
	return outputs
}
