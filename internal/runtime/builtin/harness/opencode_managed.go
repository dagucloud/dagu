// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/opencodehost"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

const maxManagedAgentEvents = 1000

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
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
}

type openCodeMessage struct {
	Info  json.RawMessage   `json:"info"`
	Parts []json.RawMessage `json:"parts"`
}

func (e *harnessExecutor) runManagedOpenCode(
	ctx context.Context,
	cfg providerConfig,
	host opencodehost.Config,
) (*os.File, error) {
	client := &openCodeClient{host: host, directory: e.workDir, http: http.DefaultClient}
	e.mu.Lock()
	e.managedHost = host
	e.hasDeterminedStatus = false
	if e.agentSession == nil {
		e.agentSession = &ir.AgentSession{Provider: "opencode", Generation: 1}
	}
	if e.agentSession.Generation == 0 {
		e.agentSession.Generation = 1
	}
	if e.agentSession.RestartPending {
		e.agentSession.SessionID = ""
		e.agentSession.PromptSent = false
		e.agentSession.RestartPending = false
	}
	e.agentSession.Agent = stringFlag(cfg.flags, "agent")
	e.agentSession.Model = stringFlag(cfg.flags, "model")
	e.agentSession.Variant = stringFlag(cfg.flags, "variant")
	e.agentSession.OwnerWorkerID = runtimeWorkerID(ctx)
	e.agentSession.State = ir.AgentSessionStarting
	e.appendAgentEventLocked("lifecycle", "starting", "OpenCode session starting", nil)
	e.mu.Unlock()
	e.notifyProgress()

	sessionID, err := e.ensureManagedSession(ctx, client, cfg)
	if err != nil {
		if errors.Is(err, errManagedSessionUnavailable) {
			return e.finishManagedUnavailable("The OpenCode session is no longer available")
		}
		e.failManagedSession(err)
		return nil, err
	}

	events, eventErrs, closeEvents, err := client.subscribe(ctx)
	if err != nil {
		e.failManagedSession(err)
		return nil, err
	}
	defer closeEvents()

	resumed, err := e.applyManagedInteractionResponses(ctx, client, sessionID)
	if err != nil {
		if errors.Is(err, errManagedSessionUnavailable) {
			return e.finishManagedUnavailable("The OpenCode session is no longer available")
		}
		e.failManagedSession(err)
		return nil, err
	}

	e.mu.Lock()
	promptSent := e.agentSession.PromptSent
	e.mu.Unlock()
	var commandErrs <-chan error
	if !resumed && !promptSent {
		commandErrs, err = e.submitManagedPrompt(ctx, client, cfg, sessionID)
		if err != nil {
			e.failManagedSession(err)
			return nil, err
		}
		e.mu.Lock()
		e.agentSession.PromptSent = true
		e.mu.Unlock()
	}

	e.setManagedState(ir.AgentSessionRunning, "running", "OpenCode is working")
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	startedAt := time.Now()
	seenBusy := false
	statusFailures := 0

	for {
		select {
		case <-ctx.Done():
			abortCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = client.abort(abortCtx, sessionID)
			cancel()
			e.setManagedState(ir.AgentSessionAborted, "aborted", "OpenCode session aborted")
			return nil, ctx.Err()
		case err, ok := <-eventErrs:
			if !ok {
				eventErrs = nil
				continue
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				// The status and pending-request polls below reconcile a dropped SSE stream.
				events = nil
			}
		case err, ok := <-commandErrs:
			commandErrs = nil
			if ok && err != nil {
				e.failManagedSession(err)
				return nil, err
			}
			return e.finishManagedSuccess(ctx, client, sessionID)
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			matched, busy, eventErr := e.handleManagedEvent(ctx, client, sessionID, event)
			seenBusy = seenBusy || busy
			if eventErr != nil {
				e.failManagedSession(eventErr)
				return nil, eventErr
			}
			if matched {
				return e.finishManagedWaiting("OpenCode needs input")
			}
			if event.Type == "session.idle" && eventSessionID(event.Properties) == sessionID {
				return e.finishManagedSuccess(ctx, client, sessionID)
			}
		case <-ticker.C:
			waiting, err := e.reconcileManagedSession(ctx, client, sessionID)
			if err != nil {
				e.failManagedSession(err)
				return nil, err
			}
			if waiting {
				return e.finishManagedWaiting("OpenCode needs input")
			}
			status, err := client.sessionStatus(ctx, sessionID)
			if err != nil {
				if errors.Is(err, errManagedSessionUnavailable) {
					return e.finishManagedUnavailable("The OpenCode session is no longer available")
				}
				statusFailures++
				if events == nil && statusFailures >= 3 {
					return e.finishManagedUnavailable("The OpenCode server can no longer be reached")
				}
				continue
			}
			statusFailures = 0
			seenBusy = seenBusy || status == "busy" || status == "retry"
			if status == "idle" && (seenBusy || time.Since(startedAt) >= time.Second) {
				return e.finishManagedSuccess(ctx, client, sessionID)
			}
		}
	}
}

