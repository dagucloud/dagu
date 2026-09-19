// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package llm

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"
)

const (
	// statusOverloaded indicates provider overload.
	statusOverloaded  = 529
	maxErrorBodyBytes = 64 * 1024
)

// HTTPClient performs HTTP requests with retry logic.
// Uses plain net/http instead of resty to ensure response bodies are
// properly closed on retries (resty + SetDoNotParseResponse leaks FDs).
type HTTPClient struct {
	client          *http.Client
	maxRetries      int
	initialInterval time.Duration
	maxInterval     time.Duration
}

// NewHTTPClient creates a new HTTP client with the given configuration.
// Each client gets its own http.Transport to avoid sharing connection state
// across unrelated providers.
func NewHTTPClient(cfg Config) *HTTPClient {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		MaxConnsPerHost:       50,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}

	return &HTTPClient{
		client:          &http.Client{Transport: transport, Timeout: cfg.Timeout},
		maxRetries:      cfg.MaxRetries,
		initialInterval: cfg.InitialInterval,
		maxInterval:     cfg.MaxInterval,
	}
}

// Do performs an HTTP POST request with retry logic.
// Returns the response body as an io.ReadCloser for streaming support.
// Retries on network errors, rate limiting, and transient server errors.
func (c *HTTPClient) Do(ctx context.Context, url string, body []byte, headers map[string]string) (io.ReadCloser, error) {
	var lastErr error

	for attempt := range c.maxRetries + 1 {
		if attempt > 0 {
			backoff := c.backoff(attempt)
			// Provider errors can contain credentials or sensitive request data.
			attrs := []any{"attempt", attempt}
			if apiErr, ok := errors.AsType[*APIError](lastErr); ok {
				attrs = append(attrs, "status", apiErr.StatusCode)
			} else {
				attrs = append(attrs, "error_kind", "transport")
			}
			slog.Warn("HTTP request failed, retrying", attrs...)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp.Body, nil
		}

		// Read error body and close before potential retry.
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		_ = resp.Body.Close()

		lastErr = NewAPIError("llm", resp.StatusCode, string(errBody))

		if !isRetryableStatusCode(resp.StatusCode) {
			return nil, lastErr
		}
	}

	return nil, lastErr
}

// backoff returns the wait duration for the given attempt (1-indexed).
func (c *HTTPClient) backoff(attempt int) time.Duration {
	d := c.initialInterval
	for range attempt - 1 {
		d *= 2
	}
	if d > c.maxInterval {
		d = c.maxInterval
	}
	return d
}
