// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
	connect_go "connectrpc.com/connect"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	"code.forgejo.org/forgejo/runner/v11/internal/pkg/client/mocks"
	"code.forgejo.org/forgejo/runner/v11/internal/pkg/common"
	"code.forgejo.org/forgejo/runner/v11/testutils"
)

func rowsToString(rows []*runnerv1.LogRow) string {
	s := ""
	for _, row := range rows {
		s += row.Content + "\n"
	}
	return s
}

func stringToRows(s string) []*runnerv1.LogRow {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	rows := make([]*runnerv1.LogRow, 0, len(lines))
	for _, line := range lines {
		rows = append(rows, &runnerv1.LogRow{Content: line})
	}
	return rows
}

func mockReporter(t *testing.T) (*Reporter, *mocks.Client, func()) {
	t.Helper()

	client := mocks.NewClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	taskCtx, err := structpb.NewStruct(map[string]any{})
	require.NoError(t, err)
	reporter := NewReporter(common.WithDaemonContext(ctx, t.Context()), cancel, client, &runnerv1.Task{
		Context: taskCtx,
	}, time.Second)
	close := func() {
		assert.NoError(t, reporter.Close(nil))
	}
	return reporter, client, close
}

func TestReporterSetOutputs(t *testing.T) {
	assertEqual := func(t *testing.T, expected map[string]string, actual *sync.Map) {
		t.Helper()
		actualMap := map[string]string{}
		actual.Range(func(k, v any) bool {
			val, ok := v.(string)
			require.True(t, ok)
			actualMap[k.(string)] = val
			return true
		})
		assert.Equal(t, expected, actualMap)
	}

	t.Run("All", func(t *testing.T) {
		reporter, _, _ := mockReporter(t)

		expected := map[string]string{"a": "b", "c": "d"}
		assert.NoError(t, reporter.SetOutputs(expected))
		assertEqual(t, expected, &reporter.outputs)
	})

	t.Run("IgnoreTooBig", func(t *testing.T) {
		reporter, _, _ := mockReporter(t)

		defer testutils.MockVariable(&outputKeyMaxLength, 5)()
		defer testutils.MockVariable(&outputValueMaxLength, 5)()

		in := map[string]string{
			"0123456": "b",       // key too big
			"c":       "ABCDEFG", // value too big
			"d":       "e",
		}
		err := reporter.SetOutputs(in)
		assert.ErrorContains(t, err, "ignore output because the length of the value for \"c\" is 7 (the maximum is 5)")
		assert.ErrorContains(t, err, "ignore output because the key is longer than 5: \"0123456\"")
		expected := map[string]string{"d": "e"}
		assertEqual(t, expected, &reporter.outputs)
	})

	t.Run("IgnoreDuplicates", func(t *testing.T) {
		reporter, _, _ := mockReporter(t)

		first := map[string]string{"a": "b", "c": "d"}
		assert.NoError(t, reporter.SetOutputs(first))
		assertEqual(t, first, &reporter.outputs)

		second := map[string]string{"c": "d", "e": "f"}
		assert.ErrorContains(t, reporter.SetOutputs(second), "ignore output because a value already exists for the key \"c\"")

		expected := map[string]string{"a": "b", "c": "d", "e": "f"}
		assertEqual(t, expected, &reporter.outputs)
	})
}

