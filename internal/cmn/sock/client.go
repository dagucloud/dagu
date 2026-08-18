// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrTimeout          = errors.New("unix socket timeout")
	ErrTransport        = errors.New("unix socket transport error")
	ErrUnexpectedStatus = errors.New("unexpected HTTP status")
)

// Client is a unix socket client that can send requests
// to the frontend over HTTP.
type Client struct {
	addr   string
	client *http.Client
}

// NewClient creates a unix socket HTTP client with a reusable transport.
func NewClient(addr string) *Client {
	cl := &Client{addr: addr}
	cl.client = &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				conn, err := (&net.Dialer{}).DialContext(ctx, "unix", cl.addr)
				if err != nil {
					return nil, wrapTimeout("dial unix socket", err)
				}
				return conn, nil
			},
			DisableCompression: true,
		},
		Timeout: defaultTimeout,
	}
	return cl
}

const (
	defaultTimeout               = 3 * time.Second
	maxUnexpectedStatusBodyBytes = 256
)

// wrapTimeout normalizes timeout errors to the exported socket timeout sentinel.
func wrapTimeout(op string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%s: %w", op, ErrTimeout)
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("%s: %w", op, ErrTimeout)
	}

	return fmt.Errorf("%s: %w", op, err)
}

// normalizePath ensures requests always use an absolute HTTP path.
func normalizePath(path string) string {
	if path == "" {
		return "/"
	}
	if strings.HasPrefix(path, "/") {
		return path
	}
	return "/" + path
}

// Request sends a request to the frontend and returns the response body.
// Non-successful HTTP responses are returned as errors.
func (cl *Client) Request(method, path string) (string, error) {
	requestURL := &url.URL{
		Scheme: "http",
		Host:   "unix",
		Path:   normalizePath(path),
	}
	request, err := http.NewRequest(method, requestURL.String(), nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}

	response, err := cl.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrTransport, wrapTimeout("send request", err))
	}
	defer func() {
		_ = response.Body.Close()
	}()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, maxUnexpectedStatusBodyBytes))
		detail := strings.TrimSpace(string(body))
		if detail == "" {
			return "", fmt.Errorf("%w: %s", ErrUnexpectedStatus, response.Status)
		}
		return "", fmt.Errorf("%w: %s: %s", ErrUnexpectedStatus, response.Status, detail)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrTransport, wrapTimeout("read response body", err))
	}

	return string(body), nil
}

// SocketAddr returns the address of the unix socket.
func (cl *Client) SocketAddr() string {
	return cl.addr
}
