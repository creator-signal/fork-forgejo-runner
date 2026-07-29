// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func PipelineURL(endpoint string) string {
	return strings.TrimSuffix(endpoint, "/") + "/api/actions_pipeline/"
}

// UploadJobSummary sends the job's GITHUB_STEP_SUMMARY markdown to the Forgejo server.
// It is then saved in the db and accessible through the same api
func (c *HTTPClient) UploadJobSummary(ctx context.Context, runID, runtimeToken, content string) error {
	url := PipelineURL(c.endpoint) + "_apis/pipelines/workflows/" + runID + "/summary"

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, strings.NewReader(content))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+runtimeToken)
	req.Header.Set("Content-Type", "text/markdown")

	resp, err := getHTTPClient(c.endpoint, c.insecure).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("unexpected status %d uploading job summary: %s", resp.StatusCode, body)
	}
	return nil
}
