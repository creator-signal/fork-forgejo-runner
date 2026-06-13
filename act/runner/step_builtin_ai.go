// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"code.forgejo.org/forgejo/runner/v12/act/common"
	"code.forgejo.org/forgejo/runner/v12/act/model"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/ver"
)

var _ step = &stepAuthorizedIntegration{}

type stepAuthorizedIntegration struct {
	Step             *model.Step
	RunContext       *RunContext
	env              map[string]string
	WorkingDirectory string
}

func (s *stepAuthorizedIntegration) pre() common.Executor {
	s.env = map[string]string{}

	return func(ctx context.Context) error {
		return nil
	}
}

func (s *stepAuthorizedIntegration) main() common.Executor {
	return runStepExecutor(s, stepStageMain, func(ctx context.Context) error {
		if common.Dryrun(ctx) {
			return nil
		}

		requestToken := s.RunContext.Env["ACTIONS_ID_TOKEN_REQUEST_TOKEN"]
		requestURL := s.RunContext.Env["ACTIONS_ID_TOKEN_REQUEST_URL"]
		if requestToken == "" || requestURL == "" {
			return errors.New("no OIDC token or request URL found; enable it by setting `enable-openid-connect: true`")
		}

		audience, found := s.Step.With["audience"]
		if !found {
			return errors.New("audience not specified")
		}

		jwt, err := requestJWT(ctx, requestURL, requestToken, audience)
		if err != nil {
			return fmt.Errorf("failed to request JWT: %w", err)
		}

		s.RunContext.AddMask(jwt)
		s.RunContext.setOutput(ctx, map[string]string{"name": "jwt"}, jwt)

		return nil
	})
}

func (s *stepAuthorizedIntegration) post() common.Executor {
	return func(ctx context.Context) error {
		return nil
	}
}

func (s *stepAuthorizedIntegration) getRunContext() *RunContext {
	return s.RunContext
}

func (s *stepAuthorizedIntegration) getGithubContext(ctx context.Context) *model.GithubContext {
	return s.getRunContext().getGithubContext(ctx)
}

func (s *stepAuthorizedIntegration) getStepModel() *model.Step {
	return s.Step
}

func (s *stepAuthorizedIntegration) getEnv() *map[string]string {
	return &s.env
}

func (s *stepAuthorizedIntegration) getIfExpression(_ context.Context, _ stepStage) string {
	return s.Step.If.Value
}

func requestJWT(ctx context.Context, requestURL, requestToken, audience string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", requestURL, nil)
	if err != nil {
		return "", fmt.Errorf("could not create a new HTTP request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", requestToken))
	req.Header.Set("User-Agent", fmt.Sprintf("forgejo-runner/%s", ver.Version()))

	query := req.URL.Query()
	query.Add("audience", audience)
	req.URL.RawQuery = query.Encode()

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not request JWT: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("JWT request failed with status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("could not read response to JWT request: %w", err)
	}

	type JWT struct {
		Value string `json:"value"`
	}

	var jwt JWT
	if err = json.Unmarshal(body, &jwt); err != nil {
		return "", fmt.Errorf("could not extract JWT from response: %w", err)
	}

	if jwt.Value == "" {
		return "", fmt.Errorf("JWT is unexpectedly empty")
	}

	return jwt.Value, nil
}
