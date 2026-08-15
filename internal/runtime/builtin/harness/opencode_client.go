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

type openCodeClient struct {
	host      opencodehost.Config
	directory string
	http      *http.Client
}

type openCodeSession struct {
	ID        string `json:"id"`
	Directory string `json:"directory"`
}

type openCodeEvent struct {
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
}

type openCodeMessage struct {
	Info  json.RawMessage   `json:"info"`
	Parts []json.RawMessage `json:"parts"`
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
	query.Set("directory", c.directory)
	return c.host.URL + path + "?" + query.Encode()
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
	if resp.StatusCode == http.StatusNotFound {
		return errManagedSessionUnavailable
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("OpenCode API %s %s returned %s", method, path, resp.Status)
	}
	if target == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func (c *openCodeClient) postNoContent(ctx context.Context, path string, body any) error {
	return c.json(ctx, http.MethodPost, path, body, nil)
}

func (c *openCodeClient) createSession(ctx context.Context, title, agent string) (openCodeSession, error) {
	body := map[string]any{}
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
	var settings map[string]any
	if err := c.json(ctx, http.MethodGet, "/config", nil, &settings); err != nil {
		return fmt.Errorf("validate managed OpenCode configuration: %w", err)
	}
	if settings["share"] != "disabled" {
		return errors.New("managed OpenCode requires sharing to be disabled")
	}
	return nil
}

func (c *openCodeClient) commandAsync(ctx context.Context, sessionID, command, arguments string, cfg providerConfig, files []map[string]any) <-chan error {
	body := map[string]any{"command": command, "arguments": arguments, "parts": files}
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
	return status.Type, nil
}

func (c *openCodeClient) permissions(ctx context.Context) ([]openCodePermissionRequest, error) {
	var requests []openCodePermissionRequest
	err := c.json(ctx, http.MethodGet, "/permission", nil, &requests)
	return requests, err
}

func (c *openCodeClient) questions(ctx context.Context) ([]openCodeQuestionRequest, error) {
	var requests []openCodeQuestionRequest
	err := c.json(ctx, http.MethodGet, "/question", nil, &requests)
	return requests, err
}

func (c *openCodeClient) replyPermission(ctx context.Context, requestID, reply string) error {
	return c.json(ctx, http.MethodPost, "/permission/"+url.PathEscape(requestID)+"/reply", map[string]any{"reply": reply}, nil)
}

func (c *openCodeClient) replyQuestion(ctx context.Context, requestID string, answers [][]string) error {
	return c.json(ctx, http.MethodPost, "/question/"+url.PathEscape(requestID)+"/reply", map[string]any{"answers": answers}, nil)
}

func (c *openCodeClient) rejectQuestion(ctx context.Context, requestID string) error {
	return c.json(ctx, http.MethodPost, "/question/"+url.PathEscape(requestID)+"/reject", nil, nil)
}

func (c *openCodeClient) abort(ctx context.Context, sessionID string) error {
	return c.json(ctx, http.MethodPost, "/session/"+url.PathEscape(sessionID)+"/abort", nil, nil)
}

func abortManagedOpenCode(ctx context.Context, host opencodehost.Config, sessionID, directory string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	client := &openCodeClient{host: host, directory: directory, http: &http.Client{Timeout: 3 * time.Second}}
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
