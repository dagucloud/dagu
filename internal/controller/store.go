// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dagucloud/dagu/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/workspace"
)

const (
	definitionDirectoryName       = "controllers"
	runtimeDirectoryName          = "controller-runtime"
	runtimeTerminalReserve        = 2 << 10
	maxDAGRunRefs                 = 20
	maxLastErrorBytes             = 1 << 10
	resourceLockStaleThreshold    = 30 * time.Second
	resourceLockHeartbeatInterval = 10 * time.Second
	resourceLockRetryInterval     = 50 * time.Millisecond
)

// DefinitionStore persists exact Controller YAML documents.
type DefinitionStore interface {
	Create(ctx context.Context, id string, data []byte) error
	Get(ctx context.Context, id string) ([]byte, error)
	Update(ctx context.Context, id string, data []byte) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context) ([]string, error)
	ModifiedAt(ctx context.Context, id string) (time.Time, error)
}

// RuntimeStore persists the current-or-last runtime snapshot.
type RuntimeStore interface {
	Get(ctx context.Context, id string) (*Runtime, error)
	Put(ctx context.Context, runtime *Runtime) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context) ([]string, error)
}

// ResourceLocker serializes all mutations for one Controller across processes.
// The callback context is canceled if lock ownership cannot be maintained.
type ResourceLocker interface {
	WithLock(ctx context.Context, id string, fn func(context.Context) error) error
}

// FileStores contains Controller stores rooted below one DataDir.
type FileStores struct {
	Definitions DefinitionStore
	Runtimes    RuntimeStore
	Locker      ResourceLocker
}

// NewFileStores creates stores at DataDir/controllers and DataDir/controller-runtime.
func NewFileStores(dataDir string) FileStores {
	runtimeDir := filepath.Join(dataDir, runtimeDirectoryName)
	return FileStores{
		Definitions: &fileDefinitionStore{dir: filepath.Join(dataDir, definitionDirectoryName)},
		Runtimes:    &fileRuntimeStore{dir: runtimeDir},
		Locker: &fileResourceLocker{
			dir:               runtimeDir,
			staleThreshold:    resourceLockStaleThreshold,
			heartbeatInterval: resourceLockHeartbeatInterval,
		},
	}
}

type fileDefinitionStore struct {
	dir string
}

func (s *fileDefinitionStore) Create(ctx context.Context, id string, data []byte) error {
	path, err := s.path(id)
	if err != nil {
		return err
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return fmt.Errorf("create controller definition directory: %w", err)
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if err := fileutil.WriteFileAtomicExclusive(path, data, 0o600); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%w: %s", ErrAlreadyExists, id)
		}
		return fmt.Errorf("create controller definition %s: %w", id, err)
	}
	return nil
}

func (s *fileDefinitionStore) Get(ctx context.Context, id string) ([]byte, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, err
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	data, err := fileutil.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return nil, fmt.Errorf("read controller definition %s: %w", id, err)
	}
	return data, nil
}

func (s *fileDefinitionStore) Update(ctx context.Context, id string, data []byte) error {
	path, err := s.path(id)
	if err != nil {
		return err
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return fmt.Errorf("stat controller definition %s: %w", id, err)
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if err := fileutil.WriteFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("update controller definition %s: %w", id, err)
	}
	return nil
}