func TestReporter_parseLogRow(t *testing.T) {
	tests := []struct {
		name               string
		debugOutputEnabled bool
		args               []string
		want               []string
	}{
		{
			"No command", false,
			[]string{"Hello, world!"},
			[]string{"Hello, world!"},
		},
		{
			"Add-mask", false,
			[]string{
				"foo mysecret bar",
				"::add-mask::mysecret",
				"foo mysecret bar",
			},
			[]string{
				"foo mysecret bar",
				"<nil>",
				"foo *** bar",
			},
		},
		{
			"Debug enabled", true,
			[]string{
				"::debug::GitHub Actions runtime token access controls",
			},
			[]string{
				"GitHub Actions runtime token access controls",
			},
		},
		{
			"Debug not enabled", false,
			[]string{
				"::debug::GitHub Actions runtime token access controls",
			},
			[]string{
				"<nil>",
			},
		},
		{
			"notice", false,
			[]string{
				"::notice file=file.name,line=42,endLine=48,title=Cool Title::Gosh, that's not going to work",
			},
			[]string{
				"::notice file=file.name,line=42,endLine=48,title=Cool Title::Gosh, that's not going to work",
			},
		},
		{
			"warning", false,
			[]string{
				"::warning file=file.name,line=42,endLine=48,title=Cool Title::Gosh, that's not going to work",
			},
			[]string{
				"::warning file=file.name,line=42,endLine=48,title=Cool Title::Gosh, that's not going to work",
			},
		},
		{
			"error", false,
			[]string{
				"::error file=file.name,line=42,endLine=48,title=Cool Title::Gosh, that's not going to work",
			},
			[]string{
				"::error file=file.name,line=42,endLine=48,title=Cool Title::Gosh, that's not going to work",
			},
		},
		{
			"group", false,
			[]string{
				"::group::",
				"::endgroup::",
			},
			[]string{
				"##[group]",
				"##[endgroup]",
			},
		},
		{
			"stop-commands", false,
			[]string{
				"::add-mask::foo",
				"::stop-commands::myverycoolstoptoken",
				"::add-mask::bar",
				"::debug::Stuff",
				"myverycoolstoptoken",
				"::add-mask::baz",
				"::myverycoolstoptoken::",
				"::add-mask::wibble",
				"foo bar baz wibble",
			},
			[]string{
				"<nil>",
				"<nil>",
				"::add-mask::bar",
				"::debug::Stuff",
				"myverycoolstoptoken",
				"::add-mask::baz",
				"<nil>",
				"<nil>",
				"*** bar baz ***",
			},
		},
		{
			"unknown command", false,
			[]string{
				"::set-mask::foo",
			},
			[]string{
				"::set-mask::foo",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Reporter{
				masker:             newMasker(),
				debugOutputEnabled: tt.debugOutputEnabled,
			}
			for idx, arg := range tt.args {
				rv := r.parseLogRow(&log.Entry{Message: arg})
				got := "<nil>"

				if rv != nil {
					r.masker.replace([]*runnerv1.LogRow{rv}, true)
					got = rv.Content
				}

				assert.Equal(t, tt.want[idx], got)
			}
		})
	}
}

func TestReporter_Fire(t *testing.T) {
	t.Run("ignore command lines", func(t *testing.T) {
		reporter, client, close := mockReporter(t)
		defer close()
		reporter.ResetSteps(5)

		client.On("UpdateLog", mock.Anything, mock.Anything).Return(func(_ context.Context, req *connect_go.Request[runnerv1.UpdateLogRequest]) (*connect_go.Response[runnerv1.UpdateLogResponse], error) {
			t.Logf("Received UpdateLog: %s", req.Msg.String())
			return connect_go.NewResponse(&runnerv1.UpdateLogResponse{
				AckIndex: req.Msg.Index + int64(len(req.Msg.Rows)),
			}), nil
		})
		client.On("UpdateTask", mock.Anything, mock.Anything).Return(func(_ context.Context, req *connect_go.Request[runnerv1.UpdateTaskRequest]) (*connect_go.Response[runnerv1.UpdateTaskResponse], error) {
			t.Logf("Received UpdateTask: %s", req.Msg.String())
			return connect_go.NewResponse(&runnerv1.UpdateTaskResponse{}), nil
		})

		dataStep0 := map[string]any{
			"stage":      "Main",
			"stepNumber": 0,
			"raw_output": true,
		}

		assert.NoError(t, reporter.Fire(&log.Entry{Message: "regular log line", Data: dataStep0}))
		assert.NoError(t, reporter.Fire(&log.Entry{Message: "::debug::debug log line", Data: dataStep0}))
		assert.NoError(t, reporter.Fire(&log.Entry{Message: "regular log line", Data: dataStep0}))
		assert.NoError(t, reporter.Fire(&log.Entry{Message: "::debug::debug log line", Data: dataStep0}))
		assert.NoError(t, reporter.Fire(&log.Entry{Message: "::debug::debug log line", Data: dataStep0}))
		assert.NoError(t, reporter.Fire(&log.Entry{Message: "regular log line", Data: dataStep0}))

		assert.Equal(t, int64(3), reporter.state.Steps[0].LogLength)
	})
}

