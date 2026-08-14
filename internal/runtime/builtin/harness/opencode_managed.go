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
	"reflect"
	"regexp"
	osruntime "runtime"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/opencodehost"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

const (
	maxManagedAgentEvents        = 1000
	maxManagedAttachmentRawBytes = 10 * 1024 * 1024
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

func (e *harnessExecutor) runManagedOpenCode(
	ctx context.Context,
	cfg providerConfig,
	host opencodehost.Config,
) (*os.File, error) {
	client := &openCodeClient{host: host, directory: e.workDir, http: &http.Client{Timeout: 30 * time.Second}}
	e.mu.Lock()
	e.managedHost = host
	e.hasDeterminedStatus = false
	if e.agentSession == nil {
		e.agentSession = &ir.AgentSession{Provider: "opencode", Generation: 1}
	}
	if e.agentSession.Generation == 0 {
		e.agentSession.Generation = 1
	}
	cleanRestart := e.agentSession.RestartPending
	if !cleanRestart && e.agentSession.SessionID != "" && (e.agentSession.HostInstanceID == "" || e.agentSession.HostInstanceID != host.InstanceID) {
		e.mu.Unlock()
		return e.finishManagedUnavailable("The OpenCode host changed; start a clean session to continue")
	}
	e.agentSession.ProviderVersion = host.Version
	e.agentSession.HostInstanceID = host.InstanceID
	e.agentSession.Directory = client.directory
	e.agentSession.Agent = stringFlag(cfg.flags, "agent")
	e.agentSession.Model = stringFlag(cfg.flags, "model")
	e.agentSession.Variant = stringFlag(cfg.flags, "variant")
	e.agentSession.OwnerWorkerID = runtimeWorkerID(ctx)
	e.agentSession.State = ir.AgentSessionStarting
	e.appendAgentEventLocked("lifecycle", "starting", "OpenCode session starting")
	e.mu.Unlock()
	e.notifyProgress()

	files, err := managedFileParts(client.directory, cfg.flags["file"])
	if err != nil {
		e.failManagedSession(err)
		return nil, err
	}

	sessionID, err := e.ensureManagedSession(ctx, client, cfg, cleanRestart)
	if err != nil {
		if errors.Is(err, errManagedSessionUnavailable) || (cleanRestart && errors.Is(err, errManagedHostUnavailable)) {
			return e.finishManagedError(err)
		}
		e.failManagedSession(err)
		return nil, err
	}
	e.mu.Lock()
	e.agentSession.RestartPending = false
	e.mu.Unlock()

	events, eventErrs, closeEvents, err := client.subscribe(ctx)
	if err != nil {
		return e.finishManagedError(err)
	}
	defer closeEvents()

	resumed, err := e.applyManagedInteractionResponses(ctx, client, sessionID)
	if err != nil {
		return e.finishManagedError(err)
	}

	e.mu.Lock()
	promptSent := e.agentSession.PromptSent
	e.mu.Unlock()
	var commandErrs <-chan error
	if !resumed && !promptSent {
		commandErrs, err = e.submitManagedPrompt(ctx, client, cfg, sessionID, files)
		if err != nil {
			return e.finishManagedError(err)
		}
		e.mu.Lock()
		e.agentSession.PromptSent = true
		e.mu.Unlock()
	}

	e.setManagedState(ir.AgentSessionRunning, "running", "OpenCode is working")
	ticker := time.NewTicker(2 * time.Second)
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
			e.markManagedCleanupPending()
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
				return e.finishManagedError(err)
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
				return e.finishManagedError(eventErr)
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
				if errors.Is(err, errManagedSessionUnavailable) {
					return e.finishManagedUnavailable("The OpenCode session is no longer available")
				}
				if errors.Is(err, errManagedHostUnavailable) {
					statusFailures++
					if statusFailures >= 3 {
						return e.finishManagedUnavailable("The OpenCode server can no longer be reached")
					}
					continue
				}
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
				if errors.Is(err, errManagedHostUnavailable) && statusFailures >= 3 {
					return e.finishManagedUnavailable("The OpenCode server can no longer be reached")
				}
				if !errors.Is(err, errManagedHostUnavailable) {
					e.failManagedSession(err)
					return nil, err
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

func (e *harnessExecutor) ensureManagedSession(ctx context.Context, client *openCodeClient, cfg providerConfig, cleanRestart bool) (string, error) {
	e.mu.Lock()
	persistedID := e.agentSession.SessionID
	hasPersistedSession := persistedID != ""
	discardedID := e.agentSession.DiscardedSessionID
	discardedOwned := e.agentSession.DiscardedOwned
	e.mu.Unlock()
	if cleanRestart && discardedID != "" && discardedOwned {
		if err := client.deleteSession(ctx, discardedID); err != nil && !errors.Is(err, errManagedSessionUnavailable) {
			e.mu.Lock()
			e.appendAgentEventLocked("cleanup", "warning", "The discarded OpenCode session could not be removed")
			e.mu.Unlock()
		} else {
			e.mu.Lock()
			e.agentSession.DiscardedSessionID = ""
			e.agentSession.DiscardedOwned = false
			e.mu.Unlock()
		}
	}
	requestedID := stringFlag(cfg.flags, "session")
	if cleanRestart {
		persistedID = ""
		hasPersistedSession = false
		requestedID = ""
	}
	if persistedID == "" {
		persistedID = requestedID
	}

	var session openCodeSession
	var err error
	owned := false
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
			if err := client.validateManagedConfig(ctx); err != nil {
				return "", err
			}
			session, err = client.forkSession(ctx, persistedID)
			if err != nil {
				return "", err
			}
			owned = true
		}
	} else {
		if err := client.validateManagedConfig(ctx); err != nil {
			return "", err
		}
		session, err = client.createSession(ctx, stringFlag(cfg.flags, "title"), stringFlag(cfg.flags, "agent"))
		if err != nil {
			return "", err
		}
		owned = true
	}

	e.mu.Lock()
	e.agentSession.SessionID = session.ID
	e.agentSession.SessionOwned = owned || (hasPersistedSession && e.agentSession.SessionOwned)
	e.agentSession.HostInstanceID = client.host.InstanceID
	e.agentSession.ProviderVersion = client.host.Version
	e.mu.Unlock()
	e.notifyProgress()
	return session.ID, nil
}

func (e *harnessExecutor) submitManagedPrompt(ctx context.Context, client *openCodeClient, cfg providerConfig, sessionID string, files []map[string]any) (<-chan error, error) {
	parts := []map[string]any{{"type": "text", "text": e.effectivePrompt()}}
	if strings.TrimSpace(e.script) != "" {
		parts = append(parts, map[string]any{"type": "text", "text": "Supplementary input:\n" + e.script})
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
	total := 0
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			path = filepath.Join(workDir, path)
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("read OpenCode attachment %s: %w", path, err)
		}
		remaining := maxManagedAttachmentRawBytes - total
		data, readErr := io.ReadAll(io.LimitReader(file, int64(remaining+1)))
		closeErr := file.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read OpenCode attachment %s: %w", path, readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close OpenCode attachment %s: %w", path, closeErr)
		}
		total += len(data)
		if total > maxManagedAttachmentRawBytes {
			return nil, fmt.Errorf("managed OpenCode attachments exceed the %d MiB limit", maxManagedAttachmentRawBytes/(1024*1024))
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
				e.recordManagedPermissionGrant(interaction)
				reply = "once"
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
			autoApproved, replyErr := e.replyFromManagedGrant(ctx, client, request)
			if replyErr != nil || autoApproved {
				return false, false, replyErr
			}
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
			return false, false, errors.New("OpenCode reported a session error")
		}
	}
	return false, busy, nil
}

