// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package license

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestCloudClient creates a test HTTP server with the given handler and a CloudClient
// pointing at it. The server is automatically closed when the test finishes.
func newTestCloudClient(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *CloudClient) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := NewCloudClient(server.URL)
	transport, ok := http.DefaultTransport.(*http.Transport)
	require.True(t, ok, "default transport should be *http.Transport")
	client.client.Transport = transport.Clone()
	t.Cleanup(client.client.CloseIdleConnections)
	return server, client
}

// TestNewCloudClient verifies URL selection logic in the constructor.
func TestNewCloudClient(t *testing.T) {
	t.Parallel()

	t.Run("empty URL uses default cloud URL", func(t *testing.T) {
		t.Parallel()

		client := NewCloudClient("")

		assert.Equal(t, defaultCloudURL, client.baseURL)
		assert.NotNil(t, client.client)
	})

	t.Run("custom URL is preserved", func(t *testing.T) {
		t.Parallel()

		customURL := "https://my-custom-cloud.example.com"
		client := NewCloudClient(customURL)

		assert.Equal(t, customURL, client.baseURL)
		assert.NotNil(t, client.client)
	})
}

// TestCloudError_Error verifies the error message formatting.
func TestCloudError_Error(t *testing.T) {
	t.Parallel()

	t.Run("formats status code and message correctly", func(t *testing.T) {
		t.Parallel()

		err := &CloudError{StatusCode: 500, Message: "something went wrong"}

		assert.Equal(t, "cloud API error (status 500): something went wrong", err.Error())
	})
}

// TestCloudClient_Activate covers all Activate method scenarios.
func TestCloudClient_Activate(t *testing.T) {
	t.Parallel()

	t.Run("200 success parses response and verifies request body", func(t *testing.T) {
		t.Parallel()

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Verify request fields.
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)

			var req ActivateRequest
			require.NoError(t, json.Unmarshal(body, &req))
			assert.Equal(t, "license-key-abc", req.Key)
			assert.Equal(t, "server-id-xyz", req.ServerID)
			assert.Equal(t, "my-machine", req.MachineName)

			// Write successful response.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(ActivateResponse{
				Token:           "signed-jwt-token",
				HeartbeatSecret: "heartbeat-secret-value",
			})
		})

		_, client := newTestCloudClient(t, handler)

		resp, err := client.Activate(context.Background(), ActivateRequest{
			Key:         "license-key-abc",
			ServerID:    "server-id-xyz",
			MachineName: "my-machine",
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, "signed-jwt-token", resp.Token)
		assert.Equal(t, "heartbeat-secret-value", resp.HeartbeatSecret)
	})

	t.Run("500 with JSON error body returns CloudError with extracted message", func(t *testing.T) {
		t.Parallel()

		handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "server error"})
		})

		_, client := newTestCloudClient(t, handler)

		resp, err := client.Activate(context.Background(), ActivateRequest{
			Key:         "key",
			ServerID:    "srv",
			MachineName: "host",
		})

		require.Error(t, err)
		assert.Nil(t, resp)

		var cloudErr *CloudError
		require.True(t, errors.As(err, &cloudErr), "error should be *CloudError")
		assert.Equal(t, http.StatusInternalServerError, cloudErr.StatusCode)
		assert.Equal(t, "server error", cloudErr.Message)
	})

	t.Run("500 with plain text body returns CloudError with raw body as message", func(t *testing.T) {
		t.Parallel()

		handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal server error"))
		})

		_, client := newTestCloudClient(t, handler)

		resp, err := client.Activate(context.Background(), ActivateRequest{
			Key:         "key",
			ServerID:    "srv",
			MachineName: "host",
		})

		require.Error(t, err)
		assert.Nil(t, resp)

		var cloudErr *CloudError
		require.True(t, errors.As(err, &cloudErr), "error should be *CloudError")
		assert.Equal(t, http.StatusInternalServerError, cloudErr.StatusCode)
		assert.Equal(t, "internal server error", cloudErr.Message)
	})

	t.Run("network error returns error", func(t *testing.T) {
		t.Parallel()

		// Close the server immediately so all connections are refused.
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
		server.Close()

		client := NewCloudClient(server.URL)

		resp, err := client.Activate(context.Background(), ActivateRequest{
			Key:         "key",
			ServerID:    "srv",
			MachineName: "host",
		})

		require.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "request failed")
	})

	t.Run("200 with invalid JSON body returns unmarshal error", func(t *testing.T) {
		t.Parallel()

		handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("this is not json {{{"))
		})

		_, client := newTestCloudClient(t, handler)

		resp, err := client.Activate(context.Background(), ActivateRequest{
			Key:         "key",
			ServerID:    "srv",
			MachineName: "host",
		})

		require.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "failed to unmarshal response")
	})
}

