// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/opencodehost"
)

// managedIdlePollInterval paces status checks while an interrupted session settles.
const managedIdlePollInterval = 100 * time.Millisecond

type openCodeClient struct {
	host      opencodehost.Config
	directory string
	sessionID string
	http      *http.Client
}

// openCodeTransport is shared by OpenCode clients and kept apart from
// http.DefaultTransport: closing the default transport's idle connections,
// as httptest.Server.Close does, can fail a request that is in flight on it.
var openCodeTransport = newOpenCodeTransport()

func newOpenCodeTransport() http.RoundTripper {
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		return base.Clone()
	}
	return http.DefaultTransport
}

func newOpenCodeHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Transport: openCodeTransport, Timeout: timeout}
}

type openCodeSession struct {
	ID        string `json:"id"`
	Directory string `json:"directory"`
	Location  struct {
		Directory string `json:"directory"`
	} `json:"location"`
}

type openCodeEvent struct {
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
	Data       json.RawMessage `json:"data"`
}

type openCodeMessage struct {
	Info  json.RawMessage   `json:"info"`
	Parts []json.RawMessage `json:"parts"`
}

type openCodeMessageInfo struct {
	ID         string          `json:"id"`
	Role       string          `json:"role"`
	ParentID   string          `json:"parentID"`
	ProviderID string          `json:"providerID"`
	ModelID    string          `json:"modelID"`
	Finish     string          `json:"finish"`
	Error      json.RawMessage `json:"error"`
	Time       struct {
		Completed int64 `json:"completed"`
	} `json:"time"`
}

type openCodePermissionRequest struct {
	ID         string   `json:"id"`
	SessionID  string   `json:"sessionID"`
	Permission string   `json:"permission"`
	Patterns   []string `json:"patterns"`
	Always     []string `json:"always"`
}

type openCodeQuestionRequest struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
	Questions []struct {
		Header   string `json:"header"`
		Question string `json:"question"`
		Options  []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
		Multiple bool `json:"multiple"`
		Custom   bool `json:"custom"`
	} `json:"questions"`
}

func (c *openCodeClient) endpoint(path string) string {
	query := url.Values{}
	if c.host.IsV2() {
		query.Set("location[directory]", c.directory)
		path = v2Path(path)
	} else {
		query.Set("directory", c.directory)
	}
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return c.host.URL + path + separator + query.Encode()
}

func (c *openCodeClient) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	return c.requestWithClient(ctx, c.http, method, path, body)
}

func (c *openCodeClient) requestWithClient(ctx context.Context, client *http.Client, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), reader)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.host.Username, c.host.Password)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", errManagedHostUnavailable, err)
	}
	return resp, nil
}