func (e *harnessExecutor) reconcileManagedSession(ctx context.Context, client *openCodeClient, sessionID string) (bool, error) {
	permissions, err := client.permissions(ctx)
	if err != nil {
		if isManagedUnavailable(err) {
			return false, err
		}
	} else {
		for _, request := range permissions {
			if request.SessionID == sessionID {
				autoApproved, replyErr := e.replyFromManagedGrant(ctx, client, request)
				if replyErr != nil {
					return false, replyErr
				}
				if autoApproved {
					continue
				}
				e.addManagedPermission(request)
				return true, nil
			}
		}
	}
	questions, err := client.questions(ctx)
	if err != nil {
		if isManagedUnavailable(err) {
			return false, err
		}
	} else {
		for _, request := range questions {
			if request.SessionID == sessionID {
				e.addManagedQuestion(request)
				return true, nil
			}
		}
	}
	if err := e.refreshManagedMessages(ctx, client, sessionID); err != nil && isManagedUnavailable(err) {
		return false, err
	}
	return false, nil
}

func (e *harnessExecutor) finishManagedWaiting(message string) (*os.File, error) {
	e.mu.Lock()
	e.agentSession.State = ir.AgentSessionWaiting
	e.determinedStatus = ir.NodeWaiting
	e.hasDeterminedStatus = true
	e.appendAgentEventLocked("lifecycle", "waiting", message)
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
	e.appendAgentEventLocked("lifecycle", "unavailable", message)
	e.mu.Unlock()
	e.notifyProgress()
	return managedStdout("")
}