func TestReporterReportLogLost(t *testing.T) {
	reporter, client, _ := mockReporter(t)
	reporter.logRows = stringToRows("A")
	reporter.logOffset = 100

	client.On("UpdateLog", mock.Anything, mock.Anything).Return(func(_ context.Context, req *connect_go.Request[runnerv1.UpdateLogRequest]) (*connect_go.Response[runnerv1.UpdateLogResponse], error) {
		t.Logf("Received UpdateLog: %s", req.Msg.String())
		return connect_go.NewResponse(&runnerv1.UpdateLogResponse{
			AckIndex: 50,
		}), nil
	})

	err := reporter.ReportLog(false)
	require.Error(t, err)
	assert.Equal(t, "submitted logs are lost 50 < 100", err.Error())
}

func TestReporterReportLogError(t *testing.T) {
	reporter, client, _ := mockReporter(t)
	reporter.logRows = stringToRows("A")
	someError := "ERROR MESSAGE"

	client.On("UpdateLog", mock.Anything, mock.Anything).Return(func(_ context.Context, req *connect_go.Request[runnerv1.UpdateLogRequest]) (*connect_go.Response[runnerv1.UpdateLogResponse], error) {
		t.Logf("Received UpdateLog: %s", req.Msg.String())
		return connect_go.NewResponse(&runnerv1.UpdateLogResponse{}), errors.New(someError)
	})

	err := reporter.ReportLog(false)
	require.Error(t, err)
	assert.Equal(t, someError, err.Error())
}

func TestReporterReportLog(t *testing.T) {
	secret := "secretOne"
	firstLine := "ONE"
	multiLineSecret := firstLine + "\nTWO\nTHREE\n"

	for _, testCase := range []struct {
		name     string
		outgoing string
		received int
		sent     string
		noMore   bool
		err      error
	}{
		{
			name:     "SecretsAreMasked",
			outgoing: fmt.Sprintf(">>>%s<<< (((%s)))", secret, multiLineSecret),
			sent:     ">>>***<<< (((***\n***\n***\n***)))\n",
			err:      nil,
		},
		{
			name: "NoRowsToSend",
			err:  nil,
		},
		{
			name:     "RetryToMaskSecrets",
			outgoing: firstLine,
			err:      NewErrRetry(errRetryNeedMoreRows),
		},
		{
			name:     "OnlyTheFirstLineIsReceived",
			outgoing: "A\nB\nC",
			received: 1,
			sent:     "A\n",
		},
		{
			name:     "RetrySendAll",
			outgoing: "A\nB\nC",
			received: 1,
			sent:     "A\n",
			noMore:   true,
			err:      NewErrRetry(errRetrySendAll, 2),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			reporter, client, _ := mockReporter(t)
			reporter.masker.add(secret)
			reporter.masker.add(multiLineSecret)
			rows := stringToRows(testCase.outgoing)
			reporter.logRows = rows

			sent := ""
			if testCase.sent != "" {
				client.On("UpdateLog", mock.Anything, mock.Anything).Return(func(_ context.Context, req *connect_go.Request[runnerv1.UpdateLogRequest]) (*connect_go.Response[runnerv1.UpdateLogResponse], error) {
					t.Logf("UpdateLogRequest: %s", req.Msg.String())
					rows := req.Msg.Rows
					if testCase.received > 0 {
						rows = rows[:testCase.received]
					}
					sent += rowsToString(rows)
					resp := &runnerv1.UpdateLogResponse{
						AckIndex: req.Msg.Index + int64(len(rows)),
					}
					t.Logf("UpdateLogResponse: %s", resp.String())
					return connect_go.NewResponse(resp), nil
				})
			}

			err := reporter.ReportLog(testCase.noMore)
			if testCase.err == nil {
				assert.NoError(t, err)
			} else if assert.ErrorIs(t, err, testCase.err) {
				assert.Equal(t, err.Error(), testCase.err.Error())
			}
			if testCase.sent != "" {
				assert.Equal(t, testCase.sent, sent)
				if testCase.received > 0 {
					remain := len(rows) - testCase.received
					assert.Equal(t, remain, len(reporter.logRows))
					assert.Equal(t, testCase.received, reporter.logOffset)
				} else {
					assert.Empty(t, reporter.logRows)
					assert.Equal(t, len(rows), reporter.logOffset)
				}
			} else {
				assert.Equal(t, len(rows), len(reporter.logRows))
				assert.Equal(t, 0, reporter.logOffset)
			}
		})
	}
}

