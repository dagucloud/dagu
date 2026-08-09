// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/remotenode"
)

// WithRemoteNode is a middleware that checks if the request has a "remoteNode" query parameter.
// If it does, it proxies the request to the specified remote node.
func WithRemoteNode(resolver *remotenode.Resolver, apiBasePath string) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			remoteNodeName := r.URL.Query().Get("remoteNode")
			if remoteNodeName == "" || remoteNodeName == "local" {
				next.ServeHTTP(w, r)
				return
			}

			if resolver == nil {
				WriteErrorResponse(w, &Error{
					HTTPStatus: http.StatusServiceUnavailable,
					Code:       api.ErrorCodeInternalError,
					Message:    "remote node resolution is not available",
				})
				return
			}

			node, err := resolver.GetByName(r.Context(), remoteNodeName)
			if err != nil {
				if errors.Is(err, remotenode.ErrRemoteNodeNotFound) {
					WriteErrorResponse(w, &Error{
						HTTPStatus: http.StatusBadRequest,
						Code:       api.ErrorCodeBadRequest,
						Message:    fmt.Sprintf("remote node %s not found", remoteNodeName),
					})
				} else {
					WriteErrorResponse(w, &Error{
						HTTPStatus: http.StatusInternalServerError,
						Code:       api.ErrorCodeInternalError,
						Message:    fmt.Sprintf("failed to resolve remote node %s", remoteNodeName),
					})
				}
				return
			}
			// If the parameter is present, we need to handle the request differently
			// Call the handleRemoteNodeProxy function to proxy the request
			remoteNodeHandler := &remoteNodeProxy{
				remoteNode:  node,
				apiBasePath: apiBasePath,
			}
			resp, err := remoteNodeHandler.proxy(r)
			if err != nil {
				// If there was an error, write the error response
				WriteErrorResponse(w, err)
				return
			}

			defer func() {
				if resp.Body != nil {
					_ = resp.Body.Close()
				}
			}()

			var reader io.Reader = resp.Body
			switch resp.Header.Get("Content-Encoding") {
			case "gzip":
				gzReader, err := gzip.NewReader(resp.Body)
				if err != nil {
					WriteErrorResponse(w, &Error{
						Code:       api.ErrorCodeBadGateway,
						HTTPStatus: http.StatusBadGateway,
						Message:    fmt.Sprintf("failed to create gzip reader: %s", err.Error()),
					})
					return
				}
				defer func() {
					_ = gzReader.Close()
				}()
				reader = gzReader
			case "deflate":
				reader = flate.NewReader(resp.Body)
			}

			respData, err := io.ReadAll(reader)
			if err != nil {
				WriteErrorResponse(w, &Error{
					Code:       api.ErrorCodeBadGateway,
					HTTPStatus: http.StatusBadGateway,
					Message:    fmt.Sprintf("failed to read response body: %s", err.Error()),
				})
				return
			}

			logger.Info(r.Context(), "Received response from remote node",
				slog.Int("status-code", resp.StatusCode),
				slog.Int64("content-length", resp.ContentLength),
				slog.String("content-type", resp.Header.Get("Content-Type")),
				slog.Int("data-length", len(respData)))

			// Preserve structured errors returned by the remote API.
			if resp.StatusCode < 200 || resp.StatusCode > 299 {
				var remoteErr api.Error
				if len(respData) == 0 || json.Unmarshal(respData, &remoteErr) != nil || remoteErr.Code == "" {
					WriteErrorResponse(w, &Error{
						Code:       api.ErrorCodeBadGateway,
						HTTPStatus: resp.StatusCode,
						Message:    fmt.Sprintf("remote node responded with status %d", resp.StatusCode),
					})
					return
				}
			}

			w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
			w.WriteHeader(resp.StatusCode)
			if _, err = w.Write(respData); err != nil {
				logger.Error(r.Context(), "Failed to write response", tag.Error(err))
			}
		}

		return http.HandlerFunc(fn)
	}
}

type remoteNodeProxy struct {
	remoteNode  *remotenode.RemoteNode
	apiBasePath string
}