func (e *harnessExecutor) finishManagedSuccess(ctx context.Context, client *openCodeClient, sessionID string) (*os.File, error) {
	if err := e.refreshManagedMessages(ctx, client, sessionID); err != nil {
		return e.finishManagedError(err)
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
	if e.agentSession.SessionOwned && e.agentSession.SessionID != "" {
		e.agentSession.CleanupPending = true
	}
	e.determinedStatus = ir.NodeSucceeded
	e.hasDeterminedStatus = true
	e.appendAgentEventLocked("lifecycle", "succeeded", "OpenCode session completed")
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
		if e.agentSession.SessionOwned && e.agentSession.SessionID != "" {
			e.agentSession.CleanupPending = true
		}
		e.agentSession.LastError = err.Error()
		e.appendAgentEventLocked("lifecycle", "failed", err.Error())
	}
	e.mu.Unlock()
	e.notifyProgress()
}

func (e *harnessExecutor) markManagedCleanupPending() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.agentSession != nil && e.agentSession.SessionOwned && e.agentSession.SessionID != "" {
		e.agentSession.CleanupPending = true
	}
}

func (e *harnessExecutor) finishManagedError(err error) (*os.File, error) {
	switch {
	case errors.Is(err, errManagedSessionUnavailable):
		return e.finishManagedUnavailable("The OpenCode session is no longer available")
	case errors.Is(err, errManagedHostUnavailable):
		return e.finishManagedUnavailable("The OpenCode server can no longer be reached")
	default:
		e.failManagedSession(err)
		return nil, err
	}
}