func (c *openCodeClient) json(ctx context.Context, method, path string, body, target any) error {
	resp, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound && strings.HasPrefix(path, "/session/") &&
		(!c.host.IsV2() || (!strings.Contains(path, "/permission/") && !strings.Contains(path, "/form/"))) {
		return errManagedSessionUnavailable
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("OpenCode API %s %s returned %s", method, path, resp.Status)
	}
	if target == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if c.host.IsV2() && path != "/config" {
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
			return err
		}
		if err := json.Unmarshal(envelope.Data, target); err != nil {
			return err
		}
		if session, ok := target.(*openCodeSession); ok {
			session.Directory = session.Location.Directory
		}
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func (c *openCodeClient) postNoContent(ctx context.Context, path string, body any) error {
	return c.json(ctx, http.MethodPost, path, body, nil)
}

func (c *openCodeClient) createSession(ctx context.Context, title, agent string) (openCodeSession, error) {
	body := map[string]any{}
	if c.host.IsV2() {
		body["location"] = map[string]string{"directory": c.directory}
	}
	if title != "" {
		body["title"] = title
	}
	if agent != "" {
		body["agent"] = agent
	}
	var session openCodeSession
	err := c.json(ctx, http.MethodPost, "/session", body, &session)
	return session, err
}

func (c *openCodeClient) getSession(ctx context.Context, sessionID string) (openCodeSession, error) {
	var session openCodeSession
	err := c.json(ctx, http.MethodGet, "/session/"+url.PathEscape(sessionID), nil, &session)
	return session, err
}

func (c *openCodeClient) forkSession(ctx context.Context, sessionID string) (openCodeSession, error) {
	var session openCodeSession
	err := c.json(ctx, http.MethodPost, "/session/"+url.PathEscape(sessionID)+"/fork", map[string]any{}, &session)
	return session, err
}

func (c *openCodeClient) deleteSession(ctx context.Context, sessionID string) error {
	return c.json(ctx, http.MethodDelete, "/session/"+url.PathEscape(sessionID), nil, nil)
}

func (c *openCodeClient) validateManagedConfig(ctx context.Context) error {
	var settings any
	if err := c.json(ctx, http.MethodGet, "/config", nil, &settings); err != nil {
		return fmt.Errorf("validate managed OpenCode configuration: %w", err)
	}
	if !opencodehost.SharingDisabled(settings) {
		return errors.New("managed OpenCode requires sharing to be disabled")
	}
	return nil
}

func (c *openCodeClient) commandAsync(ctx context.Context, sessionID, messageID, command, arguments string, cfg providerConfig, files []map[string]any) <-chan error {
	body := map[string]any{"messageID": messageID, "command": command, "arguments": arguments, "parts": files}
	if agent := stringFlag(cfg.flags, "agent"); agent != "" {
		body["agent"] = agent
	}
	if model := stringFlag(cfg.flags, "model"); model != "" {
		body["model"] = model
	}
	if variant := stringFlag(cfg.flags, "variant"); variant != "" {
		body["variant"] = variant
	}
	errs := make(chan error, 1)
	go func() {
		defer close(errs)
		commandClient := *c
		commandClient.http = &http.Client{Transport: c.http.Transport}
		errs <- commandClient.json(ctx, http.MethodPost, "/session/"+url.PathEscape(sessionID)+"/command", body, nil)
	}()
	return errs
}

func (c *openCodeClient) messages(ctx context.Context, sessionID string) ([]openCodeMessage, error) {
	if c.host.IsV2() {
		return c.messagesV2(ctx, sessionID)
	}
	var messages []openCodeMessage
	err := c.json(ctx, http.MethodGet, "/session/"+url.PathEscape(sessionID)+"/message", nil, &messages)
	return messages, err
}

func (c *openCodeClient) sessionStatus(ctx context.Context, sessionID string) (string, error) {
	var statuses map[string]struct {
		Type string `json:"type"`
	}
	if err := c.json(ctx, http.MethodGet, "/session/status", nil, &statuses); err != nil {
		return "", err
	}
	status, ok := statuses[sessionID]
	if !ok {
		return "idle", nil
	}
	if c.host.IsV2() && status.Type == "running" {
		return "busy", nil
	}
	return status.Type, nil
}

func (c *openCodeClient) permissions(ctx context.Context) ([]openCodePermissionRequest, error) {
	if c.host.IsV2() {
		return c.permissionsV2(ctx)
	}
	var requests []openCodePermissionRequest
	err := c.json(ctx, http.MethodGet, "/permission", nil, &requests)
	return requests, err
}

func (c *openCodeClient) questions(ctx context.Context) ([]openCodeQuestionRequest, error) {
	if c.host.IsV2() {
		return c.questionsV2(ctx)
	}
	var requests []openCodeQuestionRequest
	err := c.json(ctx, http.MethodGet, "/question", nil, &requests)
	return requests, err
}

func (c *openCodeClient) replyPermission(ctx context.Context, requestID, reply string) error {
	if c.host.IsV2() {
		return c.json(ctx, http.MethodPost, "/session/"+url.PathEscape(c.sessionID)+"/permission/"+url.PathEscape(requestID)+"/reply", map[string]any{"decision": reply}, nil)
	}
	return c.json(ctx, http.MethodPost, "/permission/"+url.PathEscape(requestID)+"/reply", map[string]any{"reply": reply}, nil)
}

func (c *openCodeClient) replyQuestion(ctx context.Context, requestID string, answers [][]string) error {
	if c.host.IsV2() {
		return c.replyQuestionV2(ctx, requestID, answers)
	}
	return c.json(ctx, http.MethodPost, "/question/"+url.PathEscape(requestID)+"/reply", map[string]any{"answers": answers}, nil)
}

func (c *openCodeClient) rejectQuestion(ctx context.Context, requestID string) error {
	if c.host.IsV2() {
		return c.json(ctx, http.MethodDelete, "/session/"+url.PathEscape(c.sessionID)+"/form/"+url.PathEscape(requestID), nil, nil)
	}
	return c.json(ctx, http.MethodPost, "/question/"+url.PathEscape(requestID)+"/reject", nil, nil)
}

func (c *openCodeClient) abort(ctx context.Context, sessionID string) error {
	return c.json(ctx, http.MethodPost, "/session/"+url.PathEscape(sessionID)+"/abort", nil, nil)
}

// interruptAndAwaitIdle interrupts the session's active execution. It returns
// nil only once the host reports the session idle or no longer has it.
func (c *openCodeClient) interruptAndAwaitIdle(ctx context.Context, sessionID string) error {
	abortErr := c.abort(ctx, sessionID)
	if errors.Is(abortErr, errManagedSessionUnavailable) {
		return nil
	}
	if err := c.awaitIdle(ctx, sessionID); err != nil {
		return errors.Join(abortErr, err)
	}
	// A failed interrupt request is moot once the session is idle.
	return nil
}

// awaitIdle waits until the host reports no active execution for the session.
// Settlement is scoped to this host's process.
func (c *openCodeClient) awaitIdle(ctx context.Context, sessionID string) error {
	ticker := time.NewTicker(managedIdlePollInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		status, err := c.sessionStatus(ctx, sessionID)
		switch {
		case err == nil && status == "idle":
			return nil
		case errors.Is(err, errManagedHostUnavailable):
			return err
		case err == nil:
			lastErr = fmt.Errorf("OpenCode session %s is still %s", sessionID, status)
		case ctx.Err() == nil:
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), lastErr)
		case <-ticker.C:
		}
	}
}

func abortManagedOpenCode(ctx context.Context, host opencodehost.Config, sessionID, directory string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	client := &openCodeClient{host: host, directory: directory, http: newOpenCodeHTTPClient(3 * time.Second)}
	return client.abort(ctx, sessionID)
}

func (c *openCodeClient) subscribe(ctx context.Context) (<-chan openCodeEvent, <-chan error, func(), error) {
	streamCtx, cancel := context.WithCancel(ctx)
	streamClient := &http.Client{Transport: c.http.Transport}
	resp, err := c.requestWithClient(streamCtx, streamClient, http.MethodGet, "/event", nil)
	if err != nil {
		cancel()
		return nil, nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		cancel()
		return nil, nil, nil, fmt.Errorf("OpenCode event stream returned %s", resp.Status)
	}
	events := make(chan openCodeEvent)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			data, ok := strings.CutPrefix(line, "data:")
			if !ok {
				continue
			}
			var event openCodeEvent
			if json.Unmarshal([]byte(strings.TrimSpace(data)), &event) != nil {
				continue
			}
			select {
			case events <- event:
			case <-streamCtx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil {
			errs <- err
		}
	}()
	return events, errs, cancel, nil
}
