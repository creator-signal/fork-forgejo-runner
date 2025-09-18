// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
	"connectrpc.com/connect"
	retry "github.com/avast/retry-go/v4"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"code.forgejo.org/forgejo/runner/v11/internal/pkg/client"
	"code.forgejo.org/forgejo/runner/v11/internal/pkg/common"
)

var (
	outputKeyMaxLength   = 255
	outputValueMaxLength = 1024 * 1024
)

type Reporter struct {
	ctx    context.Context
	cancel context.CancelFunc

	closed  bool
	client  client.Client
	clientM sync.Mutex

	logOffset      int
	logRows        []*runnerv1.LogRow
	masker         *masker
	reportInterval time.Duration

	state   *runnerv1.TaskState
	stateMu sync.RWMutex
	outputs sync.Map

	debugOutputEnabled  bool
	stopCommandEndToken string
}

func NewReporter(ctx context.Context, cancel context.CancelFunc, c client.Client, task *runnerv1.Task, reportInterval time.Duration) *Reporter {
	masker := newMasker()
	if v := task.Context.Fields["token"].GetStringValue(); v != "" {
		masker.add(v)
	}
	if v := client.BackwardCompatibleContext(task, "runtime_token"); v != "" {
		masker.add(v)
	}
	for _, v := range task.Secrets {
		masker.add(v)
	}

	rv := &Reporter{
		// ctx & cancel are related: the daemon context allows the reporter
		// to continue to operate even after the context is canceled, to
		// conclude the converation
		ctx:            common.DaemonContext(ctx),
		cancel:         cancel,
		client:         c,
		masker:         masker,
		reportInterval: reportInterval,
		state: &runnerv1.TaskState{
			Id: task.Id,
		},
	}

	if task.Secrets["ACTIONS_STEP_DEBUG"] == "true" {
		rv.debugOutputEnabled = true
	}

	return rv
}

func (r *Reporter) ResetSteps(l int) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	for i := range l {
		r.state.Steps = append(r.state.Steps, &runnerv1.StepState{
			Id: int64(i),
		})
	}
}

func (r *Reporter) Levels() []log.Level {
	return log.AllLevels
}

func appendIfNotNil[T any](s []*T, v *T) []*T {
	if v != nil {
		return append(s, v)
	}
	return s
}

func (r *Reporter) Fire(entry *log.Entry) error {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()

	log.WithFields(entry.Data).Trace(entry.Message)

	timestamp := entry.Time
	if r.state.StartedAt == nil {
		r.state.StartedAt = timestamppb.New(timestamp)
	}

	stage := entry.Data["stage"]

	if stage != "Main" {
		if v, ok := entry.Data["jobResult"]; ok {
			if jobResult, ok := r.parseResult(v); ok {
				r.state.Result = jobResult
				r.state.StoppedAt = timestamppb.New(timestamp)
				for _, s := range r.state.Steps {
					if s.Result == runnerv1.Result_RESULT_UNSPECIFIED {
						s.Result = runnerv1.Result_RESULT_CANCELLED
						if jobResult == runnerv1.Result_RESULT_SKIPPED {
							s.Result = runnerv1.Result_RESULT_SKIPPED
						}
					}
				}
			}
			if r.state.Result == runnerv1.Result_RESULT_SUCCESS {
				if v, ok := entry.Data["jobOutputs"]; ok {
					_ = r.setOutputs(v.(map[string]string))
				} else {
					log.Panicf("received log entry with successful jobResult, but without jobOutputs -- outputs will be corrupted for this job")
				}
			}
		}
		if !r.duringSteps() {
			r.logRows = appendIfNotNil(r.logRows, r.parseLogRow(entry))
		}
		return nil
	}

	var step *runnerv1.StepState
	if v, ok := entry.Data["stepNumber"]; ok {
		if v, ok := v.(int); ok && len(r.state.Steps) > v {
			step = r.state.Steps[v]
		}
	}
	if step == nil {
		if !r.duringSteps() {
			r.logRows = appendIfNotNil(r.logRows, r.parseLogRow(entry))
		}
		return nil
	}

	if step.StartedAt == nil {
		step.StartedAt = timestamppb.New(timestamp)
	}
	if v, ok := entry.Data["raw_output"]; ok {
		if rawOutput, ok := v.(bool); ok && rawOutput {
			if row := r.parseLogRow(entry); row != nil {
				if step.LogLength == 0 {
					step.LogIndex = int64(r.logOffset + len(r.logRows))
				}
				step.LogLength++
				r.logRows = append(r.logRows, row)
			}
		}
	} else if !r.duringSteps() {
		r.logRows = appendIfNotNil(r.logRows, r.parseLogRow(entry))
	}
	if v, ok := entry.Data["stepResult"]; ok {
		if stepResult, ok := r.parseResult(v); ok {
			if step.LogLength == 0 {
				step.LogIndex = int64(r.logOffset + len(r.logRows))
			}
			step.Result = stepResult
			step.StoppedAt = timestamppb.New(timestamp)
		}
	}

	return nil
}