func TestReporterClose(t *testing.T) {
	mockReporterCloser := func(t *testing.T, message *string, result *runnerv1.Result) *Reporter {
		t.Helper()
		reporter, client, _ := mockReporter(t)
		if message != nil {
			client.On("UpdateLog", mock.Anything, mock.Anything).Return(func(_ context.Context, req *connect_go.Request[runnerv1.UpdateLogRequest]) (*connect_go.Response[runnerv1.UpdateLogResponse], error) {
				t.Logf("UpdateLogRequest: %s", req.Msg.String())
				assert.Equal(t, (*message)+"\n", rowsToString(req.Msg.Rows))
				resp := &runnerv1.UpdateLogResponse{
					AckIndex: req.Msg.Index + 1,
				}
				t.Logf("UpdateLogResponse: %s", resp.String())
				return connect_go.NewResponse(resp), nil
			})
		}

		if result != nil {
			client.On("UpdateTask", mock.Anything, mock.Anything).Return(func(_ context.Context, req *connect_go.Request[runnerv1.UpdateTaskRequest]) (*connect_go.Response[runnerv1.UpdateTaskResponse], error) {
				t.Logf("Received UpdateTask: %s", req.Msg.String())
				assert.Equal(t, result.String(), req.Msg.State.Result.String())
				return connect_go.NewResponse(&runnerv1.UpdateTaskResponse{}), nil
			})
		}
		return reporter
	}

	for _, testCase := range []struct {
		name                    string
		err                     error
		expectedMessage         string
		result                  runnerv1.Result
		expectedStateResult     runnerv1.Result
		expectedStepStateResult runnerv1.Result
	}{
		{
			name:                "ResultSuccessAndNilErrorIsResultSuccess",
			err:                 nil,
			expectedMessage:     "",
			result:              runnerv1.Result_RESULT_SUCCESS,
			expectedStateResult: runnerv1.Result_RESULT_SUCCESS,
		},
		{
			name:                    "ResultUnspecifiedAndErrorIsResultFailure",
			err:                     errors.New("ERROR_MESSAGE"),
			expectedMessage:         "ERROR_MESSAGE",
			result:                  runnerv1.Result_RESULT_UNSPECIFIED,
			expectedStateResult:     runnerv1.Result_RESULT_FAILURE,
			expectedStepStateResult: runnerv1.Result_RESULT_CANCELLED,
		},
		{
			name:                    "ResultUnspecifiedAndNilErrorIsResultFailure",
			err:                     nil,
			expectedMessage:         closeCancelledMessage,
			result:                  runnerv1.Result_RESULT_UNSPECIFIED,
			expectedStateResult:     runnerv1.Result_RESULT_FAILURE,
			expectedStepStateResult: runnerv1.Result_RESULT_CANCELLED,
		},
		{
			name:                "ResultSkippedAndErrorIsResultSkipped",
			err:                 errors.New("ERROR_MESSAGE"),
			expectedMessage:     "ERROR_MESSAGE",
			result:              runnerv1.Result_RESULT_SKIPPED,
			expectedStateResult: runnerv1.Result_RESULT_SKIPPED,
		},
		{
			name:                    "Timeout",
			err:                     context.DeadlineExceeded,
			expectedMessage:         closeTimeoutMessage,
			result:                  runnerv1.Result_RESULT_UNSPECIFIED,
			expectedStateResult:     runnerv1.Result_RESULT_CANCELLED,
			expectedStepStateResult: runnerv1.Result_RESULT_CANCELLED,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var message *string
			if testCase.expectedMessage != "" {
				message = &testCase.expectedMessage
			}
			reporter := mockReporterCloser(t, message, &testCase.expectedStateResult)

			// cancel() verifies Close can operate after the context is cancelled
			// because it uses the daemon context instead
			reporter.cancel()
			reporter.state.Result = testCase.result
			reporter.state.Steps = []*runnerv1.StepState{
				{
					Result: runnerv1.Result_RESULT_UNSPECIFIED,
				},
			}
			require.NoError(t, reporter.Close(testCase.err))
			assert.Equal(t, testCase.expectedStepStateResult, reporter.state.Steps[0].Result)
		})
	}
}