func (e *harnessExecutor) setManagedState(state ir.AgentSessionState, status, content string) {
	e.mu.Lock()
	e.agentSession.State = state
	e.appendAgentEventLocked("lifecycle", status, content)
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

func (e *harnessExecutor) appendAgentEventLocked(eventType, status, content string) {
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
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if len(e.agentSession.Events) > maxManagedAgentEvents {
		e.agentSession.Events = append([]ir.AgentSessionEvent(nil), e.agentSession.Events[len(e.agentSession.Events)-maxManagedAgentEvents:]...)
	}
}

func (e *harnessExecutor) refreshManagedMessages(ctx context.Context, client *openCodeClient, sessionID string) error {
	messages, err := client.messages(ctx, sessionID)
	if err != nil {
		return err
	}
	chat, events, usage := normalizeOpenCodeMessages(messages)
	e.mu.Lock()
	changed := !reflect.DeepEqual(e.savedMessages, chat) || e.agentSession.Usage != usage
	e.savedMessages = chat
	eventIndexes := make(map[string]int, len(e.agentSession.Events))
	for i := range e.agentSession.Events {
		eventIndexes[e.agentSession.Events[i].ID] = i
	}
	for _, event := range events {
		if i, ok := eventIndexes[event.ID]; ok {
			existing := e.agentSession.Events[i]
			event.Sequence = existing.Sequence
			event.Timestamp = existing.Timestamp
			if !reflect.DeepEqual(existing, event) {
				e.agentSession.Events[i] = event
				changed = true
			}
			continue
		}
		event.Sequence = int64(1)
		if count := len(e.agentSession.Events); count > 0 {
			event.Sequence = e.agentSession.Events[count-1].Sequence + 1
		}
		e.agentSession.Events = append(e.agentSession.Events, event)
		eventIndexes[event.ID] = len(e.agentSession.Events) - 1
		changed = true
	}
	e.agentSession.Usage = usage
	if len(e.agentSession.Events) > maxManagedAgentEvents {
		e.agentSession.Events = append([]ir.AgentSessionEvent(nil), e.agentSession.Events[len(e.agentSession.Events)-maxManagedAgentEvents:]...)
		changed = true
	}
	e.mu.Unlock()
	if changed {
		e.notifyProgress()
	}
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
			event := ir.AgentSessionEvent{ID: partID, Type: partType, Role: role, Timestamp: time.Now().UTC().Format(time.RFC3339Nano)}
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

func (e *harnessExecutor) addManagedPermission(request openCodePermissionRequest) {
	interaction := ir.AgentInteraction{
		ID: request.ID, Kind: ir.AgentInteractionPermission, Status: ir.AgentInteractionPending,
		Permission: request.Permission, Patterns: request.Patterns, AllowForSessionPatterns: request.Always,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	e.addManagedInteraction(interaction)
}

func (e *harnessExecutor) recordManagedPermissionGrant(interaction ir.AgentInteraction) {
	if len(interaction.AllowForSessionPatterns) == 0 {
		return
	}
	grant := ir.AgentPermissionGrant{
		Permission: interaction.Permission, Patterns: append([]string(nil), interaction.AllowForSessionPatterns...),
		GrantedAt: interaction.RespondedAt, GrantedBy: interaction.RespondedBy, GrantedByID: interaction.RespondedByID,
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, existing := range e.agentSession.PermissionGrants {
		if existing.Permission == grant.Permission && reflect.DeepEqual(existing.Patterns, grant.Patterns) {
			return
		}
	}
	e.agentSession.PermissionGrants = append(e.agentSession.PermissionGrants, grant)
}

func (e *harnessExecutor) replyFromManagedGrant(ctx context.Context, client *openCodeClient, request openCodePermissionRequest) (bool, error) {
	e.mu.Lock()
	grants := append([]ir.AgentPermissionGrant(nil), e.agentSession.PermissionGrants...)
	e.mu.Unlock()
	for _, grant := range grants {
		if grant.Permission != request.Permission || !permissionPatternsCovered(grant.Patterns, request.Patterns) {
			continue
		}
		if err := client.replyPermission(ctx, request.ID, "once"); err != nil {
			return false, err
		}
		e.mu.Lock()
		e.appendAgentEventLocked("permission", "auto-approved", "Permission approved from this session's saved scope")
		e.mu.Unlock()
		e.notifyProgress()
		return true, nil
	}
	return false, nil
}

func permissionPatternsCovered(grants, requested []string) bool {
	if len(grants) == 0 || len(requested) == 0 {
		return false
	}
	for _, target := range requested {
		matched := false
		for _, pattern := range grants {
			if openCodeWildcardMatch(pattern, target) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func openCodeWildcardMatch(pattern, target string) bool {
	pattern = strings.ReplaceAll(pattern, "\\", "/")
	target = strings.ReplaceAll(target, "\\", "/")
	if osruntime.GOOS == "windows" {
		pattern = strings.ToLower(pattern)
		target = strings.ToLower(target)
	}
	if base, ok := strings.CutSuffix(pattern, " *"); ok && (target == base || strings.HasPrefix(target, base+" ")) {
		return true
	}
	quoted := regexp.QuoteMeta(pattern)
	quoted = strings.ReplaceAll(quoted, `\*`, `.*`)
	quoted = strings.ReplaceAll(quoted, `\?`, `.`)
	matched, err := regexp.MatchString("^"+quoted+"$", target)
	return err == nil && matched
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
	e.appendAgentEventLocked("interaction", "pending", "OpenCode needs input")
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

func eventSessionID(raw json.RawMessage) string {
	var value struct {
		SessionID string `json:"sessionID"`
	}
	_ = json.Unmarshal(raw, &value)
	return value.SessionID
}

var (
	errManagedHostUnavailable    = errors.New("managed OpenCode host is unavailable")
	errManagedSessionUnavailable = errors.New("managed OpenCode session is unavailable")
)

func isManagedUnavailable(err error) bool {
	return errors.Is(err, errManagedHostUnavailable) || errors.Is(err, errManagedSessionUnavailable)
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