func runtimeWorkerID(ctx context.Context) string {
	return runtime.GetEnv(ctx).WorkerID
}

func (e *harnessExecutor) ensureManagedSession(ctx context.Context, client *openCodeClient, cfg providerConfig) (string, error) {
	e.mu.Lock()
	persistedID := e.agentSession.SessionID
	hasPersistedSession := persistedID != ""
	e.mu.Unlock()
	requestedID := stringFlag(cfg.flags, "session")
	if persistedID == "" {
		persistedID = requestedID
	}

	var session openCodeSession
	var err error
	if persistedID != "" {
		session, err = client.getSession(ctx, persistedID)
		if err != nil {
			if !errors.Is(err, errManagedSessionUnavailable) {
				err = fmt.Errorf("%w: %v", errManagedSessionUnavailable, err)
			}
			return "", err
		}
		if session.Directory != "" && filepath.Clean(session.Directory) != filepath.Clean(client.directory) {
			return "", fmt.Errorf("OpenCode session %s belongs to a different working directory", persistedID)
		}
		if boolFlag(cfg.flags, "fork") && requestedID != "" && !hasPersistedSession {
			session, err = client.forkSession(ctx, persistedID)
			if err != nil {
				return "", err
			}
		}
	} else {
		session, err = client.createSession(ctx, stringFlag(cfg.flags, "title"), stringFlag(cfg.flags, "agent"))
		if err != nil {
			return "", err
		}
	}

	e.mu.Lock()
	e.agentSession.SessionID = session.ID
	e.mu.Unlock()
	e.notifyProgress()
	if boolFlag(cfg.flags, "share") {
		if err := client.shareSession(ctx, session.ID); err != nil {
			return "", err
		}
	}
	return session.ID, nil
}

func (e *harnessExecutor) submitManagedPrompt(ctx context.Context, client *openCodeClient, cfg providerConfig, sessionID string) (<-chan error, error) {
	parts := []map[string]any{{"type": "text", "text": e.effectivePrompt()}}
	if strings.TrimSpace(e.script) != "" {
		parts = append(parts, map[string]any{"type": "text", "text": "Supplementary input:\n" + e.script})
	}
	files, err := managedFileParts(client.directory, cfg.flags["file"])
	if err != nil {
		return nil, err
	}
	parts = append(parts, files...)

	if command := stringFlag(cfg.flags, "command"); command != "" {
		return client.commandAsync(ctx, sessionID, command, e.effectivePrompt(), cfg, files), nil
	}
	body := map[string]any{"parts": parts}
	if agent := stringFlag(cfg.flags, "agent"); agent != "" {
		body["agent"] = agent
	}
	if model := stringFlag(cfg.flags, "model"); model != "" {
		provider, modelID, ok := strings.Cut(model, "/")
		if !ok || provider == "" || modelID == "" {
			return nil, errors.New("managed OpenCode model must use provider/model format")
		}
		body["model"] = map[string]any{"providerID": provider, "modelID": modelID}
	}
	if variant := stringFlag(cfg.flags, "variant"); variant != "" {
		body["variant"] = variant
	}
	return nil, client.postNoContent(ctx, "/session/"+url.PathEscape(sessionID)+"/prompt_async", body)
}