// handleRemoteNodeProxy checks if 'remoteNode' is present in the query parameters.
// If yes, it proxies the request to the remote node and returns the remote response.
// If not, it returns nil, indicating to proceed locally.
func (h *remoteNodeProxy) proxy(r *http.Request) (*http.Response, error) {
	legacyPath, hasLegacyWikiPath := legacyWikiProxyPath(r.URL.Path, h.apiBasePath)
	if !hasLegacyWikiPath {
		return h.doRequest(r.Body, r)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}
	resp, err := h.doRequest(bytes.NewReader(body), r)
	if err != nil || resp.StatusCode != http.StatusNotFound || strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		return resp, err
	}
	_ = resp.Body.Close()

	legacyRequest := r.Clone(r.Context())
	legacyURL := *r.URL
	legacyURL.Path = legacyPath
	legacyRequest.URL = &legacyURL
	legacyRequest.Body = io.NopCloser(bytes.NewReader(body))
	legacyRequest.ContentLength = int64(len(body))
	return h.doRequest(bytes.NewReader(body), legacyRequest)
}

func legacyWikiProxyPath(requestPath, apiBasePath string) (string, bool) {
	suffix, ok := strings.CutPrefix(requestPath, strings.TrimRight(apiBasePath, "/"))
	if !ok {
		return "", false
	}

	legacySuffix := ""
	switch {
	case suffix == "/wiki":
		legacySuffix = "/docs"
	case strings.HasPrefix(suffix, "/wiki/page"):
		legacySuffix = "/docs/doc" + strings.TrimPrefix(suffix, "/wiki/page")
	case strings.HasPrefix(suffix, "/wiki/"):
		legacySuffix = "/docs/" + strings.TrimPrefix(suffix, "/wiki/")
	case strings.HasPrefix(suffix, "/search/wiki"):
		legacySuffix = "/search/docs" + strings.TrimPrefix(suffix, "/search/wiki")
	case suffix == "/events/wiki-tree":
		legacySuffix = "/events/docs-tree"
	case strings.HasPrefix(suffix, "/events/wiki/"):
		legacySuffix = "/events/docs/" + strings.TrimPrefix(suffix, "/events/wiki/")
	default:
		return "", false
	}
	return strings.TrimRight(apiBasePath, "/") + legacySuffix, true
}

// doRequest performs the actual proxying of the request to the remote node.
func (h *remoteNodeProxy) doRequest(body io.Reader, r *http.Request) (*http.Response, error) {
	q := r.URL.Query()
	q.Del("remoteNode")

	remoteURL, err := buildRemoteNodeProxyURL(h.remoteNode.APIBaseURL, r.URL.Path, h.apiBasePath, q)
	if err != nil {
		return nil, &Error{
			Code:       api.ErrorCodeBadRequest,
			HTTPStatus: http.StatusBadRequest,
			Message:    err.Error(),
		}
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, remoteURL, body) //nolint:gosec // remoteURL is built from a validated remote-node base URL.
	if err != nil {
		return nil, fmt.Errorf("failed to create new request: %w", err)
	}
	req.ContentLength = r.ContentLength

	// Apply authentication from node configuration
	h.remoteNode.ApplyAuth(req)

	// Copy headers from the original request (skip Authorization since we set it above)
	for k, v := range r.Header {
		if k == "Authorization" {
			continue
		}
		for _, vv := range v {
			req.Header.Add(k, vv)
		}
	}

	// Set the Accept-Encoding header to handle gzip and deflate responses
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	// Add application/json content type
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	// Create a custom transport that skips certificate verification
	transport := &http.Transport{
		DisableCompression: true,
		TLSClientConfig: &tls.Config{
			// Allow insecure TLS connections if the remote node is configured to skip verification
			// This may be necessary for some enterprise setups
			InsecureSkipVerify: h.remoteNode.SkipTLSVerify, // nolint:gosec
			MinVersion:         tls.VersionTLS12,
		},
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second, // Add a reasonable timeout
	}

	resp, err := doRemoteNodeProxyRequest(client, req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request to remote node: %w", err)
	}

	if resp == nil {
		return nil, fmt.Errorf("received nil response from remote node")
	}

	return resp, nil
}

func buildRemoteNodeProxyURL(baseURL, requestPath, apiBasePath string, query url.Values) (string, error) {
	suffix, ok := strings.CutPrefix(requestPath, apiBasePath)
	if !ok {
		return "", fmt.Errorf("invalid URL path: %s", requestPath)
	}

	u, err := remotenode.ParseAPIBaseURL(baseURL)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(suffix, "/")
	u.RawPath = ""
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func doRemoteNodeProxyRequest(client *http.Client, req *http.Request) (*http.Response, error) {
	return client.Do(req) //nolint:gosec // request URL is constrained by buildRemoteNodeProxyURL.
}
