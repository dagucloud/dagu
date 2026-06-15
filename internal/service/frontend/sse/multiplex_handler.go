// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sse

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/remotenode"
)

// TopicMutationRequest updates the topic set for a multiplexed SSE session.
type TopicMutationRequest struct {
	SessionID string   `json:"sessionID"`
	Add       []string `json:"add"`
	Remove    []string `json:"remove"`
}

// MultiplexHandler serves the multiplexed SSE stream and topic mutation API.
type MultiplexHandler struct {
	mux          *Multiplexer
	nodeResolver *remotenode.Resolver
}

// NewMultiplexHandler creates a handler for multiplexed SSE endpoints.
func NewMultiplexHandler(mux *Multiplexer, nodeResolver *remotenode.Resolver) *MultiplexHandler {
	return &MultiplexHandler{
		mux:          mux,
		nodeResolver: nodeResolver,
	}
}

// HandleStream opens the multiplexed SSE stream.
func (h *MultiplexHandler) HandleStream(w http.ResponseWriter, r *http.Request) {
	SetSSEHeaders(w)
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	remoteNode := r.URL.Query().Get("remoteNode")
	if remoteNode != "" && remoteNode != "local" {
		h.proxyStreamToRemoteNode(w, r, remoteNode)
		return
	}

	lastEventID, err := parseLastEventID(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_last_event_id", err.Error())
		return
	}

	result, err := h.mux.createSession(r.Context(), w, parseInitialTopics(r.URL.Query()), lastEventID)
	if err != nil {
		http.Error(w, "unable to open SSE stream", http.StatusServiceUnavailable)
		return
	}
	defer h.mux.removeSession(result.session)

	if err := result.session.writeControl(result.control); err != nil {
		return
	}
	result.session.bootstrapTopics(r.Context(), lastEventID, result.topics)
	_ = result.session.Serve(r.Context())
}

// HandleTopicMutation adds and removes topics for an existing stream.
func (h *MultiplexHandler) HandleTopicMutation(w http.ResponseWriter, r *http.Request) {
	remoteNode := r.URL.Query().Get("remoteNode")
	if remoteNode != "" && remoteNode != "local" {
		h.proxyTopicMutationToRemoteNode(w, r, remoteNode)
		return
	}

	var req TopicMutationRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "invalid JSON request body")
		return
	}
	if req.SessionID == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "sessionID is required")
		return
	}

	result, err := h.mux.mutateSession(r.Context(), req.SessionID, req.Add, req.Remove)
	if err != nil {
		switch {
		case errors.Is(err, ErrUnknownSession):
			writeJSONError(w, http.StatusNotFound, "unknown_session", "unknown_session")
		case errors.Is(err, ErrTooManyTopics):
			writeJSONError(w, http.StatusBadRequest, "too_many_topics", err.Error())
		case errors.Is(err, ErrConflictingTopicMutation):
			writeJSONError(w, http.StatusBadRequest, "invalid_request", err.Error())
		default:
			writeJSONError(w, http.StatusBadRequest, "invalid_topic", err.Error())
		}
		return
	}

	if len(result.added) > 0 {
		if session, sessionErr := h.mux.getSession(req.SessionID); sessionErr == nil {
			session.bootstrapTopics(r.Context(), 0, result.added)
		}
	}

	writeJSON(w, result.statusCode, result.response)
}

func parseLastEventID(r *http.Request) (uint64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("lastEventId"))
	if raw == "" {
		raw = strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	}
	if raw == "" {
		return 0, nil
	}

	var lastEventID uint64
	if _, err := fmt.Sscanf(raw, "%d", &lastEventID); err != nil {
		return 0, err
	}
	return lastEventID, nil
}