func (r *Reporter) RunDaemon() {
	if r.closed {
		return
	}
	if r.ctx.Err() != nil {
		return
	}

	_ = r.ReportLog(false)
	_ = r.ReportState()

	time.AfterFunc(r.reportInterval, r.RunDaemon)
}

func (r *Reporter) Logf(format string, a ...any) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()

	r.logf(format, a...)
}

func (r *Reporter) logf(format string, a ...any) {
	if !r.duringSteps() {
		r.logRows = append(r.logRows, &runnerv1.LogRow{
			Time:    timestamppb.Now(),
			Content: fmt.Sprintf(format, a...),
		})
	}
}

func (r *Reporter) cloneOutputs() map[string]string {
	outputs := make(map[string]string)
	r.outputs.Range(func(k, v any) bool {
		if val, ok := v.(string); ok {
			outputs[k.(string)] = val
		}
		return true
	})
	return outputs
}

// Errors from setOutputs are logged into the reporter automatically; the `errors` return value is only used for unit
// tests.
func (r *Reporter) setOutputs(outputs map[string]string) error {
	var errs []error
	recordError := func(format string, a ...any) {
		r.logf(format, a...)
		errs = append(errs, fmt.Errorf(format, a...))
	}
	for k, v := range outputs {
		if len(k) > outputKeyMaxLength {
			recordError("ignore output because the key is longer than %d: %q", outputKeyMaxLength, k)
			continue
		}
		if l := len(v); l > outputValueMaxLength {
			recordError("ignore output because the length of the value for %q is %d (the maximum is %d)", k, l, outputValueMaxLength)
			continue
		}
		if _, ok := r.outputs.Load(k); ok {
			recordError("ignore output because a value already exists for the key %q", k)
			continue
		}
		r.outputs.Store(k, v)
	}
	return errors.Join(errs...)
}

const (
	closeTimeoutMessage   = "The runner cancelled the job because it exceeds the maximum run time"
	closeCancelledMessage = "Cancelled"
)

func (r *Reporter) Close(runErr error) error {
	r.closed = true

	setStepCancel := func() {
		for _, v := range r.state.Steps {
			if v.Result == runnerv1.Result_RESULT_UNSPECIFIED {
				v.Result = runnerv1.Result_RESULT_CANCELLED
			}
		}
	}

	r.stateMu.Lock()
	var lastWords string
	if errors.Is(runErr, context.DeadlineExceeded) {
		lastWords = closeTimeoutMessage
		r.state.Result = runnerv1.Result_RESULT_CANCELLED
		setStepCancel()
	} else if r.state.Result == runnerv1.Result_RESULT_UNSPECIFIED {
		if runErr == nil {
			lastWords = closeCancelledMessage
		} else {
			lastWords = runErr.Error()
		}
		r.state.Result = runnerv1.Result_RESULT_FAILURE
		setStepCancel()
	} else if runErr != nil {
		lastWords = runErr.Error()
	}

	if lastWords != "" {
		r.logRows = append(r.logRows, &runnerv1.LogRow{
			Time:    timestamppb.Now(),
			Content: lastWords,
		})
	}
	r.state.StoppedAt = timestamppb.Now()
	r.stateMu.Unlock()

	return retry.Do(func() error {
		if err := r.ReportLog(true); err != nil {
			return err
		}
		return r.ReportState()
	}, retry.Context(r.ctx))
}

type ErrRetry struct {
	message string
}

func (err ErrRetry) Error() string {
	return err.message
}

func (err ErrRetry) Is(target error) bool {
	_, ok := target.(*ErrRetry)
	return ok
}

const (
	errRetryNeedMoreRows = "need more rows to figure out if multiline secrets must be masked"
	errRetrySendAll      = "not all logs are submitted %d remain"
)

func NewErrRetry(message string, args ...any) error {
	return &ErrRetry{message: fmt.Sprintf(message, args...)}
}

