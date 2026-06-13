// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package runner

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"code.forgejo.org/forgejo/runner/v12/act/model"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/ver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestStepAuthorizedIntegration(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		var receivedRequest *http.Request
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedRequest = r
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"value": "g6novup9"}`))
		}))
		srv.Start()
		defer srv.Close()

		cm := &containerMock{}

		sr := &stepAuthorizedIntegration{
			RunContext: &RunContext{
				StepResults: map[string]*model.StepResult{},
				ExprEval:    &expressionEvaluator{},
				Config:      &Config{},
				Run: &model.Run{
					JobID: "1",
					Workflow: &model.Workflow{
						Env: map[string]string{
							"ACTIONS_ID_TOKEN_REQUEST_TOKEN": "very-secret",
							"ACTIONS_ID_TOKEN_REQUEST_URL":   srv.URL,
						},
						Jobs: map[string]*model.Job{
							"1": {
								Defaults: model.Defaults{
									Run: model.RunDefaults{},
								},
							},
						},
					},
				},
				JobContainer: cm,
			},
			Step: &model.Step{
				ID:      "1",
				Builtin: "authorized-integration@v1",
				With:    map[string]string{"audience": "u:29:a1487420-fd3e-4787-9901-33f21e95b9d6"},
			},
		}

		cm.
			On("Copy", "/var/run/act", mock.AnythingOfType("[]*container.FileEntry")).
			Return(func(ctx context.Context) error { return nil })
		cm.
			On("Exec", []string{"bash", "--noprofile", "--norc", "-e", "-o", "pipefail", "/var/run/act/workflow/1.sh"}, mock.AnythingOfType("map[string]string"), "", "workdir").
			Return(func(ctx context.Context) error { return nil })
		cm.
			On("Copy", "/var/run/act", mock.AnythingOfType("[]*container.FileEntry")).
			Return(func(ctx context.Context) error { return nil })
		cm.
			On("UpdateFromEnv", "/var/run/act/workflow/envs.txt", mock.AnythingOfType("*map[string]string")).
			Return(func(ctx context.Context) error { return nil })
		cm.
			On("UpdateFromEnv", "/var/run/act/workflow/statecmd.txt", mock.AnythingOfType("*map[string]string")).
			Return(func(ctx context.Context) error { return nil })
		cm.
			On("UpdateFromEnv", "/var/run/act/workflow/outputcmd.txt", mock.AnythingOfType("*map[string]string")).
			Return(func(ctx context.Context) error { return nil })
		cm.
			On("GetContainerArchive", t.Context(), "/var/run/act/workflow/SUMMARY.md").
			Return(io.NopCloser(&bytes.Buffer{}), nil)
		cm.
			On("GetContainerArchive", t.Context(), "/var/run/act/workflow/pathcmd.txt").
			Return(io.NopCloser(&bytes.Buffer{}), nil)

		err := sr.pre().Then(sr.main()).Then(sr.post())(t.Context())
		require.NoError(t, err)

		assert.Equal(t, "GET", receivedRequest.Method)
		assert.Equal(t, "Bearer very-secret", receivedRequest.Header.Get("Authorization"))
		assert.Equal(t, "forgejo-runner/"+ver.Version(), receivedRequest.Header.Get("User-Agent"))
		assert.Equal(t, "audience=u%3A29%3Aa1487420-fd3e-4787-9901-33f21e95b9d6", receivedRequest.URL.RawQuery)

		assert.Contains(t, sr.RunContext.Masks, "g6novup9")
		assert.Equal(t, map[string]*model.StepResult{"1": {Outputs: map[string]string{"jwt": "g6novup9"}}},
			sr.RunContext.StepResults)
	})

	t.Run("ODIC disabled", func(t *testing.T) {
		var receivedRequest *http.Request
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedRequest = r
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"value": "g6novup9"}`))
		}))
		srv.Start()
		defer srv.Close()

		cm := &containerMock{}

		sr := &stepAuthorizedIntegration{
			RunContext: &RunContext{
				StepResults: map[string]*model.StepResult{},
				ExprEval:    &expressionEvaluator{},
				Config:      &Config{},
				Run: &model.Run{
					JobID: "1",
					Workflow: &model.Workflow{
						Env: map[string]string{},
						Jobs: map[string]*model.Job{
							"1": {
								Defaults: model.Defaults{
									Run: model.RunDefaults{},
								},
							},
						},
					},
				},
				JobContainer: cm,
			},
			Step: &model.Step{
				ID:      "1",
				Builtin: "authorized-integration@v1",
				With:    map[string]string{"audience": "u:29:a1487420-fd3e-4787-9901-33f21e95b9d6"},
			},
		}

		cm.
			On("Copy", "/var/run/act", mock.AnythingOfType("[]*container.FileEntry")).
			Return(func(ctx context.Context) error { return nil })
		cm.
			On("Exec", []string{"bash", "--noprofile", "--norc", "-e", "-o", "pipefail", "/var/run/act/workflow/1.sh"}, mock.AnythingOfType("map[string]string"), "", "workdir").
			Return(func(ctx context.Context) error { return nil })
		cm.
			On("Copy", "/var/run/act", mock.AnythingOfType("[]*container.FileEntry")).
			Return(func(ctx context.Context) error { return nil })
		cm.
			On("UpdateFromEnv", "/var/run/act/workflow/envs.txt", mock.AnythingOfType("*map[string]string")).
			Return(func(ctx context.Context) error { return nil })
		cm.
			On("UpdateFromEnv", "/var/run/act/workflow/statecmd.txt", mock.AnythingOfType("*map[string]string")).
			Return(func(ctx context.Context) error { return nil })
		cm.
			On("UpdateFromEnv", "/var/run/act/workflow/outputcmd.txt", mock.AnythingOfType("*map[string]string")).
			Return(func(ctx context.Context) error { return nil })
		cm.
			On("GetContainerArchive", t.Context(), "/var/run/act/workflow/SUMMARY.md").
			Return(io.NopCloser(&bytes.Buffer{}), nil)
		cm.
			On("GetContainerArchive", t.Context(), "/var/run/act/workflow/pathcmd.txt").
			Return(io.NopCloser(&bytes.Buffer{}), nil)

		err := sr.pre().Then(sr.main()).Then(sr.post())(t.Context())
		require.ErrorContains(t, err, "no OIDC token or request URL found; enable it by setting `enable-openid-connect: true")

		assert.Nil(t, receivedRequest)

		assert.Empty(t, sr.RunContext.Masks)
		assert.Equal(t, map[string]*model.StepResult{"1": {Outputs: map[string]string{}, Conclusion: 1, Outcome: 1}},
			sr.RunContext.StepResults)
	})

	t.Run("Missing audience argument", func(t *testing.T) {
		var receivedRequest *http.Request
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedRequest = r
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"value": "g6novup9"}`))
		}))
		srv.Start()
		defer srv.Close()

		cm := &containerMock{}

		sr := &stepAuthorizedIntegration{
			RunContext: &RunContext{
				StepResults: map[string]*model.StepResult{},
				ExprEval:    &expressionEvaluator{},
				Config:      &Config{},
				Run: &model.Run{
					JobID: "1",
					Workflow: &model.Workflow{
						Env: map[string]string{
							"ACTIONS_ID_TOKEN_REQUEST_TOKEN": "very-secret",
							"ACTIONS_ID_TOKEN_REQUEST_URL":   srv.URL,
						},
						Jobs: map[string]*model.Job{
							"1": {
								Defaults: model.Defaults{
									Run: model.RunDefaults{},
								},
							},
						},
					},
				},
				JobContainer: cm,
			},
			Step: &model.Step{
				ID:      "1",
				Builtin: "authorized-integration@v1",
			},
		}

		cm.
			On("Copy", "/var/run/act", mock.AnythingOfType("[]*container.FileEntry")).
			Return(func(ctx context.Context) error { return nil })
		cm.
			On("Exec", []string{"bash", "--noprofile", "--norc", "-e", "-o", "pipefail", "/var/run/act/workflow/1.sh"}, mock.AnythingOfType("map[string]string"), "", "workdir").
			Return(func(ctx context.Context) error { return nil })
		cm.
			On("Copy", "/var/run/act", mock.AnythingOfType("[]*container.FileEntry")).
			Return(func(ctx context.Context) error { return nil })
		cm.
			On("UpdateFromEnv", "/var/run/act/workflow/envs.txt", mock.AnythingOfType("*map[string]string")).
			Return(func(ctx context.Context) error { return nil })
		cm.
			On("UpdateFromEnv", "/var/run/act/workflow/statecmd.txt", mock.AnythingOfType("*map[string]string")).
			Return(func(ctx context.Context) error { return nil })
		cm.
			On("UpdateFromEnv", "/var/run/act/workflow/outputcmd.txt", mock.AnythingOfType("*map[string]string")).
			Return(func(ctx context.Context) error { return nil })
		cm.
			On("GetContainerArchive", t.Context(), "/var/run/act/workflow/SUMMARY.md").
			Return(io.NopCloser(&bytes.Buffer{}), nil)
		cm.
			On("GetContainerArchive", t.Context(), "/var/run/act/workflow/pathcmd.txt").
			Return(io.NopCloser(&bytes.Buffer{}), nil)

		err := sr.pre().Then(sr.main()).Then(sr.post())(t.Context())
		require.ErrorContains(t, err, "audience not specified")

		assert.Nil(t, receivedRequest)

		assert.Empty(t, sr.RunContext.Masks)
		assert.Equal(t, map[string]*model.StepResult{"1": {Outputs: map[string]string{}, Conclusion: 1, Outcome: 1}},
			sr.RunContext.StepResults)
	})
}

func TestRequestJWT(t *testing.T) {
	t.Run("Successful request", func(t *testing.T) {
		var receivedRequest *http.Request
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedRequest = r
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"value": "vpLSjEP"}`))
		}))
		srv.Start()
		defer srv.Close()

		token, err := requestJWT(t.Context(), srv.URL, "very-secret", "u:1:82f4d1bf-1d44-4e80-adff-42c5c113ac1f")
		require.NoError(t, err)

		assert.Equal(t, "GET", receivedRequest.Method)
		assert.Equal(t, "Bearer very-secret", receivedRequest.Header.Get("Authorization"))
		assert.Equal(t, "forgejo-runner/"+ver.Version(), receivedRequest.Header.Get("User-Agent"))
		assert.Equal(t, "audience=u%3A1%3A82f4d1bf-1d44-4e80-adff-42c5c113ac1f", receivedRequest.URL.RawQuery)
		assert.Equal(t, "vpLSjEP", token)
	})

	t.Run("Unauthorized", func(t *testing.T) {
		var receivedRequest *http.Request
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedRequest = r
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"value": "vpLSjEP"}`))
		}))
		srv.Start()
		defer srv.Close()

		_, err := requestJWT(t.Context(), srv.URL, "invalid", "u:2:d6f6cd33-8a15-43be-b4b1-079b4ecff181")
		require.ErrorContains(t, err, "JWT request failed with status 401")

		assert.Equal(t, "GET", receivedRequest.Method)
		assert.Equal(t, "Bearer invalid", receivedRequest.Header.Get("Authorization"))
		assert.Equal(t, "forgejo-runner/"+ver.Version(), receivedRequest.Header.Get("User-Agent"))
		assert.Equal(t, "audience=u%3A2%3Ad6f6cd33-8a15-43be-b4b1-079b4ecff181", receivedRequest.URL.RawQuery)
	})

	t.Run("Malformed response", func(t *testing.T) {
		var receivedRequest *http.Request
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedRequest = r
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`not json`))
		}))
		srv.Start()
		defer srv.Close()

		_, err := requestJWT(t.Context(), srv.URL, "very-secret", "u:2:d6f6cd33-8a15-43be-b4b1-079b4ecff181")
		require.ErrorContains(t, err, "could not extract JWT from response")

		assert.Equal(t, "GET", receivedRequest.Method)
		assert.Equal(t, "Bearer very-secret", receivedRequest.Header.Get("Authorization"))
		assert.Equal(t, "forgejo-runner/"+ver.Version(), receivedRequest.Header.Get("User-Agent"))
		assert.Equal(t, "audience=u%3A2%3Ad6f6cd33-8a15-43be-b4b1-079b4ecff181", receivedRequest.URL.RawQuery)
	})

	t.Run("Empty JWT", func(t *testing.T) {
		var receivedRequest *http.Request
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedRequest = r
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"value": ""}`))
		}))
		srv.Start()
		defer srv.Close()

		_, err := requestJWT(t.Context(), srv.URL, "very-secret", "u:2:d6f6cd33-8a15-43be-b4b1-079b4ecff181")
		require.ErrorContains(t, err, "JWT is unexpectedly empty")

		assert.Equal(t, "GET", receivedRequest.Method)
		assert.Equal(t, "Bearer very-secret", receivedRequest.Header.Get("Authorization"))
		assert.Equal(t, "forgejo-runner/"+ver.Version(), receivedRequest.Header.Get("User-Agent"))
		assert.Equal(t, "audience=u%3A2%3Ad6f6cd33-8a15-43be-b4b1-079b4ecff181", receivedRequest.URL.RawQuery)
	})
}