func managedFileParts(workDir string, value any) ([]map[string]any, error) {
	var paths []string
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case string:
		paths = []string{typed}
	case []string:
		paths = typed
	case []any:
		for _, item := range typed {
			paths = append(paths, fmt.Sprint(item))
		}
	default:
		return nil, errors.New("managed OpenCode file must be a string or array")
	}
	parts := make([]map[string]any, 0, len(paths))
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			path = filepath.Join(workDir, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read OpenCode attachment %s: %w", path, err)
		}
		mediaType := mime.TypeByExtension(filepath.Ext(path))
		if mediaType == "" {
			mediaType = http.DetectContentType(data)
		}
		parts = append(parts, map[string]any{
			"type": "file", "mime": mediaType, "filename": filepath.Base(path),
			"url": "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data),
		})
	}
	return parts, nil
}

func (e *harnessExecutor) applyManagedInteractionResponses(ctx context.Context, client *openCodeClient, sessionID string) (bool, error) {
	e.mu.Lock()
	interactions := append([]ir.AgentInteraction(nil), e.agentSession.Interactions...)
	e.mu.Unlock()
	resumed := false
	for i := range interactions {
		interaction := interactions[i]
		if interaction.Status == ir.AgentInteractionPending || interaction.Applied {
			continue
		}
		var err error
		switch interaction.Kind {
		case ir.AgentInteractionPermission:
			reply := interaction.Decision
			if reply == "session" {
				reply = "always"
			}
			err = client.replyPermission(ctx, interaction.ID, reply)
		case ir.AgentInteractionQuestion:
			if interaction.Status == ir.AgentInteractionRejected {
				err = client.rejectQuestion(ctx, interaction.ID)
			} else {
				err = client.replyQuestion(ctx, interaction.ID, interaction.Answers)
			}
		}
		if err != nil {
			if errors.Is(err, errManagedSessionUnavailable) {
				e.mu.Lock()
				e.agentSession.State = ir.AgentSessionUnavailable
				e.agentSession.LastError = "The pending OpenCode interaction no longer exists"
				e.mu.Unlock()
				e.notifyProgress()
			}
			return resumed, err
		}
		e.mu.Lock()
		for j := range e.agentSession.Interactions {
			if e.agentSession.Interactions[j].ID == interaction.ID {
				e.agentSession.Interactions[j].Applied = true
				break
			}
		}
		e.mu.Unlock()
		resumed = true
	}
	return resumed, nil
}

func (e *harnessExecutor) handleManagedEvent(ctx context.Context, client *openCodeClient, sessionID string, event openCodeEvent) (waiting, busy bool, err error) {
	switch event.Type {
	case "permission.asked":
		var request openCodePermissionRequest
		if json.Unmarshal(event.Properties, &request) == nil && request.SessionID == sessionID {
			e.addManagedPermission(request)
			return true, false, nil
		}
	case "question.asked":
		var request openCodeQuestionRequest
		if json.Unmarshal(event.Properties, &request) == nil && request.SessionID == sessionID {
			e.addManagedQuestion(request)
			return true, false, nil
		}
	case "session.status":
		var status struct {
			SessionID string `json:"sessionID"`
			Status    struct {
				Type string `json:"type"`
			} `json:"status"`
		}
		if json.Unmarshal(event.Properties, &status) == nil && status.SessionID == sessionID {
			busy = status.Status.Type == "busy" || status.Status.Type == "retry"
		}
	case "session.error":
		if eventSessionID(event.Properties) == sessionID {
			return false, false, fmt.Errorf("OpenCode session failed: %s", compactJSON(event.Properties))
		}
	case "todo.updated", "session.diff", "message.part.delta":
		if eventSessionID(event.Properties) == sessionID {
			e.appendRawManagedEvent(event)
		}
	}
	if eventSessionID(event.Properties) == sessionID {
		_ = e.refreshManagedMessages(ctx, client, sessionID)
	}
	return false, busy, nil
}

func (e *harnessExecutor) reconcileManagedSession(ctx context.Context, client *openCodeClient, sessionID string) (bool, error) {
	permissions, err := client.permissions(ctx)
	if err == nil {
		for _, request := range permissions {
			if request.SessionID == sessionID {
				e.addManagedPermission(request)
				return true, nil
			}
		}
	}
	questions, err := client.questions(ctx)
	if err == nil {
		for _, request := range questions {
			if request.SessionID == sessionID {
				e.addManagedQuestion(request)
				return true, nil
			}
		}
	}
	_ = e.refreshManagedMessages(ctx, client, sessionID)
	return false, nil
}

