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
	"sync"
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

	mu      sync.Mutex
	parents map[string]string
}

const maxParentCache = 256

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
	ParentID  string `json:"parentID"`
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
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return c.apiError(resp, method, path)
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

func (c *openCodeClient) apiError(resp *http.Response, method, path string) error {
	if resp.StatusCode != http.StatusNotFound || !strings.HasPrefix(path, "/session/") {
		return fmt.Errorf("OpenCode API %s %s returned %s", method, path, resp.Status)
	}
	if !c.host.IsV2() {
		return errManagedSessionUnavailable
	}
	// V2 session routes can also return missing command, model, or form errors.
	var failure struct {
		Tag string `json:"_tag"`
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return fmt.Errorf("OpenCode API %s %s returned %s with an unreadable error response: %w", method, path, resp.Status, err)
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("OpenCode API %s %s returned %s with an oversized error response", method, path, resp.Status)
	}
	if err := json.Unmarshal(data, &failure); err != nil {
		return fmt.Errorf("OpenCode API %s %s returned %s with an invalid error response: %w", method, path, resp.Status, err)
	}
	switch failure.Tag {
	case "SessionNotFoundError":
		return errManagedSessionUnavailable
	case "":
		return fmt.Errorf("OpenCode API %s %s returned %s", method, path, resp.Status)
	}
	return fmt.Errorf("OpenCode API %s %s returned %s (%s): %w", method, path, resp.Status, failure.Tag, errManagedNotFound)
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
		commandClient := openCodeClient{host: c.host, directory: c.directory, sessionID: c.sessionID}
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

func (c *openCodeClient) activeSessions(ctx context.Context) (map[string]string, error) {
	var statuses map[string]struct {
		Type string `json:"type"`
	}
	if err := c.json(ctx, http.MethodGet, "/session/status", nil, &statuses); err != nil {
		return nil, err
	}
	active := make(map[string]string, len(statuses))
	for id, status := range statuses {
		active[id] = status.Type
		if c.host.IsV2() && status.Type == "running" {
			active[id] = "busy"
		}
	}
	return active, nil
}

func (c *openCodeClient) sessionStatus(ctx context.Context, sessionID string) (string, error) {
	active, err := c.activeSessions(ctx)
	if err != nil {
		return "", err
	}
	if status, ok := active[sessionID]; ok {
		return status, nil
	}
	return "idle", nil
}

// maxSessionDepth bounds the walk from a session up to its root.
const maxSessionDepth = 16

// openCodeFamily is a root session together with the sessions descended from it.
type openCodeFamily struct {
	// status is the root's own status.
	status string
	// descendants are the active sessions descended from the root.
	descendants []string
	// unknown are active sessions whose ancestry could not be established.
	unknown []string
	// hasChildren is set only when the whole family is idle.
	hasChildren bool
}

func (f openCodeFamily) idle() bool {
	return f.status == "idle" && len(f.descendants) == 0 && len(f.unknown) == 0
}

// family reads the status of a root session and its descendants. Sessions are
// related by parent linkage only, so unrelated sessions on a shared host are
// ignored. Parent chains are read only for active sessions other than the root.
func (c *openCodeClient) family(ctx context.Context, rootID string) (openCodeFamily, error) {
	active, err := c.activeSessions(ctx)
	if err != nil {
		return openCodeFamily{}, err
	}
	family := openCodeFamily{status: "idle"}
	if status, ok := active[rootID]; ok {
		family.status = status
	}
	if !c.host.IsV2() {
		// v1 has no background children.
		return family, nil
	}
	for id := range active {
		if id == rootID {
			continue
		}
		membership, err := c.descendsFrom(ctx, id, rootID)
		if err != nil {
			return openCodeFamily{}, err
		}
		switch membership {
		case memberYes:
			family.descendants = append(family.descendants, id)
		case memberUnknown:
			family.unknown = append(family.unknown, id)
		}
	}
	if family.idle() {
		family.hasChildren, err = c.hasChildren(ctx, rootID)
	}
	return family, err
}

type membership int

const (
	memberNo membership = iota
	memberYes
	memberUnknown
)

// descendsFrom walks the parent chain of a session. A chain that ends without
// the root, or hits a missing session, is unrelated. A read error, a cycle, or
// exhausting maxSessionDepth leaves membership unknown, to be retried on the
// next poll; only a lost host or a cancelled context is an error.
func (c *openCodeClient) descendsFrom(ctx context.Context, sessionID, rootID string) (membership, error) {
	seen := map[string]bool{}
	for range maxSessionDepth {
		if seen[sessionID] {
			return memberUnknown, nil
		}
		seen[sessionID] = true
		parent, err := c.parentOf(ctx, sessionID)
		switch {
		case errors.Is(err, errManagedHostUnavailable) || ctx.Err() != nil:
			return memberNo, errors.Join(err, ctx.Err())
		case errors.Is(err, errManagedSessionUnavailable):
			return memberNo, nil
		case err != nil:
			return memberUnknown, nil
		case parent == "":
			return memberNo, nil
		case parent == rootID:
			return memberYes, nil
		}
		sessionID = parent
	}
	return memberUnknown, nil
}

// parentOf returns a session's parent ID. Parent links never change, so they
// are cached; the cache is cleared when it grows past its cap.
func (c *openCodeClient) parentOf(ctx context.Context, sessionID string) (string, error) {
	c.mu.Lock()
	parent, ok := c.parents[sessionID]
	c.mu.Unlock()
	if ok {
		return parent, nil
	}
	session, err := c.getSession(ctx, sessionID)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.parents == nil || len(c.parents) >= maxParentCache {
		c.parents = map[string]string{}
	}
	c.parents[sessionID] = session.ParentID
	return session.ParentID, nil
}

// hasChildren reports whether the session spawned child sessions, whose reports
// can still start another turn of it. Only OpenCode v2 spawns background children.
func (c *openCodeClient) hasChildren(ctx context.Context, sessionID string) (bool, error) {
	if !c.host.IsV2() {
		return false, nil
	}
	var children []openCodeSession
	if err := c.json(ctx, http.MethodGet, "/session?limit=1&parentID="+url.QueryEscape(sessionID), nil, &children); err != nil {
		if isManagedUnavailable(err) {
			return false, err
		}
		// The check only delays settling, so a host that rejects it is treated as childless.
		return false, nil
	}
	return len(children) > 0, nil
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

// interruptAndAwaitIdle interrupts the session's active execution and that of
// its descendants. It returns nil only once the host reports the whole family
// idle or no longer has the session.
func (c *openCodeClient) interruptAndAwaitIdle(ctx context.Context, sessionID string) error {
	return c.interruptAndSettle(ctx, sessionID, true)
}

// interruptRootAndAwaitIdle interrupts only the session itself and waits for
// its descendants, which may belong to earlier turns of a user's session.
func (c *openCodeClient) interruptRootAndAwaitIdle(ctx context.Context, sessionID string) error {
	return c.interruptAndSettle(ctx, sessionID, false)
}

func (c *openCodeClient) interruptAndSettle(ctx context.Context, sessionID string, descendants bool) error {
	abortErr := c.abort(ctx, sessionID)
	if errors.Is(abortErr, errManagedSessionUnavailable) {
		return nil
	}
	if err := c.settle(ctx, sessionID, true, descendants); err != nil {
		return errors.Join(abortErr, err)
	}
	// A failed interrupt request is moot once the family is idle.
	return nil
}

// awaitIdle waits until the host reports no active execution for the session
// or its descendants. Settlement is scoped to this host's process.
func (c *openCodeClient) awaitIdle(ctx context.Context, sessionID string) error {
	return c.settle(ctx, sessionID, false, false)
}

// settle waits for the session's family to stay idle for one more poll when
// the session has children, because a child's report can start another turn of
// the session. With interrupt, an active root is interrupted again; with
// descendants, so are the active descendants. Sessions of unknown membership
// are never interrupted.
func (c *openCodeClient) settle(ctx context.Context, sessionID string, interrupt, descendants bool) error {
	ticker := time.NewTicker(managedIdlePollInterval)
	defer ticker.Stop()
	var lastErr error
	settling, rootIdle := false, false
	for {
		family, err := c.family(ctx, sessionID)
		switch {
		case err == nil && family.idle() && (!family.hasChildren || settling):
			return nil
		case errors.Is(err, errManagedHostUnavailable):
			return err
		case err == nil:
			settling = family.idle()
			if !settling {
				lastErr = fmt.Errorf("OpenCode session %s is still %s", sessionID, family.status)
				if family.status == "idle" {
					lastErr = fmt.Errorf("OpenCode session %s has active subagent sessions %v", sessionID, family.descendants)
					if len(family.descendants) == 0 {
						lastErr = fmt.Errorf("OpenCode session %s has active sessions of unresolved ancestry %v", sessionID, family.unknown)
					}
				}
			}
			if interrupt {
				if descendants {
					for _, id := range family.descendants {
						_ = c.abort(ctx, id)
					}
				}
				// A child's report can start a new turn of an interrupted root.
				if rootIdle && family.status != "idle" {
					_ = c.abort(ctx, sessionID)
				}
			}
			rootIdle = family.status == "idle"
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

// awaitPromptStart waits until the host shows that it took up the prompt: the
// prompt has a reply, or the session no longer exists. In a session Dagu owns,
// an active session also counts; in another session the active turn may not
// be Dagu's.
func (c *openCodeClient) awaitPromptStart(ctx context.Context, sessionID, promptMessageID string, owned bool) error {
	ticker := time.NewTicker(managedIdlePollInterval)
	defer ticker.Stop()
	for {
		status, err := c.sessionStatus(ctx, sessionID)
		if owned && err == nil && status != "idle" {
			return nil
		}
		if errors.Is(err, errManagedHostUnavailable) {
			return err
		}
		messages, err := c.messages(ctx, sessionID)
		if errors.Is(err, errManagedSessionUnavailable) {
			return nil
		}
		if reply, _ := openCodeReply(messages, promptMessageID); err == nil && reply != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the turn for prompt %s has not started: %w", promptMessageID, ctx.Err())
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
