// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

func v2Path(path string) string {
	switch path {
	case "/session/status":
		path = "/session/active"
	case "/permission":
		path = "/permission/request"
	case "/question":
		path = "/form"
	}
	if before, ok := strings.CutSuffix(path, "/abort"); ok {
		path = before + "/interrupt"
	}
	return "/api" + path
}

func (e *harnessExecutor) submitManagedPromptV2(ctx context.Context, c *openCodeClient, cfg providerConfig, sessionID, messageID string, files []map[string]any) error {
	base := "/session/" + url.PathEscape(sessionID)
	if agent := stringFlag(cfg.flags, "agent"); agent != "" {
		if err := c.postNoContent(ctx, base+"/agent", map[string]any{"agent": agent}); err != nil {
			return err
		}
	}
	model := stringFlag(cfg.flags, "model")
	variant := stringFlag(cfg.flags, "variant")
	if model != "" {
		provider, id, ok := strings.Cut(model, "/")
		if !ok || provider == "" || id == "" {
			return errors.New("managed OpenCode model must use provider/model format")
		}
		id, inlineVariant, _ := strings.Cut(id, "#")
		if variant == "" {
			variant = inlineVariant
		}
		ref := map[string]any{"providerID": provider, "id": id}
		if variant != "" {
			ref["variant"] = variant
		}
		if err := c.postNoContent(ctx, base+"/model", map[string]any{"model": ref}); err != nil {
			return err
		}
	} else if variant != "" {
		return errors.New("OpenCode v2 requires model when variant is specified")
	}
	text := e.effectivePrompt()
	if strings.TrimSpace(e.script) != "" {
		text += "\nSupplementary input:\n" + e.script
	}
	body := map[string]any{"id": messageID, "text": text}
	if len(files) > 0 {
		attachments := make([]map[string]any, 0, len(files))
		for _, file := range files {
			attachments = append(attachments, map[string]any{"uri": file["url"], "name": file["filename"]})
		}
		body["files"] = attachments
	}
	if command := stringFlag(cfg.flags, "command"); command != "" {
		delete(body, "id")
		body["name"] = command
		before, err := c.userMessageIDsV2(ctx, sessionID)
		if err != nil {
			return err
		}
		admitted := false
		defer func() {
			if !admitted {
				// The server may accept a command before the caller observes a failure.
				abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
				defer cancel()
				_ = c.abort(abortCtx, sessionID)
			}
		}()
		if err := c.postNoContent(ctx, base+"/command", body); err != nil {
			return err
		}
		// V2 commands return no content. Find the user input admitted by the
		// callback in either the durable inbox or the projected history.
		waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			ids, err := c.userMessageIDsV2(waitCtx, sessionID)
			if err != nil {
				return err
			}
			for _, id := range ids {
				found := slices.Contains(before, id)
				if !found {
					e.mu.Lock()
					e.agentSession.PromptMessageID = id
					e.mu.Unlock()
					admitted = true
					return nil
				}
			}
			select {
			case <-waitCtx.Done():
				return fmt.Errorf("OpenCode command did not submit a prompt: %w", waitCtx.Err())
			case <-ticker.C:
			}
		}
	}
	return c.postNoContent(ctx, base+"/prompt", body)
}

type openCodeV2Message struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Text    string `json:"text"`
	Finish  string `json:"finish"`
	Outcome string `json:"outcome"`
	Time    struct {
		Created   int64 `json:"created"`
		Completed int64 `json:"completed"`
	} `json:"time"`
	Model struct {
		ID         string `json:"id"`
		ProviderID string `json:"providerID"`
	} `json:"model"`
	Content []map[string]any `json:"content"`
	Tokens  map[string]any   `json:"tokens"`
	Cost    *float64         `json:"cost"`
	Error   *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *openCodeClient) messagesV2(ctx context.Context, sessionID string) ([]openCodeMessage, error) {
	var messages []openCodeV2Message
	query := "order=asc"
	seen := map[string]bool{}
	for {
		resp, err := c.request(ctx, http.MethodGet, "/session/"+url.PathEscape(sessionID)+"/message?"+query, nil)
		if err != nil {
			return nil, err
		}
		var page struct {
			Data   []openCodeV2Message `json:"data"`
			Cursor struct {
				Next *string `json:"next"`
			} `json:"cursor"`
		}
		err = func() error {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusNotFound {
				return errManagedSessionUnavailable
			}
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("OpenCode message history returned %s", resp.Status)
			}
			return json.NewDecoder(resp.Body).Decode(&page)
		}()
		if err != nil {
			return nil, err
		}
		messages = append(messages, page.Data...)
		if page.Cursor.Next == nil || *page.Cursor.Next == "" {
			break
		}
		cursor := *page.Cursor.Next
		if seen[cursor] {
			return nil, errors.New("OpenCode repeated a message cursor")
		}
		seen[cursor] = true
		query = "cursor=" + url.QueryEscape(cursor)
	}
	return normalizeV2Messages(messages), nil
}