func (e *harnessExecutor) finishManagedWaiting(message string) (*os.File, error) {
	e.mu.Lock()
	e.agentSession.State = ir.AgentSessionWaiting
	e.determinedStatus = ir.NodeWaiting
	e.hasDeterminedStatus = true
	e.appendAgentEventLocked("lifecycle", "waiting", message, nil)
	e.mu.Unlock()
	e.notifyProgress()
	return managedStdout("")
}

func (e *harnessExecutor) finishManagedUnavailable(message string) (*os.File, error) {
	e.mu.Lock()
	e.agentSession.State = ir.AgentSessionUnavailable
	e.agentSession.LastError = message
	e.determinedStatus = ir.NodeWaiting
	e.hasDeterminedStatus = true
	e.appendAgentEventLocked("lifecycle", "unavailable", message, nil)
	e.mu.Unlock()
	e.notifyProgress()
	return managedStdout("")
}

func (e *harnessExecutor) finishManagedSuccess(ctx context.Context, client *openCodeClient, sessionID string) (*os.File, error) {
	if err := e.refreshManagedMessages(ctx, client, sessionID); err != nil {
		return e.finishManagedUnavailable("The OpenCode session is no longer available")
	}
	e.mu.Lock()
	final := ""
	for i := len(e.savedMessages) - 1; i >= 0; i-- {
		if e.savedMessages[i].Role == ir.LLMRoleAssistant && e.savedMessages[i].Content != "" {
			final = e.savedMessages[i].Content
			break
		}
	}
	e.agentSession.State = ir.AgentSessionSucceeded
	e.determinedStatus = ir.NodeSucceeded
	e.hasDeterminedStatus = true
	e.appendAgentEventLocked("lifecycle", "succeeded", "OpenCode session completed", nil)
	e.mu.Unlock()
	e.notifyProgress()
	return managedStdout(final)
}

func managedStdout(content string) (*os.File, error) {
	file, err := newStdoutSpool()
	if err != nil {
		return nil, err
	}
	if content != "" {
		if _, err := io.WriteString(file, content+"\n"); err != nil {
			_ = cleanupStdoutSpool(file)
			return nil, err
		}
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = cleanupStdoutSpool(file)
		return nil, err
	}
	return file, nil
}

func (e *harnessExecutor) failManagedSession(err error) {
	e.mu.Lock()
	if e.agentSession != nil {
		e.agentSession.State = ir.AgentSessionFailed
		e.agentSession.LastError = err.Error()
		e.appendAgentEventLocked("lifecycle", "failed", err.Error(), nil)
	}
	e.mu.Unlock()
	e.notifyProgress()
}

func (e *harnessExecutor) setManagedState(state ir.AgentSessionState, status, content string) {
	e.mu.Lock()
	e.agentSession.State = state
	e.appendAgentEventLocked("lifecycle", status, content, nil)
	e.mu.Unlock()
	e.notifyProgress()
}

func (e *harnessExecutor) notifyProgress() {
	e.mu.Lock()
	callback := e.progressCallback
	e.mu.Unlock()
	if callback != nil {
		callback()
	}
}