// TestCloudClient_Heartbeat covers all Heartbeat method scenarios.
func TestCloudClient_Heartbeat(t *testing.T) {
	t.Parallel()

	t.Run("200 success parses response and verifies request body", func(t *testing.T) {
		t.Parallel()

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Verify request fields.
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)

			var req HeartbeatRequest
			require.NoError(t, json.Unmarshal(body, &req))
			assert.Equal(t, "license-id-123", req.LicenseID)
			assert.Equal(t, "server-id-xyz", req.ServerID)
			assert.Equal(t, "heartbeat-secret-value", req.HeartbeatSecret)

			// Write successful response.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(HeartbeatResponse{
				Token: "refreshed-jwt-token",
			})
		})

		_, client := newTestCloudClient(t, handler)

		resp, err := client.Heartbeat(context.Background(), HeartbeatRequest{
			LicenseID:       "license-id-123",
			ServerID:        "server-id-xyz",
			HeartbeatSecret: "heartbeat-secret-value",
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, "refreshed-jwt-token", resp.Token)
	})

	t.Run("410 Gone returns CloudError with status 410", func(t *testing.T) {
		t.Parallel()

		handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusGone)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "license has been revoked"})
		})

		_, client := newTestCloudClient(t, handler)

		resp, err := client.Heartbeat(context.Background(), HeartbeatRequest{
			LicenseID:       "license-id-123",
			ServerID:        "server-id-xyz",
			HeartbeatSecret: "secret",
		})

		require.Error(t, err)
		assert.Nil(t, resp)

		var cloudErr *CloudError
		require.True(t, errors.As(err, &cloudErr), "error should be *CloudError")
		assert.Equal(t, http.StatusGone, cloudErr.StatusCode)
	})

	t.Run("401 Unauthorized returns CloudError with status 401", func(t *testing.T) {
		t.Parallel()

		handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "unauthorized"})
		})

		_, client := newTestCloudClient(t, handler)

		resp, err := client.Heartbeat(context.Background(), HeartbeatRequest{
			LicenseID:       "license-id-123",
			ServerID:        "server-id-xyz",
			HeartbeatSecret: "wrong-secret",
		})

		require.Error(t, err)
		assert.Nil(t, resp)

		var cloudErr *CloudError
		require.True(t, errors.As(err, &cloudErr), "error should be *CloudError")
		assert.Equal(t, http.StatusUnauthorized, cloudErr.StatusCode)
	})

	t.Run("canceled context returns error", func(t *testing.T) {
		t.Parallel()

		// Use a channel to keep the handler blocked until the test is done,
		// ensuring the context cancellation is the first thing the client sees.
		blockCh := make(chan struct{})
		handler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			<-blockCh
		})

		_, client := newTestCloudClient(t, handler)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately before making the request.

		resp, err := client.Heartbeat(ctx, HeartbeatRequest{
			LicenseID:       "license-id-123",
			ServerID:        "server-id-xyz",
			HeartbeatSecret: "secret",
		})

		close(blockCh) // Unblock the handler so the test server can clean up.

		require.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "request failed")
	})
}

// TestCloudClient_RequestHeaders verifies that doJSON (exercised via Activate and Heartbeat)
// sets the expected HTTP headers on every outgoing request.
func TestCloudClient_RequestHeaders(t *testing.T) {
	t.Parallel()

	t.Run("Content-Type header is application/json", func(t *testing.T) {
		t.Parallel()

		var capturedContentType string
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedContentType = r.Header.Get("Content-Type")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(ActivateResponse{Token: "tok", HeartbeatSecret: "sec"})
		})

		_, client := newTestCloudClient(t, handler)

		_, err := client.Activate(context.Background(), ActivateRequest{
			Key:         "key",
			ServerID:    "srv",
			MachineName: "host",
		})

		require.NoError(t, err)
		assert.Equal(t, "application/json", capturedContentType)
	})

	t.Run("User-Agent header starts with dagu-oss/", func(t *testing.T) {
		t.Parallel()

		var capturedUserAgent string
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedUserAgent = r.Header.Get("User-Agent")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(HeartbeatResponse{Token: "refreshed-token"})
		})

		_, client := newTestCloudClient(t, handler)

		_, err := client.Heartbeat(context.Background(), HeartbeatRequest{
			LicenseID:       "license-id-123",
			ServerID:        "srv",
			HeartbeatSecret: "secret",
		})

		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(capturedUserAgent, "dagu-oss/"),
			"User-Agent %q should start with \"dagu-oss/\"", capturedUserAgent)
	})
}