func (h *MultiplexHandler) proxyStreamToRemoteNode(w http.ResponseWriter, r *http.Request, nodeName string) {
	node, ok := h.resolveNode(w, r, nodeName)
	if !ok {
		return
	}

	req, err := newRemoteEventRequest(r.Context(), http.MethodGet, node, "/events/stream", r.URL.Query(), nil)
	if err != nil {
		http.Error(w, "failed to create proxy request", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Accept", "text/event-stream")
	if lastEventID := strings.TrimSpace(r.Header.Get("Last-Event-ID")); lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	node.ApplyAuth(req)

	resp, err := doRemoteEventRequest(newProxyHTTPClient(node.SkipTLSVerify), req)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		http.Error(w, "failed to connect to remote node", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		copyJSONResponse(w, resp)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	streamResponse(w, flusher, resp.Body)
}

func (h *MultiplexHandler) proxyTopicMutationToRemoteNode(w http.ResponseWriter, r *http.Request, nodeName string) {
	node, ok := h.resolveNode(w, r, nodeName)
	if !ok {
		return
	}

	req, err := newRemoteEventRequest(r.Context(), http.MethodPost, node, "/events/stream/topics", r.URL.Query(), r.Body)
	if err != nil {
		http.Error(w, "failed to create proxy request", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	node.ApplyAuth(req)

	resp, err := doRemoteEventRequest(newProxyHTTPClient(node.SkipTLSVerify), req)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		http.Error(w, "failed to connect to remote node", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	copyJSONResponse(w, resp)
}

func (h *MultiplexHandler) resolveNode(w http.ResponseWriter, r *http.Request, nodeName string) (*remotenode.RemoteNode, bool) {
	if h.nodeResolver == nil {
		http.Error(w, "remote node resolution not available", http.StatusServiceUnavailable)
		return nil, false
	}
	node, err := h.nodeResolver.GetByName(r.Context(), nodeName)
	if err != nil {
		http.Error(w, fmt.Sprintf("unknown remote node: %s", nodeName), http.StatusBadRequest)
		return nil, false
	}
	return node, true
}

func buildRemoteStreamURL(baseURL string, query url.Values) string {
	return buildRemoteEventURL(baseURL, "/events/stream", query)
}

func buildRemoteTopicMutationURL(baseURL string, query url.Values) string {
	return buildRemoteEventURL(baseURL, "/events/stream/topics", query)
}

func buildRemoteEventURL(baseURL, route string, query url.Values) string {
	u, err := buildValidatedRemoteEventURL(baseURL, route, query)
	if err != nil {
		return strings.TrimSuffix(baseURL, "/") + route
	}
	return u.String()
}

func newRemoteEventRequest(ctx context.Context, method string, node *remotenode.RemoteNode, route string, query url.Values, body io.Reader) (*http.Request, error) {
	u, err := buildValidatedRemoteEventURL(node.APIBaseURL, route, query)
	if err != nil {
		return nil, err
	}
	return http.NewRequestWithContext(ctx, method, u.String(), body) //nolint:gosec // target URL is built from a validated remote-node base URL.
}

func buildValidatedRemoteEventURL(baseURL, route string, query url.Values) (*url.URL, error) {
	u, err := remotenode.ParseAPIBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(route, "/")
	u.RawPath = ""
	u.RawQuery = cleanRemoteEventQuery(query).Encode()
	return u, nil
}

func cleanRemoteEventQuery(query url.Values) url.Values {
	cloned := make(url.Values, len(query))
	for key, values := range query {
		copied := make([]string, len(values))
		copy(copied, values)
		cloned[key] = copied
	}
	cloned.Del("remoteNode")
	cloned.Del("token")
	return cloned
}

func doRemoteEventRequest(client *http.Client, req *http.Request) (*http.Response, error) {
	return client.Do(req) //nolint:gosec // request URL is constrained by newRemoteEventRequest.
}

func newProxyHTTPClient(skipTLSVerify bool) *http.Client {
	return &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: skipTLSVerify, //nolint:gosec
				MinVersion:         tls.VersionTLS12,
			},
			MaxIdleConns:       10,
			IdleConnTimeout:    90 * time.Second,
			DisableCompression: true,
		},
	}
}

func copyJSONResponse(w http.ResponseWriter, resp *http.Response) {
	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func writeJSON(w http.ResponseWriter, statusCode int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONError(w http.ResponseWriter, statusCode int, code, message string) {
	writeJSON(w, statusCode, map[string]string{
		"error":   code,
		"message": message,
	})
}
