// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/clicontext"
)

const defaultRemoteTimeout = 30 * time.Second

type remoteClient struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

type remoteError struct {
	StatusCode int
	Message    string
}

func (e *remoteError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("remote request failed with status %d", e.StatusCode)
	}
	return e.Message
}

func (e *remoteError) NotFound() bool {
	return e != nil && e.StatusCode == http.StatusNotFound
}

type remoteHistoryQuery struct {
	Name     string
	From     *int64
	To       *int64
	Statuses []int
	RunID    string
	Labels   []string
}

func newRemoteClient(ctx *clicontext.Context) (*remoteClient, error) {
	if ctx == nil {
		return nil, errors.New("remote context is required")
	}
	timeout := defaultRemoteTimeout
	if ctx.TimeoutSeconds > 0 {
		timeout = time.Duration(ctx.TimeoutSeconds) * time.Second
	}
	baseURL := strings.TrimRight(ctx.ServerURL, "/")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: ctx.SkipTLSVerify} //nolint:gosec
	return &remoteClient{
		baseURL: baseURL,
		apiKey:  ctx.APIKey,
		client: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
	}, nil
}

func (c *remoteClient) Test(ctx context.Context) error {
	_, err := c.getCurrentUser(ctx)
	return err
}

func (c *remoteClient) getCurrentUser(ctx context.Context) (*api.UserResponse, error) {
	var out api.UserResponse
	if err := c.do(ctx, http.MethodGet, "/auth/me", nil, &out, nil); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) getDAGByFileName(ctx context.Context, fileName string) (*api.DAGFile, error) {
	var out api.DAGFile
	err := c.do(ctx, http.MethodGet, "/dags/"+url.PathEscape(fileName), nil, &out, nil)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) listDAGs(ctx context.Context, params map[string]string) ([]api.DAGFile, error) {
	var out struct {
		Dags []api.DAGFile `json:"dags"`
	}
	if err := c.do(ctx, http.MethodGet, "/dags", nil, &out, params); err != nil {
		return nil, err
	}
	return out.Dags, nil
}

func (c *remoteClient) resolveDAG(ctx context.Context, arg string) (*api.DAGFile, error) {
	if isLikelyLocalDAGArg(arg) {
		return nil, fmt.Errorf("remote contexts only operate on DAGs already deployed on the server; use the remote DAG fileName instead of %q", arg)
	}
	dag, err := c.getDAGByFileName(ctx, arg)
	if err == nil {
		return dag, nil
	}
	var rerr *remoteError
	if !errors.As(err, &rerr) || !rerr.NotFound() {
		return nil, err
	}
	dags, err := c.listDAGs(ctx, map[string]string{"name": arg})
	if err != nil {
		return nil, err
	}
	if len(dags) == 0 {
		return nil, fmt.Errorf("remote DAG %q was not found", arg)
	}
	if len(dags) > 1 {
		return nil, fmt.Errorf("remote DAG name %q is ambiguous; use the exact fileName", arg)
	}
	return &dags[0], nil
}

func isLikelyLocalDAGArg(arg string) bool {
	if strings.HasSuffix(arg, ".yaml") || strings.HasSuffix(arg, ".yml") {
		return true
	}
	if strings.ContainsRune(arg, filepath.Separator) {
		return true
	}
	return false
}

func (c *remoteClient) startDAG(ctx context.Context, fileName string, body api.ExecuteDAGJSONBody) (*api.DAGRunSummary, error) {
	var out api.DAGRunSummary
	if err := c.do(ctx, http.MethodPost, "/dags/"+url.PathEscape(fileName)+"/start", body, &out, nil); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) enqueueDAG(ctx context.Context, fileName string, body api.EnqueueDAGDAGRunJSONBody) (*api.DAGRunSummary, error) {
	var out api.DAGRunSummary
	if err := c.do(ctx, http.MethodPost, "/dags/"+url.PathEscape(fileName)+"/enqueue", body, &out, nil); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) listDAGRuns(ctx context.Context, query remoteHistoryQuery) ([]api.DAGRunSummary, error) {
	params := url.Values{}
	if query.From != nil {
		params.Set("fromDate", fmt.Sprintf("%d", *query.From))
	}
	if query.To != nil {
		params.Set("toDate", fmt.Sprintf("%d", *query.To))
	}
	if len(query.Statuses) > 0 {
		for _, status := range query.Statuses {
			params.Add("status", fmt.Sprintf("%d", status))
		}
	}
	if query.RunID != "" {
		params.Set("dagRunId", query.RunID)
	}
	if len(query.Labels) > 0 {
		params.Set("labels", strings.Join(query.Labels, ","))
	}

	var out struct {
		DagRuns []api.DAGRunSummary `json:"dagRuns"`
	}
	path := "/dag-runs"
	if query.Name != "" {
		path = "/dag-runs/" + url.PathEscape(query.Name)
	}
	if err := c.doWithQueryValues(ctx, http.MethodGet, path, nil, &out, params); err != nil {
		return nil, err
	}
	return out.DagRuns, nil
}