func TestCloudClient_PullGitHubDispatch(t *testing.T) {
	t.Parallel()

	t.Run("200 returns dispatch job", func(t *testing.T) {
		t.Parallel()

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v1/github/dispatch/pull", r.URL.Path)
			var req PullGitHubDispatchRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			assert.Equal(t, "lic-1", req.LicenseID)
			assert.Equal(t, "srv-1", req.ServerID)
			assert.Equal(t, "secret-1", req.HeartbeatSecret)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(GitHubDispatchJob{
				ID:             "job-1",
				LicenseID:      "lic-1",
				DAGName:        "ci.yaml",
				EventName:      "push",
				Payload:        json.RawMessage(`{"ref":"refs/heads/main"}`),
				Headers:        json.RawMessage(`{"x-github-event":["push"]}`),
				RepositoryName: "dagucloud/dagu",
			})
		})

		_, client := newTestCloudClient(t, handler)

		job, err := client.PullGitHubDispatch(context.Background(), PullGitHubDispatchRequest{
			LicenseID:       "lic-1",
			ServerID:        "srv-1",
			HeartbeatSecret: "secret-1",
		})
		require.NoError(t, err)
		require.NotNil(t, job)
		assert.Equal(t, "job-1", job.ID)
		assert.JSONEq(t, `{"ref":"refs/heads/main"}`, string(job.Payload))
	})

	t.Run("204 returns nil job", func(t *testing.T) {
		t.Parallel()

		handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})

		_, client := newTestCloudClient(t, handler)

		job, err := client.PullGitHubDispatch(context.Background(), PullGitHubDispatchRequest{})
		require.NoError(t, err)
		assert.Nil(t, job)
	})

	t.Run("200 empty body returns error", func(t *testing.T) {
		t.Parallel()

		handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		_, client := newTestCloudClient(t, handler)

		job, err := client.PullGitHubDispatch(context.Background(), PullGitHubDispatchRequest{})
		require.Error(t, err)
		assert.Nil(t, job)
		assert.Contains(t, err.Error(), "empty response body")
	})

	t.Run("200 missing id returns error", func(t *testing.T) {
		t.Parallel()

		handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(GitHubDispatchJob{
				DAGName: "ci.yaml",
			})
		})

		_, client := newTestCloudClient(t, handler)

		job, err := client.PullGitHubDispatch(context.Background(), PullGitHubDispatchRequest{})
		require.Error(t, err)
		assert.Nil(t, job)
		assert.Contains(t, err.Error(), "missing id")
	})
}

func TestCloudClient_AcceptAndFinishGitHubDispatch(t *testing.T) {
	t.Parallel()

	t.Run("accept posts dag run id", func(t *testing.T) {
		t.Parallel()

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v1/github/dispatch/job%2F1%3Fx=1/accept", r.URL.EscapedPath())
			assert.Empty(t, r.URL.RawQuery)
			var req AcceptGitHubDispatchRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			assert.Equal(t, "lic-1", req.LicenseID)
			assert.Equal(t, "srv-1", req.ServerID)
			assert.Equal(t, "secret-1", req.Secret)
			assert.Equal(t, "run-1", req.DAGRunID)
			w.WriteHeader(http.StatusNoContent)
		})

		_, client := newTestCloudClient(t, handler)

		err := client.AcceptGitHubDispatch(context.Background(), "job/1?x=1", AcceptGitHubDispatchRequest{
			LicenseID: "lic-1",
			ServerID:  "srv-1",
			Secret:    "secret-1",
			DAGRunID:  "run-1",
		})
		require.NoError(t, err)
	})

	t.Run("finish posts terminal status", func(t *testing.T) {
		t.Parallel()

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v1/github/dispatch/job%2F1%3Fx=1/finish", r.URL.EscapedPath())
			assert.Empty(t, r.URL.RawQuery)
			var req FinishGitHubDispatchRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			assert.Equal(t, "succeeded", req.ResultStatus)
			assert.Equal(t, "all green", req.ResultSummary)
			w.WriteHeader(http.StatusNoContent)
		})

		_, client := newTestCloudClient(t, handler)

		err := client.FinishGitHubDispatch(context.Background(), "job/1?x=1", FinishGitHubDispatchRequest{
			LicenseID:     "lic-1",
			ServerID:      "srv-1",
			Secret:        "secret-1",
			ResultStatus:  "succeeded",
			ResultSummary: "all green",
		})
		require.NoError(t, err)
	})
}