func (s *fileDefinitionStore) Delete(ctx context.Context, id string) error {
	path, err := s.path(id)
	if err != nil {
		return err
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if err := fileutil.RemoveFileDurable(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete controller definition %s: %w", id, err)
	}
	return nil
}

func (s *fileDefinitionStore) List(ctx context.Context) ([]string, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("list controller definitions: %w", err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".yaml")
		if !controllerIDPattern.MatchString(id) {
			return nil, fmt.Errorf("%w: invalid definition filename %q", ErrDefinitionCorrupt, entry.Name())
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// ModifiedAt returns the persisted definition file timestamp.
func (s *fileDefinitionStore) ModifiedAt(ctx context.Context, id string) (time.Time, error) {
	path, err := s.path(id)
	if err != nil {
		return time.Time{}, err
	}
	if err := context.Cause(ctx); err != nil {
		return time.Time{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return time.Time{}, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return time.Time{}, fmt.Errorf("stat controller definition %s: %w", id, err)
	}
	return info.ModTime().UTC(), nil
}

func (s *fileDefinitionStore) path(id string) (string, error) {
	if err := ValidateID(id); err != nil {
		return "", err
	}
	return filepath.Join(s.dir, id+".yaml"), nil
}

type fileRuntimeStore struct {
	dir string
}

func (s *fileRuntimeStore) Get(ctx context.Context, id string) (*Runtime, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, err
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	data, err := fileutil.ReadFileLimit(path, MaxRuntimeBytes+1)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return nil, fmt.Errorf("read controller runtime %s: %w", id, err)
	}
	if len(data) > MaxRuntimeBytes {
		return nil, fmt.Errorf("%w: %s: snapshot exceeds %d bytes", ErrRuntimeCorrupt, id, MaxRuntimeBytes)
	}
	runtime, err := decodeRuntime(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrRuntimeCorrupt, id, err)
	}
	if runtime.FinishedAt == nil && len(data) > MaxRuntimeBytes-runtimeTerminalReserve {
		return nil, fmt.Errorf("%w: %s: unfinished snapshot exceeds %d bytes", ErrRuntimeCorrupt, id, MaxRuntimeBytes-runtimeTerminalReserve)
	}
	if runtime.ID != id {
		return nil, fmt.Errorf("%w: runtime ID %q does not match record %q", ErrRuntimeCorrupt, runtime.ID, id)
	}
	return runtime, nil
}

func (s *fileRuntimeStore) Put(ctx context.Context, runtime *Runtime) error {
	if err := validateRuntime(runtime); err != nil {
		return err
	}
	data, err := json.Marshal(runtime)
	if err != nil {
		return fmt.Errorf("marshal controller runtime %s: %w", runtime.ID, err)
	}
	limit := MaxRuntimeBytes
	if runtime.FinishedAt == nil {
		limit -= runtimeTerminalReserve
	}
	if len(data) > limit {
		return fmt.Errorf("%w: got %d bytes", ErrSnapshotTooLarge, len(data))
	}
	path, err := s.path(runtime.ID)
	if err != nil {
		return err
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return fmt.Errorf("create controller runtime directory: %w", err)
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if err := fileutil.WriteFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("write controller runtime %s: %w", runtime.ID, err)
	}
	return nil
}

func (s *fileRuntimeStore) Delete(ctx context.Context, id string) error {
	path, err := s.path(id)
	if err != nil {
		return err
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if err := fileutil.RemoveFileDurable(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete controller runtime %s: %w", id, err)
	}
	return nil
}

func (s *fileRuntimeStore) List(ctx context.Context) ([]string, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("list controller runtimes: %w", err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !controllerIDPattern.MatchString(id) {
			return nil, fmt.Errorf("%w: invalid runtime filename %q", ErrRuntimeCorrupt, entry.Name())
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func (s *fileRuntimeStore) path(id string) (string, error) {
	if !controllerIDPattern.MatchString(id) {
		return "", fmt.Errorf("%w: invalid Controller ID %q", ErrNotFound, id)
	}
	return filepath.Join(s.dir, id+".json"), nil
}

type fileResourceLocker struct {
	dir               string
	staleThreshold    time.Duration
	heartbeatInterval time.Duration
}

func (l *fileResourceLocker) WithLock(ctx context.Context, id string, fn func(context.Context) error) (err error) {
	if err := ValidateID(id); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("controller lock callback is required")
	}
	lock := dirlock.New(filepath.Join(l.dir, id), &dirlock.LockOptions{
		StaleThreshold: l.staleThreshold,
		RetryInterval:  resourceLockRetryInterval,
	})
	if err := lock.Lock(ctx); err != nil {
		return err
	}

	lockedCtx, cancelLocked := context.WithCancelCause(ctx)
	heartbeatCtx, stopHeartbeat := context.WithCancel(context.WithoutCancel(ctx))
	heartbeatDone := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(l.heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				heartbeatDone <- nil
				return
			case <-ticker.C:
				if heartbeatErr := lock.Heartbeat(heartbeatCtx); heartbeatErr != nil {
					heartbeatErr = fmt.Errorf("maintain Controller lock: %w", heartbeatErr)
					cancelLocked(heartbeatErr)
					heartbeatDone <- heartbeatErr
					return
				}
			}
		}
	}()
	defer func() {
		stopHeartbeat()
		heartbeatErr := <-heartbeatDone
		cancelLocked(nil)
		if heartbeatErr != nil && !errors.Is(err, heartbeatErr) {
			err = errors.Join(err, heartbeatErr)
		}
		if unlockErr := lock.Unlock(); unlockErr != nil {
			err = errors.Join(err, fmt.Errorf("release Controller lock: %w", unlockErr))
		}
	}()
	return fn(lockedCtx)
}

func validateRuntime(runtime *Runtime) error {
	if runtime == nil {
		return fmt.Errorf("%w: runtime is nil", ErrRuntimeCorrupt)
	}
	if runtime.RuntimeVersion != RuntimeVersion {
		return fmt.Errorf("%w: unsupported runtimeVersion %d", ErrRuntimeCorrupt, runtime.RuntimeVersion)
	}
	if !controllerIDPattern.MatchString(runtime.ID) {
		return fmt.Errorf("%w: invalid runtime ID %q", ErrRuntimeCorrupt, runtime.ID)
	}
	if runtime.Workspace != "" {
		if err := workspace.ValidateName(runtime.Workspace); err != nil {
			return fmt.Errorf("%w: invalid workspace %q", ErrRuntimeCorrupt, runtime.Workspace)
		}
	}
	if !stateNamePattern.MatchString(runtime.CurrentState) {
		return fmt.Errorf("%w: invalid currentState %q", ErrRuntimeCorrupt, runtime.CurrentState)
	}
	if runtime.TurnCount < 0 {
		return fmt.Errorf("%w: turnCount must not be negative", ErrRuntimeCorrupt)
	}
	if runtime.StartedAt.IsZero() || runtime.UpdatedAt.IsZero() || runtime.UpdatedAt.Before(runtime.StartedAt) {
		return fmt.Errorf("%w: invalid runtime timestamps", ErrRuntimeCorrupt)
	}
	if runtime.FinishedAt != nil && (runtime.FinishedAt.IsZero() || runtime.FinishedAt.Before(runtime.StartedAt) || runtime.FinishedAt.After(runtime.UpdatedAt)) {
		return fmt.Errorf("%w: invalid finishedAt timestamp", ErrRuntimeCorrupt)
	}
	if runtime.LastError != nil && (!utf8.ValidString(*runtime.LastError) || strings.TrimSpace(*runtime.LastError) == "" || len(*runtime.LastError) > maxLastErrorBytes) {
		return fmt.Errorf("%w: lastError must contain at most %d bytes of non-whitespace UTF-8 text", ErrRuntimeCorrupt, maxLastErrorBytes)
	}
	if len(runtime.DAGRunRefs) > maxDAGRunRefs {
		return fmt.Errorf("%w: dagRunRefs exceeds %d entries", ErrRuntimeCorrupt, maxDAGRunRefs)
	}
	refsByRunID := make(map[string]DAGRunRef, len(runtime.DAGRunRefs))
	for _, ref := range runtime.DAGRunRefs {
		if !stateNamePattern.MatchString(ref.State) || !validDAGFileName(ref.DAG) || exec.ValidateDAGRunID(ref.DAGRunID) != nil {
			return fmt.Errorf("%w: incomplete dagRunRef", ErrRuntimeCorrupt)
		}
		if _, duplicate := refsByRunID[ref.DAGRunID]; duplicate {
			return fmt.Errorf("%w: duplicate DAG run ID %q", ErrRuntimeCorrupt, ref.DAGRunID)
		}
		refsByRunID[ref.DAGRunID] = ref
	}
	if runtime.ActiveDAGRun != nil {
		active := runtime.ActiveDAGRun
		canonicalParams, err := canonicalJSONObject(active.Params)
		if active.ToolCallID == "" || !validDAGFileName(active.DAG) || exec.ValidateDAGRunID(active.DAGRunID) != nil ||
			err != nil || !bytes.Equal(active.Params, canonicalParams) {
			return fmt.Errorf("%w: invalid activeDAGRun", ErrRuntimeCorrupt)
		}
		if ref, exists := refsByRunID[active.DAGRunID]; exists && (ref.State != runtime.CurrentState || ref.DAG != active.DAG) {
			return fmt.Errorf("%w: activeDAGRun does not match its dagRunRef", ErrRuntimeCorrupt)
		}
	}
	contextState, err := validateRuntimeContext(runtime.Context)
	if err != nil {
		return err
	}
	return validateRuntimeAgainstContext(runtime, contextState)
}

func validateRuntimeAgainstContext(runtime *Runtime, contextState persistedContextState) error {
	expectedState := DefaultStateName
	if contextState.lastRoute != nil {
		expectedState = contextState.lastRoute.Decision.NextState
	}
	if runtime.CurrentState != expectedState {
		return fmt.Errorf("%w: currentState does not match the latest route", ErrRuntimeCorrupt)
	}

	pendingRoute := contextState.pendingRoute
	if runtime.ActiveDAGRun != nil {
		active := runtime.ActiveDAGRun
		if pendingRoute == nil || pendingRoute.ToolCallID != active.ToolCallID || pendingRoute.Decision.Action != "run" ||
			pendingRoute.Decision.NextState != runtime.CurrentState || pendingRoute.Decision.DAG != active.DAG ||
			!bytes.Equal(pendingRoute.Decision.Params, active.Params) {
			return fmt.Errorf("%w: activeDAGRun does not match the pending route tool call", ErrRuntimeCorrupt)
		}
	} else if pendingRoute != nil {
		if pendingRoute.Decision.Action != "run" || (runtime.Status != core.Failed && runtime.Status != core.Aborted) {
			return fmt.Errorf("%w: unresolved route tool call without an active child", ErrRuntimeCorrupt)
		}
	}
	if contextState.phase == persistedContextPromptWait &&
		runtime.Status != core.Waiting && runtime.Status != core.Failed && runtime.Status != core.Aborted {
		return fmt.Errorf("%w: prompt wait context does not match runtime lifecycle", ErrRuntimeCorrupt)
	}
	if runtime.Status == core.Waiting && runtime.ActiveDAGRun == nil {
		if contextState.phase != persistedContextPromptWait || contextState.lastRoute == nil ||
			runtime.WaitingQuestion == nil || *runtime.WaitingQuestion != contextState.lastRoute.Decision.Question {
			return fmt.Errorf("%w: prompt wait does not match the latest route", ErrRuntimeCorrupt)
		}
	}
	if terminal := contextState.terminal; terminal != nil {
		if runtime.Status != terminal.Status {
			return fmt.Errorf("%w: terminal context does not match runtime lifecycle", ErrRuntimeCorrupt)
		}
		if terminal.LastError == "" {
			if runtime.LastError != nil {
				return fmt.Errorf("%w: completed runtime has lastError", ErrRuntimeCorrupt)
			}
		} else if runtime.LastError == nil || *runtime.LastError != terminal.LastError {
			return fmt.Errorf("%w: terminal context does not match lastError", ErrRuntimeCorrupt)
		}
	} else {
		if runtime.Status == core.Succeeded {
			return fmt.Errorf("%w: succeeded runtime has no completion outcome", ErrRuntimeCorrupt)
		}
		if runtime.Status == core.Failed && runtime.LastError == nil {
			return fmt.Errorf("%w: operational failure has no lastError", ErrRuntimeCorrupt)
		}
	}
	return validateRuntimeLifecycle(runtime)
}

type persistedRouteCall struct {
	ToolCallID string
	Decision   RouteDecision
}

type persistedContextPhase uint8

const (
	persistedContextRouteReady persistedContextPhase = iota
	persistedContextPendingRoute
	persistedContextPromptWait
	persistedContextTerminal
)

type persistedTerminal struct {
	Status    core.Status
	LastError string
}

type persistedContextState struct {
	pendingRoute *persistedRouteCall
	lastRoute    *persistedRouteCall
	terminal     *persistedTerminal
	phase        persistedContextPhase
}

type persistedToolResult struct {
	terminal *persistedTerminal
	phase    persistedContextPhase
}

type persistedEnvelope struct {
	Kind    string          `json:"kind"`
	Trust   string          `json:"trust"`
	Source  string          `json:"source"`
	Payload json.RawMessage `json:"payload"`
}

type persistedRoutingOutcome struct {
	Action  string  `json:"action"`
	Outcome string  `json:"outcome"`
	State   string  `json:"state"`
	Status  *string `json:"status,omitempty"`
}

type persistedExecutionEvidence struct {
	DAG           string            `json:"dag"`
	DAGRunID      string            `json:"dag_run_id"`
	Status        string            `json:"status"`
	Outputs       map[string]string `json:"outputs"`
	Untrusted     bool              `json:"untrusted"`
	ErrorCategory string            `json:"error_category,omitempty"`
	Truncated     bool              `json:"truncated,omitempty"`
	OmittedCount  int               `json:"omitted_count,omitempty"`
}

func validateRuntimeContext(messages []exec.LLMMessage) (persistedContextState, error) {
	if len(messages) == 0 || messages[0].Role != core.LLMRoleUser {
		return persistedContextState{}, fmt.Errorf("%w: context must begin with a user message", ErrRuntimeCorrupt)
	}
	phase := persistedContextRouteReady
	var pending *persistedRouteCall
	var lastRoute *persistedRouteCall
	var terminal *persistedTerminal
	seenToolCallIDs := make(map[string]struct{})
	for index, message := range messages {
		if phase == persistedContextTerminal {
			return persistedContextState{}, fmt.Errorf("%w: context continues after a terminal outcome", ErrRuntimeCorrupt)
		}
		switch message.Role {
		case core.LLMRoleUser:
			if (index != 0 && phase != persistedContextPromptWait) || message.ToolCallID != "" ||
				len(message.ToolCalls) != 0 || message.Metadata != nil || ValidatePrompt(message.Content) != nil {
				return persistedContextState{}, fmt.Errorf("%w: invalid user context message", ErrRuntimeCorrupt)
			}
			phase = persistedContextRouteReady
		case core.LLMRoleAssistant:
			if phase != persistedContextRouteReady || message.ToolCallID != "" || message.Content != "" || len(message.ToolCalls) != 1 {
				return persistedContextState{}, fmt.Errorf("%w: invalid assistant context message", ErrRuntimeCorrupt)
			}
			call := message.ToolCalls[0]
			if call.ID == "" || call.Type != "function" || call.Function.Name != routeToolName {
				return persistedContextState{}, fmt.Errorf("%w: invalid route tool call", ErrRuntimeCorrupt)
			}
			if _, duplicate := seenToolCallIDs[call.ID]; duplicate {
				return persistedContextState{}, fmt.Errorf("%w: duplicate route tool call ID %q", ErrRuntimeCorrupt, call.ID)
			}
			arguments, err := decodeRouteArguments(call.Function.Arguments)
			if err != nil {
				return persistedContextState{}, fmt.Errorf("%w: invalid route tool arguments", ErrRuntimeCorrupt)
			}
			decision, err := normalizeRouteArguments(arguments)
			if err != nil {
				return persistedContextState{}, fmt.Errorf("%w: invalid route tool arguments", ErrRuntimeCorrupt)
			}
			canonical, err := json.Marshal(routeArgumentsFromDecision(*decision))
			if err != nil || call.Function.Arguments != string(canonical) {
				return persistedContextState{}, fmt.Errorf("%w: route tool arguments are not canonical", ErrRuntimeCorrupt)
			}
			seenToolCallIDs[call.ID] = struct{}{}
			pending = &persistedRouteCall{ToolCallID: call.ID, Decision: *decision}
			lastRoute = pending
			phase = persistedContextPendingRoute
		case core.LLMRoleTool:
			if phase != persistedContextPendingRoute || pending == nil || message.ToolCallID != pending.ToolCallID ||
				len(message.ToolCalls) != 0 || message.Metadata != nil {
				return persistedContextState{}, fmt.Errorf("%w: unmatched route tool result", ErrRuntimeCorrupt)
			}
			result, err := validatePersistedToolResult(message, *pending)
			if err != nil {
				return persistedContextState{}, err
			}
			phase = result.phase
			terminal = result.terminal
			pending = nil
		case core.LLMRoleSystem:
			return persistedContextState{}, fmt.Errorf("%w: system messages must not be persisted in context", ErrRuntimeCorrupt)
		default:
			return persistedContextState{}, fmt.Errorf("%w: invalid persisted context role %q", ErrRuntimeCorrupt, message.Role)
		}
	}
	return persistedContextState{pendingRoute: pending, lastRoute: lastRoute, terminal: terminal, phase: phase}, nil
}

func validatePersistedToolResult(message exec.LLMMessage, route persistedRouteCall) (persistedToolResult, error) {
	var envelope persistedEnvelope
	if err := decodeStrictJSON([]byte(message.Content), &envelope); err != nil {
		return persistedToolResult{}, fmt.Errorf("%w: invalid route tool result", ErrRuntimeCorrupt)
	}
	switch route.Decision.Action {
	case "run":
		return validatePersistedExecutionEvidence(message, route, envelope)
	case "wait", "complete":
		return validatePersistedRoutingOutcome(message, route, envelope)
	default:
		return persistedToolResult{}, fmt.Errorf("%w: invalid persisted route action", ErrRuntimeCorrupt)
	}
}

func validatePersistedRoutingOutcome(message exec.LLMMessage, route persistedRouteCall, envelope persistedEnvelope) (persistedToolResult, error) {
	if envelope.Kind != "routing_outcome" || envelope.Trust != "dagu_generated" || envelope.Source != "controller_runner" {
		return persistedToolResult{}, fmt.Errorf("%w: invalid routing outcome envelope", ErrRuntimeCorrupt)
	}
	var payload persistedRoutingOutcome
	if err := decodeStrictJSON(envelope.Payload, &payload); err != nil {
		return persistedToolResult{}, fmt.Errorf("%w: invalid routing outcome payload", ErrRuntimeCorrupt)
	}
	terminal := ""
	if route.Decision.Action == "complete" {
		if payload.Status == nil || (*payload.Status != "succeeded" && *payload.Status != "failed") {
			return persistedToolResult{}, fmt.Errorf("%w: invalid completion outcome status", ErrRuntimeCorrupt)
		}
		terminal = *payload.Status
	} else if payload.Status != nil {
		return persistedToolResult{}, fmt.Errorf("%w: wait outcome contains a terminal status", ErrRuntimeCorrupt)
	}
	decision := route.Decision
	decision.ToolCallID = route.ToolCallID
	expected, err := RoutingOutcomeMessage(decision, terminal)
	if err != nil || message.Content != expected.Content {
		return persistedToolResult{}, fmt.Errorf("%w: routing outcome does not match its route", ErrRuntimeCorrupt)
	}
	if route.Decision.Action != "complete" {
		return persistedToolResult{phase: persistedContextPromptWait}, nil
	}
	status := core.Succeeded
	if terminal == "failed" {
		status = core.Failed
	}
	return persistedToolResult{
		phase:    persistedContextTerminal,
		terminal: &persistedTerminal{Status: status},
	}, nil
}

func validatePersistedExecutionEvidence(message exec.LLMMessage, route persistedRouteCall, envelope persistedEnvelope) (persistedToolResult, error) {
	if envelope.Kind != "execution_evidence" || envelope.Trust != "runtime_untrusted" || len(message.Content) > maxEvidenceBytes {
		return persistedToolResult{}, fmt.Errorf("%w: invalid execution evidence envelope", ErrRuntimeCorrupt)
	}
	var payload persistedExecutionEvidence
	if err := decodeStrictJSON(envelope.Payload, &payload); err != nil {
		return persistedToolResult{}, fmt.Errorf("%w: invalid execution evidence payload", ErrRuntimeCorrupt)
	}
	status, ok := persistedChildStatus(payload.Status)
	if !ok || payload.DAG != route.Decision.DAG || !validDAGFileName(payload.DAG) || exec.ValidateDAGRunID(payload.DAGRunID) != nil ||
		payload.Outputs == nil || !payload.Untrusted || payload.OmittedCount < 0 || payload.Truncated != (payload.OmittedCount > 0) {
		return persistedToolResult{}, fmt.Errorf("%w: execution evidence does not match its route", ErrRuntimeCorrupt)
	}
	active := ActiveDAGRun{ToolCallID: route.ToolCallID, DAG: payload.DAG, DAGRunID: payload.DAGRunID}
	observation := ChildRunObservation{Status: status, Outputs: payload.Outputs, ErrorCategory: payload.ErrorCategory}
	expected, err := evidenceEnvelope(active, observation, payload.Outputs, payload.OmittedCount)
	if err != nil || message.Content != expected || envelope.Source != "dag_run:"+payload.DAGRunID {
		return persistedToolResult{}, fmt.Errorf("%w: execution evidence is not canonical", ErrRuntimeCorrupt)
	}
	if status == core.Succeeded || status == core.PartiallySucceeded {
		return persistedToolResult{phase: persistedContextRouteReady}, nil
	}
	return persistedToolResult{
		phase: persistedContextTerminal,
		terminal: &persistedTerminal{
			Status:    core.Failed,
			LastError: "child_dag_failed",
		},
	}, nil
}

func persistedChildStatus(value string) (core.Status, bool) {
	switch value {
	case "succeeded":
		return core.Succeeded, true
	case "partially_succeeded":
		return core.PartiallySucceeded, true
	case "failed":
		return core.Failed, true
	case "aborted":
		return core.Aborted, true
	case "rejected":
		return core.Rejected, true
	default:
		return core.NotStarted, false
	}
}

func validateRuntimeLifecycle(runtime *Runtime) error {
	if runtime.Status != core.Failed && runtime.LastError != nil {
		return fmt.Errorf("%w: lastError is only valid for failed runtimes", ErrRuntimeCorrupt)
	}
	switch runtime.Status {
	case core.Running:
		if runtime.FinishedAt != nil || runtime.WaitingQuestion != nil {
			return fmt.Errorf("%w: running runtime has terminal or waiting fields", ErrRuntimeCorrupt)
		}
	case core.Waiting:
		if runtime.FinishedAt != nil {
			return fmt.Errorf("%w: waiting runtime has finishedAt", ErrRuntimeCorrupt)
		}
		if runtime.ActiveDAGRun == nil {
			if runtime.WaitingQuestion == nil || !boundedNonWhitespace(*runtime.WaitingQuestion, maxQuestionRunes) {
				return fmt.Errorf("%w: prompt wait requires waitingQuestion", ErrRuntimeCorrupt)
			}
		} else if runtime.WaitingQuestion != nil {
			return fmt.Errorf("%w: child wait must not have waitingQuestion", ErrRuntimeCorrupt)
		}
	case core.Succeeded, core.Failed:
		if runtime.FinishedAt == nil || runtime.ActiveDAGRun != nil || runtime.WaitingQuestion != nil {
			return fmt.Errorf("%w: terminal runtime has inconsistent fields", ErrRuntimeCorrupt)
		}
	case core.Aborted:
		if runtime.WaitingQuestion != nil {
			return fmt.Errorf("%w: aborted runtime has waitingQuestion", ErrRuntimeCorrupt)
		}
		if runtime.FinishedAt != nil && runtime.ActiveDAGRun != nil {
			return fmt.Errorf("%w: settled aborted runtime has an active child", ErrRuntimeCorrupt)
		}
	case core.NotStarted, core.Queued, core.PartiallySucceeded, core.Rejected:
		return fmt.Errorf("%w: unsupported Controller status %s", ErrRuntimeCorrupt, runtime.Status)
	default:
		return fmt.Errorf("%w: unknown Controller status %d", ErrRuntimeCorrupt, runtime.Status)
	}
	return nil
}

func decodeRuntime(data []byte) (*Runtime, error) {
	var runtime Runtime
	if err := decodeStrictJSON(data, &runtime); err != nil {
		return nil, err
	}
	if err := validateRuntime(&runtime); err != nil {
		return nil, err
	}
	return &runtime, nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return fmt.Errorf("multiple JSON values are not allowed")
	}
	return nil
}
