// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package license

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
)

const defaultCloudURL = "https://console.dagu.sh"

// maxResponseSize is the maximum number of bytes read from a Cloud API response (1 MB).
const maxResponseSize = 1 << 20

// CloudClient communicates with the Dagu Cloud API for license operations.
type CloudClient struct {
	baseURL string
	client  *http.Client
}

// NewCloudClient creates a client for the given Cloud API base URL.
// If baseURL is empty, the production URL is used.
func NewCloudClient(baseURL string) *CloudClient {
	if baseURL == "" {
		baseURL = defaultCloudURL
	}
	return &CloudClient{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ActivateRequest is the request body for license activation.
type ActivateRequest struct {
	Key           string `json:"key"`
	ServerID      string `json:"server_id"`
	MachineName   string `json:"machine_name"`
	ClientVersion string `json:"client_version,omitempty"`
}

// ActivateResponse is the response body from license activation.
type ActivateResponse struct {
	Token           string `json:"token"`
	HeartbeatSecret string `json:"heartbeat_secret"`
}

// HeartbeatRequest is the request body for heartbeat.
type HeartbeatRequest struct {
	LicenseID       string `json:"license_id"`
	ServerID        string `json:"server_id"`
	HeartbeatSecret string `json:"heartbeat_secret"`
	ClientVersion   string `json:"client_version,omitempty"`
}

// HeartbeatResponse is the response body from heartbeat.
type HeartbeatResponse struct {
	Token string `json:"token"`
}

// CloudError represents an error response from the Cloud API.
type CloudError struct {
	StatusCode int
	Message    string
}

func (e *CloudError) Error() string {
	return fmt.Sprintf("cloud API error (status %d): %s", e.StatusCode, e.Message)
}

// Activate exchanges a license key for a signed JWT token.
func (c *CloudClient) Activate(ctx context.Context, req ActivateRequest) (*ActivateResponse, error) {
	var resp ActivateResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/licenses/activate", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Heartbeat sends a heartbeat to keep the license active and get a refreshed token.
func (c *CloudClient) Heartbeat(ctx context.Context, req HeartbeatRequest) (*HeartbeatResponse, error) {
	var resp HeartbeatResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/licenses/heartbeat", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *CloudClient) doJSON(ctx context.Context, method, path string, reqBody, respBody any) error {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "dagu-oss/"+config.Version)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respData, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := string(respData)
		// Try to extract a message from JSON error response
		var errResp struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(respData, &errResp) == nil && errResp.Message != "" {
			msg = errResp.Message
		}
		return &CloudError{
			StatusCode: resp.StatusCode,
			Message:    msg,
		}
	}

	if err := json.Unmarshal(respData, respBody); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return nil
}