func (e *harnessExecutor) appendAgentEventLocked(eventType, status, content string, data json.RawMessage) {
	if e.agentSession == nil {
		return
	}
	sequence := int64(1)
	if count := len(e.agentSession.Events); count > 0 {
		sequence = e.agentSession.Events[count-1].Sequence + 1
	}
	e.agentSession.Events = append(e.agentSession.Events, ir.AgentSessionEvent{
		Sequence:  sequence,
		ID:        fmt.Sprintf("dagu-%d-%d", e.agentSession.Generation, sequence),
		Type:      eventType,
		Status:    status,
		Content:   content,
		Data:      append(json.RawMessage(nil), data...),
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if len(e.agentSession.Events) > maxManagedAgentEvents {
		e.agentSession.Events = append([]ir.AgentSessionEvent(nil), e.agentSession.Events[len(e.agentSession.Events)-maxManagedAgentEvents:]...)
	}
}

func (e *harnessExecutor) appendRawManagedEvent(event openCodeEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if event.ID != "" {
		for _, existing := range e.agentSession.Events {
			if existing.ID == event.ID {
				return
			}
		}
	}
	sequence := int64(1)
	if count := len(e.agentSession.Events); count > 0 {
		sequence = e.agentSession.Events[count-1].Sequence + 1
	}
	if event.ID == "" {
		event.ID = fmt.Sprintf("opencode-%d-%d", e.agentSession.Generation, sequence)
	}
	e.agentSession.Events = append(e.agentSession.Events, ir.AgentSessionEvent{
		Sequence: sequence, ID: event.ID, Type: event.Type,
		Data: append(json.RawMessage(nil), event.Properties...), Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func (e *harnessExecutor) refreshManagedMessages(ctx context.Context, client *openCodeClient, sessionID string) error {
	messages, err := client.messages(ctx, sessionID)
	if err != nil {
		return err
	}
	chat, events, usage := normalizeOpenCodeMessages(messages)
	e.mu.Lock()
	e.savedMessages = chat
	for _, event := range events {
		found := false
		for _, existing := range e.agentSession.Events {
			if existing.ID == event.ID {
				found = true
				break
			}
		}
		if found {
			continue
		}
		event.Sequence = int64(1)
		if count := len(e.agentSession.Events); count > 0 {
			event.Sequence = e.agentSession.Events[count-1].Sequence + 1
		}
		e.agentSession.Events = append(e.agentSession.Events, event)
	}
	e.agentSession.Usage = usage
	if len(e.agentSession.Events) > maxManagedAgentEvents {
		e.agentSession.Events = append([]ir.AgentSessionEvent(nil), e.agentSession.Events[len(e.agentSession.Events)-maxManagedAgentEvents:]...)
	}
	e.mu.Unlock()
	e.notifyProgress()
	return nil
}

func normalizeOpenCodeMessages(messages []openCodeMessage) ([]ir.LLMMessage, []ir.AgentSessionEvent, ir.AgentUsage) {
	chat := make([]ir.LLMMessage, 0, len(messages))
	var events []ir.AgentSessionEvent
	var usage ir.AgentUsage
	for _, message := range messages {
		var info map[string]any
		if json.Unmarshal(message.Info, &info) != nil {
			continue
		}
		role, _ := info["role"].(string)
		messageID, _ := info["id"].(string)
		var textParts []string
		var toolCalls []ir.ToolCall
		for _, raw := range message.Parts {
			var part map[string]any
			if json.Unmarshal(raw, &part) != nil {
				continue
			}
			partType, _ := part["type"].(string)
			partID, _ := part["id"].(string)
			event := ir.AgentSessionEvent{ID: partID, Type: partType, Role: role, Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Data: append(json.RawMessage(nil), raw...)}
			switch partType {
			case "text":
				event.Content, _ = part["text"].(string)
				textParts = append(textParts, event.Content)
			case "reasoning":
				event.Content, _ = part["text"].(string)
			case "tool":
				event.Name, _ = part["tool"].(string)
				callID, _ := part["callID"].(string)
				state, _ := part["state"].(map[string]any)
				event.Status, _ = state["status"].(string)
				arguments, _ := json.Marshal(state["input"])
				toolCalls = append(toolCalls, ir.ToolCall{
					ID:   callID,
					Type: "function",
					Function: ir.ToolCallFunction{
						Name:      event.Name,
						Arguments: string(arguments),
					},
				})
			case "patch":
				if files, ok := part["files"].([]any); ok {
					for _, file := range files {
						event.Files = append(event.Files, fmt.Sprint(file))
					}
				}
			case "step-finish":
				if tokens, ok := part["tokens"].(map[string]any); ok {
					usage.InputTokens += int64(number(tokens["input"]))
					usage.OutputTokens += int64(number(tokens["output"]))
					usage.ReasoningTokens += int64(number(tokens["reasoning"]))
					usage.TotalTokens += int64(number(tokens["total"]))
				}
				usage.Cost += number(part["cost"])
			}
			if event.ID == "" {
				event.ID = messageID + ":" + partType
			}
			events = append(events, event)
		}
		if role != "user" && role != "assistant" {
			continue
		}
		msg := ir.LLMMessage{Role: ir.LLMRole(role), Content: strings.Join(textParts, "\n"), ToolCalls: toolCalls}
		if role == "assistant" {
			msg.Metadata = &ir.LLMMessageMetadata{
				Provider: fmt.Sprint(info["providerID"]), Model: fmt.Sprint(info["modelID"]),
				PromptTokens: int(usage.InputTokens), CompletionTokens: int(usage.OutputTokens), TotalTokens: int(usage.TotalTokens),
			}
		}
		chat = append(chat, msg)
	}
	return chat, events, usage
}

func number(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case json.Number:
		value, _ := typed.Float64()
		return value
	default:
		return 0
	}
}

type openCodePermissionRequest struct {
	ID         string          `json:"id"`
	SessionID  string          `json:"sessionID"`
	Permission string          `json:"permission"`
	Patterns   []string        `json:"patterns"`
	Metadata   json.RawMessage `json:"metadata"`
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

func (e *harnessExecutor) addManagedPermission(request openCodePermissionRequest) {
	interaction := ir.AgentInteraction{
		ID: request.ID, Kind: ir.AgentInteractionPermission, Status: ir.AgentInteractionPending,
		Permission: request.Permission, Patterns: request.Patterns, Metadata: request.Metadata,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	e.addManagedInteraction(interaction)
}

func (e *harnessExecutor) addManagedQuestion(request openCodeQuestionRequest) {
	interaction := ir.AgentInteraction{
		ID: request.ID, Kind: ir.AgentInteractionQuestion, Status: ir.AgentInteractionPending,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	for _, question := range request.Questions {
		item := ir.AgentQuestion{Header: question.Header, Question: question.Question, Multiple: question.Multiple, Custom: question.Custom}
		for _, option := range question.Options {
			item.Options = append(item.Options, ir.AgentQuestionOption{Label: option.Label, Description: option.Description})
		}
		interaction.Questions = append(interaction.Questions, item)
	}
	e.addManagedInteraction(interaction)
}

func (e *harnessExecutor) addManagedInteraction(interaction ir.AgentInteraction) {
	e.mu.Lock()
	for _, existing := range e.agentSession.Interactions {
		if existing.ID == interaction.ID {
			e.mu.Unlock()
			return
		}
	}
	e.agentSession.Interactions = append(e.agentSession.Interactions, interaction)
	raw, _ := json.Marshal(interaction)
	e.appendAgentEventLocked("interaction", "pending", "OpenCode needs input", raw)
	e.mu.Unlock()
	e.notifyProgress()
}

func stringFlag(flags map[string]any, key string) string {
	value, _ := flags[key].(string)
	return value
}

func boolFlag(flags map[string]any, key string) bool {
	value, _ := flags[key].(bool)
	return value
}

func compactJSON(raw json.RawMessage) string {
	var out bytes.Buffer
	if json.Compact(&out, raw) == nil {
		return out.String()
	}
	return string(raw)
}

func eventSessionID(raw json.RawMessage) string {
	var value struct {
		SessionID string `json:"sessionID"`
	}
	_ = json.Unmarshal(raw, &value)
	return value.SessionID
}

var errManagedSessionUnavailable = errors.New("managed OpenCode session is unavailable")

func (c *openCodeClient) endpoint(path string) string {
	query := url.Values{}
	query.Set("directory", c.directory)
	return c.host.URL + path + "?" + query.Encode()
}

func (c *openCodeClient) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
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
	return c.http.Do(req)
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
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return fmt.Errorf("OpenCode API %s %s returned %s: %s", method, path, resp.Status, strings.TrimSpace(string(data)))
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

func (c *openCodeClient) shareSession(ctx context.Context, sessionID string) error {
	return c.json(ctx, http.MethodPost, "/session/"+url.PathEscape(sessionID)+"/share", nil, nil)
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
		errs <- c.json(ctx, http.MethodPost, "/session/"+url.PathEscape(sessionID)+"/command", body, nil)
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
	client := &openCodeClient{host: host, directory: directory, http: http.DefaultClient}
	return client.abort(ctx, sessionID)
}

func (c *openCodeClient) subscribe(ctx context.Context) (<-chan openCodeEvent, <-chan error, func(), error) {
	streamCtx, cancel := context.WithCancel(ctx)
	resp, err := c.request(streamCtx, http.MethodGet, "/event", nil)
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