func (r *Reporter) ReportLog(noMore bool) error {
	r.clientM.Lock()
	defer r.clientM.Unlock()

	r.stateMu.RLock()
	rows := r.logRows
	r.stateMu.RUnlock()

	if len(rows) == 0 {
		return nil
	}

	if needMore := r.masker.replace(rows, noMore); needMore {
		return NewErrRetry(errRetryNeedMoreRows)
	}

	resp, err := r.client.UpdateLog(r.ctx, connect.NewRequest(&runnerv1.UpdateLogRequest{
		TaskId: r.state.Id,
		Index:  int64(r.logOffset),
		Rows:   rows,
		NoMore: noMore,
	}))
	if err != nil {
		return err
	}

	ack := int(resp.Msg.GetAckIndex())
	if ack < r.logOffset {
		return fmt.Errorf("submitted logs are lost %d < %d", ack, r.logOffset)
	}

	r.stateMu.Lock()
	r.logRows = r.logRows[ack-r.logOffset:]
	r.logOffset = ack
	r.stateMu.Unlock()

	if noMore && len(r.logRows) > 0 {
		return NewErrRetry(errRetrySendAll, len(r.logRows))
	}

	return nil
}

func (r *Reporter) ReportState() error {
	r.clientM.Lock()
	defer r.clientM.Unlock()

	r.stateMu.RLock()
	state := proto.Clone(r.state).(*runnerv1.TaskState)
	outputs := r.cloneOutputs()
	r.stateMu.RUnlock()

	resp, err := r.client.UpdateTask(r.ctx, connect.NewRequest(&runnerv1.UpdateTaskRequest{
		State:   state,
		Outputs: outputs,
	}))
	if err != nil {
		return err
	}

	for _, k := range resp.Msg.GetSentOutputs() {
		r.outputs.Store(k, struct{}{})
	}

	switch resp.Msg.GetState().GetResult() {
	case runnerv1.Result_RESULT_CANCELLED, runnerv1.Result_RESULT_FAILURE:
		r.cancel()
	}

	var noSent []string
	r.outputs.Range(func(k, v any) bool {
		if _, ok := v.(string); ok {
			noSent = append(noSent, k.(string))
		}
		return true
	})
	if len(noSent) > 0 {
		return NewErrRetry(errRetrySendAll, len(noSent))
	}

	return nil
}

func (r *Reporter) duringSteps() bool {
	if steps := r.state.Steps; len(steps) == 0 {
		return false
	} else if first := steps[0]; first.Result == runnerv1.Result_RESULT_UNSPECIFIED && first.LogLength == 0 {
		return false
	} else if last := steps[len(steps)-1]; last.Result != runnerv1.Result_RESULT_UNSPECIFIED {
		return false
	}
	return true
}

var stringToResult = map[string]runnerv1.Result{
	"success":   runnerv1.Result_RESULT_SUCCESS,
	"failure":   runnerv1.Result_RESULT_FAILURE,
	"skipped":   runnerv1.Result_RESULT_SKIPPED,
	"cancelled": runnerv1.Result_RESULT_CANCELLED,
}

func (r *Reporter) parseResult(result any) (runnerv1.Result, bool) {
	str := ""
	if v, ok := result.(string); ok { // for jobResult
		str = v
	} else if v, ok := result.(fmt.Stringer); ok { // for stepResult
		str = v.String()
	}

	ret, ok := stringToResult[str]
	return ret, ok
}

var cmdRegex = regexp.MustCompile(`^::([^ :]+)( .*)?::(.*)$`)

func (r *Reporter) handleCommand(originalContent, command, parameters, value string) *string {
	if r.stopCommandEndToken != "" && command != r.stopCommandEndToken {
		return &originalContent
	}

	switch command {
	case "add-mask":
		r.masker.add(value)
		return nil
	case "debug":
		if r.debugOutputEnabled {
			return &value
		}
		return nil

	case "notice":
		// Not implemented yet, so just return the original content.
		return &originalContent
	case "warning":
		// Not implemented yet, so just return the original content.
		return &originalContent
	case "error":
		// Not implemented yet, so just return the original content.
		return &originalContent
	case "group":
		// Rewriting into ##[] syntax which the frontend understands
		content := "##[group]" + value
		return &content
	case "endgroup":
		// Ditto
		content := "##[endgroup]"
		return &content
	case "stop-commands":
		r.stopCommandEndToken = value
		return nil
	case r.stopCommandEndToken:
		r.stopCommandEndToken = ""
		return nil
	}
	return &originalContent
}

func (r *Reporter) parseLogRow(entry *log.Entry) *runnerv1.LogRow {
	content := strings.TrimRightFunc(entry.Message, func(r rune) bool { return r == '\r' || r == '\n' })

	matches := cmdRegex.FindStringSubmatch(content)
	if matches != nil {
		if output := r.handleCommand(content, matches[1], matches[2], matches[3]); output != nil {
			content = *output
		} else {
			return nil
		}
	}

	return &runnerv1.LogRow{
		Time:    timestamppb.New(entry.Time),
		Content: strings.ToValidUTF8(content, "?"),
	}
}