func (c *remoteClient) getDAGRunDetails(ctx context.Context, name, dagRunID string) (*api.DAGRunDetails, error) {
	var out api.DAGRunDetails
	if err := c.do(ctx, http.MethodGet, dagRunPath(name, dagRunID), nil, &out, nil); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) stopAllDAGRuns(ctx context.Context, fileName string) error {
	return c.do(ctx, http.MethodPost, "/dags/"+url.PathEscape(fileName)+"/stop-all", nil, nil, nil)
}

func (c *remoteClient) stopDAGRun(ctx context.Context, name, dagRunID string) error {
	return c.do(ctx, http.MethodPost, dagRunPath(name, dagRunID)+"/stop", nil, nil, nil)
}

func (c *remoteClient) retryDAGRun(ctx context.Context, name, dagRunID string, body api.RetryDAGRunJSONBody) error {
	return c.do(ctx, http.MethodPost, dagRunPath(name, dagRunID)+"/retry", body, nil, nil)
}

func (c *remoteClient) rescheduleDAGRun(ctx context.Context, name, dagRunID string, body api.RescheduleDAGRunJSONBody) (*api.RescheduleDAGRun200JSONResponse, error) {
	var out api.RescheduleDAGRun200JSONResponse
	if err := c.do(ctx, http.MethodPost, dagRunPath(name, dagRunID)+"/reschedule", body, &out, nil); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) dequeueDAGRun(ctx context.Context, name, dagRunID string) error {
	return c.do(ctx, http.MethodGet, dagRunPath(name, dagRunID)+"/dequeue", nil, nil, nil)
}

func (c *remoteClient) listQueueItems(ctx context.Context, queueName string, limit int, cursor string) (*api.QueuedDAGRunsPageResponse, error) {
	var out api.QueuedDAGRunsPageResponse
	params := map[string]string{}
	if limit > 0 {
		params["limit"] = fmt.Sprintf("%d", limit)
	}
	if cursor != "" {
		params["cursor"] = cursor
	}
	if err := c.do(ctx, http.MethodGet, "/queues/"+url.PathEscape(queueName)+"/items", nil, &out, params); err != nil {
		return nil, err
	}
	return &out, nil
}

func dagRunPath(name, dagRunID string) string {
	return "/dag-runs/" + url.PathEscape(name) + "/" + url.PathEscape(dagRunID)
}

func (c *remoteClient) do(ctx context.Context, method, path string, body any, out any, query map[string]string) error {
	values := url.Values{}
	for key, value := range query {
		if value != "" {
			values.Set(key, value)
		}
	}
	return c.doWithQueryValues(ctx, method, path, body, out, values)
}

func (c *remoteClient) doWithQueryValues(ctx context.Context, method, path string, body any, out any, query url.Values) error {
	fullURL := c.baseURL + path
	if len(query) > 0 {
		if encoded := query.Encode(); encoded != "" {
			fullURL += "?" + encoded
		}
	}

	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeRemoteError(resp)
	}
	if out == nil {
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			return err
		}
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func decodeRemoteError(resp *http.Response) error {
	data, _ := io.ReadAll(resp.Body)
	if len(data) == 0 {
		return &remoteError{StatusCode: resp.StatusCode, Message: resp.Status}
	}
	var apiErr api.Error
	if err := json.Unmarshal(data, &apiErr); err == nil && apiErr.Message != "" {
		return &remoteError{StatusCode: resp.StatusCode, Message: apiErr.Message}
	}
	return &remoteError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(data))}
}