func normalizeV2Messages(messages []openCodeV2Message) []openCodeMessage {
	result := make([]openCodeMessage, 0, len(messages))
	parent := ""
	for _, message := range messages {
		if message.Type == "user" {
			parent = message.ID
		}
		if message.Type != "user" && message.Type != "assistant" && message.Type != "idle" {
			continue
		}
		if message.Type == "idle" && message.Outcome == "succeeded" {
			continue
		}
		role := message.Type
		if role == "idle" {
			role = "assistant"
		}
		info := map[string]any{"id": message.ID, "role": role, "parentID": parent, "providerID": message.Model.ProviderID, "modelID": message.Model.ID, "finish": message.Finish, "time": map[string]any{"completed": message.Time.Completed}}
		if message.Error != nil {
			info["error"] = map[string]any{"name": message.Error.Type, "data": map[string]any{"message": message.Error.Message}}
		} else if message.Type == "idle" {
			info["error"] = map[string]any{"name": message.Outcome, "data": map[string]any{"message": "OpenCode session " + message.Outcome}}
		}
		raw, _ := json.Marshal(info)
		normalized := openCodeMessage{Info: raw}
		add := func(part any) { data, _ := json.Marshal(part); normalized.Parts = append(normalized.Parts, data) }
		if message.Type == "user" {
			add(map[string]any{"type": "text", "text": message.Text})
		}
		for _, part := range message.Content {
			if part["type"] == "tool" {
				part["tool"] = part["name"]
				part["callID"] = part["id"]
			}
			add(part)
		}
		if message.Tokens != nil {
			message.Tokens["total"] = number(message.Tokens["input"]) + number(message.Tokens["output"])
			part := map[string]any{"type": "step-finish", "tokens": message.Tokens}
			if message.Cost != nil {
				part["cost"] = *message.Cost
			}
			add(part)
		}
		result = append(result, normalized)
	}
	return result
}

func (c *openCodeClient) permissionsV2(ctx context.Context) ([]openCodePermissionRequest, error) {
	var data []struct {
		ID        string   `json:"id"`
		SessionID string   `json:"sessionID"`
		Action    string   `json:"action"`
		Resources []string `json:"resources"`
		Save      []string `json:"save"`
	}
	if err := c.json(ctx, http.MethodGet, "/permission", nil, &data); err != nil {
		return nil, err
	}
	result := make([]openCodePermissionRequest, 0, len(data))
	for _, item := range data {
		result = append(result, openCodePermissionRequest{ID: item.ID, SessionID: item.SessionID, Permission: item.Action, Patterns: item.Resources, Always: item.Save})
	}
	return result, nil
}

type openCodeV2Form struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
	Title     string `json:"title"`
	Fields    []struct {
		Key         string `json:"key"`
		Type        string `json:"type"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Custom      bool   `json:"custom"`
		Options     []struct {
			Value       string `json:"value"`
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	} `json:"fields"`
}

func (c *openCodeClient) questionsV2(ctx context.Context) ([]openCodeQuestionRequest, error) {
	var forms []openCodeV2Form
	if err := c.json(ctx, http.MethodGet, "/question", nil, &forms); err != nil {
		return nil, err
	}
	result := make([]openCodeQuestionRequest, 0, len(forms))
	for _, form := range forms {
		// Decode through the existing question shape to keep the runtime/UI contract stable.
		questions := make([]map[string]any, 0, len(form.Fields))
		for _, field := range form.Fields {
			options := make([]map[string]string, 0, len(field.Options))
			for _, option := range field.Options {
				options = append(options, map[string]string{"label": option.Label, "description": option.Description})
			}
			questions = append(questions, map[string]any{"header": field.Title, "question": field.Description, "options": options, "multiple": field.Type == "multiselect", "custom": field.Custom || len(options) == 0})
		}
		data, _ := json.Marshal(map[string]any{"id": form.ID, "sessionID": form.SessionID, "questions": questions})
		var question openCodeQuestionRequest
		if err := json.Unmarshal(data, &question); err != nil {
			return nil, err
		}
		result = append(result, question)
	}
	return result, nil
}

func (c *openCodeClient) replyQuestionV2(ctx context.Context, requestID string, answers [][]string) error {
	path := "/session/" + url.PathEscape(c.sessionID) + "/form/" + url.PathEscape(requestID)
	var form openCodeV2Form
	if err := c.json(ctx, http.MethodGet, path, nil, &form); err != nil {
		return err
	}
	if len(answers) != len(form.Fields) {
		return errors.New("OpenCode form answer count does not match its fields")
	}
	answer := map[string]any{}
	for i, field := range form.Fields {
		values := append([]string(nil), answers[i]...)
		for j, value := range values {
			for _, option := range field.Options {
				if value == option.Label {
					values[j] = option.Value
					break
				}
			}
		}
		if field.Type == "multiselect" {
			answer[field.Key] = values
			continue
		}
		if len(values) != 1 {
			return fmt.Errorf("OpenCode form field %q requires one answer", field.Key)
		}
		var value any = values[0]
		var err error
		switch field.Type {
		case "number":
			value, err = strconv.ParseFloat(values[0], 64)
		case "integer":
			value, err = strconv.ParseInt(values[0], 10, 64)
		case "boolean":
			value, err = strconv.ParseBool(values[0])
		case "string":
		default:
			return fmt.Errorf("unsupported OpenCode form field type %q", field.Type)
		}
		if err != nil {
			return fmt.Errorf("invalid OpenCode form field %q: %w", field.Key, err)
		}
		answer[field.Key] = value
	}
	return c.postNoContent(ctx, path+"/reply", map[string]any{"answer": answer})
}

func (c *openCodeClient) userMessageIDsV2(ctx context.Context, sessionID string) ([]string, error) {
	// Read history after inbox so an input moving between them is not missed.
	var inbox []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err := c.json(ctx, http.MethodGet, "/session/"+url.PathEscape(sessionID)+"/inbox", nil, &inbox); err != nil {
		return nil, err
	}
	messages, err := c.messagesV2(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, item := range inbox {
		if item.Type == "user" {
			ids = append(ids, item.ID)
		}
	}
	for _, message := range messages {
		var info openCodeMessageInfo
		if err := json.Unmarshal(message.Info, &info); err != nil {
			return nil, err
		}
		if info.Role == "user" {
			ids = append(ids, info.ID)
		}
	}
	return ids, nil
}
