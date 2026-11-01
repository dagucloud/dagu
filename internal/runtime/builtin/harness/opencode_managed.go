// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
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
	"github.com/dagucloud/dagu/v2/internal/runtime/modelusage"
	"github.com/google/uuid"
)

const (
	maxManagedAgentEvents        = 1000
	maxManagedAttachmentRawBytes = 10 * 1024 * 1024
	// managedCleanupTimeout bounds each wait for an interrupted or discarded
	// session to settle.
	managedCleanupTimeout = 10 * time.Second
)

func (e *harnessExecutor) runManagedOpenCode(
	parent context.Context,
	cfg providerConfig,
	host opencodehost.Config,
) (*os.File, error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	client := &openCodeClient{host: host, directory: e.workDir, http: newOpenCodeHTTPClient(30 * time.Second)}
	e.mu.Lock()
	// Register before setup so a Stop that arrives while the session starts is not lost.
	if e.stopRequested {
		e.mu.Unlock()
		return nil, context.Canceled
	}
	e.managedCancel = cancel
	defer func() {
		e.mu.Lock()
		e.managedCancel = nil
		e.mu.Unlock()
	}()
	e.managedHost = host
	e.promptSubmitted = false
	e.hasDeterminedStatus = false
	if e.agentSession == nil {
		e.agentSession = &ir.AgentSession{Provider: "opencode", Generation: 1}
	} else if e.agentSession.PromptSent && (e.agentSession.State == ir.AgentSessionFailed || e.agentSession.State == ir.AgentSessionAborted) {
		e.agentSession.StartNewGeneration()
		e.contextMessages = nil
		e.savedMessages = []ir.LLMMessage{}
	}
	if e.agentSession.Generation == 0 {
		e.agentSession.Generation = 1
	}
	cleanRestart := e.agentSession.RestartPending
	discardedID, discardedOwned := e.agentSession.DiscardedSessionID, e.agentSession.DiscardedOwned
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

	if cleanRestart && discardedID != "" {
		// Settlement outlives cancellation, so a stop cannot leave the
		// previous attempt's work running unnoticed.
		err := settleDiscardedSession(context.WithoutCancel(ctx), client, discardedID, discardedOwned)
		if ctx.Err() != nil {
			return e.finishManagedAbort(ctx, err)
		}
		if err != nil {
			return e.finishManagedError(err)
		}
	}
	sessionID, err := e.ensureManagedSession(ctx, client, cfg, cleanRestart)
	if err != nil {
		if ctx.Err() != nil {
			e.mu.Lock()
			persistedID := e.agentSession.SessionID
			e.mu.Unlock()
			if persistedID != "" {
				// A resumed run's session may still run Dagu's earlier turn.
				return e.finishManagedCancelled(ctx, client, persistedID)
			}
			// No session of this attempt exists, so there is no turn to stop.
			return e.finishManagedAbort(ctx, nil)
		}
		if errors.Is(err, errManagedSessionUnavailable) || (cleanRestart && errors.Is(err, errManagedHostUnavailable)) {
			return e.finishManagedError(err)
		}
		e.failManagedSession(err)
		return nil, err
	}
	client.sessionID = sessionID
	e.mu.Lock()
	e.agentSession.RestartPending = false
	e.mu.Unlock()

	events, eventErrs, closeEvents, err := client.subscribe(ctx)
	if err != nil {
		return e.finishManagedRunError(ctx, client, sessionID, err)
	}
	defer closeEvents()

	resumed, err := e.applyManagedInteractionResponses(ctx, client, sessionID)
	if err != nil {
		return e.finishManagedRunError(ctx, client, sessionID, err)
	}

	e.mu.Lock()
	promptSent := e.agentSession.PromptSent
	promptMessageID := e.agentSession.PromptMessageID
	e.mu.Unlock()
	if promptSent && promptMessageID == "" {
		messages, refreshErr := e.refreshManagedMessages(ctx, client, sessionID)
		if refreshErr != nil {
			return e.finishManagedRunError(ctx, client, sessionID, refreshErr)
		}
		promptMessageID = latestOpenCodeUserMessageID(messages)
		if promptMessageID != "" {
			e.mu.Lock()
			e.agentSession.PromptMessageID = promptMessageID
			e.mu.Unlock()
			e.notifyProgress()
		}
	}
	var commandErrs <-chan error
	if !resumed && !promptSent {
		if promptMessageID == "" {
			promptMessageID = "msg_dagu_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			e.mu.Lock()
			e.agentSession.PromptMessageID = promptMessageID
			e.mu.Unlock()
			e.notifyProgress()
		}
		if ctx.Err() != nil {
			return e.finishManagedCancelled(ctx, client, sessionID)
		}
		var submitted bool
		commandErrs, submitted, err = e.submitManagedPrompt(ctx, client, cfg, sessionID, promptMessageID, files)
		e.mu.Lock()
		e.promptSubmitted = submitted
		e.mu.Unlock()
		if err != nil {
			return e.finishManagedRunError(ctx, client, sessionID, err)
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
			return e.finishManagedCancelled(ctx, client, sessionID)
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
				return e.finishManagedRunError(ctx, client, sessionID, err)
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
				return e.finishManagedRunError(ctx, client, sessionID, eventErr)
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
				if ctx.Err() != nil {
					return e.finishManagedCancelled(ctx, client, sessionID)
				}
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
				if ctx.Err() != nil {
					return e.finishManagedCancelled(ctx, client, sessionID)
				}
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

// settleDiscardedSession keeps a replacement session from running beside work
// left by the previous attempt in the same directory. Only a session that Dagu
// created is interrupted; any other session is waited on.
func settleDiscardedSession(ctx context.Context, client *openCodeClient, sessionID string, owned bool) error {
	ctx, cancel := context.WithTimeout(ctx, managedCleanupTimeout)
	defer cancel()
	settle := client.awaitIdle
	if owned {
		settle = client.interruptAndAwaitIdle
	}
	if err := settle(ctx, sessionID); err != nil {
		return fmt.Errorf("OpenCode session %s from the previous attempt may still be running: %w", sessionID, err)
	}
	return nil
}

func runtimeWorkerID(ctx context.Context) string {
	return runtime.GetEnv(ctx).WorkerID
}

func (e *harnessExecutor) ensureManagedSession(ctx context.Context, client *openCodeClient, cfg providerConfig, cleanRestart bool) (string, error) {
	e.mu.Lock()
	persistedID := e.agentSession.SessionID
	hasPersistedSession := persistedID != ""
	e.mu.Unlock()
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
	if session.Directory != "" {
		e.agentSession.Directory = session.Directory
	} else {
		e.agentSession.Directory = client.directory
	}
	e.mu.Unlock()
	e.notifyProgress()
	return session.ID, nil
}

// submitManagedPrompt reports whether the prompt submission began, so a
// cancelled caller knows whether the host may have taken the prompt.
func (e *harnessExecutor) submitManagedPrompt(ctx context.Context, client *openCodeClient, cfg providerConfig, sessionID, messageID string, files []map[string]any) (<-chan error, bool, error) {
	if client.host.IsV2() {
		submitted, err := e.submitManagedPromptV2(ctx, client, cfg, sessionID, messageID, files)
		return nil, submitted, err
	}
	parts := []map[string]any{{"type": "text", "text": e.effectivePrompt()}}
	if strings.TrimSpace(e.script) != "" {
		parts = append(parts, map[string]any{"type": "text", "text": "Supplementary input:\n" + e.script})
	}
	parts = append(parts, files...)

	if command := stringFlag(cfg.flags, "command"); command != "" {
		// The command request lasts for its whole turn, so it stays cancellable.
		return client.commandAsync(ctx, sessionID, messageID, command, e.effectivePrompt(), cfg, files), true, nil
	}
	body := map[string]any{"messageID": messageID, "parts": parts}
	if agent := stringFlag(cfg.flags, "agent"); agent != "" {
		body["agent"] = agent
	}
	if model := stringFlag(cfg.flags, "model"); model != "" {
		provider, modelID, ok := strings.Cut(model, "/")
		if !ok || provider == "" || modelID == "" {
			return nil, false, errors.New("managed OpenCode model must use provider/model format")
		}
		body["model"] = map[string]any{"providerID": provider, "modelID": modelID}
	}
	if variant := stringFlag(cfg.flags, "variant"); variant != "" {
		body["variant"] = variant
	}
	// A started submission outlives cancellation, so the run learns whether
	// the host took the prompt. The client timeout still bounds it.
	return nil, true, client.postNoContent(context.WithoutCancel(ctx), "/session/"+url.PathEscape(sessionID)+"/prompt_async", body)
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
		mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
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
	if client.host.IsV2() {
		// V2 events carry data rather than properties. Reconcile the current
		// requests so a reply racing the event cannot restore a stale prompt.
		if event.Type == "permission.asked" || event.Type == "form.created" {
			var data struct {
				SessionID string `json:"sessionID"`
				Form      struct {
					SessionID string `json:"sessionID"`
				} `json:"form"`
			}
			if json.Unmarshal(event.Data, &data) == nil && (data.SessionID == sessionID || data.Form.SessionID == sessionID) {
				waiting, err = e.reconcileManagedSession(ctx, client, sessionID)
				return waiting, false, err
			}
		}
		return false, false, nil
	}

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
		if message, ok := openCodeSessionError(event.Properties, sessionID); ok {
			e.mu.Lock()
			e.agentSession.LastError = message
			e.mu.Unlock()
			e.notifyProgress()
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
	if _, err := e.refreshManagedMessages(ctx, client, sessionID); err != nil && isManagedUnavailable(err) {
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
	messages, err := e.refreshManagedMessages(ctx, client, sessionID)
	if err != nil {
		return e.finishManagedRunError(ctx, client, sessionID, err)
	}
	e.mu.Lock()
	promptMessageID := e.agentSession.PromptMessageID
	lastError := e.agentSession.LastError
	e.mu.Unlock()
	final, completed, resultErr := managedOpenCodeResult(messages, promptMessageID)
	if resultErr != nil {
		return e.finishManagedError(resultErr)
	}
	if !completed {
		if lastError == "" {
			lastError = "OpenCode became idle without completing the submitted prompt"
		}
		return e.finishManagedError(errors.New(lastError))
	}
	e.mu.Lock()
	e.agentSession.State = ir.AgentSessionSucceeded
	e.agentSession.LastError = ""
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
		e.agentSession.LastError = err.Error()
		e.appendAgentEventLocked("lifecycle", "failed", err.Error())
	}
	e.mu.Unlock()
	e.notifyProgress()
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

// finishManagedRunError reports a failure of a run whose session exists. A
// failure caused by cancellation interrupts the session instead.
func (e *harnessExecutor) finishManagedRunError(ctx context.Context, client *openCodeClient, sessionID string, err error) (*os.File, error) {
	if ctx.Err() != nil {
		return e.finishManagedCancelled(ctx, client, sessionID)
	}
	return e.finishManagedError(err)
}

// finishManagedCancelled interrupts the turn of a cancelled run and records
// whether the host confirmed that the session stopped.
func (e *harnessExecutor) finishManagedCancelled(ctx context.Context, client *openCodeClient, sessionID string) (*os.File, error) {
	e.mu.Lock()
	promptSent, submitted := e.agentSession.PromptSent, e.promptSubmitted
	promptID, owned := e.agentSession.PromptMessageID, e.agentSession.SessionOwned
	e.mu.Unlock()
	if !promptSent && !submitted {
		// Dagu has no turn here; the session may run work that is not Dagu's.
		return e.finishManagedAbort(ctx, nil)
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), managedCleanupTimeout)
	defer cancel()
	var err error
	if submitted && promptID != "" {
		// An interrupt does nothing before the host starts the prompt's turn.
		if err = client.awaitPromptStart(cleanupCtx, sessionID, promptID, owned); err != nil {
			// The turn may still start in this session, so a retry submits the
			// prompt to it again instead of replacing it.
			e.mu.Lock()
			e.agentSession.PromptSent = false
			e.mu.Unlock()
		}
	}
	switch {
	case err != nil:
		if owned && cleanupCtx.Err() == nil {
			// A lost status can hide a turn that started; the interrupt does
			// nothing if it has not. Another session may run the user's own turn.
			if abortErr := client.abort(cleanupCtx, sessionID); abortErr != nil {
				err = errors.Join(err, abortErr)
			}
		}
	case owned:
		err = client.interruptAndAwaitIdle(cleanupCtx, sessionID)
	default:
		err = settleUserSession(cleanupCtx, client, sessionID, promptID)
	}
	return e.finishManagedAbort(ctx, err)
}

// settleUserSession stops Dagu's turn in a session the user supplied. While
// Dagu's prompt is the session's latest input the active turn is Dagu's and
// is interrupted; newer input belongs to the user, whose turn is waited on.
func settleUserSession(ctx context.Context, client *openCodeClient, sessionID, promptID string) error {
	messages, err := client.messages(ctx, sessionID)
	if errors.Is(err, errManagedSessionUnavailable) {
		return nil
	}
	if err != nil {
		return err
	}
	if promptID != "" && latestOpenCodeUserMessageID(messages) == promptID {
		return client.interruptAndAwaitIdle(ctx, sessionID)
	}
	return client.awaitIdle(ctx, sessionID)
}

// finishManagedAbort records a cancelled run, and whether cleanup confirmed
// that Dagu's work in OpenCode stopped.
func (e *harnessExecutor) finishManagedAbort(ctx context.Context, cleanupErr error) (*os.File, error) {
	if cleanupErr == nil {
		e.setManagedState(ir.AgentSessionAborted, "aborted", "OpenCode session aborted")
		return nil, ctx.Err()
	}
	cleanupErr = fmt.Errorf("OpenCode did not confirm the session stopped: %w", cleanupErr)
	e.mu.Lock()
	e.agentSession.State = ir.AgentSessionAborted
	e.agentSession.LastError = cleanupErr.Error()
	e.appendAgentEventLocked("lifecycle", "abort-unverified", cleanupErr.Error())
	e.mu.Unlock()
	e.notifyProgress()
	return nil, errors.Join(ctx.Err(), cleanupErr)
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

func (e *harnessExecutor) refreshManagedMessages(ctx context.Context, client *openCodeClient, sessionID string) ([]openCodeMessage, error) {
	messages, err := client.messages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	chat, events, usage, models := normalizeOpenCodeMessages(messages)
	e.mu.Lock()
	generation := e.agentSession.Generation
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
	// The session's totals replace what it reported before, so a resumed
	// session does not count its earlier calls twice.
	modelusage.FromContext(ctx).SetReported(fmt.Sprintf("opencode:%s:%d", sessionID, generation), models)
	if changed {
		e.notifyProgress()
	}
	return messages, nil
}

func latestOpenCodeUserMessageID(messages []openCodeMessage) string {
	latest := ""
	for _, message := range messages {
		var info openCodeMessageInfo
		if json.Unmarshal(message.Info, &info) == nil && info.Role == "user" {
			latest = info.ID
		}
	}
	return latest
}

func managedOpenCodeResult(messages []openCodeMessage, promptMessageID string) (string, bool, error) {
	if promptMessageID == "" {
		return "", false, nil
	}
	response, responseInfo := openCodeReply(messages, promptMessageID)
	if response == nil {
		return "", false, nil
	}
	if message := openCodeErrorMessage(responseInfo.Error); message != "" {
		return "", true, errors.New(message)
	}
	if responseInfo.Finish == "" || responseInfo.Time.Completed == 0 {
		return "", false, nil
	}
	var textParts []string
	for _, raw := range response.Parts {
		var part struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &part) == nil && part.Type == "text" {
			textParts = append(textParts, part.Text)
		}
	}
	return strings.Join(textParts, "\n"), true, nil
}

// openCodeReply returns the latest assistant reply to the prompt, or nil.
func openCodeReply(messages []openCodeMessage, promptMessageID string) (*openCodeMessage, openCodeMessageInfo) {
	var response *openCodeMessage
	var responseInfo openCodeMessageInfo
	for i := range messages {
		var info openCodeMessageInfo
		if json.Unmarshal(messages[i].Info, &info) == nil && info.Role == "assistant" && info.ParentID == promptMessageID {
			response = &messages[i]
			responseInfo = info
		}
	}
	return response, responseInfo
}

func openCodeSessionError(properties json.RawMessage, sessionID string) (string, bool) {
	var event struct {
		SessionID string          `json:"sessionID"`
		Error     json.RawMessage `json:"error"`
	}
	if json.Unmarshal(properties, &event) != nil || event.SessionID != sessionID {
		return "", false
	}
	if message := openCodeErrorMessage(event.Error); message != "" {
		return message, true
	}
	return "OpenCode reported a session error", true
}

func openCodeErrorMessage(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value struct {
		Name string `json:"name"`
		Data struct {
			Message string `json:"message"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	if message := strings.TrimSpace(value.Data.Message); message != "" {
		return message
	}
	return strings.TrimSpace(value.Name)
}

// normalizeOpenCodeMessages turns a session's messages into the step's chat,
// its events, the session's usage, and that usage by the model each
// assistant message came from.
func normalizeOpenCodeMessages(messages []openCodeMessage) ([]ir.LLMMessage, []ir.AgentSessionEvent, ir.AgentUsage, []ir.ModelUsage) {
	chat := make([]ir.LLMMessage, 0, len(messages))
	var events []ir.AgentSessionEvent
	var usage ir.AgentUsage
	var models []ir.ModelUsage
	for _, message := range messages {
		var info openCodeMessageInfo
		if json.Unmarshal(message.Info, &info) != nil {
			continue
		}
		role := info.Role
		messageID := info.ID
		var textParts []string
		var toolCalls []ir.ToolCall
		var messageUsage ir.AgentUsage
		modelUsage := ir.ModelUsage{Provider: info.ProviderID, Model: info.ModelID}
		for partIndex, raw := range message.Parts {
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
					messageUsage.InputTokens += int64(number(tokens["input"]))
					messageUsage.OutputTokens += int64(number(tokens["output"]))
					messageUsage.ReasoningTokens += int64(number(tokens["reasoning"]))
					messageUsage.TotalTokens += int64(number(tokens["total"]))
					addOpenCodeStep(&modelUsage, tokens)
				}
				modelUsage.Calls++
				messageUsage.Cost += number(part["cost"])
				if _, reported := part["cost"]; reported {
					modelUsage.Cost += number(part["cost"])
					modelUsage.CostReportedCalls++
				}
			}
			if event.ID == "" {
				event.ID = fmt.Sprintf("%s:%s:%d", messageID, partType, partIndex)
			}
			events = append(events, event)
		}
		usage.InputTokens += messageUsage.InputTokens
		usage.OutputTokens += messageUsage.OutputTokens
		usage.ReasoningTokens += messageUsage.ReasoningTokens
		usage.TotalTokens += messageUsage.TotalTokens
		usage.Cost += messageUsage.Cost
		if role == "assistant" && modelUsage.Calls > 0 {
			models = ir.MergeModelUsage(models, modelUsage)
		}
		if role != "user" && role != "assistant" {
			continue
		}
		msg := ir.LLMMessage{Role: ir.LLMRole(role), Content: strings.Join(textParts, "\n"), ToolCalls: toolCalls}
		if role == "assistant" {
			msg.Metadata = &ir.LLMMessageMetadata{
				Provider: info.ProviderID, Model: info.ModelID,
				PromptTokens: int(messageUsage.InputTokens), CompletionTokens: int(messageUsage.OutputTokens), TotalTokens: int(messageUsage.TotalTokens),
			}
		}
		chat = append(chat, msg)
	}
	return chat, events, usage, models
}

// addOpenCodeStep adds one model step's tokens. OpenCode counts the prompt
// apart from the tokens read from and written to the prompt cache, and the
// output with the reasoning in it.
func addOpenCodeStep(usage *ir.ModelUsage, tokens map[string]any) {
	cache, _ := tokens["cache"].(map[string]any)
	read, write := int64(number(cache["read"])), int64(number(cache["write"]))
	usage.InputTokens += int64(number(tokens["input"])) + read + write
	usage.CachedInputTokens += read
	usage.CacheWriteTokens += write
	usage.OutputTokens += int64(number(tokens["output"]))
	usage.ReasoningTokens += int64(number(tokens["reasoning"]))
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
